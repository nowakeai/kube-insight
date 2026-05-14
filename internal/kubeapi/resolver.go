package kubeapi

import (
	"strings"

	"kube-insight/internal/core"
)

type ResourceInfo struct {
	Group      string
	Version    string
	Resource   string
	Kind       string
	Namespaced bool
	Verbs      []string
}

type Resolver struct {
	byGVK           map[string]ResourceInfo
	byGroupKind     map[string]ResourceInfo
	byKind          map[string]ResourceInfo
	byGVR           map[string]ResourceInfo
	byGroupResource map[string]ResourceInfo
	byResource      map[string]ResourceInfo
}

func NewResolver() *Resolver {
	r := &Resolver{
		byGVK:           map[string]ResourceInfo{},
		byGroupKind:     map[string]ResourceInfo{},
		byKind:          map[string]ResourceInfo{},
		byGVR:           map[string]ResourceInfo{},
		byGroupResource: map[string]ResourceInfo{},
		byResource:      map[string]ResourceInfo{},
	}
	for _, info := range standardResources() {
		r.Register(info)
	}
	return r
}

func (r *Resolver) Register(info ResourceInfo) {
	if r == nil || info.Kind == "" || info.Resource == "" {
		return
	}
	info.Resource = strings.ToLower(info.Resource)
	r.byGVK[gvkKey(info.Group, info.Version, info.Kind)] = info
	r.byGroupKind[groupKindKey(info.Group, info.Kind)] = info
	if _, exists := r.byKind[info.Kind]; !exists || info.Group == "" {
		r.byKind[info.Kind] = info
	}
	r.byGVR[gvrKey(info.Group, info.Version, info.Resource)] = info
	r.byGroupResource[groupResourceKey(info.Group, info.Resource)] = info
	if _, exists := r.byResource[info.Resource]; !exists || info.Group == "" {
		r.byResource[info.Resource] = info
	}
}

func (r *Resolver) RegisterFromObject(obj map[string]any) {
	if r == nil {
		return
	}
	apiVersion, _ := obj["apiVersion"].(string)
	kind, _ := obj["kind"].(string)
	if kind == "CustomResourceDefinition" {
		r.RegisterCRD(obj)
		return
	}
	group, version := SplitAPIVersion(apiVersion)
	if info, ok := r.ResolveGVK(group, version, kind); ok {
		r.Register(info)
	}
}

func (r *Resolver) RegisterCRD(obj map[string]any) {
	spec, _ := obj["spec"].(map[string]any)
	group, _ := spec["group"].(string)
	names, _ := spec["names"].(map[string]any)
	kind, _ := names["kind"].(string)
	plural, _ := names["plural"].(string)
	scope, _ := spec["scope"].(string)
	if group == "" || kind == "" || plural == "" {
		return
	}
	namespaced := scope != "Cluster"
	versions, _ := spec["versions"].([]any)
	for _, item := range versions {
		versionMap, ok := item.(map[string]any)
		if !ok {
			continue
		}
		version, _ := versionMap["name"].(string)
		if version == "" {
			continue
		}
		r.Register(ResourceInfo{
			Group:      group,
			Version:    version,
			Resource:   plural,
			Kind:       kind,
			Namespaced: namespaced,
		})
	}
	if len(versions) == 0 {
		version, _ := spec["version"].(string)
		if version != "" {
			r.Register(ResourceInfo{Group: group, Version: version, Resource: plural, Kind: kind, Namespaced: namespaced})
		}
	}
}

func (r *Resolver) ResourceRefFromObject(clusterID string, obj map[string]any) core.ResourceRef {
	apiVersion, _ := obj["apiVersion"].(string)
	kind, _ := obj["kind"].(string)
	group, version := SplitAPIVersion(apiVersion)
	info, _ := r.ResolveGVK(group, version, kind)

	metadata, _ := obj["metadata"].(map[string]any)
	name, _ := metadata["name"].(string)
	namespace, _ := metadata["namespace"].(string)
	uid, _ := metadata["uid"].(string)

	return core.ResourceRef{
		ClusterID: clusterID,
		Group:     group,
		Version:   version,
		Resource:  info.Resource,
		Kind:      kind,
		Namespace: namespace,
		Name:      name,
		UID:       uid,
	}
}

func (r *Resolver) ResolveGVK(group, version, kind string) (ResourceInfo, bool) {
	if r == nil {
		return fallbackInfo(group, version, "", kind), false
	}
	if info, ok := r.byGVK[gvkKey(group, version, kind)]; ok {
		return info, true
	}
	if info, ok := r.byGroupKind[groupKindKey(group, kind)]; ok {
		if version != "" {
			info.Version = version
		}
		return info, true
	}
	if group == "" {
		if info, ok := r.byKind[kind]; ok {
			if version != "" {
				info.Version = version
			}
			return info, true
		}
	}
	return fallbackInfo(group, version, "", kind), false
}

func (r *Resolver) ResolveGVR(group, version, resource string) (ResourceInfo, bool) {
	resource = strings.ToLower(resource)
	if r == nil {
		return fallbackInfo(group, version, resource, ""), false
	}
	if info, ok := r.byGVR[gvrKey(group, version, resource)]; ok {
		return info, true
	}
	if info, ok := r.byGroupResource[groupResourceKey(group, resource)]; ok {
		if version != "" {
			info.Version = version
		}
		return info, true
	}
	if group == "" {
		if info, ok := r.byResource[resource]; ok {
			if version != "" {
				info.Version = version
			}
			return info, true
		}
	}
	return fallbackInfo(group, version, resource, ""), false
}

func (r *Resolver) ResolveTarget(group, version, kind, resource, namespace, name string) (core.ResourceRef, bool) {
	if group == "" && version != "" && strings.Contains(version, "/") {
		group, version = SplitAPIVersion(version)
	}
	var info ResourceInfo
	var ok bool
	if resource != "" {
		info, ok = r.ResolveGVR(group, version, resource)
	} else {
		info, ok = r.ResolveGVK(group, version, kind)
	}
	if namespace == "" && info.Namespaced {
		namespace = ""
	}
	return core.ResourceRef{
		Group:     info.Group,
		Version:   info.Version,
		Resource:  info.Resource,
		Kind:      info.Kind,
		Namespace: namespace,
		Name:      name,
	}, ok
}

func (r *Resolver) IsNamespacedKind(kind string) bool {
	info, ok := r.ResolveGVK("", "", kind)
	return !ok || info.Namespaced
}

func SplitAPIVersion(apiVersion string) (string, string) {
	if apiVersion == "" {
		return "", ""
	}
	if i := strings.IndexByte(apiVersion, '/'); i >= 0 {
		return apiVersion[:i], apiVersion[i+1:]
	}
	return "", apiVersion
}

func LogicalPath(ref core.ResourceRef) string {
	prefix := ref.Resource
	if ref.Group != "" {
		prefix = ref.Group + "/" + ref.Resource
	}
	if ref.Namespace != "" {
		return prefix + "/" + ref.Namespace + "/" + ref.Name
	}
	return prefix + "/" + ref.Name
}

func fallbackInfo(group, version, resource, kind string) ResourceInfo {
	if resource == "" {
		resource = fallbackResource(kind)
	}
	if kind == "" {
		kind = fallbackKind(resource)
	}
	if version == "" {
		version = "v1"
	}
	namespaced := true
	if group == "" {
		namespaced = !clusterScopedKind(kind)
	}
	return ResourceInfo{Group: group, Version: version, Resource: resource, Kind: kind, Namespaced: namespaced}
}

func fallbackResource(kind string) string {
	if kind == "" {
		return ""
	}
	base := strings.ToLower(kind)
	switch {
	case strings.HasSuffix(base, "y"):
		return strings.TrimSuffix(base, "y") + "ies"
	case strings.HasSuffix(base, "s"), strings.HasSuffix(base, "x"), strings.HasSuffix(base, "ch"), strings.HasSuffix(base, "sh"):
		return base + "es"
	default:
		return base + "s"
	}
}

func fallbackKind(resource string) string {
	if resource == "" {
		return ""
	}
	resource = strings.TrimSuffix(resource, "s")
	return strings.ToUpper(resource[:1]) + resource[1:]
}

func clusterScopedKind(kind string) bool {
	switch kind {
	case "APIService", "ClusterIssuer", "ClusterRole", "ClusterRoleBinding", "CustomResourceDefinition", "GatewayClass", "Group", "MutatingWebhookConfiguration", "Namespace", "Node", "PersistentVolume", "PriorityClass", "StorageClass", "User", "ValidatingWebhookConfiguration":
		return true
	default:
		return false
	}
}

func gvkKey(group, version, kind string) string {
	return group + "\x00" + version + "\x00" + kind
}

func groupKindKey(group, kind string) string {
	return group + "\x00" + kind
}

func gvrKey(group, version, resource string) string {
	return group + "\x00" + version + "\x00" + resource
}

func groupResourceKey(group, resource string) string {
	return group + "\x00" + resource
}

func standardResources() []ResourceInfo {
	return []ResourceInfo{
		{Group: "", Version: "v1", Resource: "configmaps", Kind: "ConfigMap", Namespaced: true},
		{Group: "", Version: "v1", Resource: "endpoints", Kind: "Endpoints", Namespaced: true},
		{Group: "", Version: "v1", Resource: "events", Kind: "Event", Namespaced: true},
		{Group: "", Version: "v1", Resource: "namespaces", Kind: "Namespace"},
		{Group: "", Version: "v1", Resource: "nodes", Kind: "Node"},
		{Group: "", Version: "v1", Resource: "persistentvolumeclaims", Kind: "PersistentVolumeClaim", Namespaced: true},
		{Group: "", Version: "v1", Resource: "persistentvolumes", Kind: "PersistentVolume"},
		{Group: "", Version: "v1", Resource: "pods", Kind: "Pod", Namespaced: true},
		{Group: "", Version: "v1", Resource: "secrets", Kind: "Secret", Namespaced: true},
		{Group: "", Version: "v1", Resource: "services", Kind: "Service", Namespaced: true},
		{Group: "", Version: "v1", Resource: "serviceaccounts", Kind: "ServiceAccount", Namespaced: true},
		{Group: "admissionregistration.k8s.io", Version: "v1", Resource: "mutatingwebhookconfigurations", Kind: "MutatingWebhookConfiguration"},
		{Group: "admissionregistration.k8s.io", Version: "v1", Resource: "validatingwebhookconfigurations", Kind: "ValidatingWebhookConfiguration"},
		{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions", Kind: "CustomResourceDefinition"},
		{Group: "apiregistration.k8s.io", Version: "v1", Resource: "apiservices", Kind: "APIService"},
		{Group: "apps", Version: "v1", Resource: "daemonsets", Kind: "DaemonSet", Namespaced: true},
		{Group: "apps", Version: "v1", Resource: "deployments", Kind: "Deployment", Namespaced: true},
		{Group: "apps", Version: "v1", Resource: "replicasets", Kind: "ReplicaSet", Namespaced: true},
		{Group: "apps", Version: "v1", Resource: "statefulsets", Kind: "StatefulSet", Namespaced: true},
		{Group: "autoscaling", Version: "v2", Resource: "horizontalpodautoscalers", Kind: "HorizontalPodAutoscaler", Namespaced: true},
		{Group: "batch", Version: "v1", Resource: "cronjobs", Kind: "CronJob", Namespaced: true},
		{Group: "batch", Version: "v1", Resource: "jobs", Kind: "Job", Namespaced: true},
		{Group: "cert-manager.io", Version: "v1", Resource: "certificates", Kind: "Certificate", Namespaced: true},
		{Group: "cert-manager.io", Version: "v1", Resource: "certificaterequests", Kind: "CertificateRequest", Namespaced: true},
		{Group: "cert-manager.io", Version: "v1", Resource: "clusterissuers", Kind: "ClusterIssuer"},
		{Group: "cert-manager.io", Version: "v1", Resource: "issuers", Kind: "Issuer", Namespaced: true},
		{Group: "acme.cert-manager.io", Version: "v1", Resource: "challenges", Kind: "Challenge", Namespaced: true},
		{Group: "acme.cert-manager.io", Version: "v1", Resource: "orders", Kind: "Order", Namespaced: true},
		{Group: "discovery.k8s.io", Version: "v1", Resource: "endpointslices", Kind: "EndpointSlice", Namespaced: true},
		{Group: "events.k8s.io", Version: "v1", Resource: "events", Kind: "Event", Namespaced: true},
		{Group: "gateway.networking.k8s.io", Version: "v1", Resource: "gatewayclasses", Kind: "GatewayClass"},
		{Group: "gateway.networking.k8s.io", Version: "v1", Resource: "gateways", Kind: "Gateway", Namespaced: true},
		{Group: "gateway.networking.k8s.io", Version: "v1", Resource: "grpcroutes", Kind: "GRPCRoute", Namespaced: true},
		{Group: "gateway.networking.k8s.io", Version: "v1", Resource: "httproutes", Kind: "HTTPRoute", Namespaced: true},
		{Group: "gateway.networking.k8s.io", Version: "v1", Resource: "tcproutes", Kind: "TCPRoute", Namespaced: true},
		{Group: "gateway.networking.k8s.io", Version: "v1", Resource: "tlsroutes", Kind: "TLSRoute", Namespaced: true},
		{Group: "gateway.networking.k8s.io", Version: "v1", Resource: "udproutes", Kind: "UDPRoute", Namespaced: true},
		{Group: "networking.k8s.io", Version: "v1", Resource: "ingresses", Kind: "Ingress", Namespaced: true},
		{Group: "networking.k8s.io", Version: "v1", Resource: "networkpolicies", Kind: "NetworkPolicy", Namespaced: true},
		{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterrolebindings", Kind: "ClusterRoleBinding"},
		{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterroles", Kind: "ClusterRole"},
		{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "groups", Kind: "Group"},
		{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "rolebindings", Kind: "RoleBinding", Namespaced: true},
		{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "roles", Kind: "Role", Namespaced: true},
		{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "users", Kind: "User"},
		{Group: "scheduling.k8s.io", Version: "v1", Resource: "priorityclasses", Kind: "PriorityClass"},
		{Group: "storage.k8s.io", Version: "v1", Resource: "storageclasses", Kind: "StorageClass"},
	}
}
