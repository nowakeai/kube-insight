package resourceprofile

import (
	"testing"

	"kube-insight/internal/kubeapi"
)

func TestForResourceSelectsKnownProfiles(t *testing.T) {
	cases := []struct {
		name     string
		info     kubeapi.ResourceInfo
		profile  string
		enabled  bool
		priority string
	}{
		{
			name:     "pod",
			info:     kubeapi.ResourceInfo{Version: "v1", Resource: "pods", Kind: "Pod", Namespaced: true},
			profile:  "pod_fast_path",
			enabled:  true,
			priority: "high",
		},
		{
			name:     "event",
			info:     kubeapi.ResourceInfo{Group: "events.k8s.io", Version: "v1", Resource: "events", Kind: "Event", Namespaced: true},
			profile:  "event_rollup",
			enabled:  true,
			priority: "high",
		},
		{
			name:     "lease",
			info:     kubeapi.ResourceInfo{Group: "coordination.k8s.io", Version: "v1", Resource: "leases", Kind: "Lease", Namespaced: true},
			profile:  "lease_skip_or_downsample",
			enabled:  false,
			priority: "low",
		},
		{
			name:     "custom",
			info:     kubeapi.ResourceInfo{Group: "example.com", Version: "v1", Resource: "widgets", Kind: "Widget", Namespaced: true},
			profile:  "generic",
			enabled:  true,
			priority: "normal",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ForResource(tc.info)
			if got.Name != tc.profile || got.Enabled != tc.enabled || got.Priority != tc.priority {
				t.Fatalf("profile = %#v", got)
			}
		})
	}
}
