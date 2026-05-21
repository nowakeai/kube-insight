package cli

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	appconfig "kube-insight/internal/config"
)

func TestRunServeHelpShowsCombinedServiceFlags(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"serve", "--help"}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	for _, want := range []string{
		"serve [RESOURCE_PATTERN ...]",
		"--watch",
		"--api",
		"--mcp",
		"--webui",
		"--metrics",
		"--api-listen",
		"--mcp-listen",
		"--webui-listen",
		"--metrics-listen",
		"kube-insight serve mcp",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("stdout missing %q: %s", want, out)
		}
	}
}

func TestWatchLogLevelClassifiesHighVolumeAndErrors(t *testing.T) {
	cases := map[string]slog.Level{
		"event":                    slog.LevelDebug,
		"bookmark":                 slog.LevelDebug,
		"listed resource":          slog.LevelDebug,
		"initial list worker done": slog.LevelDebug,
		"retry watch":              slog.LevelWarn,
		"event error":              slog.LevelWarn,
		"watch stream interrupted": slog.LevelInfo,
		"watch stream reconnect":   slog.LevelInfo,
		"resourceVersion expired":  slog.LevelWarn,
		"watch finished":           slog.LevelInfo,
	}
	for message, want := range cases {
		if got := watchLogLevel(message); got != want {
			t.Fatalf("%q level = %v, want %v", message, got, want)
		}
	}
}

func TestServiceStorageTargetUsesConfiguredBackend(t *testing.T) {
	cfg := appconfig.Default()
	cfg.Storage.Driver = "clickhouse"
	cfg.Storage.ClickHouse.Database = "ki"
	if got := serviceStorageTarget(cfg, "kubeinsight.db"); got != "clickhouse:ki" {
		t.Fatalf("clickhouse target = %q", got)
	}

	cfg.Storage.Driver = "sqlite"
	if got := serviceStorageTarget(cfg, "custom.db"); got != "custom.db" {
		t.Fatalf("sqlite target = %q", got)
	}
}

func TestAPIServerOptionsIncludesSecretSafeServerInfo(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-secret-value")
	cfg := appconfig.Default()
	cfg.Storage.Driver = "clickhouse"
	cfg.Storage.ClickHouse.Database = "ki"
	cfg.Server.Chat.Enabled = true
	cfg.Server.Chat.Provider = "openai-compatible"
	cfg.Server.Chat.APIKeyEnv = "OPENAI_API_KEY"
	cfg.Server.Chat.Model = "mimo-v2.5-pro"

	opts := apiServerOptions(cfg, "kubeinsight.db", serveSelection{
		API:           true,
		MCP:           true,
		WebUI:         true,
		Metrics:       true,
		Watch:         true,
		APIListen:     "127.0.0.1:8080",
		MCPListen:     "127.0.0.1:8090",
		WebUIListen:   "127.0.0.1:8081",
		MetricsListen: "127.0.0.1:9090",
	})
	info := opts.ServerInfo
	if info.Storage.Driver != "clickhouse" || info.Storage.Target != "clickhouse:ki" {
		t.Fatalf("storage info = %#v", info.Storage)
	}
	if !info.Components["api"].Enabled || info.Components["api"].Listen != "127.0.0.1:8080" {
		t.Fatalf("api component = %#v", info.Components["api"])
	}
	if !info.Components["metrics"].Enabled || info.Components["metrics"].URL != "http://127.0.0.1:9090/metrics" {
		t.Fatalf("metrics component = %#v", info.Components["metrics"])
	}
	if info.Chat.Provider != "openai-compatible" || info.Chat.Model != "mimo-v2.5-pro" {
		t.Fatalf("chat info = %#v", info.Chat)
	}
	if info.Chat.APIKeyEnv != "OPENAI_API_KEY" || !info.Chat.APIKeyConfigured {
		t.Fatalf("chat key info = %#v", info.Chat)
	}
}
