package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/tool"
)

func TestNewEvidenceCondenserTool(t *testing.T) {
	ctx := context.Background()
	condenser, err := NewEvidenceCondenserTool(ctx, &capabilityStaticModel{answer: "- artifact_sql: vmagent has OOMKilled facts"})
	if err != nil {
		t.Fatal(err)
	}
	info, err := condenser.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != evidenceCondenserToolName || !strings.Contains(strings.ToLower(info.Desc), "condense") {
		t.Fatalf("tool info = %#v", info)
	}
	invokable, ok := condenser.(tool.InvokableTool)
	if !ok {
		t.Fatalf("condenser is not invokable: %T", condenser)
	}
	out, err := invokable.InvokableRun(ctx, `{"request":"summarize artifact_sql"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "artifact_sql") || !strings.Contains(out, "OOMKilled") {
		t.Fatalf("condenser output = %q", out)
	}
}

func TestEvidenceCondenserToolCreatesChildRunWhenParentContextExists(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	session, err := store.CreateSession(ctx, CreateSessionInput{})
	if err != nil {
		t.Fatal(err)
	}
	parent, err := store.CreateRun(ctx, session.ID, CreateRunInput{Input: "triage evidence", Provider: "openai-compatible", Model: "test-model"})
	if err != nil {
		t.Fatal(err)
	}
	ctx = withRunExecutionContext(ctx, RunExecutionContext{Store: store, RunID: parent.ID, Provider: parent.Provider, Model: parent.Model})
	ctx = context.WithValue(ctx, toolCallExecutionContextKey{}, ToolCallExecutionContext{Name: evidenceCondenserToolName, CallID: "call_condenser"})
	condenser, err := NewEvidenceCondenserTool(ctx, &capabilityStaticModel{answer: "- artifact_sql: vmagent has OOMKilled facts"})
	if err != nil {
		t.Fatal(err)
	}
	out, err := condenser.(tool.InvokableTool).InvokableRun(ctx, `{"request":"summarize artifact_sql"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "artifact_sql") || !strings.Contains(out, "OOMKilled") {
		t.Fatalf("condenser output = %q", out)
	}
	sessionAfter, err := store.GetSession(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	var child Run
	for _, run := range sessionAfter.Runs {
		if run.ID != parent.ID {
			child = run
			break
		}
	}
	if child.ID == "" {
		t.Fatalf("child run was not created: %#v", sessionAfter.Runs)
	}
	var metadata map[string]any
	if err := json.Unmarshal(child.Metadata, &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata["parentRunId"] != parent.ID || metadata["parentToolCallId"] != "call_condenser" || metadata["subagentName"] != evidenceCondenserToolName {
		t.Fatalf("child metadata = %#v", metadata)
	}
	events, err := store.ListRunEvents(ctx, child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !runEventsContainTypes(events, EventRunCreated, EventRunStarted, EventCompletionRequest, EventCompletionMessage, EventFinalAnswer, EventRunCompleted) {
		t.Fatalf("child events = %#v", events)
	}
}

func TestEvidenceCondenserInstructionIsEvidenceBounded(t *testing.T) {
	instruction := EvidenceCondenserInstruction()
	for _, want := range []string{"Do not fetch new Kubernetes data", "artifact IDs", "do not invent facts", "Sources line", "source artifact"} {
		if !strings.Contains(instruction, want) {
			t.Fatalf("instruction missing %q: %s", want, instruction)
		}
	}
}
