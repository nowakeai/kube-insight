package extractor

import (
	"context"
	"testing"
	"time"

	"kube-insight/internal/core"
)

func TestServiceExtractorEmitsLoadBalancerIngressFacts(t *testing.T) {
	obs := core.Observation{
		ObservedAt: time.Unix(10, 0),
		Ref: core.ResourceRef{
			ClusterID: "c1",
			Resource:  "services",
			Kind:      "Service",
			Namespace: "default",
			Name:      "api",
			UID:       "svc-uid",
		},
		Object: map[string]any{
			"spec": map[string]any{
				"type":      "LoadBalancer",
				"clusterIP": "10.0.0.10",
			},
			"status": map[string]any{
				"loadBalancer": map[string]any{
					"ingress": []any{
						map[string]any{"ip": "34.1.2.3", "ipMode": "VIP"},
					},
				},
			},
		},
	}

	evidence, err := ServiceExtractor{}.Extract(context.Background(), obs)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []struct {
		key   string
		value string
	}{
		{"service.type", "LoadBalancer"},
		{"service.cluster_ip", "10.0.0.10"},
		{"service.load_balancer.pending", "false"},
		{"service.load_balancer.ingress_ip", "34.1.2.3"},
		{"service.load_balancer.ingress_count", "1"},
	} {
		if !hasFact(evidence.Facts, want.key, want.value) {
			t.Fatalf("missing fact %s=%s in %#v", want.key, want.value, evidence.Facts)
		}
	}
	if !hasChange(evidence.Changes, "status.loadBalancer.ingress.ip", "34.1.2.3") {
		t.Fatalf("expected ingress change, got %#v", evidence.Changes)
	}
}

func TestServiceExtractorEmitsPendingAndDeletedFacts(t *testing.T) {
	obs := core.Observation{
		Type:       core.ObservationDeleted,
		ObservedAt: time.Unix(10, 0),
		Ref: core.ResourceRef{
			ClusterID: "c1",
			Resource:  "services",
			Kind:      "Service",
			Namespace: "default",
			Name:      "api",
			UID:       "svc-uid",
		},
		Object: map[string]any{
			"spec": map[string]any{"type": "LoadBalancer"},
			"status": map[string]any{
				"loadBalancer": map[string]any{},
			},
		},
	}

	evidence, err := ServiceExtractor{}.Extract(context.Background(), obs)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []struct {
		key   string
		value string
	}{
		{"service.load_balancer.pending", "true"},
		{"service.load_balancer.ingress_count", "0"},
		{"service.deleted", "true"},
	} {
		if !hasFact(evidence.Facts, want.key, want.value) {
			t.Fatalf("missing fact %s=%s in %#v", want.key, want.value, evidence.Facts)
		}
	}
	if !hasChange(evidence.Changes, "status.loadBalancer.ingress", "pending") ||
		!hasChange(evidence.Changes, "metadata.deletionTimestamp", "deleted") {
		t.Fatalf("expected pending and deleted changes, got %#v", evidence.Changes)
	}
}
