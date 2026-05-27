package clickhouse

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"kube-insight/internal/storage"
)

func TestStoreClustersRoundTripMetadataQueriesClusterTable(t *testing.T) {
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		request := string(data)
		requests = append(requests, request)
		switch {
		case strings.Contains(request, "INSERT INTO `ki`.`clusters`"):
			w.WriteHeader(http.StatusOK)
		case strings.Contains(request, "FROM `ki`.clusters"):
			_, _ = w.Write([]byte(`{"data":[{"name":"k8s-abc","uid":"abc","source":"gke_project_region_cluster https://10.0.0.1","created_at":"2026-05-25 00:00:00.000","objects":"2","versions":"3","latest":"2"}],"rows":1}`))
		default:
			t.Fatalf("unexpected query: %s", request)
		}
	}))
	defer server.Close()

	store := &Store{Client: HTTPClient{Endpoint: server.URL, HTTPClient: server.Client()}, Database: "ki"}
	if err := store.UpsertCluster(context.Background(), storage.ClusterRecord{Name: "k8s-abc", UID: "abc", Source: "gke_project_region_cluster https://10.0.0.1"}); err != nil {
		t.Fatal(err)
	}
	clusters, err := store.ListClusters(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(clusters) != 1 || clusters[0].Name != "k8s-abc" || clusters[0].Source != "gke_project_region_cluster https://10.0.0.1" || clusters[0].Objects != 2 || clusters[0].Versions != 3 || clusters[0].Latest != 2 {
		t.Fatalf("clusters = %#v", clusters)
	}
	joined := strings.Join(requests, "\n")
	for _, want := range []string{"INSERT INTO `ki`.`clusters`", "FROM `ki`.clusters", "FROM `ki`.versions", "latest_created_at AS created_at"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("requests missing %q: %s", want, joined)
		}
	}
	if strings.Contains(joined, "max(created_at) AS created_at") {
		t.Fatalf("ListClusters should avoid same-name aggregate aliases that can trip ClickHouse analyzer: %s", joined)
	}
}
