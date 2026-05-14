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
	"kube-insight/internal/storage/sqlite"
)

func TestRunQueryHistoryWithSQLite(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "kube-insight.db")
	store, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
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
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	err = Run(context.Background(), []string{
		"query", "history",
		"--db", dbPath,
		"--kind", "ConfigMap",
		"--namespace", "default",
		"--name", "settings",
		"--max-observations", "5",
	}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"kind": "ConfigMap"`,
		`"versions": 1`,
		`"observations": 2`,
		`"unchangedObservations": 1`,
		`"contentChanged": false`,
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("history missing %q: %s", want, stdout.String())
		}
	}
}
