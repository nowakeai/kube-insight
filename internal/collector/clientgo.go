package collector

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

type ClusterIdentity struct {
	ID      string
	UID     string
	Context string
	Server  string
}

func ConfiguredContextsClientGo() ([]string, error) {
	config, err := loadClientConfig()
	if err != nil {
		return nil, err
	}
	contexts := make([]string, 0, len(config.Contexts))
	for name := range config.Contexts {
		contexts = append(contexts, name)
	}
	sort.Strings(contexts)
	return contexts, nil
}

func CurrentContextClientGo() (string, error) {
	config, err := loadClientConfig()
	if err != nil {
		return "", err
	}
	if config.CurrentContext == "" {
		return "", fmt.Errorf("current kubeconfig context is empty")
	}
	return config.CurrentContext, nil
}

func ResolveClusterIdentityClientGo(ctx context.Context, kubeContext string) (ClusterIdentity, error) {
	config, err := loadClientConfig()
	if err != nil {
		return ClusterIdentity{}, err
	}
	if kubeContext == "" {
		kubeContext = config.CurrentContext
	}
	if kubeContext == "" {
		return ClusterIdentity{}, fmt.Errorf("current kubeconfig context is empty")
	}
	identity := ClusterIdentity{Context: kubeContext}
	if contextConfig := config.Contexts[kubeContext]; contextConfig != nil {
		if clusterConfig := config.Clusters[contextConfig.Cluster]; clusterConfig != nil {
			identity.Server = clusterConfig.Server
		}
	}

	rest, err := restConfig(kubeContext)
	if err != nil {
		return ClusterIdentity{}, err
	}
	client, err := kubernetes.NewForConfig(rest)
	if err != nil {
		return ClusterIdentity{}, err
	}
	namespace, err := client.CoreV1().Namespaces().Get(ctx, "kube-system", metav1.GetOptions{})
	if err == nil && namespace.UID != "" {
		identity.UID = string(namespace.UID)
		identity.ID = "k8s-" + identity.UID
		return identity, nil
	}
	if identity.Server != "" {
		identity.ID = "server-" + clusterShortHash(identity.Server)
		return identity, nil
	}
	identity.ID = "context-" + clusterShortHash(kubeContext)
	return identity, nil
}

func clusterShortHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return fmt.Sprintf("%x", sum[:6])
}

func DiscoverResourcesClientGo(ctx context.Context, kubeContext string) ([]Resource, error) {
	config, err := restConfig(kubeContext)
	if err != nil {
		return nil, err
	}
	client, err := discovery.NewDiscoveryClientForConfig(config)
	if err != nil {
		return nil, err
	}
	lists, err := client.ServerPreferredResources()
	if err != nil {
		if groupErr, ok := err.(*discovery.ErrGroupDiscoveryFailed); ok {
			err = errors.NewAggregate(mapDiscoveryErrors(groupErr.Groups))
		}
		if len(lists) == 0 {
			return nil, err
		}
	}
	resources := resourcesFromAPIResourceLists(lists)
	if err != nil {
		return resources, err
	}
	return resources, nil
}

func ListResourceClientGo(ctx context.Context, kubeContext string, resource Resource, namespace string) ([]byte, error) {
	config, err := restConfig(kubeContext)
	if err != nil {
		return nil, err
	}
	resolved, err := resolveResourceClientGo(ctx, kubeContext, resource)
	if err != nil {
		return nil, err
	}
	client, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, err
	}
	gvr := schema.GroupVersionResource{
		Group:    resolved.Group,
		Version:  resolved.Version,
		Resource: resolved.Resource,
	}
	listOptions := metav1.ListOptions{}
	var list any
	if resolved.Namespaced {
		if namespace == "" {
			namespace = metav1.NamespaceAll
		}
		list, err = client.Resource(gvr).Namespace(namespace).List(ctx, listOptions)
	} else {
		list, err = client.Resource(gvr).List(ctx, listOptions)
	}
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(list)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func resolveResourceClientGo(ctx context.Context, kubeContext string, resource Resource) (Resource, error) {
	if resource.Resource != "" && resource.Version != "" {
		return resource, nil
	}
	resources, err := DiscoverResourcesClientGo(ctx, kubeContext)
	if err != nil && len(resources) == 0 {
		return Resource{}, err
	}
	for _, candidate := range resources {
		if resourceMatches(candidate, resource) {
			return candidate, nil
		}
	}
	if resource.Resource == "" {
		resource.Resource, resource.Group = splitResourceName(resource.Name)
	}
	if resource.Version == "" {
		return Resource{}, fmt.Errorf("resource %q needs discovery metadata for client-go list", resource.Name)
	}
	return resource, nil
}

func resourcesFromAPIResourceLists(lists []*metav1.APIResourceList) []Resource {
	var resources []Resource
	for _, list := range lists {
		groupVersion, err := schema.ParseGroupVersion(list.GroupVersion)
		if err != nil {
			continue
		}
		for _, item := range list.APIResources {
			if item.Name == "" || item.Kind == "" || strings.Contains(item.Name, "/") || !hasVerb(item.Verbs, "list") {
				continue
			}
			name := item.Name
			if groupVersion.Group != "" {
				name += "." + groupVersion.Group
			}
			resources = append(resources, Resource{
				Name:       name,
				Group:      groupVersion.Group,
				Version:    groupVersion.Version,
				Resource:   item.Name,
				Kind:       item.Kind,
				Namespaced: item.Namespaced,
				Verbs:      append([]string(nil), item.Verbs...),
			})
		}
	}
	sort.Slice(resources, func(i, j int) bool {
		if resources[i].Name == resources[j].Name {
			return !resources[i].Namespaced && resources[j].Namespaced
		}
		return resources[i].Name < resources[j].Name
	})
	return resources
}

func resourceMatches(candidate, target Resource) bool {
	if candidate.Namespaced != target.Namespaced {
		return false
	}
	if target.Resource != "" && candidate.Resource == target.Resource && candidate.Group == target.Group {
		return true
	}
	return candidate.Name == target.Name
}

func restConfig(kubeContext string) (*rest.Config, error) {
	return restConfigWithTimeout(kubeContext, 20*time.Second)
}

func watchRestConfig(kubeContext string) (*rest.Config, error) {
	return restConfigWithTimeout(kubeContext, 0)
}

func restConfigWithTimeout(kubeContext string, timeout time.Duration) (*rest.Config, error) {
	overrides := &clientcmd.ConfigOverrides{CurrentContext: kubeContext}
	config, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		clientcmd.NewDefaultClientConfigLoadingRules(),
		overrides,
	).ClientConfig()
	if err != nil {
		return nil, err
	}
	config.UserAgent = "kube-insight"
	config.QPS = 20
	config.Burst = 40
	config.Timeout = timeout
	return config, nil
}

func loadClientConfig() (*clientcmdapi.Config, error) {
	config, err := clientcmd.NewDefaultClientConfigLoadingRules().Load()
	if err != nil {
		return nil, err
	}
	return config, nil
}

func mapDiscoveryErrors(in map[schema.GroupVersion]error) []error {
	out := make([]error, 0, len(in))
	for groupVersion, err := range in {
		out = append(out, fmt.Errorf("%s: %w", groupVersion.String(), err))
	}
	return out
}

func hasVerb(verbs []string, want string) bool {
	for _, verb := range verbs {
		if strings.Trim(verb, "[],") == want {
			return true
		}
	}
	return false
}
