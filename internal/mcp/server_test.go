package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"kube-insight/internal/ingest"
	"kube-insight/internal/storage"
	"kube-insight/internal/storage/sqlite"
)

func seedMCPDB(t *testing.T) string {
	t.Helper()
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
	return dbPath
}

func connectInMemoryClient(t *testing.T, server *Server) *sdkmcp.ClientSession {
	t.Helper()
	serverTransport, clientTransport := sdkmcp.NewInMemoryTransports()
	serverSession, err := server.sdkServer.Connect(context.Background(), serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test-client", Version: "0.0.1"}, nil)
	clientSession, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = clientSession.Close()
		_ = serverSession.Close()
	})
	return clientSession
}

func callToolText(t *testing.T, session *sdkmcp.ClientSession, name string, args map[string]any) (string, bool) {
	t.Helper()
	result, err := session.CallTool(context.Background(), &sdkmcp.CallToolParams{
		Name:      name,
		Arguments: args,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Content) == 0 {
		t.Fatalf("%s returned no content", name)
	}
	content, ok := result.Content[0].(*sdkmcp.TextContent)
	if !ok {
		t.Fatalf("%s returned %T, want TextContent", name, result.Content[0])
	}
	return content.Text, result.IsError
}

func TestSDKMCPTools(t *testing.T) {
	server, err := NewServer(ServerOptions{DBPath: seedMCPDB(t)})
	if err != nil {
		t.Fatal(err)
	}
	session := connectInMemoryClient(t, server)
	init := session.InitializeResult()
	if init.ServerInfo.Name != "kube-insight" {
		t.Fatalf("server name = %q", init.ServerInfo.Name)
	}

	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var toolNames string
	for _, tool := range tools.Tools {
		toolNames += tool.Name + "\n"
	}
	for _, want := range []string{"kube_insight_schema", "kube_insight_sql", "kube_insight_history"} {
		if !strings.Contains(toolNames, want) {
			t.Fatalf("tools missing %q: %s", want, toolNames)
		}
	}

	prompts, err := session.ListPrompts(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(prompts.Prompts) == 0 {
		t.Fatalf("expected prompts")
	}
	prompt, err := session.GetPrompt(context.Background(), &sdkmcp.GetPromptParams{
		Name:      "kube_insight_coverage_first",
		Arguments: map[string]string{"symptom": "webhook failures"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt.Messages[0].Content.(*sdkmcp.TextContent).Text, "webhook failures") {
		t.Fatalf("prompt missing symptom: %#v", prompt.Messages[0].Content)
	}

	sqlText, sqlError := callToolText(t, session, "kube_insight_sql", map[string]any{
		"sql":     "select name from latest_index",
		"maxRows": 5,
	})
	if sqlError || !strings.Contains(sqlText, `"name": "api-1"`) {
		t.Fatalf("sql result isError=%v text=%s", sqlError, sqlText)
	}

	historyText, historyError := callToolText(t, session, "kube_insight_history", map[string]any{
		"kind":            "Pod",
		"namespace":       "default",
		"name":            "api-1",
		"maxVersions":     2,
		"maxObservations": 5,
	})
	if historyError || !strings.Contains(historyText, `"observations": 1`) {
		t.Fatalf("history result isError=%v text=%s", historyError, historyText)
	}

	deleteText, deleteError := callToolText(t, session, "kube_insight_sql", map[string]any{
		"sql": "delete from latest_index",
	})
	if !deleteError || deleteText == "" {
		t.Fatalf("delete result isError=%v text=%s", deleteError, deleteText)
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

func TestStreamableHTTPMCPTransport(t *testing.T) {
	handler, err := NewServer(ServerOptions{DBPath: seedMCPDB(t)})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(sdkmcp.NewStreamableHTTPHandler(func(*http.Request) *sdkmcp.Server {
		return handler.sdkServer
	}, nil))
	defer httpServer.Close()

	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test-client", Version: "0.0.1"}, nil)
	session, err := client.Connect(context.Background(), &sdkmcp.StreamableClientTransport{Endpoint: httpServer.URL}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	text, isError := callToolText(t, session, "kube_insight_sql", map[string]any{
		"sql":     "select name from latest_index",
		"maxRows": 5,
	})
	if isError || !strings.Contains(text, `"name": "api-1"`) {
		t.Fatalf("streamable HTTP result isError=%v text=%s", isError, text)
	}
}

func TestSSEMCPTransport(t *testing.T) {
	handler, err := NewServer(ServerOptions{DBPath: seedMCPDB(t)})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/sse", sdkmcp.NewSSEHandler(func(*http.Request) *sdkmcp.Server {
		return handler.sdkServer
	}, nil))
	httpServer := httptest.NewServer(mux)
	defer httpServer.Close()

	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test-client", Version: "0.0.1"}, nil)
	session, err := client.Connect(context.Background(), &sdkmcp.SSEClientTransport{Endpoint: httpServer.URL + "/sse"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	text, isError := callToolText(t, session, "kube_insight_sql", map[string]any{
		"sql":     "select name from latest_index",
		"maxRows": 5,
	})
	if isError || !strings.Contains(text, `"name": "api-1"`) {
		t.Fatalf("SSE result isError=%v text=%s", isError, text)
	}
}

func TestPromptsGuideSQLFirstInvestigation(t *testing.T) {
	cases := []struct {
		name string
		want []string
	}{
		{
			name: "kube_insight_coverage_first",
			want: []string{
				"Default to SQL after schema detection",
				"kube_insight_sql",
				"ingestion_offsets is append-only",
				"argMax(status, updated_at)",
				"facts and changes",
				"service.load_balancer.pending",
				"observations and versions",
			},
		},
		{
			name: "kube_insight_event_history",
			want: []string{
				"kube_insight_sql as the primary interface",
				"object_facts",
				"ClickHouse-compatible backends use facts",
				"object_edges",
				"ClickHouse-compatible backends use edges",
				"changes, observations, and versions",
			},
		},
		{
			name: "kube_insight_object_history",
			want: []string{
				"kube_insight_schema first",
				"kube_insight_sql as the primary investigation interface",
				"object_facts/object_edges/object_observations/latest_index",
				"facts/edges/changes/observations/versions",
				"argMax(status, updated_at)",
				"Use kube_insight_history after SQL has identified the object",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			text, _, err := promptText(tc.name, map[string]string{
				"cluster": "c1",
				"symptom": "pod restarts",
				"reason":  "FailedScheduling",
				"keyword": "quota",
			})
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range tc.want {
				if !strings.Contains(text, want) {
					t.Fatalf("prompt missing %q:\n%s", want, text)
				}
			}
		})
	}
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

	session := connectInMemoryClient(t, server)
	calls := []struct {
		name string
		args map[string]any
	}{
		{name: "kube_insight_schema", args: map[string]any{}},
		{name: "kube_insight_sql", args: map[string]any{"sql": "select kind from versions", "maxRows": 1}},
		{name: "kube_insight_health", args: map[string]any{"cluster": "c1"}},
		{name: "kube_insight_history", args: map[string]any{"kind": "Pod", "namespace": "default", "name": "api-1"}},
	}
	var output string
	for _, call := range calls {
		text, isError := callToolText(t, session, call.name, call.args)
		if isError {
			t.Fatalf("%s returned tool error: %s", call.name, text)
		}
		output += text
	}
	if opened != len(calls) {
		t.Fatalf("open count = %d, want %d", opened, len(calls))
	}
	for _, want := range []string{`clickhouse`, `fake injected backend`, `"kind": "Pod"`, `"healthy": 1`, `"observations": 1`} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q: %s", want, output)
		}
	}
	if !store.closed {
		t.Fatalf("per-request store was not closed")
	}
}
