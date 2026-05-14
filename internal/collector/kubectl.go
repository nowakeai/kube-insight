package collector

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"kube-insight/internal/logging"
	"kube-insight/internal/sanitize"
)

type Resource struct {
	Name       string   `json:"name"`
	Group      string   `json:"group,omitempty"`
	Version    string   `json:"version,omitempty"`
	Resource   string   `json:"resource,omitempty"`
	Kind       string   `json:"kind,omitempty"`
	Namespaced bool     `json:"namespaced"`
	Verbs      []string `json:"verbs,omitempty"`
}

type SampleOptions struct {
	OutputDir         string
	Contexts          []string
	AllContexts       bool
	Namespace         string
	DiscoverResources bool
	UseClientGo       bool
	Resources         []Resource
	MaxItems          int
	Salt              string
}

type SampleManifest struct {
	GeneratedAt time.Time      `json:"generatedAt"`
	OutputDir   string         `json:"outputDir"`
	MaxItems    int            `json:"maxItems"`
	Clusters    []ClusterEntry `json:"clusters"`
}

type ClusterEntry struct {
	Alias     string          `json:"alias"`
	Resources []ResourceEntry `json:"resources"`
}

type ResourceEntry struct {
	Name       string `json:"name"`
	Group      string `json:"group,omitempty"`
	Version    string `json:"version,omitempty"`
	Resource   string `json:"resource,omitempty"`
	Kind       string `json:"kind,omitempty"`
	Namespaced bool   `json:"namespaced"`
	File       string `json:"file,omitempty"`
	Items      int    `json:"items,omitempty"`
	Error      string `json:"error,omitempty"`
}

func DefaultResources() []Resource {
	return []Resource{
		{Name: "pods", Namespaced: true},
		{Name: "services", Namespaced: true},
		{Name: "endpoints", Namespaced: true},
		{Name: "endpointslices.discovery.k8s.io", Namespaced: true},
		{Name: "events", Namespaced: true},
		{Name: "events.events.k8s.io", Namespaced: true},
		{Name: "deployments.apps", Namespaced: true},
		{Name: "replicasets.apps", Namespaced: true},
		{Name: "daemonsets.apps", Namespaced: true},
		{Name: "statefulsets.apps", Namespaced: true},
		{Name: "jobs.batch", Namespaced: true},
		{Name: "cronjobs.batch", Namespaced: true},
		{Name: "configmaps", Namespaced: true},
		{Name: "secrets", Namespaced: true},
		{Name: "persistentvolumeclaims", Namespaced: true},
		{Name: "ingresses.networking.k8s.io", Namespaced: true},
		{Name: "networkpolicies.networking.k8s.io", Namespaced: true},
		{Name: "serviceaccounts", Namespaced: true},
		{Name: "roles.rbac.authorization.k8s.io", Namespaced: true},
		{Name: "rolebindings.rbac.authorization.k8s.io", Namespaced: true},
		{Name: "horizontalpodautoscalers.autoscaling", Namespaced: true},
		{Name: "nodes"},
		{Name: "namespaces"},
		{Name: "persistentvolumes"},
		{Name: "storageclasses.storage.k8s.io"},
		{Name: "priorityclasses.scheduling.k8s.io"},
		{Name: "clusterroles.rbac.authorization.k8s.io"},
		{Name: "clusterrolebindings.rbac.authorization.k8s.io"},
		{Name: "customresourcedefinitions.apiextensions.k8s.io"},
	}
}

func ParseResources(values []string) ([]Resource, error) {
	if len(values) == 0 {
		return DefaultResources(), nil
	}
	var out []Resource
	for _, value := range values {
		for _, item := range strings.Split(value, ",") {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			namespaced := true
			if strings.HasPrefix(item, "!") {
				namespaced = false
				item = strings.TrimPrefix(item, "!")
			}
			out = append(out, Resource{Name: item, Namespaced: namespaced})
		}
	}
	if len(out) == 0 {
		return nil, errors.New("at least one resource is required")
	}
	return out, nil
}

func ConfiguredContexts(ctx context.Context) ([]string, error) {
	out, err := runKubectl(ctx, "config", "get-contexts", "-o", "name")
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var contexts []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			contexts = append(contexts, line)
		}
	}
	return contexts, nil
}

func CurrentContext(ctx context.Context) (string, error) {
	out, err := runKubectl(ctx, "config", "current-context")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func CollectSamples(ctx context.Context, opts SampleOptions) (SampleManifest, error) {
	logger := logging.FromContext(ctx).With("component", "collector")
	if opts.OutputDir == "" {
		opts.OutputDir = filepath.Join("testdata", "kube-samples")
	}
	if opts.MaxItems <= 0 {
		opts.MaxItems = 500
	}
	if opts.Salt == "" {
		salt, err := randomSalt()
		if err != nil {
			return SampleManifest{}, err
		}
		opts.Salt = salt
	}
	if len(opts.Resources) == 0 {
		opts.Resources = DefaultResources()
	}
	contexts := opts.Contexts
	if opts.AllContexts {
		var err error
		contexts, err = configuredContexts(ctx, opts.UseClientGo)
		if err != nil {
			return SampleManifest{}, err
		}
	}
	if len(contexts) == 0 {
		current, err := currentContext(ctx, opts.UseClientGo)
		if err != nil {
			return SampleManifest{}, err
		}
		contexts = []string{current}
	}
	logger.Info("collect samples started", "contexts", len(contexts), "resources", len(opts.Resources), "discoverResources", opts.DiscoverResources, "outputDir", opts.OutputDir)
	if err := os.MkdirAll(opts.OutputDir, 0o755); err != nil {
		return SampleManifest{}, err
	}

	manifest := SampleManifest{
		GeneratedAt: time.Now().UTC(),
		OutputDir:   opts.OutputDir,
		MaxItems:    opts.MaxItems,
	}
	for _, kubeContext := range contexts {
		logger.Info("collect context started", "context", kubeContext)
		clusterAlias := "cluster-" + shortHash(opts.Salt, kubeContext)
		clusterDir := filepath.Join(opts.OutputDir, clusterAlias)
		if err := os.MkdirAll(clusterDir, 0o755); err != nil {
			return manifest, err
		}
		cluster := ClusterEntry{Alias: clusterAlias}
		resources := opts.Resources
		if opts.DiscoverResources {
			discovered, err := discoverResources(ctx, kubeContext, opts.UseClientGo)
			if err != nil {
				logger.Warn("resource discovery failed", "context", kubeContext, "error", err)
				cluster.Resources = append(cluster.Resources, ResourceEntry{
					Name:  "api-resources",
					Error: err.Error(),
				})
			} else {
				logger.Info("resource discovery completed", "context", kubeContext, "resources", len(discovered))
				resources = mergeResources(discovered, resources)
			}
		}
		for _, resource := range resources {
			logger.Debug("collect resource started", "context", kubeContext, "resource", resource.Name)
			entry := ResourceEntry{
				Name:       resource.Name,
				Group:      resource.Group,
				Version:    resource.Version,
				Resource:   resource.Resource,
				Kind:       resource.Kind,
				Namespaced: resource.Namespaced,
			}
			data, err := getResourceWithOptions(ctx, kubeContext, resource, opts.UseClientGo, opts.Namespace)
			if err != nil {
				entry.Error = err.Error()
				logger.Warn("collect resource failed", "context", kubeContext, "resource", resource.Name, "error", err)
				cluster.Resources = append(cluster.Resources, entry)
				continue
			}
			capped, count, err := capList(data, opts.MaxItems)
			if err != nil {
				entry.Error = err.Error()
				logger.Warn("cap resource list failed", "context", kubeContext, "resource", resource.Name, "error", err)
				cluster.Resources = append(cluster.Resources, entry)
				continue
			}
			sanitized, err := sanitize.KubernetesJSON(capped, sanitize.KubernetesOptions{Salt: opts.Salt})
			if err != nil {
				entry.Error = err.Error()
				logger.Warn("sanitize resource failed", "context", kubeContext, "resource", resource.Name, "error", err)
				cluster.Resources = append(cluster.Resources, entry)
				continue
			}
			fileName := resourceFileName(resource.Name)
			filePath := filepath.Join(clusterDir, fileName)
			if err := os.WriteFile(filePath, sanitized, 0o600); err != nil {
				return manifest, err
			}
			entry.File = filepath.ToSlash(filepath.Join(clusterAlias, fileName))
			entry.Items = count
			logger.Debug("collect resource completed", "context", kubeContext, "resource", resource.Name, "items", count)
			cluster.Resources = append(cluster.Resources, entry)
		}
		logger.Info("collect context completed", "context", kubeContext, "resources", len(cluster.Resources))
		manifest.Clusters = append(manifest.Clusters, cluster)
	}

	manifestData, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return manifest, err
	}
	manifestData = append(manifestData, '\n')
	if err := os.WriteFile(filepath.Join(opts.OutputDir, "manifest.json"), manifestData, 0o600); err != nil {
		return manifest, err
	}
	logger.Info("collect samples completed", "contexts", len(manifest.Clusters), "outputDir", opts.OutputDir)
	return manifest, nil
}

func DiscoverResources(ctx context.Context, kubeContext string) ([]Resource, error) {
	return discoverResources(ctx, kubeContext, false)
}

func discoverResources(ctx context.Context, kubeContext string, useClientGo bool) ([]Resource, error) {
	if useClientGo {
		return DiscoverResourcesClientGo(ctx, kubeContext)
	}
	out, err := runKubectl(ctx,
		"--context", kubeContext,
		"api-resources",
		"--verbs=list",
		"-o", "wide",
		"--request-timeout=20s",
	)
	if err != nil {
		return nil, err
	}
	return parseAPIResourcesWide(out), nil
}

func parseAPIResourcesWide(data []byte) []Resource {
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) <= 1 {
		return nil
	}
	resources := make([]Resource, 0, len(lines)-1)
	for _, line := range lines[1:] {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		scopeIndex := -1
		for i := 1; i < len(fields); i++ {
			if fields[i] == "true" || fields[i] == "false" {
				scopeIndex = i
				break
			}
		}
		if scopeIndex < 2 || scopeIndex+1 >= len(fields) {
			continue
		}
		name := fields[0]
		apiVersion := fields[scopeIndex-1]
		namespaced := fields[scopeIndex] == "true"
		kind := fields[scopeIndex+1]
		verbs := fields[scopeIndex+2:]
		group, version := splitAPIVersion(apiVersion)
		commandName := name
		if group != "" {
			commandName = name + "." + group
		}
		resources = append(resources, Resource{
			Name:       commandName,
			Group:      group,
			Version:    version,
			Resource:   name,
			Kind:       kind,
			Namespaced: namespaced,
			Verbs:      verbs,
		})
	}
	return resources
}

func mergeResources(groups ...[]Resource) []Resource {
	seen := map[string]bool{}
	var out []Resource
	for _, group := range groups {
		for _, resource := range group {
			key := resource.Name + "|" + strconv.FormatBool(resource.Namespaced)
			if seen[key] {
				continue
			}
			if resource.Resource == "" {
				resource.Resource, resource.Group = splitResourceName(resource.Name)
			}
			seen[key] = true
			out = append(out, resource)
		}
	}
	return out
}

func getResource(ctx context.Context, kubeContext string, resource Resource) ([]byte, error) {
	return getResourceWithOptions(ctx, kubeContext, resource, false, "")
}

func getResourceWithOptions(ctx context.Context, kubeContext string, resource Resource, useClientGo bool, namespace string) ([]byte, error) {
	if useClientGo {
		return ListResourceClientGo(ctx, kubeContext, resource, namespace)
	}
	args := []string{"--context", kubeContext, "get", resource.Name, "-o", "json", "--request-timeout=20s"}
	if resource.Namespaced {
		scopeArgs := []string{"-A"}
		if namespace != "" {
			scopeArgs = []string{"-n", namespace}
		}
		args = append(args[:4], append(scopeArgs, args[4:]...)...)
	}
	return runKubectl(ctx, args...)
}

func configuredContexts(ctx context.Context, useClientGo bool) ([]string, error) {
	if useClientGo {
		return ConfiguredContextsClientGo()
	}
	return ConfiguredContexts(ctx)
}

func currentContext(ctx context.Context, useClientGo bool) (string, error) {
	if useClientGo {
		return CurrentContextClientGo()
	}
	return CurrentContext(ctx)
}

func runKubectl(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "kubectl", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("kubectl %s: %s", strings.Join(args, " "), msg)
	}
	return out, nil
}

func capList(data []byte, maxItems int) ([]byte, int, error) {
	var root map[string]any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&root); err != nil {
		return nil, 0, err
	}
	items, ok := root["items"].([]any)
	if !ok {
		out, err := json.Marshal(root)
		return out, 1, err
	}
	count := len(items)
	if maxItems > 0 && len(items) > maxItems {
		root["items"] = items[:maxItems]
		root["kubeInsightSample"] = map[string]any{
			"truncated":     true,
			"originalItems": json.Number(strconv.Itoa(count)),
			"retainedItems": json.Number(strconv.Itoa(maxItems)),
		}
		count = maxItems
	}
	out, err := json.Marshal(root)
	return out, count, err
}

func resourceFileName(resource string) string {
	slug := regexp.MustCompile(`[^A-Za-z0-9_.-]+`).ReplaceAllString(resource, "-")
	slug = strings.Trim(slug, "-")
	if slug == "" {
		slug = "resource"
	}
	return slug + ".json"
}

func splitAPIVersion(apiVersion string) (string, string) {
	if i := strings.IndexByte(apiVersion, '/'); i >= 0 {
		return apiVersion[:i], apiVersion[i+1:]
	}
	return "", apiVersion
}

func splitResourceName(name string) (string, string) {
	parts := strings.SplitN(name, ".", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return name, ""
}

func randomSalt() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf[:]), nil
}

func shortHash(salt, value string) string {
	sum := sha256.Sum256([]byte(salt + "\x00" + value))
	return hex.EncodeToString(sum[:])[:10]
}
