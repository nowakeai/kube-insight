package collector

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"kube-insight/internal/core"
	"kube-insight/internal/ingest"
	"kube-insight/internal/storage"
)

func TestParseResources(t *testing.T) {
	resources, err := ParseResources([]string{"pods,!nodes", "deployments.apps"})
	if err != nil {
		t.Fatal(err)
	}
	if len(resources) != 3 {
		t.Fatalf("resources = %d, want 3", len(resources))
	}
	if resources[0].Name != "pods" || !resources[0].Namespaced {
		t.Fatalf("resource[0] = %#v", resources[0])
	}
	if resources[1].Name != "nodes" || resources[1].Namespaced {
		t.Fatalf("resource[1] = %#v", resources[1])
	}
}

func TestCapList(t *testing.T) {
	out, count, err := capList([]byte(`{"kind":"List","items":[{"a":1},{"a":2},{"a":3}]}`), 2)
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("count = %d, want 2", count)
	}
	if string(out) == "" {
		t.Fatal("empty output")
	}
}

func TestMergeResourcesDeduplicatesByScope(t *testing.T) {
	resources := mergeResources(
		[]Resource{{Name: "pods", Namespaced: true}, {Name: "nodes"}},
		[]Resource{{Name: "pods", Namespaced: true}, {Name: "pods"}},
	)
	if len(resources) != 3 {
		t.Fatalf("resources = %#v", resources)
	}
}

func TestParseAPIResourcesWideHandlesEmptyShortnames(t *testing.T) {
	input := []byte(`NAME                              SHORTNAMES   APIVERSION                             NAMESPACED   KIND                             VERBS
bindings                                       v1                                     true         Binding                          [create]
configmaps                        cm           v1                                     true         ConfigMap                        [create delete get list watch]
customresourcedefinitions         crd,crds     apiextensions.k8s.io/v1                false        CustomResourceDefinition         [create delete get list watch]
widgets                                        example.com/v1                         true         Widget                           [get list watch]
`)
	resources := parseAPIResourcesWide(input)
	if len(resources) != 4 {
		t.Fatalf("resources = %#v", resources)
	}
	if resources[0].Name != "bindings" || resources[0].Version != "v1" || resources[0].Kind != "Binding" || !resources[0].Namespaced {
		t.Fatalf("resource[0] = %#v", resources[0])
	}
	if resources[2].Name != "customresourcedefinitions.apiextensions.k8s.io" || resources[2].Group != "apiextensions.k8s.io" || resources[2].Resource != "customresourcedefinitions" || resources[2].Namespaced {
		t.Fatalf("resource[2] = %#v", resources[2])
	}
	if resources[3].Name != "widgets.example.com" || resources[3].Group != "example.com" || resources[3].Kind != "Widget" {
		t.Fatalf("resource[3] = %#v", resources[3])
	}
}

func TestResourcesFromAPIResourceLists(t *testing.T) {
	resources := resourcesFromAPIResourceLists([]*metav1.APIResourceList{
		{
			GroupVersion: "v1",
			APIResources: []metav1.APIResource{
				{Name: "pods", Kind: "Pod", Namespaced: true, Verbs: []string{"get", "list", "watch"}},
				{Name: "pods/status", Kind: "Pod", Namespaced: true, Verbs: []string{"get"}},
			},
		},
		{
			GroupVersion: "example.com/v1",
			APIResources: []metav1.APIResource{
				{Name: "widgets", Kind: "Widget", Namespaced: true, Verbs: []string{"get", "list"}},
				{Name: "writeonlys", Kind: "WriteOnly", Namespaced: true, Verbs: []string{"create"}},
			},
		},
	})
	if len(resources) != 2 {
		t.Fatalf("resources = %#v", resources)
	}
	if resources[0].Name != "pods" || resources[0].Group != "" || resources[0].Version != "v1" {
		t.Fatalf("resource[0] = %#v", resources[0])
	}
	if resources[1].Name != "widgets.example.com" || resources[1].Group != "example.com" || resources[1].Resource != "widgets" {
		t.Fatalf("resource[1] = %#v", resources[1])
	}
}

func TestWatchableResourcesSkipsNonWatchableDiscoveredResources(t *testing.T) {
	resources := watchableResources([]Resource{
		{Name: "pods", Verbs: []string{"get", "list", "watch"}},
		{Name: "bindings", Verbs: []string{"create"}},
		{Name: "widgets"},
	})
	if len(resources) != 2 {
		t.Fatalf("resources = %#v", resources)
	}
	if resources[0].Name != "pods" || resources[1].Name != "widgets" {
		t.Fatalf("resources = %#v", resources)
	}
}

func TestIsResourceVersionExpired(t *testing.T) {
	for _, message := range []string{
		"the resourceVersion for the provided watch is too old",
		"Expired: too old resource version",
		"410 Gone",
	} {
		if !isResourceVersionExpired(fakeError(message)) {
			t.Fatalf("expected expired resourceVersion for %q", message)
		}
	}
	if isResourceVersionExpired(fakeError("connection reset by peer")) {
		t.Fatal("connection reset should not be stale resourceVersion")
	}
}

type fakeError string

func (e fakeError) Error() string { return string(e) }

func TestReconcileDeletedFromListWritesDeletedObservation(t *testing.T) {
	store := storage.NewMemoryStore()
	info := resourceInfo(Resource{Name: "pods", Version: "v1", Resource: "pods", Kind: "Pod", Namespaced: true})
	previous := []core.ResourceRef{
		{ClusterID: "c1", Version: "v1", Resource: "pods", Kind: "Pod", Namespace: "default", Name: "keep", UID: "keep-uid"},
		{ClusterID: "c1", Version: "v1", Resource: "pods", Kind: "Pod", Namespace: "default", Name: "gone", UID: "gone-uid"},
	}
	list := &unstructured.UnstructuredList{Items: []unstructured.Unstructured{
		{Object: map[string]any{
			"apiVersion": "v1",
			"kind":       "Pod",
			"metadata": map[string]any{
				"name":      "keep",
				"namespace": "default",
				"uid":       "keep-uid",
			},
		}},
	}}
	summary, err := reconcileDeletedFromList(context.Background(), store, "c1", info, "", "20", previous, list, nil, ingest.FilterChains{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Deleted != 1 || summary.Ingest.StoredObservations != 1 {
		t.Fatalf("summary = %#v", summary)
	}
	if len(store.Observations) != 1 || store.Observations[0].Type != core.ObservationDeleted || store.Observations[0].Ref.Name != "gone" {
		t.Fatalf("observations = %#v", store.Observations)
	}
}

func TestReconcileDeletedFromListCountsOutOfScopeAsUnknownVisibility(t *testing.T) {
	store := storage.NewMemoryStore()
	info := resourceInfo(Resource{Name: "pods", Version: "v1", Resource: "pods", Kind: "Pod", Namespaced: true})
	previous := []core.ResourceRef{
		{ClusterID: "c1", Version: "v1", Resource: "pods", Kind: "Pod", Namespace: "other", Name: "api", UID: "pod-uid"},
	}
	list := &unstructured.UnstructuredList{}

	summary, err := reconcileDeletedFromList(context.Background(), store, "c1", info, "default", "20", previous, list, nil, ingest.FilterChains{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Deleted != 0 || summary.UnknownVisibility != 1 || summary.Ingest.StoredObservations != 0 {
		t.Fatalf("summary = %#v", summary)
	}
	if len(store.Observations) != 0 {
		t.Fatalf("unexpected delete observations = %#v", store.Observations)
	}
}

func TestRunFakeWatchReconciliationAggregatesMultiGVRMetrics(t *testing.T) {
	store := storage.NewMemoryStore()
	summary, err := RunFakeWatchReconciliation(context.Background(), FakeWatchReconciliationOptions{
		Context:   "c1",
		Store:     store,
		Namespace: "default",
	})
	if err != nil {
		t.Fatal(err)
	}
	if summary.Resources != 2 || summary.Completed != 2 || summary.Errors != 0 {
		t.Fatalf("summary = %#v", summary)
	}
	if summary.ReconciledDeleted != 2 || summary.UnknownVisibility != 1 || summary.Stored != 2 {
		t.Fatalf("summary = %#v", summary)
	}
	if summary.MaxQueueDepth != 1 || summary.BackpressureEvents != 1 || summary.QueueWaitMS <= 0 {
		t.Fatalf("queue metrics = %#v", summary)
	}
	if len(summary.ResourceQueue) != 2 || !summary.ResourceQueue[1].Queued || summary.ResourceQueue[1].QueueDepth != 1 {
		t.Fatalf("resource queue = %#v", summary.ResourceQueue)
	}
	if summary.ResourceQueue[0].Priority != "high" || summary.ResourceQueue[1].Priority != "normal" {
		t.Fatalf("resource queue priorities = %#v", summary.ResourceQueue)
	}
	if len(store.Observations) != 2 {
		t.Fatalf("observations = %#v", store.Observations)
	}
	for _, obs := range store.Observations {
		if obs.Type != core.ObservationDeleted {
			t.Fatalf("observation = %#v", obs)
		}
	}
}
