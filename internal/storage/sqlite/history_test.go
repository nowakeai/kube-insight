package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"kube-insight/internal/core"
	"kube-insight/internal/extractor"
)

func TestObjectHistorySeparatesVersionsAndObservations(t *testing.T) {
	ctx := context.Background()
	store, err := Open(filepath.Join(t.TempDir(), "kube-insight.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ref := core.ResourceRef{ClusterID: "c1", Version: "v1", Resource: "configmaps", Kind: "ConfigMap", Namespace: "default", Name: "settings", UID: "cm-uid"}
	obj := map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": map[string]any{
			"name":      "settings",
			"namespace": "default",
			"uid":       "cm-uid",
		},
		"data": map[string]any{"mode": "prod"},
	}
	if err := store.PutObservation(ctx, core.Observation{Type: core.ObservationModified, ObservedAt: time.Unix(10, 0), ResourceVersion: "10", Ref: ref, Object: obj}, extractor.Evidence{}); err != nil {
		t.Fatal(err)
	}
	if err := store.PutObservation(ctx, core.Observation{Type: core.ObservationModified, ObservedAt: time.Unix(20, 0), ResourceVersion: "11", Ref: ref, Object: obj}, extractor.Evidence{}); err != nil {
		t.Fatal(err)
	}
	obj["data"] = map[string]any{"mode": "dev"}
	if err := store.PutObservation(ctx, core.Observation{Type: core.ObservationModified, ObservedAt: time.Unix(30, 0), ResourceVersion: "12", Ref: ref, Object: obj}, extractor.Evidence{}); err != nil {
		t.Fatal(err)
	}

	history, err := store.ObjectHistory(ctx, ObjectTarget{ClusterID: "c1", Kind: "ConfigMap", Namespace: "default", Name: "settings"}, ObjectHistoryOptions{
		IncludeDiffs: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if history.Summary.Versions != 2 || history.Summary.Observations != 3 || history.Summary.UnchangedObservations != 1 {
		t.Fatalf("summary = %#v", history.Summary)
	}
	if len(history.Versions) != 2 || history.Versions[1].ObservationCount != 2 || history.Versions[1].UnchangedObservations != 1 {
		t.Fatalf("versions = %#v", history.Versions)
	}
	if len(history.Observations) != 3 || history.Observations[0].ResourceVersion != "12" || history.Observations[1].ContentChanged {
		t.Fatalf("observations = %#v", history.Observations)
	}
	if len(history.VersionDiffs) != 1 || len(history.VersionDiffs[0].Changes) == 0 {
		t.Fatalf("diffs = %#v", history.VersionDiffs)
	}
}
