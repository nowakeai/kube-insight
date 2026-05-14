package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"kube-insight/internal/kubeapi"
	"kube-insight/internal/storage"
)

func TestStoreResourceHealthTreatsRetryingAsNonTerminal(t *testing.T) {
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
	if health.Summary.Errors != 0 || health.Summary.Healthy != 1 || !health.Summary.Complete {
		t.Fatalf("summary = %#v", health.Summary)
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
