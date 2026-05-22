package agent

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/filesystem"
	"github.com/cloudwego/eino/adk/middlewares/reduction"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

func TestEinoCapabilityRunnerEventsHandlersAndStreaming(t *testing.T) {
	ctx := context.Background()
	callTool := &capabilityTool{name: "capability_lookup", result: "lookup result"}
	handler := &capabilityHandler{}
	mdl := &capabilityToolCallingModel{}
	agent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:        "capability_agent",
		Description: "capability agent",
		Model:       mdl,
		ToolsConfig: adk.ToolsConfig{ToolsNodeConfig: compose.ToolsNodeConfig{Tools: []tool.BaseTool{callTool}}},
		Handlers:    []adk.ChatModelAgentMiddleware{handler},
	})
	if err != nil {
		t.Fatal(err)
	}
	runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: agent, EnableStreaming: true})
	iter := runner.Query(ctx, "check capability")
	var contents []string
	var toolEventSeen bool
	for {
		event, ok := iter.Next()
		if !ok {
			break
		}
		if event.Err != nil {
			t.Fatal(event.Err)
		}
		if event.Output == nil || event.Output.MessageOutput == nil {
			continue
		}
		msg, err := event.Output.MessageOutput.GetMessage()
		if err != nil {
			t.Fatal(err)
		}
		if msg == nil {
			continue
		}
		if msg.Role == schema.Tool && msg.Content == "lookup result" {
			toolEventSeen = true
		}
		if msg.Content != "" {
			contents = append(contents, msg.Content)
		}
	}
	if !toolEventSeen {
		t.Fatalf("tool result event was not emitted; contents=%v", contents)
	}
	if got := strings.Join(contents, "|"); !strings.Contains(got, "streamed final") {
		t.Fatalf("streamed final answer not observed; contents=%v", contents)
	}
	if handler.beforeAgent == 0 || handler.beforeModel == 0 || handler.afterModel == 0 || len(handler.toolCalls) != 1 || handler.toolCalls[0] != "capability_lookup" {
		t.Fatalf("handler did not observe expected lifecycle: %#v", handler)
	}
}

func TestEinoCapabilityCheckpointInterruptAndResume(t *testing.T) {
	ctx := context.Background()
	store := newCapabilityCheckpointStore()
	var resumed bool
	agent := &capabilityInterruptAgent{
		runFn: func(ctx context.Context, input *adk.AgentInput, opts ...adk.AgentRunOption) *adk.AsyncIterator[*adk.AgentEvent] {
			iter, gen := adk.NewAsyncIteratorPair[*adk.AgentEvent]()
			interrupt := adk.StatefulInterrupt(ctx, "namespace required", "pending namespace")
			interrupt.Action.Interrupted.Data = "namespace required"
			gen.Send(interrupt)
			gen.Close()
			return iter
		},
		resumeFn: func(ctx context.Context, info *adk.ResumeInfo, opts ...adk.AgentRunOption) *adk.AsyncIterator[*adk.AgentEvent] {
			if !info.WasInterrupted || !info.IsResumeTarget || info.Data != "namespace required" {
				t.Fatalf("unexpected resume info: %#v", info)
			}
			resumed = true
			iter, gen := adk.NewAsyncIteratorPair[*adk.AgentEvent]()
			gen.Send(adk.EventFromMessage(schema.AssistantMessage("resumed", nil), nil, schema.Assistant, ""))
			gen.Close()
			return iter
		},
	}
	runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: agent, CheckPointStore: store})
	iter := runner.Query(ctx, "needs namespace", adk.WithCheckPointID("capability-checkpoint"))
	var interrupt *adk.AgentEvent
	for {
		event, ok := iter.Next()
		if !ok {
			break
		}
		if event.Err != nil {
			t.Fatal(event.Err)
		}
		if event.Action != nil && event.Action.Interrupted != nil {
			interrupt = event
		}
	}
	if interrupt == nil || len(interrupt.Action.Interrupted.InterruptContexts) == 0 {
		t.Fatalf("interrupt event missing contexts: %#v", interrupt)
	}
	if _, ok, err := store.Get(ctx, "capability-checkpoint"); err != nil || !ok {
		t.Fatalf("checkpoint was not stored: ok=%v err=%v", ok, err)
	}
	iter, err := runner.ResumeWithParams(ctx, "capability-checkpoint", &adk.ResumeParams{Targets: map[string]any{interrupt.Action.Interrupted.InterruptContexts[0].ID: nil}})
	if err != nil {
		t.Fatal(err)
	}
	var resumedContent string
	for {
		event, ok := iter.Next()
		if !ok {
			break
		}
		if event.Err != nil {
			t.Fatal(event.Err)
		}
		if event.Output != nil && event.Output.MessageOutput != nil && event.Output.MessageOutput.Message != nil {
			resumedContent = event.Output.MessageOutput.Message.Content
		}
	}
	if !resumed || resumedContent != "resumed" {
		t.Fatalf("resume failed: resumed=%v content=%q", resumed, resumedContent)
	}
}

func TestEinoCapabilityReductionMiddlewareOffloadsLargeToolOutput(t *testing.T) {
	ctx := context.Background()
	backend := filesystem.NewInMemoryBackend()
	mw, err := reduction.New(ctx, &reduction.Config{
		Backend:           backend,
		RootDir:           "/artifacts",
		SkipClear:         true,
		SkipTruncation:    false,
		MaxLengthForTrunc: 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	wrapped, err := mw.WrapInvokableToolCall(ctx, (&capabilityTool{name: "large_output", result: strings.Repeat("abcdef", 20)}).InvokableRun, &adk.ToolContext{Name: "large_output", CallID: "tool_call_1"})
	if err != nil {
		t.Fatal(err)
	}
	output, err := wrapped(ctx, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "<persisted-output>") || !strings.Contains(output, "/artifacts/trunc/tool_call_1") {
		t.Fatalf("unexpected reduced output: %s", output)
	}
	stored, err := backend.Read(ctx, &filesystem.ReadRequest{FilePath: "/artifacts/trunc/tool_call_1"})
	if err != nil {
		t.Fatal(err)
	}
	if stored.Content != strings.Repeat("abcdef", 20) {
		t.Fatalf("offloaded content mismatch: %q", stored.Content)
	}
}

func TestEinoCapabilityAgentAsTool(t *testing.T) {
	ctx := context.Background()
	inner, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:        "evidence_condenser",
		Description: "condense large evidence artifacts into cited findings",
		Model:       &capabilityStaticModel{answer: "artifact_1 shows OOMKilled"},
	})
	if err != nil {
		t.Fatal(err)
	}
	agentTool := adk.NewAgentTool(ctx, inner)
	info, err := agentTool.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "evidence_condenser" || !strings.Contains(info.Desc, "condense") {
		t.Fatalf("agent tool info = %#v", info)
	}
	invokable, ok := agentTool.(tool.InvokableTool)
	if !ok {
		t.Fatalf("agent tool is not invokable: %T", agentTool)
	}
	result, err := invokable.InvokableRun(ctx, `{"request":"summarize artifact_1"}`)
	if err != nil {
		t.Fatal(err)
	}
	if result != "artifact_1 shows OOMKilled" {
		t.Fatalf("agent tool result = %q", result)
	}
}

type capabilityHandler struct {
	adk.BaseChatModelAgentMiddleware
	beforeAgent int
	beforeModel int
	afterModel  int
	toolCalls   []string
}

func (h *capabilityHandler) BeforeAgent(ctx context.Context, runCtx *adk.ChatModelAgentContext) (context.Context, *adk.ChatModelAgentContext, error) {
	h.beforeAgent++
	return ctx, runCtx, nil
}

func (h *capabilityHandler) BeforeModelRewriteState(ctx context.Context, state *adk.ChatModelAgentState, mc *adk.ModelContext) (context.Context, *adk.ChatModelAgentState, error) {
	h.beforeModel++
	return ctx, state, nil
}

func (h *capabilityHandler) AfterModelRewriteState(ctx context.Context, state *adk.ChatModelAgentState, mc *adk.ModelContext) (context.Context, *adk.ChatModelAgentState, error) {
	h.afterModel++
	return ctx, state, nil
}

func (h *capabilityHandler) WrapInvokableToolCall(ctx context.Context, endpoint adk.InvokableToolCallEndpoint, tCtx *adk.ToolContext) (adk.InvokableToolCallEndpoint, error) {
	return func(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
		h.toolCalls = append(h.toolCalls, tCtx.Name)
		return endpoint(ctx, argumentsInJSON, opts...)
	}, nil
}

type capabilityTool struct {
	name   string
	result string
}

func (t *capabilityTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: t.name, Desc: "capability tool"}, nil
}

func (t *capabilityTool) InvokableRun(context.Context, string, ...tool.Option) (string, error) {
	return t.result, nil
}

type capabilityToolCallingModel struct {
	mu         sync.Mutex
	calls      int
	boundTools []*schema.ToolInfo
}

func (m *capabilityToolCallingModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	if m.calls == 1 {
		return schema.AssistantMessage("checking evidence", []schema.ToolCall{{ID: "tool_call_1", Function: schema.FunctionCall{Name: "capability_lookup", Arguments: `{"query":"api"}`}}}), nil
	}
	return schema.AssistantMessage("final from generate", nil), nil
}

func (m *capabilityToolCallingModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	if m.calls == 1 {
		return schema.StreamReaderFromArray([]*schema.Message{schema.AssistantMessage("checking evidence", []schema.ToolCall{{ID: "tool_call_1", Function: schema.FunctionCall{Name: "capability_lookup", Arguments: `{"query":"api"}`}}})}), nil
	}
	return schema.StreamReaderFromArray([]*schema.Message{schema.AssistantMessage("streamed ", nil), schema.AssistantMessage("final", nil)}), nil
}

func (m *capabilityToolCallingModel) WithTools(infos []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	m.boundTools = append([]*schema.ToolInfo(nil), infos...)
	return m, nil
}

type capabilityStaticModel struct {
	answer string
}

func (m *capabilityStaticModel) Generate(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
	return schema.AssistantMessage(m.answer, nil), nil
}

func (m *capabilityStaticModel) Stream(context.Context, []*schema.Message, ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, errors.New("stream not implemented")
}

type capabilityInterruptAgent struct {
	runFn    func(context.Context, *adk.AgentInput, ...adk.AgentRunOption) *adk.AsyncIterator[*adk.AgentEvent]
	resumeFn func(context.Context, *adk.ResumeInfo, ...adk.AgentRunOption) *adk.AsyncIterator[*adk.AgentEvent]
}

func (a *capabilityInterruptAgent) Name(context.Context) string { return "capability_interrupt" }
func (a *capabilityInterruptAgent) Description(context.Context) string {
	return "interrupt capability agent"
}
func (a *capabilityInterruptAgent) Run(ctx context.Context, input *adk.AgentInput, opts ...adk.AgentRunOption) *adk.AsyncIterator[*adk.AgentEvent] {
	return a.runFn(ctx, input, opts...)
}
func (a *capabilityInterruptAgent) Resume(ctx context.Context, info *adk.ResumeInfo, opts ...adk.AgentRunOption) *adk.AsyncIterator[*adk.AgentEvent] {
	return a.resumeFn(ctx, info, opts...)
}

type capabilityCheckpointStore struct {
	values map[string][]byte
}

func newCapabilityCheckpointStore() *capabilityCheckpointStore {
	return &capabilityCheckpointStore{values: map[string][]byte{}}
}

func (s *capabilityCheckpointStore) Get(ctx context.Context, key string) ([]byte, bool, error) {
	value, ok := s.values[key]
	return value, ok, nil
}

func (s *capabilityCheckpointStore) Set(ctx context.Context, key string, value []byte) error {
	s.values[key] = append([]byte(nil), value...)
	return nil
}
