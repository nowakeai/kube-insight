package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestRunDBAgentContextCompatibilityReplayUsesActiveRetryBranch(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "kubeinsight.db")
	store, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	session, err := store.CreateSession(context.Background(), agent.CreateSessionInput{})
	if err != nil {
		t.Fatal(err)
	}
	original, err := store.CreateRun(context.Background(), session.ID, agent.CreateRunInput{Input: "最近有没有 OOM 现象？"})
	if err != nil {
		t.Fatal(err)
	}
	appendLegacyFinalAnswerForAgentContextTest(t, store, original.ID, "旧分支回答：发现 OOM。")
	time.Sleep(time.Millisecond)
	later, err := store.CreateRun(context.Background(), session.ID, agent.CreateRunInput{Input: "最近1小时内呢"})
	if err != nil {
		t.Fatal(err)
	}
	appendLegacyFinalAnswerForAgentContextTest(t, store, later.ID, "旧分支后续回答：1 小时内有 OOM。")
	time.Sleep(time.Millisecond)
	retry, err := store.CreateRun(context.Background(), session.ID, agent.CreateRunInput{
		Input:    original.Input,
		Metadata: []byte(`{"retryOfRunId":"` + original.ID + `","retryRootRunId":"` + original.ID + `"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	appendLegacyFinalAnswerForAgentContextTest(t, store, retry.ID, "重试分支回答：没有发现 OOM。")
	time.Sleep(time.Millisecond)
	run, err := store.CreateRun(context.Background(), session.ID, agent.CreateRunInput{Input: "那最近10分钟呢？"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if err := Run(context.Background(), []string{"--db", dbPath, "db", "agent-context", run.ID, "--output", "json"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	out := stdout.String()
	for _, want := range []string{`"compatibilityReplay"`, "最近有没有 OOM 现象？", "重试分支回答：没有发现 OOM。", "那最近10分钟呢？"} {
		if !strings.Contains(out, want) {
			t.Fatalf("stdout missing %q: %s", want, out)
		}
	}
	for _, unwanted := range []string{"旧分支回答", "最近1小时内呢", "旧分支后续回答"} {
		if strings.Contains(out, unwanted) {
			t.Fatalf("stdout contains superseded branch %q: %s", unwanted, out)
		}
	}
}

func TestRunDBAgentContextJSONShowsCompactedToolReplayAndClientContextOrder(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "kubeinsight.db")
	store, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	session, err := store.CreateSession(context.Background(), agent.CreateSessionInput{})
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.CreateRun(context.Background(), session.ID, agent.CreateRunInput{
		Input:    "最近1小时内呢",
		Provider: "openai-compatible",
		Model:    "test-model",
		Metadata: []byte(`{"clientContext":{"sentAt":"2026-05-26T10:00:00Z","timeZone":"Asia/Shanghai"}}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	compactedToolResult := `{"format":"kube-insight.agent.compacted_tool_result.v1","toolCallId":"call_schema","name":"kube_insight_schema","status":"ok","originalChars":9000,"summary":"schema tables=8 examples=12","preview":"large schema result omitted","outputArtifactId":"artifact_schema"}`
	requestData, err := json.Marshal(map[string]any{
		"mode":     "generate",
		"provider": "openai-compatible",
		"model":    "test-model",
		"messages": []map[string]any{
			{"role": "system", "content": "Stable instruction."},
			{"role": "user", "content": "看一下 schema"},
			{"role": "assistant", "tool_calls": []map[string]any{
				{
					"id":   "call_schema",
					"type": "function",
					"function": map[string]any{
						"name":      "kube_insight_schema",
						"arguments": "{}",
					},
				},
			}},
			{"role": "tool", "tool_call_id": "call_schema", "name": "kube_insight_schema", "content": compactedToolResult},
			{"role": "assistant", "content": "schema 已检查。"},
			{"role": "system", "content": "Client context for this run:\n- Client sent at: 2026-05-26T10:00:00Z.\n- Client time zone: Asia/Shanghai."},
			{"role": "user", "content": "最近1小时内呢"},
		},
		"tools": []map[string]any{
			{"type": "function", "function": map[string]any{"name": "kube_insight_schema"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendRunEvent(context.Background(), run.ID, agent.AppendEventInput{
		Type: agent.EventCompletionRequest,
		Data: requestData,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if err := Run(context.Background(), []string{"--db", dbPath, "db", "agent-context", run.ID, "--all", "--output", "json"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	var report agentContextAuditOutput
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("unmarshal stdout: %v\n%s", err, stdout.String())
	}
	if len(report.Requests) != 1 {
		t.Fatalf("expected 1 request, got %d: %s", len(report.Requests), stdout.String())
	}
	request := report.Requests[0]
	if request.MessageCount != 7 || len(request.Messages) != 7 {
		t.Fatalf("expected 7 messages, got count=%d len=%d", request.MessageCount, len(request.Messages))
	}
	if request.ToolCount != 1 {
		t.Fatalf("expected 1 tool, got %d", request.ToolCount)
	}
	wantRoles := []string{"system", "user", "assistant", "tool", "assistant", "system", "user"}
	for i, want := range wantRoles {
		if got := request.Messages[i].Role; got != want {
			t.Fatalf("message %d role = %q, want %q", i, got, want)
		}
	}
	assistantToolCall := request.Messages[2]
	if len(assistantToolCall.ToolCalls) != 1 || assistantToolCall.ToolCalls[0] != "kube_insight_schema" {
		t.Fatalf("unexpected assistant tool calls: %#v", assistantToolCall.ToolCalls)
	}
	toolMessage := request.Messages[3]
	for _, want := range []string{"kube-insight.agent.compacted_tool_result.v1", `"summary":"schema tables=8 examples=12"`, `"outputArtifactId":"artifact_schema"`, `"originalChars":9000`} {
		if !strings.Contains(toolMessage.Content, want) {
			t.Fatalf("tool message missing %q: %s", want, toolMessage.Content)
		}
	}
	if toolMessage.ToolCallID != "call_schema" || toolMessage.ToolName != "kube_insight_schema" {
		t.Fatalf("unexpected tool binding: callID=%q name=%q", toolMessage.ToolCallID, toolMessage.ToolName)
	}
	if !strings.Contains(toolMessage.ContentPreview, "kube-insight.agent.compacted_tool_result.v1") {
		t.Fatalf("tool preview should expose compact format: %q", toolMessage.ContentPreview)
	}
	if !strings.Contains(request.Messages[5].Content, "Client context for this run") || !strings.Contains(request.Messages[5].Content, "Asia/Shanghai") {
		t.Fatalf("client context message not preserved at index 5: %q", request.Messages[5].Content)
	}
	if request.Messages[6].Content != "最近1小时内呢" {
		t.Fatalf("current user message should be last, got %q", request.Messages[6].Content)
	}
}

func appendLegacyFinalAnswerForAgentContextTest(t *testing.T, store agent.Store, runID string, answer string) {
	t.Helper()
	if _, err := store.AppendRunEvent(context.Background(), runID, agent.AppendEventInput{
		Type: agent.EventFinalAnswer,
		Data: []byte(`{"role":"assistant","content":"` + answer + `"}`),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateRunStatus(context.Background(), runID, agent.RunCompleted, ""); err != nil {
		t.Fatal(err)
	}
}
