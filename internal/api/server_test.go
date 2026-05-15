package api

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"kube-insight/internal/ingest"
	"kube-insight/internal/storage/sqlite"
)

func TestServerReadOnlyAgentEndpoints(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "kube-insight.db")
	store, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	pipeline := ingest.DefaultPipeline(store)
	pipeline.ClusterID = "c1"
	_, err = pipeline.IngestJSON(context.Background(), []byte(`{
	  "apiVersion": "v1",
	  "kind": "Pod",
	  "metadata": {"name": "api-1", "namespace": "default", "uid": "pod-uid"},
	  "status": {"phase": "Running"}
	}`))
	closeErr := store.Close()
	if err != nil {
		t.Fatal(err)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}

	handler, err := NewServer(ServerOptions{DBPath: dbPath})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	assertGETContains(t, server.URL+"/healthz", `"ok": true`)
	assertGETContains(t, server.URL+"/api/v1/schema", `"name": "latest_index"`)
	assertGETContains(t, server.URL+"/api/v1/schema", `"relationships":`)
	assertGETContains(t, server.URL+"/api/v1/schema", `"recipes":`)
	assertPOSTContains(t, server.URL+"/api/v1/sql", `{"sql":"select name from latest_index","maxRows":5}`, `"name": "api-1"`)
	assertPOSTContains(t, server.URL+"/api/v1/sql", `{"sql":"delete from latest_index"}`, `"error":`)
	assertGETContains(t, server.URL+"/api/v1/health?limit=5", `"summary":`)
	assertGETContains(t, server.URL+"/api/v1/history?kind=Pod&namespace=default&name=api-1&maxVersions=2&maxObservations=5", `"observations": 1`)
	assertGETContains(t, server.URL+"/api/v1/history?kind=Pod&namespace=default&name=api-1&maxVersions=2&maxObservations=5", `"versions": 1`)
}

func assertGETContains(t *testing.T, url string, want string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body bytes.Buffer
	if _, err := body.ReadFrom(resp.Body); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body.String(), want) {
		t.Fatalf("GET %s missing %q: %s", url, want, body.String())
	}
}

func assertPOSTContains(t *testing.T, url, body, want string) {
	t.Helper()
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out bytes.Buffer
	if _, err := out.ReadFrom(resp.Body); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), want) {
		t.Fatalf("POST %s missing %q: %s", url, want, out.String())
	}
}
