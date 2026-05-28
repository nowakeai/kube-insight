package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"kube-insight/internal/agent"
)

func TestAgentConversationReplayPreservesToolCallsAndResults(t *testing.T) {
	ctx := context.Background()
	store := agent.NewMemoryStore()
	session, err := store.CreateSession(ctx, agent.CreateSessionInput{})
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.CreateRun(ctx, session.ID, agent.CreateRunInput{Input: "最近有没有 OOM 现象？"})
	if err != nil {
		t.Fatal(err)
	}
	events := []agent.AppendEventInput{
		{Type: agent.EventCompletionMessage, Data: mustJSON(completionMessageEventValue(agent.Message{Role: agent.RoleUser, Content: first.Input, RunID: first.ID}))},
		{Type: agent.EventCompletionMessage, Data: mustJSON(completionMessageEventValue(agent.Message{
			Role: agent.RoleAssistant,
			ToolCalls: []agent.ToolCall{{
				ID:   "call_oom",
				Type: "function",
				Function: agent.FunctionCall{
					Name:      "kube_insight_search",
					Arguments: `{"query":"OOMKilled","kind":"Pod"}`,
				},
			}},
			RunID: first.ID,
		}))},
		{Type: agent.EventCompletionToolResult, Data: mustJSON(map[string]any{
			"format":        "kube-insight.agent.message.v1",
			"role":          agent.RoleTool,
			"toolCallId":    "call_oom",
			"name":          "kube_insight_search",
			"content":       `{"matches":1}`,
			"runId":         first.ID,
			"outputSummary": "matches=1",
		})},
		{Type: agent.EventCompletionMessage, Data: mustJSON(completionMessageEventValue(agent.Message{Role: agent.RoleAssistant, Content: "发现 1 个 OOMKilled Pod。", RunID: first.ID}))},
	}
	for _, event := range events {
		if _, err := store.AppendRunEvent(ctx, first.ID, event); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.UpdateRunStatus(ctx, first.ID, agent.RunCompleted, ""); err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateRun(ctx, session.ID, agent.CreateRunInput{Input: "最近1小时内呢"})
	if err != nil {
		t.Fatal(err)
	}

	messages := agentConversationMessagesForRun(ctx, store, second)
	if len(messages) != 4 {
		t.Fatalf("messages = %#v", messages)
	}
	if messages[1].Role != agent.RoleAssistant || len(messages[1].ToolCalls) != 1 || messages[1].ToolCalls[0].Function.Name != "kube_insight_search" {
		t.Fatalf("assistant tool call was not replayed: %#v", messages[1])
	}
	if messages[2].Role != agent.RoleTool || messages[2].ToolCallID != "call_oom" || messages[2].ToolName != "kube_insight_search" || messages[2].Content != `{"matches":1}` {
		t.Fatalf("tool result was not replayed: %#v", messages[2])
	}
}

func TestAgentConversationReplayBackfillsStreamingToolCallsFromLatestRequest(t *testing.T) {
	ctx := context.Background()
	store := agent.NewMemoryStore()
	session, err := store.CreateSession(ctx, agent.CreateSessionInput{})
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.CreateRun(ctx, session.ID, agent.CreateRunInput{Input: "最近有没有 OOM 现象？"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendRunEvent(ctx, first.ID, agent.AppendEventInput{
		Type: agent.EventCompletionRequest,
		Data: mustJSON(map[string]any{
			"format": "kube-insight.agent.completion.v1",
			"messages": []map[string]any{
				{"role": "system", "content": "Stable instruction."},
				{"role": "user", "content": first.Input},
				{"role": "assistant", "tool_calls": []map[string]any{{
					"id":   "call_oom",
					"type": "function",
					"function": map[string]any{
						"name":      "kube_insight_search",
						"arguments": `{"query":"OOMKilled","kind":"Pod"}`,
					},
				}}},
				{"role": "tool", "tool_call_id": "call_oom", "name": "kube_insight_search", "content": `{"matches":1}`},
			},
		}),
	}); err != nil {
		t.Fatal(err)
	}
	for _, event := range []agent.AppendEventInput{
		{Type: agent.EventCompletionMessage, Data: mustJSON(completionMessageEventValue(agent.Message{Role: agent.RoleUser, Content: first.Input, RunID: first.ID}))},
		{Type: agent.EventCompletionToolResult, Data: mustJSON(map[string]any{
			"format":     "kube-insight.agent.message.v1",
			"role":       agent.RoleTool,
			"toolCallId": "call_oom",
			"name":       "kube_insight_search",
			"content":    `{"matches":1}`,
			"runId":      first.ID,
		})},
		{Type: agent.EventCompletionMessage, Data: mustJSON(completionMessageEventValue(agent.Message{Role: agent.RoleAssistant, Content: "发现 1 个 OOMKilled Pod。", RunID: first.ID}))},
		{Type: agent.EventFinalAnswer, Data: mustJSON(agent.MessageEventData{Role: agent.RoleAssistant, Content: "发现 1 个 OOMKilled Pod。"})},
	} {
		if _, err := store.AppendRunEvent(ctx, first.ID, event); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.UpdateRunStatus(ctx, first.ID, agent.RunCompleted, ""); err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateRun(ctx, session.ID, agent.CreateRunInput{Input: "最近1小时内呢"})
	if err != nil {
		t.Fatal(err)
	}

	messages := agentConversationMessagesForRun(ctx, store, second)
	if len(messages) != 4 {
		t.Fatalf("messages = %#v", messages)
	}
	if messages[0].Role != agent.RoleUser || messages[0].Content != first.Input {
		t.Fatalf("first replay message = %#v", messages[0])
	}
	if messages[1].Role != agent.RoleAssistant || len(messages[1].ToolCalls) != 1 || messages[1].ToolCalls[0].ID != "call_oom" {
		t.Fatalf("assistant tool call was not backfilled from request: %#v", messages[1])
	}
	if messages[2].Role != agent.RoleTool || messages[2].ToolCallID != "call_oom" || messages[2].Content != `{"matches":1}` {
		t.Fatalf("tool result order changed: %#v", messages[2])
	}
	if messages[3].Role != agent.RoleAssistant || messages[3].Content != "发现 1 个 OOMKilled Pod。" {
		t.Fatalf("final answer missing: %#v", messages[3])
	}
}

func TestAgentConversationReplayCompactsLargeHistoricalToolResults(t *testing.T) {
	ctx := context.Background()
	store := agent.NewMemoryStore()
	session, err := store.CreateSession(ctx, agent.CreateSessionInput{})
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.CreateRun(ctx, session.ID, agent.CreateRunInput{Input: "看一下 schema"})
	if err != nil {
		t.Fatal(err)
	}
	largeContent := strings.Repeat("schema row with detailed columns\n", 300)
	if _, err := store.AppendRunEvent(ctx, first.ID, agent.AppendEventInput{
		Type: agent.EventCompletionRequest,
		Data: mustJSON(map[string]any{
			"format": "kube-insight.agent.completion.v1",
			"messages": []map[string]any{
				{"role": "user", "content": first.Input},
				{"role": "assistant", "tool_calls": []map[string]any{{
					"id":   "call_schema",
					"type": "function",
					"function": map[string]any{
						"name":      "kube_insight_schema",
						"arguments": `{}`,
					},
				}}},
				{"role": "tool", "tool_call_id": "call_schema", "name": "kube_insight_schema", "content": largeContent},
			},
		}),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendRunEvent(ctx, first.ID, agent.AppendEventInput{
		Type: agent.EventCompletionToolResult,
		Data: mustJSON(map[string]any{
			"format":           "kube-insight.agent.message.v1",
			"role":             agent.RoleTool,
			"toolCallId":       "call_schema",
			"name":             "kube_insight_schema",
			"content":          largeContent,
			"outputSummary":    "schema tables=8 examples=12",
			"outputArtifactId": "artifact_schema",
			"status":           "completed",
		}),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendRunEvent(ctx, first.ID, agent.AppendEventInput{
		Type: agent.EventFinalAnswer,
		Data: mustJSON(agent.MessageEventData{Role: agent.RoleAssistant, Content: "schema 已检查。"}),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateRunStatus(ctx, first.ID, agent.RunCompleted, ""); err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateRun(ctx, session.ID, agent.CreateRunInput{Input: "继续"})
	if err != nil {
		t.Fatal(err)
	}

	messages := agentConversationMessagesForRun(ctx, store, second)
	if len(messages) != 4 {
		t.Fatalf("messages = %#v", messages)
	}
	if messages[1].Role != agent.RoleAssistant || len(messages[1].ToolCalls) != 1 {
		t.Fatalf("assistant tool call missing: %#v", messages[1])
	}
	toolMessage := messages[2]
	if toolMessage.Role != agent.RoleTool || toolMessage.ToolCallID != "call_schema" || toolMessage.ToolName != "kube_insight_schema" {
		t.Fatalf("tool message identity changed: %#v", toolMessage)
	}
	if len([]rune(toolMessage.Content)) >= maxHistoricalToolReplayContentRunes {
		t.Fatalf("tool message was not compacted: %d chars", len([]rune(toolMessage.Content)))
	}
	for _, want := range []string{"kube-insight.agent.compacted_tool_result.v1", "schema tables=8 examples=12", "artifact_schema", "originalChars"} {
		if !strings.Contains(toolMessage.Content, want) {
			t.Fatalf("compacted tool message missing %q: %s", want, toolMessage.Content)
		}
	}
}

func TestAgentMessagesPlaceVolatileClientContextAfterStableHistory(t *testing.T) {
	ctx := context.Background()
	store := agent.NewMemoryStore()
	session, err := store.CreateSession(ctx, agent.CreateSessionInput{})
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.CreateRun(ctx, session.ID, agent.CreateRunInput{Input: "最近有没有 OOM 现象？"})
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range []agent.AppendEventInput{
		{Type: agent.EventCompletionMessage, Data: mustJSON(completionMessageEventValue(agent.Message{Role: agent.RoleUser, Content: first.Input, RunID: first.ID}))},
		{Type: agent.EventCompletionMessage, Data: mustJSON(completionMessageEventValue(agent.Message{Role: agent.RoleAssistant, Content: "发现 1 个 OOMKilled Pod。", RunID: first.ID}))},
	} {
		if _, err := store.AppendRunEvent(ctx, first.ID, event); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.UpdateRunStatus(ctx, first.ID, agent.RunCompleted, ""); err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateRun(ctx, session.ID, agent.CreateRunInput{
		Input: "最近1小时内呢",
		Metadata: mustJSON(map[string]any{
			"clientContext": map[string]any{
				"sentAt":   "2026-05-26T10:00:00Z",
				"timeZone": "Asia/Shanghai",
			},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}

	server := &Server{agentStore: store}
	messages := server.agentMessagesForRun(ctx, second)
	if len(messages) != 4 {
		t.Fatalf("messages = %#v", messages)
	}
	if messages[0].Content != "最近有没有 OOM 现象？" || messages[1].Content != "发现 1 个 OOMKilled Pod。" {
		t.Fatalf("stable history order changed: %#v", messages)
	}
	if messages[2].Role != agent.RoleSystem || !strings.Contains(messages[2].Content, "Client context for this run") || !strings.Contains(messages[2].Content, "Asia/Shanghai") {
		t.Fatalf("client context should be late system message: %#v", messages[2])
	}
	if messages[3].Role != agent.RoleUser || messages[3].Content != "最近1小时内呢" {
		t.Fatalf("current user should remain final message: %#v", messages[3])
	}
}

func TestAgentConversationReplayKeepsMixedFormatPriorUserTurns(t *testing.T) {
	ctx := context.Background()
	store := agent.NewMemoryStore()
	session, err := store.CreateSession(ctx, agent.CreateSessionInput{})
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.CreateRun(ctx, session.ID, agent.CreateRunInput{Input: "最近有没有 OOM 现象？"})
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range []agent.AppendEventInput{
		{Type: agent.EventCompletionMessage, Data: mustJSON(completionMessageEventValue(agent.Message{Role: agent.RoleUser, Content: first.Input, RunID: first.ID}))},
		{Type: agent.EventCompletionMessage, Data: mustJSON(completionMessageEventValue(agent.Message{Role: agent.RoleAssistant, Content: "发现 vmagent 有 OOMKilled。", RunID: first.ID}))},
	} {
		if _, err := store.AppendRunEvent(ctx, first.ID, event); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.UpdateRunStatus(ctx, first.ID, agent.RunCompleted, ""); err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateRun(ctx, session.ID, agent.CreateRunInput{Input: "最近1小时内呢"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendRunEvent(ctx, second.ID, agent.AppendEventInput{
		Type: agent.EventFinalAnswer,
		Data: mustJSON(agent.MessageEventData{Role: agent.RoleAssistant, Content: "最近1小时内 vmagent 仍有 OOM 重启。"}),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateRunStatus(ctx, second.ID, agent.RunCompleted, ""); err != nil {
		t.Fatal(err)
	}
	third, err := store.CreateRun(ctx, session.ID, agent.CreateRunInput{Input: "那资源限制配置是什么？"})
	if err != nil {
		t.Fatal(err)
	}

	messages := agentConversationMessagesForRun(ctx, store, third)
	joined := completionRequestMessageText(messagesForCompletionRequestTest(messages))
	for _, want := range []string{"最近有没有 OOM 现象？", "发现 vmagent 有 OOMKilled。", "最近1小时内呢", "最近1小时内 vmagent 仍有 OOM 重启。"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("mixed-format replay missing %q: %#v", want, messages)
		}
	}
	if indexOfMessageContaining(messagesForCompletionRequestTest(messages), "最近1小时内呢") > indexOfMessageContaining(messagesForCompletionRequestTest(messages), "最近1小时内 vmagent") {
		t.Fatalf("legacy user turn must precede its answer: %#v", messages)
	}
}

func TestAgentConversationReplayKeepsLegacyTurnsBeforeLatestRequest(t *testing.T) {
	ctx := context.Background()
	store := agent.NewMemoryStore()
	session, err := store.CreateSession(ctx, agent.CreateSessionInput{})
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.CreateRun(ctx, session.ID, agent.CreateRunInput{Input: "最近有没有 OOM 现象？"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendRunEvent(ctx, first.ID, agent.AppendEventInput{
		Type: agent.EventFinalAnswer,
		Data: mustJSON(agent.MessageEventData{Role: agent.RoleAssistant, Content: "发现 vmagent 有 OOMKilled。"}),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateRunStatus(ctx, first.ID, agent.RunCompleted, ""); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Millisecond)
	second, err := store.CreateRun(ctx, session.ID, agent.CreateRunInput{Input: "最近1小时内呢"})
	if err != nil {
		t.Fatal(err)
	}
	appendCompletionRequestAndFinalAnswer(t, ctx, store, second, "最近1小时内 vmagent 仍有 OOM 重启。")
	time.Sleep(time.Millisecond)
	third, err := store.CreateRun(ctx, session.ID, agent.CreateRunInput{Input: "那资源限制配置是什么？"})
	if err != nil {
		t.Fatal(err)
	}

	messages := agentConversationMessagesForRun(ctx, store, third)
	joined := completionRequestMessageText(messagesForCompletionRequestTest(messages))
	for _, want := range []string{"最近有没有 OOM 现象？", "发现 vmagent 有 OOMKilled。", "最近1小时内呢", "最近1小时内 vmagent 仍有 OOM 重启。"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("mixed request replay missing %q: %#v", want, messages)
		}
	}
	if indexOfMessageContaining(messagesForCompletionRequestTest(messages), "最近有没有 OOM") > indexOfMessageContaining(messagesForCompletionRequestTest(messages), "最近1小时内呢") {
		t.Fatalf("legacy turn should remain before later provider request: %#v", messages)
	}
}

func TestAgentConversationReplaySkipsSubagentChildRuns(t *testing.T) {
	ctx := context.Background()
	store := agent.NewMemoryStore()
	session, err := store.CreateSession(ctx, agent.CreateSessionInput{})
	if err != nil {
		t.Fatal(err)
	}
	parent, err := store.CreateRun(ctx, session.ID, agent.CreateRunInput{Input: "最近有没有 OOM 现象？"})
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range []agent.AppendEventInput{
		{Type: agent.EventCompletionMessage, Data: mustJSON(completionMessageEventValue(agent.Message{Role: agent.RoleUser, Content: parent.Input, RunID: parent.ID}))},
		{Type: agent.EventCompletionMessage, Data: mustJSON(completionMessageEventValue(agent.Message{Role: agent.RoleAssistant, Content: "会检查 OOM 分支。", RunID: parent.ID}))},
	} {
		if _, err := store.AppendRunEvent(ctx, parent.ID, event); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.UpdateRunStatus(ctx, parent.ID, agent.RunCompleted, ""); err != nil {
		t.Fatal(err)
	}
	child, err := store.CreateRun(ctx, session.ID, agent.CreateRunInput{
		Input: "child branch prompt",
		Metadata: mustJSON(map[string]any{
			"parentRunId":  parent.ID,
			"subagentName": "parallel_investigation",
			"branchName":   "oom_restarts",
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range []agent.AppendEventInput{
		{Type: agent.EventCompletionMessage, Data: mustJSON(completionMessageEventValue(agent.Message{Role: agent.RoleUser, Content: child.Input, RunID: child.ID}))},
		{Type: agent.EventCompletionMessage, Data: mustJSON(completionMessageEventValue(agent.Message{Role: agent.RoleAssistant, Content: "child branch answer", RunID: child.ID}))},
	} {
		if _, err := store.AppendRunEvent(ctx, child.ID, event); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.UpdateRunStatus(ctx, child.ID, agent.RunCompleted, ""); err != nil {
		t.Fatal(err)
	}
	next, err := store.CreateRun(ctx, session.ID, agent.CreateRunInput{Input: "最近1小时内呢"})
	if err != nil {
		t.Fatal(err)
	}

	messages := agentConversationMessagesForRun(ctx, store, next)
	if len(messages) != 2 {
		t.Fatalf("messages = %#v", messages)
	}
	joined := completionRequestMessageText([]completionRequestMessageForTest{
		{Role: string(messages[0].Role), Content: messages[0].Content},
		{Role: string(messages[1].Role), Content: messages[1].Content},
	})
	if strings.Contains(joined, "child branch prompt") || strings.Contains(joined, "child branch answer") {
		t.Fatalf("child run leaked into main replay: %#v", messages)
	}
}

func TestAgentConversationReplayUsesActiveRetryBranchForLaterFollowUp(t *testing.T) {
	ctx := context.Background()
	store := agent.NewMemoryStore()
	session, err := store.CreateSession(ctx, agent.CreateSessionInput{})
	if err != nil {
		t.Fatal(err)
	}
	original, err := store.CreateRun(ctx, session.ID, agent.CreateRunInput{Input: "最近有没有 OOM 现象？"})
	if err != nil {
		t.Fatal(err)
	}
	appendCompletionRequestAndFinalAnswer(t, ctx, store, original, "旧分支回答：发现 OOM。")
	time.Sleep(time.Millisecond)
	later, err := store.CreateRun(ctx, session.ID, agent.CreateRunInput{Input: "最近1小时内呢"})
	if err != nil {
		t.Fatal(err)
	}
	appendCompletionRequestAndFinalAnswer(t, ctx, store, later, "旧分支后续回答：1 小时内有 OOM。")
	time.Sleep(time.Millisecond)
	retry, err := store.CreateRun(ctx, session.ID, agent.CreateRunInput{
		Input: original.Input,
		Metadata: mustJSON(map[string]any{
			"retryOfRunId":     original.ID,
			"retryRootRunId":   original.ID,
			"retryReplacement": true,
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	appendCompletionRequestAndFinalAnswer(t, ctx, store, retry, "重试分支回答：没有发现 OOM。")
	time.Sleep(time.Millisecond)
	next, err := store.CreateRun(ctx, session.ID, agent.CreateRunInput{Input: "那最近10分钟呢？"})
	if err != nil {
		t.Fatal(err)
	}
	messages := agentConversationMessagesForRun(ctx, store, next)
	joined := completionRequestMessageText(messagesForCompletionRequestTest(messages))
	for _, want := range []string{"最近有没有 OOM 现象？", "重试分支回答：没有发现 OOM。"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("active retry branch replay missing %q: %#v", want, messages)
		}
	}
	for _, unwanted := range []string{"旧分支回答", "最近1小时内呢", "旧分支后续回答"} {
		if strings.Contains(joined, unwanted) {
			t.Fatalf("superseded retry branch leaked %q into replay: %#v", unwanted, messages)
		}
	}
}

func TestCreateAgentRunProviderRequestPreservesPriorIntentForFollowUp(t *testing.T) {
	ctx := context.Background()
	runner, err := agent.NewEinoRunner(ctx, agent.EinoRunnerConfig{
		Instruction: "Stable instruction.",
		Model:       intentFollowupModel{},
	})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewServer(ServerOptions{
		OpenStore: func(context.Context) (ReadStore, error) {
			closed := false
			return fakeReadStore{closed: &closed}, nil
		},
		AgentRunner: runner,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer handler.Close()
	server := httptest.NewServer(handler)
	defer server.Close()

	var session struct {
		ID string `json:"id"`
	}
	postJSON(t, server.URL+"/api/v1/agent/sessions", `{}`, http.StatusCreated, &session)

	var first struct {
		ID string `json:"id"`
	}
	postJSON(t, server.URL+"/api/v1/agent/sessions/"+session.ID+"/runs", `{"input":"最近有没有 OOM 现象？","provider":"openai-compatible","model":"test-model"}`, http.StatusCreated, &first)
	getBody(t, server.URL+"/api/v1/agent/runs/"+first.ID+"/events?follow=true", http.StatusOK)

	var second struct {
		ID string `json:"id"`
	}
	postJSON(t, server.URL+"/api/v1/agent/sessions/"+session.ID+"/runs", `{"input":"最近1小时内呢","provider":"openai-compatible","model":"test-model","metadata":{"clientContext":{"sentAt":"2026-05-26T10:00:00Z","localTime":"2026-05-26T18:00:00+08:00","timeZone":"Asia/Shanghai","timezoneOffsetMinutes":480,"locale":"zh-CN"}}}`, http.StatusCreated, &second)
	getBody(t, server.URL+"/api/v1/agent/runs/"+second.ID+"/events?follow=true", http.StatusOK)

	request := completionRequestForRun(t, handler.agentStore, second.ID)
	if request.Provider != "openai-compatible" || request.Model != "test-model" {
		t.Fatalf("request provider/model = %#v", request)
	}
	joined := completionRequestMessageText(request.Messages)
	for _, want := range []string{"Stable instruction.", "最近有没有 OOM 现象？", "发现 OOMKilled Pod", "Client context for this run", "2026-05-26T10:00:00Z", "2026-05-26T18:00:00+08:00", "Asia/Shanghai", "480", "zh-CN", "current time base", "最近1小时内呢"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("completion request missing %q: %#v", want, request.Messages)
		}
	}
	if indexOfMessageContaining(request.Messages, "最近有没有 OOM 现象？") > indexOfMessageContaining(request.Messages, "最近1小时内呢") {
		t.Fatalf("prior intent must appear before follow-up input: %#v", request.Messages)
	}
	clientContextIndex := indexOfMessageContaining(request.Messages, "Client context for this run")
	if clientContextIndex < indexOfMessageContaining(request.Messages, "发现 OOMKilled Pod") {
		t.Fatalf("volatile client context should stay after stable history: %#v", request.Messages)
	}
	currentUserIndex := indexOfMessageContaining(request.Messages, "最近1小时内呢")
	if clientContextIndex+1 != currentUserIndex {
		t.Fatalf("client context should immediately precede the relative-time follow-up input: %#v", request.Messages)
	}
}

func appendCompletionRequestAndFinalAnswer(t *testing.T, ctx context.Context, store agent.Store, run agent.Run, answer string) {
	t.Helper()
	if _, err := store.AppendRunEvent(ctx, run.ID, agent.AppendEventInput{
		Type: agent.EventCompletionRequest,
		Data: mustJSON(map[string]any{
			"format": "kube-insight.agent.completion.v1",
			"messages": []map[string]any{
				{"role": "user", "content": run.Input},
				{"role": "assistant", "content": answer},
			},
		}),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendRunEvent(ctx, run.ID, agent.AppendEventInput{
		Type: agent.EventFinalAnswer,
		Data: mustJSON(agent.MessageEventData{Role: agent.RoleAssistant, Content: answer}),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateRunStatus(ctx, run.ID, agent.RunCompleted, ""); err != nil {
		t.Fatal(err)
	}
}

type intentFollowupModel struct{}

func (m intentFollowupModel) Generate(_ context.Context, input []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	lastUser := ""
	for _, message := range input {
		if message.Role == schema.User {
			lastUser = message.Content
		}
	}
	if strings.Contains(lastUser, "1小时") {
		return schema.AssistantMessage("最近1小时内仍按 OOM 问题继续检查。", nil), nil
	}
	return schema.AssistantMessage("发现 OOMKilled Pod。", nil), nil
}

func (m intentFollowupModel) Stream(context.Context, []*schema.Message, ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, errors.New("stream is not implemented in intentFollowupModel")
}

type completionRequestMessageForTest struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type completionRequestForTest struct {
	Provider string                            `json:"provider"`
	Model    string                            `json:"model"`
	Messages []completionRequestMessageForTest `json:"messages"`
}

func completionRequestForRun(t *testing.T, store agent.Store, runID string) completionRequestForTest {
	t.Helper()
	events, err := store.ListRunEvents(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Type != agent.EventCompletionRequest {
			continue
		}
		var request completionRequestForTest
		if err := json.Unmarshal(event.Data, &request); err != nil {
			t.Fatal(err)
		}
		return request
	}
	t.Fatalf("run %s has no completion.request event", runID)
	return completionRequestForTest{}
}

func completionRequestMessageText(messages []completionRequestMessageForTest) string {
	var b strings.Builder
	for _, message := range messages {
		b.WriteString(message.Role)
		b.WriteString(": ")
		b.WriteString(message.Content)
		b.WriteByte('\n')
	}
	return b.String()
}

func messagesForCompletionRequestTest(messages []agent.Message) []completionRequestMessageForTest {
	values := make([]completionRequestMessageForTest, 0, len(messages))
	for _, message := range messages {
		values = append(values, completionRequestMessageForTest{Role: string(message.Role), Content: message.Content})
	}
	return values
}

func indexOfMessageContaining(messages []completionRequestMessageForTest, needle string) int {
	for i, message := range messages {
		if strings.Contains(message.Content, needle) {
			return i
		}
	}
	return -1
}
