package kubeapi

import "testing"

func TestResolverUsesDiscoveredResourceForCRDPluralAndScope(t *testing.T) {
	resolver := NewResolver()
	resolver.Register(ResourceInfo{
		Group:      "example.com",
		Version:    "v1",
		Resource:   "indices",
		Kind:       "Index",
		Namespaced: true,
	})

	info, ok := resolver.ResolveGVK("example.com", "v1", "Index")
	if !ok {
		t.Fatal("expected discovered resource match")
	}
	if info.Resource != "indices" || !info.Namespaced {
		t.Fatalf("info = %#v", info)
	}
}

func TestResolverDoesNotApplyKindOnlySeedAcrossGroups(t *testing.T) {
	resolver := NewResolver()

	info, ok := resolver.ResolveGVK("example.com", "v1", "GatewayClass")
	if ok {
		t.Fatalf("unexpected cross-group seed match: %#v", info)
	}
	if info.Group != "example.com" || info.Resource != "gatewayclasses" || !info.Namespaced {
		t.Fatalf("fallback info = %#v", info)
	}
}

func TestResolverDoesNotApplyResourceOnlySeedAcrossGroups(t *testing.T) {
	resolver := NewResolver()

	info, ok := resolver.ResolveGVR("example.com", "v1", "services")
	if ok {
		t.Fatalf("unexpected cross-group seed match: %#v", info)
	}
	if info.Group != "example.com" || info.Kind != "Service" || !info.Namespaced {
		t.Fatalf("fallback info = %#v", info)
	}
}

func TestResolverStillUsesCoreKindOnlySeed(t *testing.T) {
	resolver := NewResolver()

	info, ok := resolver.ResolveGVK("", "v1", "Pod")
	if !ok {
		t.Fatal("expected core seed match")
	}
	if info.Resource != "pods" || !info.Namespaced {
		t.Fatalf("info = %#v", info)
	}
}
