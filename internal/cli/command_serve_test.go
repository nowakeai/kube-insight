package cli

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
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
		"--api-listen",
		"--mcp-listen",
		"--webui-listen",
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
