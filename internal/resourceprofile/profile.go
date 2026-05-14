package resourceprofile

import (
	"strings"

	"kube-insight/internal/kubeapi"
)

type Profile struct {
	Name               string
	RetentionClass     string
	FilterChain        string
	ExtractorSet       string
	CompactionStrategy string
	Priority           string
	MaxEventBuffer     int
	Enabled            bool
}

func ForResource(info kubeapi.ResourceInfo) Profile {
	resource := strings.ToLower(info.Resource)
	group := strings.ToLower(info.Group)
	kind := strings.ToLower(info.Kind)
	profile := Profile{
		Name:               "generic",
		RetentionClass:     "standard",
		FilterChain:        "default",
		ExtractorSet:       "generic",
		CompactionStrategy: "full_json",
		Priority:           "normal",
		MaxEventBuffer:     256,
		Enabled:            true,
	}

	switch {
	case resource == "pods" && group == "":
		profile.Name = "pod_fast_path"
		profile.RetentionClass = "hot"
		profile.ExtractorSet = "pod"
		profile.CompactionStrategy = "status_aware"
		profile.Priority = "high"
		profile.MaxEventBuffer = 1024
	case resource == "nodes" && group == "":
		profile.Name = "node_summary"
		profile.RetentionClass = "large_status"
		profile.ExtractorSet = "node"
		profile.CompactionStrategy = "status_summary"
		profile.Priority = "high"
	case kind == "event" || resource == "events":
		profile.Name = "event_rollup"
		profile.RetentionClass = "churn"
		profile.ExtractorSet = "event"
		profile.CompactionStrategy = "rollup"
		profile.Priority = "high"
		profile.MaxEventBuffer = 2048
	case resource == "endpointslices" && group == "discovery.k8s.io":
		profile.Name = "endpointslice_topology"
		profile.RetentionClass = "topology"
		profile.ExtractorSet = "endpointslice"
		profile.CompactionStrategy = "membership_delta"
		profile.Priority = "high"
	case resource == "leases" && group == "coordination.k8s.io":
		profile.Name = "lease_skip_or_downsample"
		profile.RetentionClass = "ephemeral"
		profile.FilterChain = "lease_skip"
		profile.ExtractorSet = "none"
		profile.CompactionStrategy = "downsample"
		profile.Priority = "low"
		profile.MaxEventBuffer = 64
		profile.Enabled = false
	case resource == "secrets" && group == "":
		profile.Name = "secret_metadata_only"
		profile.RetentionClass = "sensitive_metadata"
		profile.FilterChain = "secret_metadata_only"
		profile.ExtractorSet = "secret_metadata"
	case isWorkloadResource(group, resource):
		profile.Name = "workload_rollout"
		profile.RetentionClass = "workload"
		profile.ExtractorSet = "workload"
		profile.CompactionStrategy = "spec_status_delta"
		profile.Priority = "high"
	case isServiceTopologyResource(group, resource):
		profile.Name = "service_topology"
		profile.RetentionClass = "topology"
		profile.ExtractorSet = "service_topology"
	case isAdmissionWebhookResource(group, resource):
		profile.Name = "admission_webhook"
		profile.RetentionClass = "control_plane"
		profile.ExtractorSet = "webhook"
	case isRBACResource(group, resource):
		profile.Name = "rbac"
		profile.RetentionClass = "security"
		profile.ExtractorSet = "rbac"
	case group == "cert-manager.io" || group == "acme.cert-manager.io":
		profile.Name = "certmanager"
		profile.RetentionClass = "certificate_lifecycle"
		profile.ExtractorSet = "certmanager"
	case resource == "customresourcedefinitions" && group == "apiextensions.k8s.io":
		profile.Name = "crd_definition"
		profile.RetentionClass = "api_definition"
		profile.ExtractorSet = "crd"
	}
	return profile
}

func isWorkloadResource(group, resource string) bool {
	if group == "apps" {
		switch resource {
		case "deployments", "replicasets", "statefulsets", "daemonsets":
			return true
		}
	}
	if group == "batch" {
		return resource == "jobs" || resource == "cronjobs"
	}
	return false
}

func isServiceTopologyResource(group, resource string) bool {
	if group == "" {
		return resource == "services" || resource == "endpoints" || resource == "persistentvolumeclaims" || resource == "persistentvolumes"
	}
	if group == "networking.k8s.io" {
		return resource == "ingresses" || resource == "networkpolicies"
	}
	if group == "gateway.networking.k8s.io" {
		return strings.Contains(resource, "gateway") || strings.Contains(resource, "route")
	}
	return false
}

func isAdmissionWebhookResource(group, resource string) bool {
	return group == "admissionregistration.k8s.io" &&
		(resource == "mutatingwebhookconfigurations" || resource == "validatingwebhookconfigurations" || resource == "validatingadmissionpolicies" || resource == "validatingadmissionpolicybindings")
}

func isRBACResource(group, resource string) bool {
	return group == "rbac.authorization.k8s.io" &&
		(resource == "roles" || resource == "rolebindings" || resource == "clusterroles" || resource == "clusterrolebindings")
}
