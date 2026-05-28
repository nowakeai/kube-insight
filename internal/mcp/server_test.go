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
	for _, want := range []string{"kube_insight_schema", "kube_insight_sql", "kube_insight_search", "kube_insight_history", "kube_insight_topology", "kube_insight_service_investigation"} {
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

func (f *fakeMCPReadStore) SearchEvidence(_ context.Context, opts storage.EvidenceSearchOptions) (storage.EvidenceSearchResult, error) {
	return storage.EvidenceSearchResult{
		Input:   opts,
		Summary: storage.EvidenceSearchSummary{Matches: 1},
		Matches: []storage.EvidenceSearchMatch{{
			Object:  storage.ObjectRecord{ClusterID: "c1", Version: "v1", Resource: "pods", Kind: "Pod", Namespace: "default", Name: "api-1"},
			Score:   2,
			Reasons: []string{"fact:status=Running"},
		}},
	}, nil
}

func (f *fakeMCPReadStore) ObjectHistory(context.Context, storage.ObjectTarget, storage.ObjectHistoryOptions) (storage.ObjectHistory, error) {
	return storage.ObjectHistory{
		Object:  storage.ObjectRecord{ClusterID: "c1", Kind: "Pod", Namespace: "default", Name: "api-1"},
		Summary: storage.ObjectHistorySummary{Versions: 1, Observations: 1},
	}, nil
}

func (f *fakeMCPReadStore) Topology(context.Context, storage.ObjectTarget) (storage.TopologyGraph, error) {
	return storage.TopologyGraph{
		Root:    storage.ObjectRecord{ClusterID: "c1", Version: "v1", Resource: "pods", Kind: "Pod", Namespace: "default", Name: "api-1"},
		Summary: storage.TopologySummary{Nodes: 1, Edges: 0},
	}, nil
}

func (f *fakeMCPReadStore) InvestigateServiceWithOptions(context.Context, storage.ObjectTarget, storage.InvestigationOptions) (storage.ServiceInvestigation, error) {
	return storage.ServiceInvestigation{
		Service: storage.EvidenceBundle{Object: storage.ObjectRecord{ClusterID: "c1", Version: "v1", Resource: "services", Kind: "Service", Namespace: "default", Name: "api"}},
		Summary: storage.ServiceInvestigationSummary{Objects: 1, Pods: 1},
	}, nil
}

type countingHealthReadStore struct {
	healthCalls int
	closeCalls  int
}

func (f *countingHealthReadStore) Close() error {
	f.closeCalls++
	return nil
}

func (f *countingHealthReadStore) ResourceHealth(context.Context, storage.ResourceHealthOptions) (storage.ResourceHealthReport, error) {
	f.healthCalls++
	return storage.ResourceHealthReport{
		Summary:   storage.ResourceHealthSummary{Resources: 1, Healthy: f.healthCalls, Complete: true},
		ByStatus:  map[string]int{"watching": f.healthCalls},
		Resources: []storage.ResourceHealthRecord{{ClusterID: "c1", Version: "v1", Resource: "pods", Kind: "Pod", Status: "watching"}},
	}, nil
}

func TestQueryHealthCachesDefaultHealthReport(t *testing.T) {
	var opened int
	store := &countingHealthReadStore{}
	server, err := NewServer(ServerOptions{
		OpenStore: func(context.Context) (ReadStore, error) {
			opened++
			return store, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()

	textValue, err := server.queryHealth(context.Background(), healthArguments{})
	if err != nil {
		t.Fatal(err)
	}
	text := textValue.(string)
	if !strings.Contains(text, "healthy=1") {
		t.Fatalf("first health output missing healthy=1: %s", text)
	}
	if opened != 1 || store.healthCalls != 1 || store.closeCalls != 1 {
		t.Fatalf("after first query opened=%d healthCalls=%d closeCalls=%d", opened, store.healthCalls, store.closeCalls)
	}

	textValue, err = server.queryHealth(context.Background(), healthArguments{})
	if err != nil {
		t.Fatal(err)
	}
	text = textValue.(string)
	if !strings.Contains(text, "healthy=1") {
		t.Fatalf("cached health output missing healthy=1: %s", text)
	}
	if opened != 1 || store.healthCalls != 1 || store.closeCalls != 1 {
		t.Fatalf("cache miss: opened=%d healthCalls=%d closeCalls=%d", opened, store.healthCalls, store.closeCalls)
	}
}

func TestRefreshHealthCacheUpdatesRegisteredReports(t *testing.T) {
	store := &countingHealthReadStore{}
	server, err := NewServer(ServerOptions{
		OpenStore: func(context.Context) (ReadStore, error) {
			return store, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()

	if _, err := server.queryHealth(context.Background(), healthArguments{}); err != nil {
		t.Fatal(err)
	}
	server.refreshHealthCache(context.Background())
	if store.healthCalls != 2 {
		t.Fatalf("healthCalls after refresh = %d, want 2", store.healthCalls)
	}

	textValue, err := server.queryHealth(context.Background(), healthArguments{})
	if err != nil {
		t.Fatal(err)
	}
	text := textValue.(string)
	if !strings.Contains(text, "healthy=2") {
		t.Fatalf("refreshed health output missing healthy=2: %s", text)
	}
	if store.healthCalls != 2 {
		t.Fatalf("refreshed cache was not reused, healthCalls=%d", store.healthCalls)
	}
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

func TestToolDescriptionsGuideBoundedMCPUsage(t *testing.T) {
	descriptions := map[string]string{}
	for _, tool := range tools() {
		descriptions[tool.Name] = tool.Description
	}
	cases := map[string][]string{
		"kube_insight_sql": {
			"Always call kube_insight_schema first",
			"ClickHouse-compatible tables",
			"first inspect kube_insight_health without a cluster filter",
			"Keep maxRows bounded",
			"timestamp predicates",
			"indexed fact/change/edge fields",
		},
		"kube_insight_health": {
			"current-state claims",
			"Do not pass a user fragment",
			"compact DSL",
			"/api/v1/health endpoint is much larger",
		},
		"kube_insight_search": {
			"broad discovery",
			"includeBundles=false",
			"includeBundles=true only for top targets",
			"from/to time",
			"client context",
		},
		"kube_insight_history": {
			"one known object's",
			"includeDocs=false",
			"raw YAML/JSON proof",
		},
		"kube_insight_topology": {
			"one known Kubernetes object",
			"do not use it for broad discovery",
		},
		"kube_insight_service_investigation": {
			"exact Service namespace/name",
			"Start with low limits",
		},
	}
	for name, wants := range cases {
		t.Run(name, func(t *testing.T) {
			description := descriptions[name]
			if description == "" {
				t.Fatalf("missing description for %s", name)
			}
			for _, want := range wants {
				if !strings.Contains(description, want) {
					t.Fatalf("%s description missing %q:\n%s", name, want, description)
				}
			}
		})
	}
}

func TestPromptsGuideCoverageFirstInvestigation(t *testing.T) {
	cases := []struct {
		name string
		want []string
	}{
		{
			name: "kube_insight_coverage_first",
			want: []string{
				"Start with collector coverage",
				"kube_insight_health is required coverage evidence",
				"Call kube_insight_health first",
				"Do not pass the user fragment as the first health cluster argument",
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
				"kube_insight_health for collector coverage",
				"Call kube_insight_health first",
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
				"kube_insight_health for collector coverage",
				"Call kube_insight_health first",
				"Call kube_insight_schema before writing SQL",
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
		{name: "kube_insight_search", args: map[string]any{"query": "api", "limit": 999, "maxVersionsPerObject": 999}},
		{name: "kube_insight_history", args: map[string]any{"kind": "Pod", "namespace": "default", "name": "api-1"}},
		{name: "kube_insight_topology", args: map[string]any{"kind": "Pod", "namespace": "default", "name": "api-1"}},
		{name: "kube_insight_service_investigation", args: map[string]any{"namespace": "default", "name": "api", "maxEvidenceObjects": 999}},
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
	for _, want := range []string{`clickhouse`, `fake injected backend`, `"kind": "Pod"`, `healthy=1`, `"matches": 1`, `"observations": 1`, `"nodes": 1`, `"pods": 1`} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q: %s", want, output)
		}
	}
	if !store.closed {
		t.Fatalf("per-request store was not closed")
	}
}
