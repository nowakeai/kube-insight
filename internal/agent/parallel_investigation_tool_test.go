package agent

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

func TestParallelInvestigationToolRunsBranchesConcurrently(t *testing.T) {
	mdl := &parallelTestModel{delay: 80 * time.Millisecond}
	tool := NewParallelInvestigationTool(mdl, nil)
	out, err := tool.(ParallelInvestigationTool).InvokableRun(context.Background(), `{
		"question":"最近有没有 OOM，同时最近一小时有什么变化？",
		"branches":[
			{"name":"oom_restarts","objective":"Check OOMKilled and restart evidence."},
			{"name":"recent_changes","objective":"Check recent changes in the last hour."}
		]
	}`)
	if err != nil {
		t.Fatal(err)
	}
	var result parallelInvestigationResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("decode output: %v: %s", err, out)
	}
	if result.Tool != parallelInvestigationToolName || len(result.Branches) != 2 {
		t.Fatalf("unexpected result = %#v", result)
	}
	for _, branch := range result.Branches {
		if branch.Status != "completed" || !strings.Contains(branch.Answer, branch.Name) {
			t.Fatalf("branch result = %#v", branch)
		}
	}
	if got := mdl.maxActive.Load(); got < 2 {
		t.Fatalf("branches did not overlap, max active = %d", got)
	}
}

func TestParallelInvestigationToolInfoEncouragesBroadTriage(t *testing.T) {
	info, err := NewParallelInvestigationTool(&parallelTestModel{}, nil).Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{parallelInvestigationToolName, "Run 2-4 independent", "Use proactively", "broad incidents", "Do not use for exact Service health"} {
		if !strings.Contains(info.Name+" "+info.Desc, want) {
			t.Fatalf("tool info missing %q: %#v", want, info)
		}
	}
}

func TestParallelInvestigationToolAcceptsFourBranches(t *testing.T) {
	tool := NewParallelInvestigationTool(&parallelTestModel{}, nil)
	out, err := tool.(ParallelInvestigationTool).InvokableRun(context.Background(), `{
		"question":"broad namespace triage",
		"branches":[
			{"name":"oom_restarts","objective":"Check OOMKilled and restart evidence."},
			{"name":"recent_changes","objective":"Check recent changes."},
			{"name":"topology_impact","objective":"Check topology impact."},
			{"name":"collector_coverage","objective":"Check collector coverage."}
		]
	}`)
	if err != nil {
		t.Fatal(err)
	}
	var result parallelInvestigationResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("decode output: %v: %s", err, out)
	}
	if len(result.Branches) != 4 {
		t.Fatalf("branches = %#v", result.Branches)
	}
}

func TestParallelInvestigationToolCreatesChildRunsWhenParentContextExists(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	session, err := store.CreateSession(ctx, CreateSessionInput{})
	if err != nil {
		t.Fatal(err)
	}
	parent, err := store.CreateRun(ctx, session.ID, CreateRunInput{Input: "triage broad issue", Provider: "openai-compatible", Model: "test-model"})
	if err != nil {
		t.Fatal(err)
	}
	ctx = withRunExecutionContext(ctx, RunExecutionContext{Store: store, RunID: parent.ID, Provider: parent.Provider, Model: parent.Model})
	ctx = context.WithValue(ctx, toolCallExecutionContextKey{}, ToolCallExecutionContext{Name: parallelInvestigationToolName, CallID: "call_parallel"})
	tool := NewParallelInvestigationTool(&parallelTestModel{}, nil).(ParallelInvestigationTool)

	out, err := tool.InvokableRun(ctx, `{
		"question":"最近有没有 OOM，同时最近一小时有什么变化？",
		"branches":[
			{"name":"oom_restarts","objective":"Check OOMKilled and restart evidence."},
			{"name":"recent_changes","objective":"Check recent changes in the last hour."}
		]
	}`)
	if err != nil {
		t.Fatal(err)
	}
	var result parallelInvestigationResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("decode output: %v: %s", err, out)
	}
	if len(result.Branches) != 2 {
		t.Fatalf("result = %#v", result)
	}
	for _, branch := range result.Branches {
		if branch.ChildRunID == "" {
			t.Fatalf("branch missing child run id: %#v", branch)
		}
		child, err := store.GetRun(ctx, branch.ChildRunID)
		if err != nil {
			t.Fatal(err)
		}
		var metadata map[string]any
		if err := json.Unmarshal(child.Metadata, &metadata); err != nil {
			t.Fatal(err)
		}
		if metadata["parentRunId"] != parent.ID || metadata["parentToolCallId"] != "call_parallel" || metadata["subagentName"] != parallelInvestigationToolName || metadata["branchName"] != branch.Name {
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
}

func TestParallelInvestigationToolMarksTimedOutChildRunFailed(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	session, err := store.CreateSession(ctx, CreateSessionInput{})
	if err != nil {
		t.Fatal(err)
	}
	parent, err := store.CreateRun(ctx, session.ID, CreateRunInput{Input: "triage slow issue", Provider: "openai-compatible", Model: "test-model"})
	if err != nil {
		t.Fatal(err)
	}
	ctx = withRunExecutionContext(ctx, RunExecutionContext{Store: store, RunID: parent.ID, Provider: parent.Provider, Model: parent.Model})
	ctx = context.WithValue(ctx, toolCallExecutionContextKey{}, ToolCallExecutionContext{Name: parallelInvestigationToolName, CallID: "call_parallel"})
	tool := NewParallelInvestigationTool(&parallelTestModel{delay: 50 * time.Millisecond}, nil).(ParallelInvestigationTool)

	out, err := tool.InvokableRun(ctx, `{
		"question":"slow branch",
		"timeoutMillis":1,
		"branches":[
			{"name":"topology_impact","objective":"Check topology impact."}
		]
	}`)
	if err != nil {
		t.Fatal(err)
	}
	var result parallelInvestigationResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("decode output: %v: %s", err, out)
	}
	if len(result.Branches) != 1 || result.Branches[0].Status != "failed" || result.Branches[0].ChildRunID == "" {
		t.Fatalf("result = %#v", result)
	}
	child, err := store.GetRun(ctx, result.Branches[0].ChildRunID)
	if err != nil {
		t.Fatal(err)
	}
	if child.Status != RunFailed || !strings.Contains(child.Error, "deadline") {
		t.Fatalf("child = %#v", child)
	}
	events, err := store.ListRunEvents(ctx, child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !runEventsContainTypes(events, EventError, EventRunFailed) {
		t.Fatalf("child events = %#v", events)
	}
}

func runEventsContainTypes(events []RunEvent, wants ...RunEventType) bool {
	seen := map[RunEventType]bool{}
	for _, event := range events {
		seen[event.Type] = true
	}
	for _, want := range wants {
		if !seen[want] {
			return false
		}
	}
	return true
}

type parallelTestModel struct {
	delay     time.Duration
	active    atomic.Int32
	maxActive atomic.Int32
}

func (m *parallelTestModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	active := m.active.Add(1)
	for {
		maxActive := m.maxActive.Load()
		if active <= maxActive || m.maxActive.CompareAndSwap(maxActive, active) {
			break
		}
	}
	defer m.active.Add(-1)
	if m.delay > 0 {
		select {
		case <-time.After(m.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	content := "branch complete"
	if len(input) > 0 {
		text := input[len(input)-1].Content
		for _, name := range []string{"oom_restarts", "recent_changes"} {
			if strings.Contains(text, name) {
				content = name + " complete"
			}
		}
	}
	return schema.AssistantMessage(content, nil), nil
}

func (m *parallelTestModel) Stream(context.Context, []*schema.Message, ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, nil
}
