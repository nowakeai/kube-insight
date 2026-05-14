package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"kube-insight/internal/collector"
	"kube-insight/internal/ingest"
	"kube-insight/internal/kubeapi"
	"kube-insight/internal/storage"
)

func ingestInputs(ctx context.Context, store storage.Store, file, dir string) (ingest.Summary, error) {
	if file != "" {
		data, err := os.ReadFile(file)
		if err != nil {
			return ingest.Summary{}, err
		}
		pipeline := ingest.DefaultPipeline(store)
		return pipeline.IngestJSON(ctx, data)
	}

	files, err := jsonFiles(dir)
	if err != nil {
		return ingest.Summary{}, err
	}
	var total ingest.Summary
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			return total, err
		}
		pipeline := ingest.DefaultPipeline(store)
		pipeline.ClusterID = clusterIDForPath(dir, path)
		summary, err := pipeline.IngestJSON(ctx, data)
		if err != nil {
			return total, fmt.Errorf("%s: %w", path, err)
		}
		total.Observations += summary.Observations
		total.StoredObservations += summary.StoredObservations
		total.ModifiedObservations += summary.ModifiedObservations
		total.DiscardedChanges += summary.DiscardedChanges
		total.DiscardedResources += summary.DiscardedResources
		total.Facts += summary.Facts
		total.Edges += summary.Edges
		total.Changes += summary.Changes
	}
	return total, nil
}

func jsonFiles(dir string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if filepath.Base(path) == "manifest.json" {
			return nil
		}
		if filepath.Ext(path) == ".json" {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	if len(files) == 0 {
		return nil, fmt.Errorf("no JSON files found in %s", dir)
	}
	return files, nil
}

func clusterIDForPath(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return "local"
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) > 1 && parts[0] != "" {
		return parts[0]
	}
	base := filepath.Base(root)
	if strings.HasPrefix(base, "cluster-") {
		return base
	}
	return "local"
}

func collectContexts(ctx context.Context, opts collector.SampleOptions) ([]string, error) {
	if opts.AllContexts {
		return configuredContexts(ctx, opts.UseClientGo)
	}
	if len(opts.Contexts) > 0 {
		return opts.Contexts, nil
	}
	current, err := currentContext(ctx, opts.UseClientGo)
	if err != nil {
		return nil, err
	}
	return []string{current}, nil
}

func discoverAndStoreAPIResources(ctx context.Context, store storage.APIResourceStore, contexts []string, useClientGo bool) ([]collector.Resource, error) {
	seen := map[string]collector.Resource{}
	for _, kubeContext := range contexts {
		resources, err := discoverResources(ctx, kubeContext, useClientGo)
		if err != nil {
			return nil, err
		}
		if err := store.UpsertAPIResources(ctx, resourcesToAPIInfos(resources), time.Now().UTC()); err != nil {
			return nil, err
		}
		for _, resource := range resources {
			seen[collectorResourceKey(resource)] = resource
		}
	}
	out := resourcesFromMap(seen)
	sort.Slice(out, func(i, j int) bool { return collectorResourceKey(out[i]) < collectorResourceKey(out[j]) })
	return out, nil
}

func discoverResources(ctx context.Context, kubeContext string, useClientGo bool) ([]collector.Resource, error) {
	if useClientGo {
		return collector.DiscoverResourcesClientGo(ctx, kubeContext)
	}
	return collector.DiscoverResources(ctx, kubeContext)
}

func configuredContexts(ctx context.Context, useClientGo bool) ([]string, error) {
	if useClientGo {
		return collector.ConfiguredContextsClientGo()
	}
	return collector.ConfiguredContexts(ctx)
}

func currentContext(ctx context.Context, useClientGo bool) (string, error) {
	if useClientGo {
		return collector.CurrentContextClientGo()
	}
	return collector.CurrentContext(ctx)
}

func resourcesToAPIInfos(resources []collector.Resource) []kubeapi.ResourceInfo {
	out := make([]kubeapi.ResourceInfo, 0, len(resources))
	for _, resource := range resources {
		if resource.Resource == "" || resource.Kind == "" {
			continue
		}
		out = append(out, kubeapi.ResourceInfo{
			Group:      resource.Group,
			Version:    resource.Version,
			Resource:   resource.Resource,
			Kind:       resource.Kind,
			Namespaced: resource.Namespaced,
			Verbs:      resource.Verbs,
		})
	}
	return out
}

func apiInfosToResources(infos []kubeapi.ResourceInfo) []collector.Resource {
	out := make([]collector.Resource, 0, len(infos))
	for _, info := range infos {
		if info.Resource == "" {
			continue
		}
		if len(info.Verbs) > 0 && !hasVerb(info.Verbs, "list") {
			continue
		}
		name := info.Resource
		if info.Group != "" {
			name += "." + info.Group
		}
		out = append(out, collector.Resource{
			Name:       name,
			Group:      info.Group,
			Version:    info.Version,
			Resource:   info.Resource,
			Kind:       info.Kind,
			Namespaced: info.Namespaced,
			Verbs:      info.Verbs,
		})
	}
	sort.Slice(out, func(i, j int) bool { return collectorResourceKey(out[i]) < collectorResourceKey(out[j]) })
	return out
}

func enrichCollectorResources(base, metadata []collector.Resource) []collector.Resource {
	byKey := map[string]collector.Resource{}
	for _, resource := range metadata {
		byKey[collectorResourceKey(resource)] = resource
	}
	out := make([]collector.Resource, len(base))
	for i, resource := range base {
		if enriched, ok := byKey[collectorResourceKey(resource)]; ok {
			out[i] = enriched
			continue
		}
		out[i] = resource
	}
	return out
}

func mergeCollectorResources(groups ...[]collector.Resource) []collector.Resource {
	seen := map[string]bool{}
	var out []collector.Resource
	for _, group := range groups {
		for _, resource := range group {
			key := collectorResourceKey(resource)
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, resource)
		}
	}
	return out
}

func collectorResourceKey(resource collector.Resource) string {
	return resource.Name + "\x00" + strconv.FormatBool(resource.Namespaced)
}

func hasVerb(verbs []string, want string) bool {
	for _, verb := range verbs {
		if strings.Trim(verb, "[],") == want {
			return true
		}
	}
	return false
}

func parseInvestigationTime(value string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return t, nil
	}
	if t, err := time.Parse("2006-01-02", value); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("expected RFC3339 timestamp or YYYY-MM-DD date")
}
