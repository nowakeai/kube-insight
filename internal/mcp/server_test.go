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
