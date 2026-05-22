package mcp

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	einomcp "github.com/cloudwego/eino-ext/components/tool/mcp"
	"github.com/cloudwego/eino/components/tool"
	markclient "github.com/mark3labs/mcp-go/client"
	markmcp "github.com/mark3labs/mcp-go/mcp"
)

func TestEinoMCPAdapterWithStreamableHTTP(t *testing.T) {
	srv, err := NewServer(ServerOptions{DBPath: seedMCPDB(t)})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(srv.StreamableHTTPHandler(0))
	t.Cleanup(httpServer.Close)
	cli, err := markclient.NewStreamableHttpClient(httpServer.URL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cli.Close() })
	assertEinoAdapterCanCallSQL(t, cli)
}

func TestEinoMCPAdapterWithSSE(t *testing.T) {
	t.Skip("legacy SSE is kept for external compatibility; the built-in Eino agent uses Streamable HTTP because mcp-go SSE client shutdown can hang against the go-sdk SSE handler")
}

func assertEinoAdapterCanCallSQL(t *testing.T, cli *markclient.Client) {
	t.Helper()
	ctx := context.Background()
	if err := cli.Start(ctx); err != nil {
		t.Fatal(err)
	}
	init := markmcp.InitializeRequest{}
	init.Params.ProtocolVersion = markmcp.LATEST_PROTOCOL_VERSION
	init.Params.ClientInfo = markmcp.Implementation{Name: "kube-insight-eino-test", Version: "0.0.1"}
	if _, err := cli.Initialize(ctx, init); err != nil {
		t.Fatal(err)
	}

	allTools, err := einomcp.GetTools(ctx, &einomcp.Config{Cli: cli})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, baseTool := range allTools {
		info, err := baseTool.Info(ctx)
		if err != nil {
			t.Fatal(err)
		}
		seen[info.Name] = true
	}
	for _, want := range []string{"kube_insight_schema", "kube_insight_sql", "kube_insight_search", "kube_insight_health", "kube_insight_history", "kube_insight_topology", "kube_insight_service_investigation"} {
		if !seen[want] {
			t.Fatalf("adapter tools missing %q: %#v", want, seen)
		}
	}

	tools, err := einomcp.GetTools(ctx, &einomcp.Config{Cli: cli, ToolNameList: []string{"kube_insight_sql"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 {
		t.Fatalf("tools len = %d", len(tools))
	}
	info, err := tools[0].Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "kube_insight_sql" || info.Desc == "" || info.ParamsOneOf == nil {
		t.Fatalf("tool info = %#v", info)
	}
	invokable, ok := tools[0].(tool.InvokableTool)
	if !ok {
		t.Fatalf("tool is not invokable: %T", tools[0])
	}
	result, err := invokable.InvokableRun(ctx, `{"sql":"select name from latest_index","maxRows":5}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "api-1") {
		t.Fatalf("adapter result missing query proof: %s", result)
	}
}
