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
	"kube-insight/internal/storage"
	"kube-insight/internal/storage/clickhouse"
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

func TestClickHouseCollectorExposesStorageEfficiencyMetrics(t *testing.T) {
	server, err := NewServer(ServerOptions{
		Driver: "clickhouse",
		OpenClickHouseStore: func(context.Context) (clickHouseMetricsStore, error) {
			return fakeClickHouseMetricsStore{}, nil
		},
	})
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
		"kube_insight_storage_compression_ratio 10",
		"kube_insight_storage_compressed_bytes_per_row 50",
		`kube_insight_storage_bytes{kind="clickhouse_compressed"} 1000`,
		`kube_insight_storage_bytes{kind="clickhouse_proof_compressed"} 700`,
		`kube_insight_storage_bytes{kind="clickhouse_kube_insight_inactive_bytes_on_disk"} 5000`,
		`kube_insight_storage_parts{kind="clickhouse_kube_insight_inactive_parts"} 12`,
		`kube_insight_storage_part_age_seconds{kind="clickhouse_kube_insight_inactive_oldest_inactive"} 480`,
		`kube_insight_storage_bytes{kind="clickhouse_system_active_bytes_on_disk"} 2000`,
		`kube_insight_resource_streams{state="healthy"} 1`,
	} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("metrics missing %q: %s", want, rec.Body.String())
		}
	}
}

type fakeClickHouseMetricsStore struct{}

func (fakeClickHouseMetricsStore) Close() error { return nil }

func (fakeClickHouseMetricsStore) Stats(context.Context) (clickhouse.Stats, error) {
	return clickhouse.Stats{
		Clusters:               1,
		APIResources:           2,
		Objects:                3,
		Observations:           4,
		Versions:               5,
		Facts:                  6,
		Edges:                  7,
		Changes:                8,
		LatestRows:             3,
		IngestionOffsets:       2,
		CompressedBytes:        1000,
		UncompressedBytes:      10000,
		ProofCompressedBytes:   700,
		DerivedCompressedBytes: 200,
		CompressionRatio:       10,
		CompressedBytesPerRow:  50,
		TableParts: []clickhouse.TablePartStats{
			{Table: "versions", Rows: 10, BytesOnDisk: 720, CompressedBytes: 700, UncompressedBytes: 7000},
		},
		Footprint: []clickhouse.FootprintStats{
			{Database: "kube_insight", State: "active", Parts: 3, BytesOnDisk: 1000},
			{Database: "kube_insight", State: "inactive", Parts: 12, BytesOnDisk: 5000, OldestInactiveAgeSeconds: 480},
			{Database: "system", State: "active", Parts: 4, BytesOnDisk: 2000},
		},
	}, nil
}

func (fakeClickHouseMetricsStore) ResourceHealth(context.Context, storage.ResourceHealthOptions) (storage.ResourceHealthReport, error) {
	return storage.ResourceHealthReport{
		Summary:  storage.ResourceHealthSummary{Healthy: 1, Resources: 1, Complete: true},
		ByStatus: map[string]int{"watching": 1},
	}, nil
}
