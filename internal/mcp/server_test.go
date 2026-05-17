package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"kube-insight/internal/ingest"
	"kube-insight/internal/storage"
	"kube-insight/internal/storage/sqlite"
)

func TestServeStdioTools(t *testing.T) {
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

	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
		`{"jsonrpc":"2.0","id":3,"method":"prompts/list","params":{}}`,
		`{"jsonrpc":"2.0","id":4,"method":"prompts/get","params":{"name":"kube_insight_coverage_first","arguments":{"symptom":"webhook failures"}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"kube_insight_sql","arguments":{"sql":"select name from latest_index","maxRows":5}}}`,
		`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"kube_insight_history","arguments":{"kind":"Pod","namespace":"default","name":"api-1","maxVersions":2,"maxObservations":5}}}`,
		`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"kube_insight_sql","arguments":{"sql":"delete from latest_index"}}}`,
	}, "\n") + "\n"
	var output bytes.Buffer
	if err := ServeStdio(context.Background(), strings.NewReader(input), &output, ServerOptions{DBPath: dbPath}); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 7 {
		t.Fatalf("responses = %d: %s", len(lines), output.String())
	}
	for _, want := range []string{
		`"protocolVersion":"2025-06-18"`,
		`"prompts":{}`,
		`"name":"kube_insight_sql"`,
		`"name":"kube_insight_history"`,
		`"name":"kube_insight_coverage_first"`,
		`webhook failures`,
		`\"name\": \"api-1\"`,
		`\"observations\": 1`,
		`"isError":true`,
	} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("output missing %q: %s", want, output.String())
		}
	}

	var response map[string]any
	if err := json.Unmarshal([]byte(lines[4]), &response); err != nil {
		t.Fatal(err)
	}
	if response["id"].(float64) != 5 {
		t.Fatalf("sql response id = %#v", response["id"])
	}
}

func TestHTTPMCPTools(t *testing.T) {
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
	mux := http.NewServeMux()
	mux.HandleFunc("POST /mcp", func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var body bytes.Buffer
		if _, err := body.ReadFrom(r.Body); err != nil {
			t.Fatal(err)
		}
		response, ok, err := handler.HandleJSONRPC(r.Context(), body.Bytes())
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		_, _ = w.Write(response)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	resp, err := http.Post(server.URL+"/mcp", "application/json", strings.NewReader(
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"kube_insight_sql","arguments":{"sql":"select name from latest_index","maxRows":5}}}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var output bytes.Buffer
	if _, err := output.ReadFrom(resp.Body); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `\"name\": \"api-1\"`) {
		t.Fatalf("response missing pod name: %s", output.String())
	}

	resp, err = http.Post(server.URL+"/mcp", "application/json", strings.NewReader(
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"kube_insight_history","arguments":{"kind":"Pod","namespace":"default","name":"api-1","maxVersions":2,"maxObservations":5}}}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	output.Reset()
	if _, err := output.ReadFrom(resp.Body); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `\"observations\": 1`) {
		t.Fatalf("history response missing observations: %s", output.String())
	}
}

type fakeMCPReadStore struct {
	closed bool
}

func (f *fakeMCPReadStore) Close() error {
	f.closed = true
	return nil
}

func (f *fakeMCPReadStore) QuerySchema(context.Context) (storage.SQLSchema, error) {
	return storage.SQLSchema{
		Notes: []string{"clickhouse fake injected backend"},
		Tables: []storage.SQLSchemaTable{{
			Name:    "versions",
			Columns: []storage.SQLSchemaColumn{{Name: "kind", Type: "String"}},
		}},
	}, nil
}

func (f *fakeMCPReadStore) QuerySQL(context.Context, storage.SQLQueryOptions) (storage.SQLQueryResult, error) {
	return storage.SQLQueryResult{
		Columns:  []string{"kind"},
		Rows:     []map[string]any{{"kind": "Pod"}},
		RowCount: 1,
	}, nil
}

func (f *fakeMCPReadStore) ResourceHealth(context.Context, storage.ResourceHealthOptions) (storage.ResourceHealthReport, error) {
	return storage.ResourceHealthReport{
		Summary:   storage.ResourceHealthSummary{Resources: 1, Healthy: 1, Complete: true},
		ByStatus:  map[string]int{"watching": 1},
		Resources: []storage.ResourceHealthRecord{{ClusterID: "c1", Version: "v1", Resource: "pods", Kind: "Pod", Status: "watching"}},
	}, nil
}

func (f *fakeMCPReadStore) ObjectHistory(context.Context, storage.ObjectTarget, storage.ObjectHistoryOptions) (storage.ObjectHistory, error) {
	return storage.ObjectHistory{
		Object:  storage.ObjectRecord{ClusterID: "c1", Kind: "Pod", Namespace: "default", Name: "api-1"},
		Summary: storage.ObjectHistorySummary{Versions: 1, Observations: 1},
	}, nil
}

func TestServerUsesInjectedReadStore(t *testing.T) {
	var opened int
	store := &fakeMCPReadStore{}
	server, err := NewServer(ServerOptions{
		OpenStore: func(context.Context) (ReadStore, error) {
			opened++
			return store, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	calls := []string{
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"kube_insight_schema","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"kube_insight_sql","arguments":{"sql":"select kind from versions","maxRows":1}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"kube_insight_health","arguments":{"cluster":"c1"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"kube_insight_history","arguments":{"kind":"Pod","namespace":"default","name":"api-1"}}}`,
	}
	var output string
	for _, call := range calls {
		response, ok, err := server.HandleJSONRPC(context.Background(), []byte(call))
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			t.Fatalf("missing response for %s", call)
		}
		output += string(response)
	}
	if opened != len(calls) {
		t.Fatalf("open count = %d, want %d", opened, len(calls))
	}
	for _, want := range []string{`clickhouse`, `fake injected backend`, `\"kind\": \"Pod\"`, `\"healthy\": 1`, `\"observations\": 1`} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q: %s", want, output)
		}
	}
	if !store.closed {
		t.Fatalf("per-request store was not closed")
	}
}
