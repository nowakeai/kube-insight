package mcp

import (
	"bytes"
	"context"
	"encoding/json"
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
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"kube_insight_sql","arguments":{"sql":"select name from latest_index","maxRows":5}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"kube_insight_sql","arguments":{"sql":"delete from latest_index"}}}`,
	}, "\n") + "\n"
	var output bytes.Buffer
	if err := ServeStdio(context.Background(), strings.NewReader(input), &output, ServerOptions{DBPath: dbPath}); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 4 {
		t.Fatalf("responses = %d: %s", len(lines), output.String())
	}
	for _, want := range []string{
		`"protocolVersion":"2025-06-18"`,
		`"name":"kube_insight_sql"`,
		`\"name\": \"api-1\"`,
		`"isError":true`,
	} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("output missing %q: %s", want, output.String())
		}
	}

	var response map[string]any
	if err := json.Unmarshal([]byte(lines[2]), &response); err != nil {
		t.Fatal(err)
	}
	if response["id"].(float64) != 3 {
		t.Fatalf("third response id = %#v", response["id"])
	}
}
