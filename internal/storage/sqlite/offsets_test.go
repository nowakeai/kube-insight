package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"kube-insight/internal/kubeapi"
	"kube-insight/internal/storage"
)

func TestStoreResourceHealthTreatsRetryingAsUnstable(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "kube-insight.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if err := store.UpsertIngestionOffset(context.Background(), storage.IngestionOffset{
		ClusterID: "c1",
		Resource: kubeapi.ResourceInfo{
			Version:    "v1",
			Resource:   "pods",
			Kind:       "Pod",
			Namespaced: true,
			Verbs:      []string{"list", "watch"},
		},
		ResourceVersion: "10",
		Event:           storage.OffsetEventWatch,
		Status:          "retrying",
		Error:           "unable to decode an event from the watch stream",
		At:              time.Unix(10, 0),
	}); err != nil {
		t.Fatal(err)
	}

	health, err := store.ResourceHealth(context.Background(), ResourceHealthOptions{ClusterID: "c1"})
	if err != nil {
		t.Fatal(err)
	}
	if health.Summary.Errors != 0 || health.Summary.Healthy != 0 || health.Summary.Unstable != 1 || health.Summary.Complete {
		t.Fatalf("summary = %#v", health.Summary)
	}
	if len(health.Summary.Warnings) != 1 {
		t.Fatalf("warnings = %#v", health.Summary.Warnings)
	}
	if health.ByStatus["retrying"] != 1 {
		t.Fatalf("by status = %#v", health.ByStatus)
	}
	errorsOnly, err := store.ResourceHealth(context.Background(), ResourceHealthOptions{ClusterID: "c1", ErrorsOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(errorsOnly.Resources) != 0 {
		t.Fatalf("retrying should not be returned as terminal error: %#v", errorsOnly.Resources)
	}
}

func TestStoreResourceHealthScopesByCluster(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "kube-insight.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()
	resource := kubeapi.ResourceInfo{
		Version:    "v1",
		Resource:   "pods",
		Kind:       "Pod",
		Namespaced: true,
		Verbs:      []string{"list", "watch"},
	}
	for _, offset := range []storage.IngestionOffset{
		{
			ClusterID:       "cluster-a",
			Resource:        resource,
			ResourceVersion: "10",
			Event:           storage.OffsetEventWatch,
			Status:          "watching",
			At:              time.Unix(10, 0),
		},
		{
			ClusterID:       "cluster-b",
			Resource:        resource,
			ResourceVersion: "20",
			Event:           storage.OffsetEventWatch,
			Status:          "watch_error",
			Error:           "stream reset",
			At:              time.Unix(20, 0),
		},
	} {
		if err := store.UpsertIngestionOffset(ctx, offset); err != nil {
			t.Fatal(err)
		}
	}

	a, err := store.ResourceHealth(ctx, ResourceHealthOptions{ClusterID: "cluster-a"})
	if err != nil {
		t.Fatal(err)
	}
	if a.Summary.Resources != 1 || a.Summary.Healthy != 1 || a.Summary.Errors != 0 || !a.Summary.Complete {
		t.Fatalf("cluster-a summary = %#v", a.Summary)
	}
	if len(a.Resources) != 1 || a.Resources[0].ClusterID != "cluster-a" {
		t.Fatalf("cluster-a resources = %#v", a.Resources)
	}

	b, err := store.ResourceHealth(ctx, ResourceHealthOptions{ClusterID: "cluster-b"})
	if err != nil {
		t.Fatal(err)
	}
	if b.Summary.Resources != 1 || b.Summary.Errors != 1 || b.Summary.Complete {
		t.Fatalf("cluster-b summary = %#v", b.Summary)
	}
	if len(b.Resources) != 1 || b.Resources[0].ClusterID != "cluster-b" || b.Resources[0].Error != "stream reset" {
		t.Fatalf("cluster-b resources = %#v", b.Resources)
	}
}

func TestStoreResourceHealthSkipsExcludedResources(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "kube-insight.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if err := store.UpsertIngestionOffset(context.Background(), storage.IngestionOffset{
		ClusterID: "c1",
		Resource: kubeapi.ResourceInfo{
			Group:      "coordination.k8s.io",
			Version:    "v1",
			Resource:   "leases",
			Kind:       "Lease",
			Namespaced: true,
			Verbs:      []string{"list", "watch"},
		},
		ResourceVersion: "20",
		Event:           storage.OffsetEventWatch,
		Status:          "retrying",
		Error:           "stream decode error",
		At:              time.Unix(20, 0),
	}); err != nil {
		t.Fatal(err)
	}

	health, err := store.ResourceHealth(context.Background(), ResourceHealthOptions{
		ClusterID:        "c1",
		ExcludeResources: []string{"leases.coordination.k8s.io"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if health.Summary.Resources != 0 || health.Summary.Skipped != 1 || !health.Summary.Complete {
		t.Fatalf("summary = %#v", health.Summary)
	}

	included, err := store.ResourceHealth(context.Background(), ResourceHealthOptions{
		ClusterID:        "c1",
		ExcludeResources: []string{"leases.coordination.k8s.io"},
		IncludeExcluded:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if included.Summary.Resources != 1 || included.Summary.Skipped != 1 || included.ByStatus["skipped"] != 1 {
		t.Fatalf("included = %#v byStatus=%#v", included.Summary, included.ByStatus)
	}
	if len(included.Resources) != 1 || !included.Resources[0].Skipped || included.Resources[0].Status != "skipped" {
		t.Fatalf("resources = %#v", included.Resources)
	}
}

func TestResourceHealthExcludeBareNameOnlyMatchesCoreResource(t *testing.T) {
	if !resourceHealthExcluded(ResourceHealthRecord{Resource: "events"}, []string{"events"}) {
		t.Fatal("bare resource exclude should match core resource")
	}
	if resourceHealthExcluded(ResourceHealthRecord{Group: "events.k8s.io", Resource: "events"}, []string{"events"}) {
		t.Fatal("bare resource exclude should not match grouped resource")
	}
	if !resourceHealthExcluded(ResourceHealthRecord{Group: "events.k8s.io", Resource: "events"}, []string{"events.events.k8s.io"}) {
		t.Fatal("resource.group exclude should match grouped resource")
	}
}

func TestStoreResourceHealthTreatsQueuedAsHealthyProgress(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "kube-insight.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if err := store.UpsertIngestionOffset(context.Background(), storage.IngestionOffset{
		ClusterID: "c1",
		Resource: kubeapi.ResourceInfo{
			Version:    "v1",
			Resource:   "pods",
			Kind:       "Pod",
			Namespaced: true,
			Verbs:      []string{"list", "watch"},
		},
		ResourceVersion: "30",
		Event:           storage.OffsetEventList,
		Status:          "queued",
		At:              time.Unix(30, 0),
	}); err != nil {
		t.Fatal(err)
	}

	health, err := store.ResourceHealth(context.Background(), ResourceHealthOptions{ClusterID: "c1"})
	if err != nil {
		t.Fatal(err)
	}
	if health.Summary.Resources != 1 || health.Summary.Queued != 1 || !health.Summary.Complete {
		t.Fatalf("summary = %#v", health.Summary)
	}
	if health.ByStatus["queued"] != 1 {
		t.Fatalf("by status = %#v", health.ByStatus)
	}
}

func TestStoreResourceHealthIgnoresNonWatchableAPIResources(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "kube-insight.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()
	if err := store.UpsertCluster(ctx, storage.ClusterRecord{Name: "c1"}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertAPIResources(ctx, []kubeapi.ResourceInfo{
		{
			Version:  "v1",
			Resource: "apps",
			Kind:     "App",
			Verbs:    nil,
		},
		{
			Version:    "v1",
			Resource:   "pods",
			Kind:       "Pod",
			Namespaced: true,
			Verbs:      []string{"list", "watch"},
		},
	}, time.Unix(30, 0)); err != nil {
		t.Fatal(err)
	}

	health, err := store.ResourceHealth(ctx, ResourceHealthOptions{ClusterID: "c1"})
	if err != nil {
		t.Fatal(err)
	}
	if health.Summary.Resources != 1 || health.Summary.NotStarted != 1 {
		t.Fatalf("summary = %#v", health.Summary)
	}
	if len(health.Resources) != 1 || health.Resources[0].Resource != "pods" {
		t.Fatalf("resources = %#v", health.Resources)
	}
}
