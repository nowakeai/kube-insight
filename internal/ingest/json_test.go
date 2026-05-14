package ingest

import (
	"context"
	"strings"
	"testing"
	"time"

	"kube-insight/internal/core"
	"kube-insight/internal/filter"
	"kube-insight/internal/kubeapi"
	"kube-insight/internal/storage"
)

func TestPipelineIngestsListJSON(t *testing.T) {
	input := `{
	  "apiVersion": "v1",
	  "kind": "List",
	  "items": [
	    {
	      "apiVersion": "v1",
	      "kind": "Pod",
	      "metadata": {"name": "api-1", "namespace": "default", "uid": "pod-uid", "resourceVersion": "10"},
	      "spec": {"nodeName": "node-a"},
	      "status": {"phase": "Running"}
	    },
	    {
	      "apiVersion": "v1",
	      "kind": "Secret",
	      "metadata": {"name": "db", "namespace": "default", "uid": "secret-uid"},
	      "data": {"password": "secret"}
	    },
	    {
	      "apiVersion": "coordination.k8s.io/v1",
	      "kind": "Lease",
	      "metadata": {"name": "node-a", "namespace": "kube-node-lease", "uid": "lease-uid"}
	    }
	  ]
	}`

	store := storage.NewMemoryStore()
	pipeline := DefaultPipeline(store)
	pipeline.Now = func() time.Time { return time.Unix(100, 0) }

	summary, err := pipeline.IngestJSON(context.Background(), []byte(input))
	if err != nil {
		t.Fatal(err)
	}
	if summary.Observations != 3 {
		t.Fatalf("observations = %d, want 3", summary.Observations)
	}
	if summary.StoredObservations != 2 {
		t.Fatalf("stored observations = %d, want 2", summary.StoredObservations)
	}
	if summary.ModifiedObservations != 1 {
		t.Fatalf("modified observations = %d, want 1", summary.ModifiedObservations)
	}
	if summary.DiscardedResources != 1 {
		t.Fatalf("discarded resources = %d, want 1", summary.DiscardedResources)
	}
	if summary.Facts != 2 {
		t.Fatalf("facts = %d, want 2", summary.Facts)
	}
	if summary.Edges != 1 {
		t.Fatalf("edges = %d, want 1", summary.Edges)
	}
	if _, ok := store.Observations[1].Object["data"]; ok {
		t.Fatal("secret payload was stored")
	}
	if len(store.FilterDecisions) != 7 {
		t.Fatalf("filter decisions = %d, want 7", len(store.FilterDecisions))
	}
	if store.FilterDecisions[4].Decision.Outcome != filter.KeepModified {
		t.Fatalf("secret decision = %#v", store.FilterDecisions[4].Decision)
	}
	if store.FilterDecisions[6].Decision.Outcome != filter.DiscardResource {
		t.Fatalf("lease decision = %#v", store.FilterDecisions[6].Decision)
	}
}

func TestPipelineKeepsDeletedObservationsDespiteDiscardChangeFilter(t *testing.T) {
	input := `{
	  "type": "DELETED",
	  "object": {
	    "apiVersion": "v1",
	    "kind": "Pod",
	    "metadata": {"name": "api-1", "namespace": "default", "uid": "pod-uid", "resourceVersion": "10"}
	  }
	}`

	store := storage.NewMemoryStore()
	pipeline := DefaultPipeline(store)
	pipeline.Filters = filter.Chain{discardChangeFilter{}}
	pipeline.Now = func() time.Time { return time.Unix(100, 0) }

	summary, err := pipeline.IngestJSON(context.Background(), []byte(input))
	if err != nil {
		t.Fatal(err)
	}
	if summary.StoredObservations != 1 {
		t.Fatalf("stored observations = %d, want 1", summary.StoredObservations)
	}
	if summary.DiscardedChanges != 0 {
		t.Fatalf("discarded changes = %d, want 0", summary.DiscardedChanges)
	}
	if len(store.Observations) != 1 || store.Observations[0].Type != core.ObservationDeleted {
		t.Fatalf("stored observation = %#v", store.Observations)
	}
	if len(store.FilterDecisions) != 1 || store.FilterDecisions[0].Decision.Outcome != filter.DiscardChange {
		t.Fatalf("filter decisions = %#v", store.FilterDecisions)
	}
	if summary.Changes != 1 {
		t.Fatalf("changes = %d, want 1", summary.Changes)
	}
}

func TestDecodeObjectsRejectsNonObjectItems(t *testing.T) {
	_, err := decodeObjects([]byte(`{"items":["bad"]}`))
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "items[0]") {
		t.Fatalf("error = %v", err)
	}
}

func TestPipelineRegistersCRDResourceMappingBeforeObjects(t *testing.T) {
	input := `{
	  "apiVersion": "v1",
	  "kind": "List",
	  "items": [
	    {
	      "apiVersion": "apiextensions.k8s.io/v1",
	      "kind": "CustomResourceDefinition",
	      "metadata": {"name": "indices.example.com", "uid": "crd-indices"},
	      "spec": {
	        "group": "example.com",
	        "names": {"kind": "Index", "plural": "indices"},
	        "scope": "Namespaced",
	        "versions": [{"name": "v1", "served": true, "storage": true}]
	      }
	    },
	    {
	      "apiVersion": "example.com/v1",
	      "kind": "Index",
	      "metadata": {"name": "search", "namespace": "default", "uid": "idx-search"}
	    }
	  ]
	}`

	store := storage.NewMemoryStore()
	pipeline := DefaultPipeline(store)
	pipeline.Now = func() time.Time { return time.Unix(100, 0) }

	if _, err := pipeline.IngestJSON(context.Background(), []byte(input)); err != nil {
		t.Fatal(err)
	}
	if len(store.Observations) != 2 {
		t.Fatalf("observations = %d, want 2", len(store.Observations))
	}
	if got := store.Observations[1].Ref.Resource; got != "indices" {
		t.Fatalf("custom resource = %q, want indices", got)
	}
}

func TestResourceNameKnownKubernetesKinds(t *testing.T) {
	resolver := kubeapi.NewResolver()
	tests := map[string]string{
		"ConfigMap":                "configmaps",
		"CronJob":                  "cronjobs",
		"CustomResourceDefinition": "customresourcedefinitions",
		"EndpointSlice":            "endpointslices",
		"HorizontalPodAutoscaler":  "horizontalpodautoscalers",
		"Ingress":                  "ingresses",
		"NetworkPolicy":            "networkpolicies",
		"PersistentVolumeClaim":    "persistentvolumeclaims",
		"ServiceAccount":           "serviceaccounts",
	}
	for kind, want := range tests {
		info, _ := resolver.ResolveGVK("", "", kind)
		if got := info.Resource; got != want {
			t.Fatalf("resource for kind %q = %q, want %q", kind, got, want)
		}
	}
}

type discardChangeFilter struct{}

func (discardChangeFilter) Name() string { return "test_discard_change_filter" }

func (discardChangeFilter) Apply(_ context.Context, obs core.Observation) (core.Observation, filter.Decision, error) {
	return obs, filter.Decision{Outcome: filter.DiscardChange, Reason: "test_change_discard"}, nil
}
