package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

func TestEinoRunnerPropagatesRunAndToolCallContextToTools(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	session, err := store.CreateSession(ctx, CreateSessionInput{})
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.CreateRun(ctx, session.ID, CreateRunInput{Input: "probe context", Provider: "openai-compatible", Model: "test-model"})
	if err != nil {
		t.Fatal(err)
	}
	probe := &contextProbeTool{}
	runner, err := NewEinoRunner(ctx, EinoRunnerConfig{
		Instruction: "Use the probe.",
		Model:       &contextProbeModel{},
		Tools:       []tool.BaseTool{probe},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Run(ctx, EinoRunInput{Messages: []Message{{Role: RoleUser, Content: run.Input}}, Store: store, RunID: run.ID, Provider: run.Provider, Model: run.Model}); err != nil {
		t.Fatal(err)
	}
	if probe.run.RunID != run.ID || probe.run.Provider != run.Provider || probe.run.Model != run.Model {
		t.Fatalf("run context = %#v", probe.run)
	}
	if probe.toolCall.CallID != "call_context_probe" || probe.toolCall.Name != "context_probe" {
		t.Fatalf("tool call context = %#v", probe.toolCall)
	}
}

type contextProbeModel struct {
	sawTool bool
}

func (m *contextProbeModel) Generate(_ context.Context, input []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	for _, message := range input {
		if message.Role == schema.Tool && strings.Contains(message.Content, "ok") {
			m.sawTool = true
		}
	}
	if !m.sawTool {
		return schema.AssistantMessage("probing", []schema.ToolCall{{ID: "call_context_probe", Function: schema.FunctionCall{Name: "context_probe", Arguments: `{}`}}}), nil
	}
	return schema.AssistantMessage("done", nil), nil
}

func (m *contextProbeModel) Stream(context.Context, []*schema.Message, ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, errors.New("stream is not implemented in contextProbeModel")
}

func (m *contextProbeModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}

type contextProbeTool struct {
	run      RunExecutionContext
	toolCall ToolCallExecutionContext
}

func (t *contextProbeTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: "context_probe", Desc: "Probe run context"}, nil
}

func (t *contextProbeTool) InvokableRun(ctx context.Context, _ string, _ ...tool.Option) (string, error) {
	t.run, _ = RunExecutionContextFromContext(ctx)
	t.toolCall, _ = ToolCallExecutionContextFromContext(ctx)
	return `{"ok":true}`, nil
}
