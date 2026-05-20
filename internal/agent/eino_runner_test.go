package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
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
	result, err := runner.Run(ctx, EinoRunInput{Messages: []Message{{Role: RoleUser, Content: run.Input}}, Store: store, RunID: run.ID})
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
	wantTypes := []RunEventType{EventRunStarted, EventMessageCreated, EventFinalAnswer, EventRunCompleted}
	if len(events) != len(wantTypes) {
		t.Fatalf("events = %#v", events)
	}
	for i, want := range wantTypes {
		if events[i].Type != want {
			t.Fatalf("event %d type = %s, want %s", i, events[i].Type, want)
		}
	}
	completed, err := store.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != RunCompleted || completed.StartedAt == nil || completed.CompletedAt == nil {
		t.Fatalf("completed run = %#v", completed)
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
	assistant := schema.AssistantMessage("I will search evidence", []schema.ToolCall{{ID: "tool_1", Function: schema.FunctionCall{Name: SearchToolName, Arguments: `{"query":"api"}`}}})
	if err := recorder.Record(ctx, adk.EventFromMessage(assistant, nil, schema.Assistant, "")); err != nil {
		t.Fatal(err)
	}
	toolMessage := schema.ToolMessage(`{"summary":{"matches":1}}`, "tool_1", schema.WithToolName(SearchToolName))
	if err := recorder.Record(ctx, adk.EventFromMessage(toolMessage, nil, schema.Tool, SearchToolName)); err != nil {
		t.Fatal(err)
	}
	events, err := store.ListRunEvents(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 5 || events[1].Type != EventMessageCreated || events[2].Type != EventToolStarted || events[3].Type != EventMessageCreated || events[4].Type != EventToolCompleted {
		t.Fatalf("events = %#v", events)
	}
	var started ToolCallEventData
	if err := json.Unmarshal(events[2].Data, &started); err != nil {
		t.Fatal(err)
	}
	if started.ToolCallID != "tool_1" || started.Name != SearchToolName || string(started.Input) != `{"query":"api"}` {
		t.Fatalf("started = %#v input=%s", started, string(started.Input))
	}
	var completed ToolCallEventData
	if err := json.Unmarshal(events[4].Data, &completed); err != nil {
		t.Fatal(err)
	}
	if completed.ToolCallID != "tool_1" || completed.Name != SearchToolName || string(completed.Output) != `{"summary":{"matches":1}}` {
		t.Fatalf("completed = %#v output=%s", completed, string(completed.Output))
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
	wantTypes := []RunEventType{EventRunStarted, EventError, EventRunFailed}
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
