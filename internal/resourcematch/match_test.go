package resourcematch

import "testing"

func TestMatchResourceFormsAndGlobs(t *testing.T) {
	resource := Resource{
		Group:    "reports.kyverno.io",
		Version:  "v1",
		Resource: "ephemeralreports",
		Kind:     "EphemeralReport",
	}
	for _, pattern := range []string{
		"ephemeralreports",
		"ephemeralreports.reports.kyverno.io",
		"reports.kyverno.io/v1/ephemeralreports",
		"*.reports.kyverno.io",
		"reports.kyverno.io/*/*",
		"*/v1/ephemeral*",
	} {
		if !MatchResource(pattern, resource) {
			t.Fatalf("pattern %q did not match", pattern)
		}
	}
	if MatchResource("policyreports.wgpolicyk8s.io", resource) {
		t.Fatalf("unexpected match")
	}
}

func TestMatchAnyStringSupportsGlob(t *testing.T) {
	if !MatchAnyString([]string{"*.cert-manager.io"}, "acme.cert-manager.io") {
		t.Fatalf("group glob did not match")
	}
}
