package filter

import (
	"context"
	"testing"

	"kube-insight/internal/core"
)

func TestSecretRedactionFilterRemovesPayload(t *testing.T) {
	obs := core.Observation{
		Ref: core.ResourceRef{Resource: "secrets", Kind: "Secret"},
		Object: map[string]any{
			"metadata":   map[string]any{"name": "db"},
			"data":       map[string]any{"password": "secret"},
			"stringData": map[string]any{"token": "secret"},
		},
	}

	got, decision, err := SecretRedactionFilter{}.Apply(context.Background(), obs)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Outcome != KeepModified {
		t.Fatalf("outcome = %s, want %s", decision.Outcome, KeepModified)
	}
	if decision.Meta["redactedFields"] != 2 || decision.Meta["secretPayloadRemoved"] != true {
		t.Fatalf("meta = %#v", decision.Meta)
	}
	if _, ok := got.Object["data"]; ok {
		t.Fatal("data was not redacted")
	}
	if _, ok := got.Object["stringData"]; ok {
		t.Fatal("stringData was not redacted")
	}
}

func TestManagedFieldsFilterCountsRemovedField(t *testing.T) {
	obs := core.Observation{
		Ref: core.ResourceRef{Resource: "pods", Kind: "Pod"},
		Object: map[string]any{
			"metadata": map[string]any{
				"name":          "api",
				"managedFields": []any{map[string]any{"manager": "kubectl"}},
			},
		},
	}
	got, decision, err := ManagedFieldsFilter{}.Apply(context.Background(), obs)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Outcome != KeepModified || decision.Meta["removedFields"] != 1 {
		t.Fatalf("decision = %#v", decision)
	}
	metadata := got.Object["metadata"].(map[string]any)
	if _, ok := metadata["managedFields"]; ok {
		t.Fatal("managedFields was not removed")
	}
}

func TestLeaseSkipFilterDiscardsLeases(t *testing.T) {
	obs := core.Observation{
		Ref: core.ResourceRef{Group: "coordination.k8s.io", Resource: "leases", Kind: "Lease"},
	}

	_, decision, err := LeaseSkipFilter{}.Apply(context.Background(), obs)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Outcome != DiscardResource {
		t.Fatalf("outcome = %s, want %s", decision.Outcome, DiscardResource)
	}
}
