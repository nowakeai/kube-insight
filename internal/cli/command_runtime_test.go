package cli

import (
	"testing"

	"kube-insight/internal/collector"
)

func TestApplyResourceExcludesSupportsGlob(t *testing.T) {
	resources := []collector.Resource{
		{Name: "pods", Version: "v1", Resource: "pods", Kind: "Pod"},
		{Name: "ephemeralreports.reports.kyverno.io", Group: "reports.kyverno.io", Version: "v1", Resource: "ephemeralreports", Kind: "EphemeralReport"},
		{Name: "certificates.cert-manager.io", Group: "cert-manager.io", Version: "v1", Resource: "certificates", Kind: "Certificate"},
	}

	got := applyResourceExcludes(resources, []string{"*.reports.kyverno.io", "cert-manager.io/*/*"})
	if len(got) != 1 || got[0].Name != "pods" {
		t.Fatalf("resources = %#v", got)
	}
}
