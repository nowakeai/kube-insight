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

func TestScopeMatchesResourceGlob(t *testing.T) {
	scope := Scope{Resources: []string{"*.reports.kyverno.io", "apps/v1/deploy*"}}
	if !scope.Matches(core.ResourceRef{Group: "reports.kyverno.io", Version: "v1", Resource: "ephemeralreports", Kind: "EphemeralReport"}) {
		t.Fatal("expected report resource to match glob")
	}
	if !scope.Matches(core.ResourceRef{Group: "apps", Version: "v1", Resource: "deployments", Kind: "Deployment"}) {
		t.Fatal("expected deployment GVR to match glob")
	}
	if scope.Matches(core.ResourceRef{Group: "batch", Version: "v1", Resource: "jobs", Kind: "Job"}) {
		t.Fatal("unexpected job match")
	}
}

func TestScopeMatchesKindGlob(t *testing.T) {
	scope := Scope{Kinds: []string{"*Policy"}}
	if !scope.Matches(core.ResourceRef{Group: "admissionregistration.k8s.io", Version: "v1", Resource: "validatingadmissionpolicies", Kind: "ValidatingAdmissionPolicy"}) {
		t.Fatal("expected kind glob to match")
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

func TestResourceVersionFilterRemovesRedundantMetadata(t *testing.T) {
	obs := core.Observation{
		Ref: core.ResourceRef{Resource: "pods", Kind: "Pod"},
		Object: map[string]any{
			"metadata": map[string]any{
				"name":            "api",
				"resourceVersion": "123",
			},
		},
	}
	got, decision, err := ResourceVersionFilter{}.Apply(context.Background(), obs)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Outcome != KeepModified || decision.Meta["removedFields"] != 1 {
		t.Fatalf("decision = %#v", decision)
	}
	metadata := got.Object["metadata"].(map[string]any)
	if _, ok := metadata["resourceVersion"]; ok {
		t.Fatal("resourceVersion was not removed")
	}
}

func TestStatusConditionTimestampFilterKeepsConditionState(t *testing.T) {
	obs := core.Observation{
		Ref: core.ResourceRef{Resource: "nodes", Kind: "Node"},
		Object: map[string]any{
			"status": map[string]any{
				"conditions": []any{
					map[string]any{
						"type":               "Ready",
						"status":             "True",
						"reason":             "KubeletReady",
						"lastHeartbeatTime":  "2026-05-14T00:00:00Z",
						"lastTransitionTime": "2026-05-14T00:00:00Z",
						"observedGeneration": float64(1),
					},
				},
			},
		},
	}
	got, decision, err := StatusConditionTimestampFilter{}.Apply(context.Background(), obs)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Outcome != KeepModified || decision.Meta["removedFields"] != 2 {
		t.Fatalf("decision = %#v", decision)
	}
	condition := got.Object["status"].(map[string]any)["conditions"].([]any)[0].(map[string]any)
	if condition["type"] != "Ready" || condition["status"] != "True" || condition["reason"] != "KubeletReady" {
		t.Fatalf("condition state changed: %#v", condition)
	}
	if _, ok := condition["lastHeartbeatTime"]; ok {
		t.Fatal("lastHeartbeatTime was not removed")
	}
	if _, ok := condition["lastTransitionTime"]; ok {
		t.Fatal("lastTransitionTime was not removed")
	}
}

func TestLeaderElectionConfigMapFilterRemovesLegacyLeaderAnnotation(t *testing.T) {
	obs := core.Observation{
		Ref: core.ResourceRef{Resource: "configmaps", Kind: "ConfigMap"},
		Object: map[string]any{
			"metadata": map[string]any{
				"name": "cluster-kubestore",
				"annotations": map[string]any{
					"control-plane.alpha.kubernetes.io/leader": `{"holderIdentity":"node-a"}`,
				},
			},
		},
	}
	got, decision, err := LeaderElectionConfigMapFilter{}.Apply(context.Background(), obs)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Outcome != KeepModified || decision.Meta["removedFields"] != 1 {
		t.Fatalf("decision = %#v", decision)
	}
	metadata := got.Object["metadata"].(map[string]any)
	if _, ok := metadata["annotations"]; ok {
		t.Fatal("empty annotations should be removed")
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
