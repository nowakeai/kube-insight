package sqlite

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kube-insight/internal/core"
	"kube-insight/internal/extractor"
)

func TestStoreRawLatestTracksUnfilteredObservationWithoutNewVersion(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "kube-insight.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ref := core.ResourceRef{ClusterID: "c1", Version: "v1", Resource: "configmaps", Kind: "ConfigMap", Namespace: "default", Name: "settings", UID: "cm-uid"}
	retained := map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": map[string]any{
			"name":      "settings",
			"namespace": "default",
			"uid":       "cm-uid",
		},
		"data": map[string]any{"mode": "prod"},
	}
	raw := map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": map[string]any{
			"name":            "settings",
			"namespace":       "default",
			"uid":             "cm-uid",
			"resourceVersion": "11",
			"generation":      7,
		},
		"data": map[string]any{"mode": "prod"},
	}
	ctx := context.Background()
	if err := store.PutRawLatest(ctx, core.Observation{Type: core.ObservationModified, ObservedAt: time.Unix(10, 0), ResourceVersion: "10", Ref: ref, Object: retained}); err != nil {
		t.Fatal(err)
	}
	if err := store.PutObservation(ctx, core.Observation{Type: core.ObservationModified, ObservedAt: time.Unix(10, 0), ResourceVersion: "10", Ref: ref, Object: retained}, extractor.Evidence{}); err != nil {
		t.Fatal(err)
	}
	if err := store.PutRawLatest(ctx, core.Observation{Type: core.ObservationModified, ObservedAt: time.Unix(20, 0), ResourceVersion: "11", Ref: ref, Object: raw}); err != nil {
		t.Fatal(err)
	}
	if err := store.PutObservation(ctx, core.Observation{Type: core.ObservationModified, ObservedAt: time.Unix(20, 0), ResourceVersion: "11", Ref: ref, Object: retained}, extractor.Evidence{}); err != nil {
		t.Fatal(err)
	}

	var versions int
	if err := store.db.QueryRow(`select count(*) from versions`).Scan(&versions); err != nil {
		t.Fatal(err)
	}
	if versions != 1 {
		t.Fatalf("versions = %d, want 1", versions)
	}
	var rawResourceVersion string
	var rawGeneration int
	var rawDoc string
	if err := store.db.QueryRow(`select resource_version, generation, doc from latest_raw_documents where name = 'settings'`).Scan(&rawResourceVersion, &rawGeneration, &rawDoc); err != nil {
		t.Fatal(err)
	}
	if rawResourceVersion != "11" || rawGeneration != 7 || !strings.Contains(rawDoc, `"resourceVersion":"11"`) {
		t.Fatalf("raw latest = resourceVersion %q generation %d doc %s", rawResourceVersion, rawGeneration, rawDoc)
	}
}
