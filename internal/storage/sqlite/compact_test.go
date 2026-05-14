package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"kube-insight/internal/core"
	"kube-insight/internal/extractor"
)

func TestCompactReportsStorageBytes(t *testing.T) {
	ctx := context.Background()
	store, err := Open(filepath.Join(t.TempDir(), "kube-insight.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	obs := core.Observation{
		Type:       core.ObservationModified,
		ObservedAt: time.Unix(10, 0),
		Ref: core.ResourceRef{
			ClusterID: "c1",
			Version:   "v1",
			Resource:  "configmaps",
			Kind:      "ConfigMap",
			Namespace: "default",
			Name:      "settings",
			UID:       "configmap-uid",
		},
		Object: map[string]any{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata": map[string]any{
				"name":      "settings",
				"namespace": "default",
				"uid":       "configmap-uid",
			},
			"data": map[string]any{"mode": "prod"},
		},
	}
	if err := store.PutObservation(ctx, obs, extractor.Evidence{}); err != nil {
		t.Fatal(err)
	}
	report, err := store.Compact(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if report.BytesBefore <= 0 || report.BytesAfter <= 0 {
		t.Fatalf("compact bytes before=%d after=%d", report.BytesBefore, report.BytesAfter)
	}
	if report.FinishedAt.IsZero() || report.FinishedAt.Before(report.StartedAt) {
		t.Fatalf("compact timestamps = %#v", report)
	}
}

func TestCompactPrunesUnchangedVersions(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "kube-insight.db")
	store, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	obs := core.Observation{
		Type:       core.ObservationModified,
		ObservedAt: time.Unix(10, 0),
		Ref: core.ResourceRef{
			ClusterID: "c1",
			Version:   "v1",
			Resource:  "configmaps",
			Kind:      "ConfigMap",
			Namespace: "default",
			Name:      "settings",
			UID:       "configmap-uid",
		},
		Object: map[string]any{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata": map[string]any{
				"name":      "settings",
				"namespace": "default",
				"uid":       "configmap-uid",
			},
			"data": map[string]any{"mode": "prod"},
		},
	}
	if err := store.PutObservation(ctx, obs, extractor.Evidence{}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `
insert into versions(
  object_id, seq, observed_at, resource_version, generation, doc_hash,
  materialization, strategy, blob_ref, raw_size, stored_size, replay_depth, summary
)
select object_id, 2, ?, '43', generation, doc_hash,
  materialization, strategy, blob_ref, raw_size, stored_size, replay_depth, summary
from versions
where seq = 1`, time.Unix(20, 0).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `delete from object_observations`); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	store, err = Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	report, err := store.Compact(ctx, CompactOptions{PruneUnchanged: true})
	if err != nil {
		t.Fatal(err)
	}
	if report.Pruned == nil || report.Pruned.Versions != 1 {
		t.Fatalf("pruned = %#v, want 1 version", report.Pruned)
	}
	var versions, observations, unchanged int
	if err := store.db.QueryRowContext(ctx, `select count(*) from versions`).Scan(&versions); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRowContext(ctx, `select count(*) from object_observations`).Scan(&observations); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRowContext(ctx, `select count(*) from object_observations where not content_changed`).Scan(&unchanged); err != nil {
		t.Fatal(err)
	}
	if versions != 1 || observations != 2 || unchanged != 1 {
		t.Fatalf("versions/observations/unchanged = %d/%d/%d, want 1/2/1", versions, observations, unchanged)
	}
}
