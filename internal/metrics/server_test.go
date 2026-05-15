package metrics

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"kube-insight/internal/core"
	"kube-insight/internal/extractor"
	"kube-insight/internal/storage/sqlite"
)

func TestServerExposesPrometheusMetrics(t *testing.T) {
	dbPath := t.TempDir() + "/metrics.db"
	store, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	ref := core.ResourceRef{ClusterID: "c1", Version: "v1", Resource: "pods", Kind: "Pod", Namespace: "default", Name: "api-1", UID: "pod-uid"}
	obj := map[string]any{
		"apiVersion": "v1",
		"kind":       "Pod",
		"metadata": map[string]any{
			"name":      "api-1",
			"namespace": "default",
			"uid":       "pod-uid",
		},
		"status": map[string]any{"phase": "Running"},
	}
	if err := store.PutObservation(context.Background(), core.Observation{Type: core.ObservationModified, ObservedAt: time.Unix(10, 0), Ref: ref, Object: obj}, extractor.Evidence{}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	server, err := NewServer(ServerOptions{DBPath: dbPath})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	for _, want := range []string{
		"kube_insight_objects",
		"kube_insight_versions",
		"kube_insight_storage_bytes",
		"kube_insight_resource_streams",
	} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("metrics missing %q: %s", want, rec.Body.String())
		}
	}
}
