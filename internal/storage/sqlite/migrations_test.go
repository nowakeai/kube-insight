package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"kube-insight/internal/core"
	"kube-insight/internal/extractor"
)

func TestMigrateLatestIndexDropsDuplicatedDocColumn(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "kube-insight.db")
	store, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	obs := core.Observation{
		Type:            core.ObservationModified,
		ObservedAt:      time.Unix(10, 0),
		ResourceVersion: "42",
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
				"name":            "settings",
				"namespace":       "default",
				"uid":             "configmap-uid",
				"resourceVersion": "42",
			},
			"data": map[string]any{"mode": "prod"},
		},
	}
	if err := store.PutObservation(ctx, obs, extractor.Evidence{}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`pragma foreign_keys = off`,
		`drop view if exists latest_documents`,
		`drop index if exists latest_kind_ns_name_idx`,
		`alter table latest_index rename to latest_index_current`,
		`create table latest_index (
  object_id integer primary key references objects(id),
  cluster_id integer not null references clusters(id),
  kind_id integer not null references object_kinds(id),
  namespace text,
  name text not null,
  uid text,
  latest_version_id integer not null,
  observed_at integer not null,
  doc text not null
)`,
		`insert into latest_index(
  object_id, cluster_id, kind_id, namespace, name, uid, latest_version_id, observed_at, doc
)
select object_id, cluster_id, kind_id, namespace, name, uid, latest_version_id, observed_at, '{}'
from latest_index_current`,
		`drop table latest_index_current`,
		`create index latest_kind_ns_name_idx
on latest_index(cluster_id, kind_id, namespace, name)`,
		`pragma foreign_keys = on`,
	} {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			_ = db.Close()
			t.Fatalf("prepare legacy latest_index: %v", err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	store, err = Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	hasDoc, err := latestIndexHasDocColumnDB(ctx, store.db)
	if err != nil {
		t.Fatal(err)
	}
	if hasDoc {
		t.Fatal("latest_index.doc still exists after migration")
	}
	var latestDocs int
	if err := store.db.QueryRowContext(ctx, `select count(*) from latest_documents`).Scan(&latestDocs); err != nil {
		t.Fatal(err)
	}
	if latestDocs != 1 {
		t.Fatalf("latest_documents rows = %d, want 1", latestDocs)
	}
}

func TestMigrateBackfillsObjectObservations(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "kube-insight.db")
	store, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	obs := core.Observation{
		Type:            core.ObservationModified,
		ObservedAt:      time.Unix(10, 0),
		ResourceVersion: "42",
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

	var observations, unchanged, retainedVersionRefs int
	if err := store.db.QueryRowContext(ctx, `select count(*) from object_observations`).Scan(&observations); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRowContext(ctx, `select count(*) from object_observations where not content_changed`).Scan(&unchanged); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRowContext(ctx, `select count(distinct version_id) from object_observations`).Scan(&retainedVersionRefs); err != nil {
		t.Fatal(err)
	}
	if observations != 2 || unchanged != 1 || retainedVersionRefs != 1 {
		t.Fatalf("observations/unchanged/version_refs = %d/%d/%d, want 2/1/1", observations, unchanged, retainedVersionRefs)
	}
}

func TestMigrateBackfillKeepsNonContiguousContentReappearance(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "kube-insight.db")
	store, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	ref := core.ResourceRef{
		ClusterID: "c1",
		Version:   "v1",
		Resource:  "configmaps",
		Kind:      "ConfigMap",
		Namespace: "default",
		Name:      "settings",
		UID:       "configmap-uid",
	}
	obj := map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": map[string]any{
			"name":      "settings",
			"namespace": "default",
			"uid":       "configmap-uid",
		},
		"data": map[string]any{"mode": "prod"},
	}
	if err := store.PutObservation(ctx, core.Observation{Type: core.ObservationModified, ObservedAt: time.Unix(10, 0), ResourceVersion: "10", Ref: ref, Object: obj}, extractor.Evidence{}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `
insert into versions(
  object_id, seq, observed_at, resource_version, generation, doc_hash,
  materialization, strategy, blob_ref, raw_size, stored_size, replay_depth, summary
)
select object_id, 2, ?, '11', generation, doc_hash,
  materialization, strategy, blob_ref, raw_size, stored_size, replay_depth, summary
from versions
where seq = 1`, time.Unix(20, 0).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	obj["data"] = map[string]any{"mode": "dev"}
	if err := store.PutObservation(ctx, core.Observation{Type: core.ObservationModified, ObservedAt: time.Unix(30, 0), ResourceVersion: "12", Ref: ref, Object: obj}, extractor.Evidence{}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `
insert into versions(
  object_id, seq, observed_at, resource_version, generation, doc_hash,
  materialization, strategy, blob_ref, raw_size, stored_size, replay_depth, summary
)
select object_id, 4, ?, '13', generation, doc_hash,
  materialization, strategy, blob_ref, raw_size, stored_size, replay_depth, summary
from versions
where seq = 1`, time.Unix(40, 0).UnixMilli()); err != nil {
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

	var observations, unchanged, retainedVersionRefs int
	if err := store.db.QueryRowContext(ctx, `select count(*) from object_observations`).Scan(&observations); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRowContext(ctx, `select count(*) from object_observations where not content_changed`).Scan(&unchanged); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRowContext(ctx, `select count(distinct version_id) from object_observations`).Scan(&retainedVersionRefs); err != nil {
		t.Fatal(err)
	}
	if observations != 4 || unchanged != 1 || retainedVersionRefs != 3 {
		t.Fatalf("observations/unchanged/version_refs = %d/%d/%d, want 4/1/3", observations, unchanged, retainedVersionRefs)
	}
}
