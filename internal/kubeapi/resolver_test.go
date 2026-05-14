package kubeapi

import (
	"fmt"
	"sync"
	"testing"
)

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

func TestResolverAllowsConcurrentRegistrationAndLookup(t *testing.T) {
	resolver := NewResolver()
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				group := fmt.Sprintf("group-%d.example.com", i)
				kind := fmt.Sprintf("Thing%d", j)
				resource := fmt.Sprintf("things-%d-%d", i, j)
				resolver.Register(ResourceInfo{
					Group:      group,
					Version:    "v1",
					Resource:   resource,
					Kind:       kind,
					Namespaced: true,
				})
				resolver.ResolveGVK(group, "v1", kind)
				resolver.ResolveGVR(group, "v1", resource)
				resolver.RegisterFromObject(map[string]any{
					"apiVersion": group + "/v1",
					"kind":       kind,
				})
			}
		}()
	}
	wg.Wait()

	info, ok := resolver.ResolveGVR("group-3.example.com", "v1", "things-3-199")
	if !ok || info.Kind != "Thing199" {
		t.Fatalf("lookup after concurrent registration = %#v, %v", info, ok)
	}
}
