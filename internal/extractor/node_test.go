package extractor

import (
	"context"
	"testing"
	"time"

	"kube-insight/internal/core"
)

func TestNodeExtractorEmitsConditionFacts(t *testing.T) {
	obs := core.Observation{
		ObservedAt: time.Unix(10, 0),
		Ref:        core.ResourceRef{ClusterID: "c1", Resource: "nodes", Kind: "Node", Name: "node-a", UID: "node-uid"},
		Object: map[string]any{
			"status": map[string]any{
				"conditions": []any{
					map[string]any{"type": "MemoryPressure", "status": "True"},
				},
			},
		},
	}

	evidence, err := NodeExtractor{}.Extract(context.Background(), obs)
	if err != nil {
		t.Fatal(err)
	}
	if len(evidence.Facts) != 1 {
		t.Fatalf("facts = %d, want 1", len(evidence.Facts))
	}
	if evidence.Facts[0].Key != "node_condition.MemoryPressure" {
		t.Fatalf("fact key = %s", evidence.Facts[0].Key)
	}
	if len(evidence.Changes) != 1 {
		t.Fatalf("changes = %d, want 1", len(evidence.Changes))
	}
}

func TestNodeExtractorEmitsPressureAndReadyTransitions(t *testing.T) {
	obs := core.Observation{
		ObservedAt: time.Unix(10, 0),
		Ref:        core.ResourceRef{ClusterID: "c1", Resource: "nodes", Kind: "Node", Name: "node-a", UID: "node-uid"},
		Object: map[string]any{
			"status": map[string]any{
				"conditions": []any{
					map[string]any{"type": "Ready", "status": "False"},
					map[string]any{"type": "MemoryPressure", "status": "True"},
					map[string]any{"type": "DiskPressure", "status": "True"},
					map[string]any{"type": "PIDPressure", "status": "False"},
				},
			},
		},
	}

	evidence, err := NodeExtractor{}.Extract(context.Background(), obs)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []struct {
		key   string
		value string
	}{
		{"node_condition.Ready", "False"},
		{"node_condition.MemoryPressure", "True"},
		{"node_condition.DiskPressure", "True"},
		{"node_condition.PIDPressure", "False"},
	} {
		if !hasFact(evidence.Facts, want.key, want.value) {
			t.Fatalf("missing fact %s=%s in %#v", want.key, want.value, evidence.Facts)
		}
	}
	if !hasChange(evidence.Changes, "status.conditions.MemoryPressure", "True") {
		t.Fatalf("memory pressure change missing: %#v", evidence.Changes)
	}
}

func TestNodeExtractorEmitsCapacityAndAllocatableFacts(t *testing.T) {
	obs := core.Observation{
		ObservedAt: time.Unix(10, 0),
		Ref:        core.ResourceRef{ClusterID: "c1", Resource: "nodes", Kind: "Node", Name: "node-a", UID: "node-uid"},
		Object: map[string]any{
			"status": map[string]any{
				"capacity": map[string]any{
					"cpu":               "8",
					"memory":            "32869472Ki",
					"pods":              "110",
					"ephemeral-storage": "47227284Ki",
				},
				"allocatable": map[string]any{
					"cpu":               "7900m",
					"memory":            "31820896Ki",
					"pods":              "110",
					"ephemeral-storage": "45922232Ki",
				},
				"conditions": []any{
					map[string]any{"type": "Ready", "status": "True"},
				},
			},
		},
	}

	evidence, err := NodeExtractor{}.Extract(context.Background(), obs)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []struct {
		key     string
		value   string
		numeric float64
	}{
		{"node_capacity.cpu", "8", 8},
		{"node_capacity.memory", "32869472Ki", 33658339328},
		{"node_capacity.pods", "110", 110},
		{"node_capacity.ephemeral_storage", "47227284Ki", 48360738816},
		{"node_allocatable.cpu", "7900m", 7.9},
		{"node_allocatable.memory", "31820896Ki", 32584597504},
		{"node_allocatable.pods", "110", 110},
		{"node_allocatable.ephemeral_storage", "45922232Ki", 47024365568},
	} {
		fact, ok := findFact(evidence.Facts, want.key, want.value)
		if !ok {
			t.Fatalf("missing fact %s=%s in %#v", want.key, want.value, evidence.Facts)
		}
		if fact.NumericValue == nil || *fact.NumericValue != want.numeric {
			t.Fatalf("fact %s numeric = %v, want %v", want.key, fact.NumericValue, want.numeric)
		}
	}
}
