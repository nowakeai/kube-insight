package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

func TestEinoRunnerRunsChatModelAgent(t *testing.T) {
	ctx := context.Background()
	fake := &fakeEinoModel{answer: "checked cluster health"}
	runner, err := NewEinoRunner(ctx, EinoRunnerConfig{
		Description: "Kubernetes investigation assistant",
		Instruction: "Answer with concise evidence.",
		Model:       fake,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := runner.Run(ctx, EinoRunInput{Messages: []Message{{Role: RoleUser, Content: "is the api healthy?"}}})
	if err != nil {
		t.Fatal(err)
	}
	if result.FinalAnswer != "checked cluster health" || result.Events != 1 {
		t.Fatalf("result = %#v", result)
	}
	if len(fake.inputs) != 2 || fake.inputs[0].Role != schema.System || fake.inputs[1].Role != schema.User || fake.inputs[1].Content != "is the api healthy?" {
		t.Fatalf("model inputs = %#v", fake.inputs)
	}
}

func TestToolBudgetMiddlewareAddsWarningAfterRepeatedTool(t *testing.T) {
	mw := toolBudgetMiddleware{maxIterations: 12}
	state := &adk.ChatModelAgentState{Messages: []adk.Message{
		schema.UserMessage("check recent OOM"),
		schema.ToolMessage(`{"matches":0}`, "tool_1", schema.WithToolName("kube_insight_search")),
		schema.ToolMessage(`{"matches":0}`, "tool_2", schema.WithToolName("kube_insight_search")),
		schema.ToolMessage(`{"matches":0}`, "tool_3", schema.WithToolName("kube_insight_search")),
	}}
	_, next, err := mw.BeforeModelRewriteState(context.Background(), state, nil)
	if err != nil {
		t.Fatal(err)
	}
	if next == state {
		t.Fatal("expected rewritten state")
	}
	if len(next.Messages) != len(state.Messages)+1 {
		t.Fatalf("messages = %d, want %d", len(next.Messages), len(state.Messages)+1)
	}
	warning := next.Messages[len(next.Messages)-1]
	if warning.Role != schema.System || !strings.Contains(warning.Content, toolBudgetWarningMarker) || !strings.Contains(warning.Content, "kube_insight_search called repeatedly") {
		t.Fatalf("warning = %#v", warning)
	}
	if len(state.Messages) != 4 {
		t.Fatalf("original state mutated: %d messages", len(state.Messages))
	}
}

func TestToolBudgetMiddlewareDoesNotDuplicateWarning(t *testing.T) {
	mw := toolBudgetMiddleware{maxIterations: 12}
	state := &adk.ChatModelAgentState{Messages: []adk.Message{
		schema.UserMessage("check allocation"),
		schema.ToolMessage(`{"rows":[]}`, "tool_1", schema.WithToolName("kube_insight_sql")),
		schema.ToolMessage(`{"rows":[]}`, "tool_2", schema.WithToolName("kube_insight_sql")),
		schema.ToolMessage(`{"rows":[]}`, "tool_3", schema.WithToolName("kube_insight_sql")),
		schema.SystemMessage(toolBudgetWarningMarker + ": already warned"),
	}}
	_, next, err := mw.BeforeModelRewriteState(context.Background(), state, nil)
	if err != nil {
		t.Fatal(err)
	}
	if next != state {
		t.Fatal("expected existing warning to be left unchanged")
	}
}

func TestEinoRunnerUsesDefaultInstruction(t *testing.T) {
	ctx := context.Background()
	fake := &fakeEinoModel{answer: "checked cluster health"}
	runner, err := NewEinoRunner(ctx, EinoRunnerConfig{Model: fake})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Run(ctx, EinoRunInput{Messages: []Message{{Role: RoleUser, Content: "what changed?"}}}); err != nil {
		t.Fatal(err)
	}
	if len(fake.inputs) != 2 || fake.inputs[0].Role != schema.System {
		t.Fatalf("model inputs = %#v", fake.inputs)
	}
	if !strings.Contains(fake.inputs[0].Content, "kube-insight Kubernetes investigation agent") || !strings.Contains(fake.inputs[0].Content, "cite the exact proof") {
		t.Fatalf("default system instruction = %q", fake.inputs[0].Content)
	}
}

func TestEinoRunnerRecordsRunEvents(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	session, err := store.CreateSession(ctx, CreateSessionInput{Title: "test"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.CreateRun(ctx, session.ID, CreateRunInput{Input: "is the api healthy?"})
	if err != nil {
		t.Fatal(err)
	}
	runner, err := NewEinoRunner(ctx, EinoRunnerConfig{Model: &fakeEinoModel{answer: "checked cluster health"}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := runner.Run(ctx, EinoRunInput{Messages: []Message{{Role: RoleUser, Content: run.Input}}, Store: store, RunID: run.ID, Provider: "openai-compatible", Model: "test-model"})
	if err != nil {
		t.Fatal(err)
	}
	if result.FinalAnswer != "checked cluster health" {
		t.Fatalf("result = %#v", result)
	}
	events, err := store.ListRunEvents(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	wantTypes := []RunEventType{EventRunStarted, EventCompletionRequest, EventMessageCreated, EventCompletionMessage, EventFinalAnswer, EventRunCompleted}
	if len(events) != len(wantTypes) {
		t.Fatalf("events = %#v", events)
	}
	for i, want := range wantTypes {
		if events[i].Type != want {
			t.Fatalf("event %d type = %s, want %s", i, events[i].Type, want)
		}
	}
	var request map[string]any
	if err := json.Unmarshal(events[1].Data, &request); err != nil {
		t.Fatal(err)
	}
	requestMessages, _ := request["messages"].([]any)
	if request["provider"] != "openai-compatible" || request["model"] != "test-model" || len(requestMessages) != 2 {
		t.Fatalf("completion request = %#v", request)
	}
	completed, err := store.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != RunCompleted || completed.StartedAt == nil || completed.CompletedAt == nil {
		t.Fatalf("completed run = %#v", completed)
	}
}

func TestEinoRunnerCompletionRequestKeepsStablePrefixBeforeVolatileContext(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	session, err := store.CreateSession(ctx, CreateSessionInput{})
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.CreateRun(ctx, session.ID, CreateRunInput{Input: "最近1小时内呢", Provider: "openai-compatible", Model: "test-model"})
	if err != nil {
		t.Fatal(err)
	}
	runner, err := NewEinoRunner(ctx, EinoRunnerConfig{
		Instruction: "Stable instruction.",
		Model:       &fakeEinoModel{answer: "checked recent OOM"},
	})
	if err != nil {
		t.Fatal(err)
	}
	inputMessages := []Message{
		{Role: RoleUser, Content: "最近有没有 OOM 现象？"},
		{Role: RoleAssistant, Content: "发现 1 个 OOMKilled Pod。"},
		{Role: RoleSystem, Content: "Client context for this run:\n- Client local time: 2026-05-26 18:00\n- Client time zone: Asia/Shanghai"},
		{Role: RoleUser, Content: run.Input},
	}
	if _, err := runner.Run(ctx, EinoRunInput{Messages: inputMessages, Store: store, RunID: run.ID, Provider: run.Provider, Model: run.Model}); err != nil {
		t.Fatal(err)
	}
	events, err := store.ListRunEvents(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) < 2 || events[1].Type != EventCompletionRequest {
		t.Fatalf("events = %#v", events)
	}
	var request struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(events[1].Data, &request); err != nil {
		t.Fatal(err)
	}
	if len(request.Messages) != 5 {
		t.Fatalf("request messages = %#v", request.Messages)
	}
	want := []struct {
		role    string
		content string
	}{
		{role: "system", content: "Stable instruction."},
		{role: "user", content: "最近有没有 OOM 现象？"},
		{role: "assistant", content: "发现 1 个 OOMKilled Pod。"},
		{role: "system", content: "Client context for this run:"},
		{role: "user", content: "最近1小时内呢"},
	}
	for i, item := range want {
		if request.Messages[i].Role != item.role || !strings.Contains(request.Messages[i].Content, item.content) {
			t.Fatalf("message %d = %#v, want role=%s content containing %q", i, request.Messages[i], item.role, item.content)
		}
	}
}

func TestEinoRunRecorderMapsToolEvents(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	session, err := store.CreateSession(ctx, CreateSessionInput{})
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.CreateRun(ctx, session.ID, CreateRunInput{Input: "search pods"})
	if err != nil {
		t.Fatal(err)
	}
	recorder := newEinoRunRecorder(store, run.ID)
	if err := recorder.Start(ctx); err != nil {
		t.Fatal(err)
	}
	assistant := schema.AssistantMessage("I will search evidence", []schema.ToolCall{{ID: "tool_1", Function: schema.FunctionCall{Name: "kube_insight_search", Arguments: `{"query":"api"}`}}})
	if _, err := recorder.Record(ctx, adk.EventFromMessage(assistant, nil, schema.Assistant, "")); err != nil {
		t.Fatal(err)
	}
	toolMessage := schema.ToolMessage(`{"content":[{"type":"text","text":"{\"summary\":{\"matches\":1}}"}]}`, "tool_1", schema.WithToolName("kube_insight_search"))
	if _, err := recorder.Record(ctx, adk.EventFromMessage(toolMessage, nil, schema.Tool, "kube_insight_search")); err != nil {
		t.Fatal(err)
	}
	events, err := store.ListRunEvents(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 8 || events[1].Type != EventMessageCreated || events[2].Type != EventCompletionMessage || events[3].Type != EventToolStarted || events[4].Type != EventArtifact || events[5].Type != EventToolCompleted || events[6].Type != EventCompletionToolResult || events[7].Type != EventToolAudit {
		t.Fatalf("events = %#v", events)
	}
	var started ToolCallEventData
	if err := json.Unmarshal(events[3].Data, &started); err != nil {
		t.Fatal(err)
	}
	if started.ToolCallID != "tool_1" || started.Name != "kube_insight_search" || string(started.Input) != `{"query":"api"}` {
		t.Fatalf("started = %#v input=%s", started, string(started.Input))
	}
	var artifact ArtifactEventData
	if err := json.Unmarshal(events[4].Data, &artifact); err != nil {
		t.Fatal(err)
	}
	if artifact.Artifact.Kind != ArtifactKindToolCall || artifact.Artifact.ID == "" || !strings.Contains(string(artifact.Artifact.Data), `"outputSummary":"summary={\"matches\":1}"`) {
		t.Fatalf("artifact = %#v data=%s", artifact, string(artifact.Artifact.Data))
	}
	var completed ToolCallEventData
	if err := json.Unmarshal(events[5].Data, &completed); err != nil {
		t.Fatal(err)
	}
	if completed.ToolCallID != "tool_1" || completed.Name != "kube_insight_search" || completed.OutputArtifactID != artifact.Artifact.ID || completed.OutputSummary != `summary={"matches":1}` || len(completed.Output) != 0 {
		t.Fatalf("completed = %#v output=%s", completed, string(completed.Output))
	}
	var toolResult map[string]any
	if err := json.Unmarshal(events[6].Data, &toolResult); err != nil {
		t.Fatal(err)
	}
	if toolResult["toolCallId"] != "tool_1" || toolResult["outputArtifactId"] != artifact.Artifact.ID || toolResult["role"] != string(RoleTool) {
		t.Fatalf("completion tool result = %#v", toolResult)
	}
	var audit ToolAuditEventData
	if err := json.Unmarshal(events[7].Data, &audit); err != nil {
		t.Fatal(err)
	}
	if audit.RunID != run.ID || audit.ToolCallID != "tool_1" || audit.Name != "kube_insight_search" || audit.Status != "completed" || string(audit.Input) != `{"query":"api"}` || audit.OutputArtifactID != artifact.Artifact.ID || audit.OutputSummary == "" || len(audit.Output) != 0 {
		t.Fatalf("audit = %#v input=%s output=%s", audit, string(audit.Input), string(audit.Output))
	}
}

func TestEinoRunRecorderUsesMiddlewareToolTimingWhenStartedEventIsMissing(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	session, err := store.CreateSession(ctx, CreateSessionInput{})
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.CreateRun(ctx, session.ID, CreateRunInput{Input: "health"})
	if err != nil {
		t.Fatal(err)
	}
	timings := newToolTimingStore()
	timings.put("tool_1", toolTiming{Name: "kube_insight_health", Input: json.RawMessage(`{"limit":5}`), StartedAt: time.Now().Add(-25 * time.Millisecond), DurationMS: 25})
	recorder := newEinoRunRecorder(store, run.ID, timings)
	if err := recorder.Start(ctx); err != nil {
		t.Fatal(err)
	}
	toolMessage := schema.ToolMessage(`{"content":[{"type":"text","text":"kube_insight_health v1"}]}`, "tool_1", schema.WithToolName("kube_insight_health"))
	if _, err := recorder.Record(ctx, adk.EventFromMessage(toolMessage, nil, schema.Tool, "kube_insight_health")); err != nil {
		t.Fatal(err)
	}
	events, err := store.ListRunEvents(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	var completed ToolCallEventData
	found := false
	for _, event := range events {
		if event.Type != EventToolCompleted {
			continue
		}
		found = true
		if err := json.Unmarshal(event.Data, &completed); err != nil {
			t.Fatal(err)
		}
	}
	if !found || completed.DurationMS != 25 {
		t.Fatalf("completed duration = %#v found=%v", completed, found)
	}
	var artifact ArtifactEventData
	var audit ToolAuditEventData
	for _, event := range events {
		switch event.Type {
		case EventArtifact:
			var candidate ArtifactEventData
			if err := json.Unmarshal(event.Data, &candidate); err != nil {
				t.Fatal(err)
			}
			if candidate.Artifact.Kind == ArtifactKindToolCall {
				artifact = candidate
			}
		case EventToolAudit:
			if err := json.Unmarshal(event.Data, &audit); err != nil {
				t.Fatal(err)
			}
		}
	}
	if !strings.Contains(string(artifact.Artifact.Data), `"input":{"limit":5}`) || string(audit.Input) != `{"limit":5}` {
		t.Fatalf("missing recovered tool input artifact=%s audit=%s", string(artifact.Artifact.Data), string(audit.Input))
	}
}

func TestEinoRunRecorderPromotesScratchHandlesToToolArtifact(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	session, err := store.CreateSession(ctx, CreateSessionInput{})
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.CreateRun(ctx, session.ID, CreateRunInput{Input: "scratch"})
	if err != nil {
		t.Fatal(err)
	}
	recorder := newEinoRunRecorder(store, run.ID)
	if err := recorder.Start(ctx); err != nil {
		t.Fatal(err)
	}
	toolMessage := schema.ToolMessage(`{"result":{"handle":{"path":"/rows/top.json","mime":"application/json","bytes":123,"sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","sessionId":"sess_1","runId":"run_1"}}}`, "tool_1", schema.WithToolName(scriptedQueryToolName))
	if _, err := recorder.Record(ctx, adk.EventFromMessage(toolMessage, nil, schema.Tool, scriptedQueryToolName)); err != nil {
		t.Fatal(err)
	}
	events, err := store.ListRunEvents(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	var artifact ArtifactEventData
	for _, event := range events {
		if event.Type != EventArtifact {
			continue
		}
		if err := json.Unmarshal(event.Data, &artifact); err != nil {
			t.Fatal(err)
		}
		break
	}
	if artifact.Artifact.ID == "" {
		t.Fatalf("missing artifact: %#v", events)
	}
	var record map[string]any
	if err := json.Unmarshal(artifact.Artifact.Data, &record); err != nil {
		t.Fatal(err)
	}
	handles, _ := record["scratchHandles"].([]any)
	if len(handles) != 1 {
		t.Fatalf("artifact data missing scratch handles: %s", string(artifact.Artifact.Data))
	}
	handle, _ := handles[0].(map[string]any)
	if handle["path"] != "/rows/top.json" || handle["sha256"] == "" {
		t.Fatalf("handle = %#v", handle)
	}
}

func TestEinoRunRecorderCreatesEvidenceArtifactsFromSearchOutput(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	session, err := store.CreateSession(ctx, CreateSessionInput{})
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.CreateRun(ctx, session.ID, CreateRunInput{Input: "find api pods"})
	if err != nil {
		t.Fatal(err)
	}
	recorder := newEinoRunRecorder(store, run.ID)
	if err := recorder.Start(ctx); err != nil {
		t.Fatal(err)
	}
	assistant := schema.AssistantMessage("searching", []schema.ToolCall{{ID: "tool_1", Function: schema.FunctionCall{Name: "kube_insight_search", Arguments: `{"query":"api"}`}}})
	if _, err := recorder.Record(ctx, adk.EventFromMessage(assistant, nil, schema.Assistant, "")); err != nil {
		t.Fatal(err)
	}
	toolOutput := `{"content":[{"type":"text","text":"{\"input\":{\"query\":\"api\"},\"summary\":{\"matches\":1},\"bundles\":[{\"object\":{\"clusterId\":\"c1\",\"kind\":\"Pod\",\"namespace\":\"default\",\"name\":\"api-0\"},\"summary\":{\"facts\":1,\"versions\":2,\"evidenceScore\":7}}]}"}]}`
	toolMessage := schema.ToolMessage(toolOutput, "tool_1", schema.WithToolName("kube_insight_search"))
	if _, err := recorder.Record(ctx, adk.EventFromMessage(toolMessage, nil, schema.Tool, "kube_insight_search")); err != nil {
		t.Fatal(err)
	}
	events, err := store.ListRunEvents(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 9 || events[8].Type != EventArtifact {
		t.Fatalf("events = %#v", events)
	}
	var artifact ArtifactEventData
	if err := json.Unmarshal(events[8].Data, &artifact); err != nil {
		t.Fatal(err)
	}
	if artifact.Artifact.Kind != ArtifactKindK8sResourceList || !strings.Contains(artifact.Artifact.Title, "Search evidence") || !strings.Contains(string(artifact.Artifact.Data), `"name":"api-0"`) {
		t.Fatalf("artifact = %#v data=%s", artifact, string(artifact.Artifact.Data))
	}
	if strings.Contains(eventsOfType(events, EventCitation), "citation") {
		t.Fatalf("unexpected tool-time citation events = %#v", events)
	}
	if err := recorder.Complete(ctx, "Pod default/api-0 is relevant evidence. {{evidence: API pod facts}}"); err != nil {
		t.Fatal(err)
	}
	events, err = store.ListRunEvents(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	var citation CitationEventData
	var final MessageEventData
	foundCitation := false
	foundFinal := false
	for _, event := range events {
		switch event.Type {
		case EventCitation:
			foundCitation = true
			if err := json.Unmarshal(event.Data, &citation); err != nil {
				t.Fatal(err)
			}
		case EventFinalAnswer:
			foundFinal = true
			if err := json.Unmarshal(event.Data, &final); err != nil {
				t.Fatal(err)
			}
		}
	}
	if !foundCitation || citation.Citation.ArtifactID != artifact.Artifact.ID || citation.Citation.Text != "API pod facts" {
		t.Fatalf("citation = %#v found=%v events=%#v", citation, foundCitation, events)
	}
	if !foundFinal || strings.Contains(final.Content, "{{evidence:") || !strings.Contains(final.Content, "[API pod facts](#citation:"+citation.Citation.ID+")") {
		t.Fatalf("final = %#v found=%v citation=%#v", final, foundFinal, citation)
	}
}

func eventsOfType(events []RunEvent, eventType RunEventType) string {
	var builder strings.Builder
	for _, event := range events {
		if event.Type == eventType {
			builder.WriteString(string(event.Type))
			builder.WriteByte(' ')
		}
	}
	return builder.String()
}

func TestEinoRunnerTreatsToolErrorsAsRecoverableToolMessages(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	session, err := store.CreateSession(ctx, CreateSessionInput{})
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.CreateRun(ctx, session.ID, CreateRunInput{Input: "query missing table"})
	if err != nil {
		t.Fatal(err)
	}
	model := &fakeToolRetryModel{}
	runner, err := NewEinoRunner(ctx, EinoRunnerConfig{
		Model: model,
		Tools: WrapRecoverableToolErrors([]tool.BaseTool{fakeFailingTool{name: "kube_insight_sql", err: errors.New("Unknown table expression identifier 'objects'")}}),
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := runner.Run(ctx, EinoRunInput{Messages: []Message{{Role: RoleUser, Content: run.Input}}, Store: store, RunID: run.ID})
	if err != nil {
		t.Fatal(err)
	}
	if result.FinalAnswer != "I saw the tool error and retried with schema context." {
		t.Fatalf("result = %#v", result)
	}
	if model.calls < 2 || !model.sawToolError {
		t.Fatalf("model did not receive recoverable tool error: calls=%d saw=%v", model.calls, model.sawToolError)
	}
	completed, err := store.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != RunCompleted || completed.Error != "" {
		t.Fatalf("run should complete despite tool error: %#v", completed)
	}
	events, err := store.ListRunEvents(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	var failed ToolCallEventData
	foundFailed := false
	for _, event := range events {
		if event.Type != EventToolFailed {
			continue
		}
		foundFailed = true
		if err := json.Unmarshal(event.Data, &failed); err != nil {
			t.Fatal(err)
		}
	}
	if !foundFailed || failed.Name != "kube_insight_sql" || failed.Status != "failed" || !strings.Contains(failed.Error, "Unknown table") || failed.DurationMS < 0 {
		t.Fatalf("failed tool event = %#v found=%v", failed, foundFailed)
	}
}

func TestEinoRunnerConfiguresHandlersCheckpointAndStreaming(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	session, err := store.CreateSession(ctx, CreateSessionInput{})
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.CreateRun(ctx, session.ID, CreateRunInput{Input: "stream answer"})
	if err != nil {
		t.Fatal(err)
	}
	handler := &runnerConfigHandler{}
	checkpointStore := newCapabilityCheckpointStore()
	runner, err := NewEinoRunner(ctx, EinoRunnerConfig{
		Model:           &fakeStreamingEinoModel{parts: []string{"hello ", "world"}},
		EnableStreaming: true,
		CheckPointStore: checkpointStore,
		Handlers:        []adk.ChatModelAgentMiddleware{handler},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := runner.Run(ctx, EinoRunInput{
		Messages:     []Message{{Role: RoleUser, Content: run.Input}},
		Store:        store,
		RunID:        run.ID,
		CheckPointID: "runner-stream-checkpoint",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.FinalAnswer != "hello world" {
		t.Fatalf("result = %#v", result)
	}
	if handler.beforeModel == 0 || handler.afterModel == 0 {
		t.Fatalf("handler was not invoked: %#v", handler)
	}
	events, err := store.ListRunEvents(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	wantTypes := []RunEventType{EventRunStarted, EventCompletionRequest, EventMessageDelta, EventMessageDelta, EventMessageDone, EventCompletionMessage, EventFinalAnswer, EventRunCompleted}
	if len(events) != len(wantTypes) {
		t.Fatalf("events = %#v", events)
	}
	for i, want := range wantTypes {
		if events[i].Type != want {
			t.Fatalf("event %d type = %s, want %s", i, events[i].Type, want)
		}
	}
	var completed MessageEventData
	if err := json.Unmarshal(events[4].Data, &completed); err != nil {
		t.Fatal(err)
	}
	if completed.Content != "hello world" || completed.Role != RoleAssistant {
		t.Fatalf("completed message = %#v", completed)
	}
}

func TestEinoRunnerRecordsFailureEvents(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	session, err := store.CreateSession(ctx, CreateSessionInput{})
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.CreateRun(ctx, session.ID, CreateRunInput{Input: "test"})
	if err != nil {
		t.Fatal(err)
	}
	runner, err := NewEinoRunner(ctx, EinoRunnerConfig{Model: &fakeEinoModel{err: errors.New("provider failed")}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Run(ctx, EinoRunInput{Messages: []Message{{Role: RoleUser, Content: run.Input}}, Store: store, RunID: run.ID}); err == nil {
		t.Fatal("expected run error")
	}
	events, err := store.ListRunEvents(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	wantTypes := []RunEventType{EventRunStarted, EventCompletionRequest, EventError, EventRunFailed}
	if len(events) != len(wantTypes) {
		t.Fatalf("events = %#v", events)
	}
	for i, want := range wantTypes {
		if events[i].Type != want {
			t.Fatalf("event %d type = %s, want %s", i, events[i].Type, want)
		}
	}
	failed, err := store.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if failed.Status != RunFailed || !strings.Contains(failed.Error, "provider failed") {
		t.Fatalf("failed run = %#v", failed)
	}
}

func TestNewEinoRunnerRequiresModel(t *testing.T) {
	_, err := NewEinoRunner(context.Background(), EinoRunnerConfig{})
	if !errors.Is(err, ErrEinoModelRequired) {
		t.Fatalf("err = %v", err)
	}
}

type fakeFailingTool struct {
	name string
	err  error
}

func (t fakeFailingTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: t.name, Desc: "failing test tool"}, nil
}

func (t fakeFailingTool) InvokableRun(context.Context, string, ...tool.Option) (string, error) {
	return "", t.err
}

type fakeToolRetryModel struct {
	calls        int
	boundTools   []*schema.ToolInfo
	sawToolError bool
}

func (m *fakeToolRetryModel) Generate(_ context.Context, input []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	m.calls++
	for _, msg := range input {
		if msg.Role == schema.Tool && strings.Contains(msg.Content, `"isError":true`) && strings.Contains(msg.Content, "Unknown table") {
			m.sawToolError = true
		}
	}
	if !m.sawToolError {
		return schema.AssistantMessage("querying", []schema.ToolCall{{ID: "tool_error_1", Function: schema.FunctionCall{Name: "kube_insight_sql", Arguments: `{"sql":"SELECT * FROM objects"}`}}}), nil
	}
	return schema.AssistantMessage("I saw the tool error and retried with schema context.", nil), nil
}

func (m *fakeToolRetryModel) Stream(context.Context, []*schema.Message, ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, errors.New("stream is not implemented in fakeToolRetryModel")
}

func (m *fakeToolRetryModel) WithTools(infos []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	m.boundTools = append([]*schema.ToolInfo(nil), infos...)
	return m, nil
}

type fakeEinoModel struct {
	answer string
	err    error
	inputs []*schema.Message
}

func (m *fakeEinoModel) Generate(_ context.Context, input []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	m.inputs = append([]*schema.Message(nil), input...)
	if m.err != nil {
		return nil, m.err
	}
	return schema.AssistantMessage(m.answer, nil), nil
}

func (m *fakeEinoModel) Stream(context.Context, []*schema.Message, ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, errors.New("stream is not implemented in fakeEinoModel")
}

type runnerConfigHandler struct {
	adk.BaseChatModelAgentMiddleware
	beforeModel int
	afterModel  int
}

func (h *runnerConfigHandler) BeforeModelRewriteState(ctx context.Context, state *adk.ChatModelAgentState, mc *adk.ModelContext) (context.Context, *adk.ChatModelAgentState, error) {
	h.beforeModel++
	return ctx, state, nil
}

func (h *runnerConfigHandler) AfterModelRewriteState(ctx context.Context, state *adk.ChatModelAgentState, mc *adk.ModelContext) (context.Context, *adk.ChatModelAgentState, error) {
	h.afterModel++
	return ctx, state, nil
}

type fakeStreamingEinoModel struct {
	parts []string
}

func (m *fakeStreamingEinoModel) Generate(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
	return schema.AssistantMessage(strings.Join(m.parts, ""), nil), nil
}

func (m *fakeStreamingEinoModel) Stream(context.Context, []*schema.Message, ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	messages := make([]*schema.Message, 0, len(m.parts))
	for _, part := range m.parts {
		messages = append(messages, schema.AssistantMessage(part, nil))
	}
	return schema.StreamReaderFromArray(messages), nil
}
