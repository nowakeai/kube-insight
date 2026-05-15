package sqlite

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"kube-insight/internal/core"
	"kube-insight/internal/extractor"
)

func TestBackfillRetainedHistoryPrunesNormalizedDuplicates(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "kube-insight.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ref := core.ResourceRef{ClusterID: "c1", Version: "v1", Resource: "configmaps", Kind: "ConfigMap", Namespace: "default", Name: "settings", UID: "cm-uid"}
	first := map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata":   map[string]any{"name": "settings", "namespace": "default", "uid": "cm-uid", "resourceVersion": "10"},
		"data":       map[string]any{"mode": "prod"},
	}
	second := map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata":   map[string]any{"name": "settings", "namespace": "default", "uid": "cm-uid", "resourceVersion": "11"},
		"data":       map[string]any{"mode": "prod"},
	}
	ctx := context.Background()
	if err := store.PutObservation(ctx, core.Observation{Type: core.ObservationModified, ObservedAt: time.Unix(10, 0), ResourceVersion: "10", Ref: ref, Object: first}, extractor.Evidence{}); err != nil {
		t.Fatal(err)
	}
	if err := store.PutObservation(ctx, core.Observation{Type: core.ObservationModified, ObservedAt: time.Unix(20, 0), ResourceVersion: "11", Ref: ref, Object: second}, extractor.Evidence{}); err != nil {
		t.Fatal(err)
	}

	dryRun, err := store.BackfillRetainedHistory(ctx, BackfillOptions{
		DryRun:    true,
		Normalize: removeResourceVersionForBackfillTest,
	})
	if err != nil {
		t.Fatal(err)
	}
	if dryRun.VersionsPruned != 1 {
		t.Fatalf("dry-run pruned = %d, want 1", dryRun.VersionsPruned)
	}
	var versionsBefore int
	if err := store.db.QueryRow(`select count(*) from versions`).Scan(&versionsBefore); err != nil {
		t.Fatal(err)
	}
	if versionsBefore != 2 {
		t.Fatalf("dry-run changed versions = %d, want 2", versionsBefore)
	}

	applied, err := store.BackfillRetainedHistory(ctx, BackfillOptions{
		DryRun:    false,
		Normalize: removeResourceVersionForBackfillTest,
	})
	if err != nil {
		t.Fatal(err)
	}
	if applied.VersionsPruned != 1 || applied.ObservationsRepointed != 1 {
		t.Fatalf("applied report = %#v", applied)
	}
	var versionsAfter, observations int
	if err := store.db.QueryRow(`select count(*) from versions`).Scan(&versionsAfter); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`select count(*) from object_observations where not content_changed`).Scan(&observations); err != nil {
		t.Fatal(err)
	}
	if versionsAfter != 1 || observations != 1 {
		t.Fatalf("versions/unchanged observations = %d/%d, want 1/1", versionsAfter, observations)
	}
}

func removeResourceVersionForBackfillTest(_ context.Context, version BackfillVersion) ([]byte, error) {
	var object map[string]any
	if err := json.Unmarshal(version.Document, &object); err != nil {
		return nil, err
	}
	metadata, _ := object["metadata"].(map[string]any)
	delete(metadata, "resourceVersion")
	return json.Marshal(object)
}
