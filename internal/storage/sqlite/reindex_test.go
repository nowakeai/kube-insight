package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"kube-insight/internal/core"
	"kube-insight/internal/extractor"
)

func TestReindexEvidenceReplacesDerivedRows(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "kube-insight.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()
	ref := core.ResourceRef{ClusterID: "c1", Version: "v1", Resource: "configmaps", Kind: "ConfigMap", Namespace: "default", Name: "settings", UID: "cm-uid"}
	obs := core.Observation{
		Type:            core.ObservationModified,
		ObservedAt:      time.Unix(10, 0),
		ResourceVersion: "10",
		Ref:             ref,
		Object: map[string]any{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata":   map[string]any{"name": "settings", "namespace": "default", "uid": "cm-uid", "resourceVersion": "10"},
			"data":       map[string]any{"mode": "prod"},
		},
	}
	oldEvidence := extractor.Evidence{
		Facts: []core.Fact{{
			Time:     obs.ObservedAt,
			ObjectID: "c1/cm-uid",
			Key:      "old.fact",
			Value:    "old",
			Severity: 10,
		}},
		Changes: []core.Change{{
			Time:     obs.ObservedAt,
			ObjectID: "c1/cm-uid",
			Family:   "old",
			Path:     "old.path",
			Op:       "replace",
			New:      "old",
			Severity: 10,
		}},
		Edges: []core.Edge{{
			Type:      "old_edge",
			SourceID:  "c1/cm-uid",
			TargetID:  "c1/configmaps/default/settings",
			ValidFrom: obs.ObservedAt,
		}},
	}
	if err := store.PutObservation(ctx, obs, oldEvidence); err != nil {
		t.Fatal(err)
	}

	buildEvidence := func(_ context.Context, version BackfillVersion) (extractor.Evidence, error) {
		if version.ObservationType != core.ObservationModified {
			t.Fatalf("observation type = %q, want MODIFIED", version.ObservationType)
		}
		return extractor.Evidence{
			Facts: []core.Fact{{
				Time:     version.ObservedAt,
				ObjectID: "c1/cm-uid",
				Key:      "new.fact",
				Value:    "new",
				Severity: 20,
			}},
			Changes: []core.Change{{
				Time:     version.ObservedAt,
				ObjectID: "c1/cm-uid",
				Family:   "new",
				Path:     "new.path",
				Op:       "replace",
				New:      "new",
				Severity: 20,
			}},
			Edges: []core.Edge{{
				Type:      "new_edge",
				SourceID:  "c1/cm-uid",
				TargetID:  "c1/configmaps/default/settings",
				ValidFrom: version.ObservedAt,
			}},
		}, nil
	}

	dryRun, err := store.ReindexEvidence(ctx, ReindexOptions{DryRun: true, Evidence: buildEvidence})
	if err != nil {
		t.Fatal(err)
	}
	if dryRun.FactsDeleted != 1 || dryRun.FactsInserted != 1 || dryRun.ChangesDeleted != 1 || dryRun.ChangesInserted != 1 || dryRun.EdgesDeleted != 1 || dryRun.EdgesInserted != 1 {
		t.Fatalf("dry-run report = %#v", dryRun)
	}
	assertSQLCount(t, store, `select count(*) from object_facts where fact_key = 'old.fact'`, 1)
	assertSQLCount(t, store, `select count(*) from object_facts where fact_key = 'new.fact'`, 0)

	applied, err := store.ReindexEvidence(ctx, ReindexOptions{Evidence: buildEvidence})
	if err != nil {
		t.Fatal(err)
	}
	if applied.FactsDeleted != 1 || applied.FactsInserted != 1 || applied.ChangesDeleted != 1 || applied.ChangesInserted != 1 || applied.EdgesDeleted != 1 || applied.EdgesInserted != 1 {
		t.Fatalf("applied report = %#v", applied)
	}
	assertSQLCount(t, store, `select count(*) from object_facts where fact_key = 'old.fact'`, 0)
	assertSQLCount(t, store, `select count(*) from object_facts where fact_key = 'new.fact'`, 1)
	assertSQLCount(t, store, `select count(*) from object_changes where change_family = 'new'`, 1)
	assertSQLCount(t, store, `select count(*) from object_edges where edge_type = 'new_edge'`, 1)
}

func assertSQLCount(t *testing.T, store *Store, query string, want int64) {
	t.Helper()
	var got int64
	if err := store.db.QueryRow(query).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("%s = %d, want %d", query, got, want)
	}
}
