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
			name:     "policy report",
			info:     kubeapi.ResourceInfo{Group: "wgpolicyk8s.io", Version: "v1alpha2", Resource: "policyreports", Kind: "PolicyReport", Namespaced: true},
			profile:  "low_signal_report",
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

func TestSelectUsesSuppliedRules(t *testing.T) {
	got := Select(kubeapi.ResourceInfo{
		Group:    "example.com",
		Version:  "v1",
		Resource: "widgets",
		Kind:     "Widget",
	}, []Rule{
		{
			Name:      "widget_profile",
			Groups:    []string{"example.com"},
			Resources: []string{"widgets"},
			Profile: Profile{
				RetentionPolicy: "custom",
				ExtractorSet:    "widget",
				Priority:        "high",
			},
		},
	})

	if got.Name != "widget_profile" || got.RetentionPolicy != "custom" || got.ExtractorSet != "widget" || got.Priority != "high" {
		t.Fatalf("profile = %#v", got)
	}
}

func TestSelectMatchesQualifiedResourceNames(t *testing.T) {
	got := Select(kubeapi.ResourceInfo{
		Group:    "events.k8s.io",
		Version:  "v1",
		Resource: "events",
		Kind:     "Event",
	}, []Rule{
		{
			Name:      "qualified_event",
			Resources: []string{"events.events.k8s.io"},
			Profile:   Profile{RetentionPolicy: "custom"},
		},
	})

	if got.Name != "qualified_event" || got.RetentionPolicy != "custom" {
		t.Fatalf("profile = %#v", got)
	}
}

func TestSelectMatchesResourceGlob(t *testing.T) {
	got := Select(kubeapi.ResourceInfo{
		Group:    "reports.kyverno.io",
		Version:  "v1",
		Resource: "ephemeralreports",
		Kind:     "EphemeralReport",
	}, []Rule{
		{
			Name:      "report_profile",
			Resources: []string{"*.reports.kyverno.io"},
			Profile:   Profile{Priority: "low"},
		},
	})

	if got.Name != "report_profile" || got.Priority != "low" {
		t.Fatalf("profile = %#v", got)
	}
}

func TestDefaultRulesReturnsCopy(t *testing.T) {
	rules := DefaultRules()
	rules[0].Resources[0] = "not-pods"

	got := ForResource(kubeapi.ResourceInfo{Version: "v1", Resource: "pods", Kind: "Pod", Namespaced: true})
	if got.Name != "pod_fast_path" {
		t.Fatalf("profile = %#v", got)
	}
}

func TestDefaultRulesYAMLParses(t *testing.T) {
	rules, err := parseDefaultRules(defaultRulesYAML)
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != len(DefaultRules()) {
		t.Fatalf("rules = %d, want %d", len(rules), len(DefaultRules()))
	}
	for _, rule := range rules {
		if !isLowerSnakeName(rule.Name) {
			t.Fatalf("rule name %q is not lower_snake_case", rule.Name)
		}
	}
}

func isLowerSnakeName(name string) bool {
	if name == "" || name[0] < 'a' || name[0] > 'z' {
		return false
	}
	prevUnderscore := false
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
			prevUnderscore = false
		case r >= '0' && r <= '9':
			prevUnderscore = false
		case r == '_':
			if prevUnderscore {
				return false
			}
			prevUnderscore = true
		default:
			return false
		}
	}
	return !prevUnderscore
}
