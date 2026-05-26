package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"kube-insight/internal/agent"
	"kube-insight/internal/storage/sqlite"
)

func TestRunDBAgentContextPrintsLatestCompletionRequest(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "kubeinsight.db")
	store, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	session, err := store.CreateSession(context.Background(), agent.CreateSessionInput{Title: "OOM follow-up"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.CreateRun(context.Background(), session.ID, agent.CreateRunInput{
		Input:    "最近1小时内呢",
		Provider: "openai-compatible",
		Model:    "test-model",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendRunEvent(context.Background(), run.ID, agent.AppendEventInput{
		Type: agent.EventCompletionRequest,
		Data: []byte(`{"mode":"generate","provider":"openai-compatible","model":"test-model","messages":[{"role":"user","content":"older request"}]}`),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendRunEvent(context.Background(), run.ID, agent.AppendEventInput{
		Type: agent.EventCompletionRequest,
		Data: []byte(`{"mode":"generate","provider":"openai-compatible","model":"test-model","messages":[{"role":"system","content":"Stable instruction."},{"role":"user","content":"最近有没有 OOM 现象？"},{"role":"assistant","tool_calls":[{"id":"call_oom","type":"function","function":{"name":"kube_insight_search","arguments":"{\"query\":\"OOMKilled\"}"}}]},{"role":"tool","tool_call_id":"call_oom","name":"kube_insight_search","content":"{\"matches\":1}"},{"role":"assistant","content":"发现 vmagent 有 OOMKilled。"},{"role":"user","content":"最近1小时内呢"}],"tools":[{"type":"function","function":{"name":"kube_insight_search"}}]}`),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if err := Run(context.Background(), []string{"--db", dbPath, "db", "agent-context", run.ID}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	for _, want := range []string{"Run: " + run.ID, "messages=6", "tools=1", "最近有没有 OOM 现象？", "kube_insight_search", "最近1小时内呢"} {
		if !strings.Contains(out, want) {
			t.Fatalf("stdout missing %q: %s", want, out)
		}
	}
	if strings.Contains(out, "older request") {
		t.Fatalf("default output should show latest request only: %s", out)
	}
}

func TestRunDBAgentContextJSONCanIncludeAllRequests(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "kubeinsight.db")
	store, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	session, err := store.CreateSession(context.Background(), agent.CreateSessionInput{})
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.CreateRun(context.Background(), session.ID, agent.CreateRunInput{Input: "follow up"})
	if err != nil {
		t.Fatal(err)
	}
	for _, content := range []string{"first request", "second request"} {
		if _, err := store.AppendRunEvent(context.Background(), run.ID, agent.AppendEventInput{
			Type: agent.EventCompletionRequest,
			Data: []byte(`{"mode":"generate","messages":[{"role":"user","content":"` + content + `"}]}`),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if err := Run(context.Background(), []string{"--db", dbPath, "db", "agent-context", run.ID, "--all", "--output", "json"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	for _, want := range []string{`"requests"`, `"messageCount": 1`, "first request", "second request"} {
		if !strings.Contains(out, want) {
			t.Fatalf("stdout missing %q: %s", want, out)
		}
	}
}

func TestRunDBAgentContextShowsLegacySummaryWithoutCompletionRequest(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "kubeinsight.db")
	store, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	session, err := store.CreateSession(context.Background(), agent.CreateSessionInput{})
	if err != nil {
		t.Fatal(err)
	}
	prior, err := store.CreateRun(context.Background(), session.ID, agent.CreateRunInput{Input: "最近有没有 OOM 现象？"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendRunEvent(context.Background(), prior.ID, agent.AppendEventInput{
		Type: agent.EventFinalAnswer,
		Data: []byte(`{"role":"assistant","content":"发现 vmagent 有 OOMKilled。"}`),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateRunStatus(context.Background(), prior.ID, agent.RunCompleted, ""); err != nil {
		t.Fatal(err)
	}
	run, err := store.CreateRun(context.Background(), session.ID, agent.CreateRunInput{Input: "最近1小时内呢"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendRunEvent(context.Background(), run.ID, agent.AppendEventInput{
		Type: agent.EventFinalAnswer,
		Data: []byte(`{"role":"assistant","content":"最近1小时内发生了大量状态变更。"}`),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if err := Run(context.Background(), []string{"--db", dbPath, "db", "agent-context", run.ID}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	for _, want := range []string{"Warning: no completion.request events recorded", "answer.final", "Legacy visible messages", "Compatibility replay candidate", "最近有没有 OOM 现象？", "发现 vmagent 有 OOMKilled。", "最近1小时内呢", "大量状态变更"} {
		if !strings.Contains(out, want) {
			t.Fatalf("stdout missing %q: %s", want, out)
		}
	}
}
