package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kube-insight/internal/core"
	"kube-insight/internal/extractor"
	"kube-insight/internal/kubeapi"
	"kube-insight/internal/storage"
	"kube-insight/internal/storage/sqlite"
)

func TestRunDBResourcesHealthTableOutput(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "kube-insight.db")
	store, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	err = store.UpsertIngestionOffset(context.Background(), storage.IngestionOffset{
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
		Status:          "watch_error",
		Error:           "stream reset",
	})
	closeErr := store.Close()
	if err != nil {
		t.Fatal(err)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}

	var stdout, stderr bytes.Buffer
	err = Run(context.Background(), []string{
		"db", "resources", "health",
		"--db", dbPath,
		"--errors-only",
	}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Resource health summary",
		"Resources",
		"watch_error",
		"stream reset",
		"v1/pods",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout missing %q: %s", want, stdout.String())
		}
	}
}

func TestRunDBReindexDryRun(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "kube-insight.db")
	store, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	ref := core.ResourceRef{ClusterID: "c1", Version: "v1", Resource: "pods", Kind: "Pod", Namespace: "default", Name: "api-1", UID: "pod-uid"}
	obj := map[string]any{
		"apiVersion": "v1",
		"kind":       "Pod",
		"metadata":   map[string]any{"name": "api-1", "namespace": "default", "uid": "pod-uid"},
		"status":     map[string]any{"phase": "Running"},
	}
	err = store.PutObservation(context.Background(), core.Observation{
		Type:            core.ObservationModified,
		ObservedAt:      time.Unix(10, 0),
		ResourceVersion: "10",
		Ref:             ref,
		Object:          obj,
	}, extractor.Evidence{})
	closeErr := store.Close()
	if err != nil {
		t.Fatal(err)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}

	var stdout, stderr bytes.Buffer
	err = Run(context.Background(), []string{
		"db", "reindex",
		"--db", dbPath,
	}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Evidence reindex",
		"dry-run",
		"Objects scanned",
		"Versions scanned",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout missing %q: %s", want, stdout.String())
		}
	}
}
