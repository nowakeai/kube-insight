package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

const toolBudgetWarningMarker = "KUBE_INSIGHT_TOOL_BUDGET_WARNING"
const toolBudgetTotalKey = "__total"

type toolBudgetMiddleware struct {
	adk.BaseChatModelAgentMiddleware
	maxIterations int

	mu     sync.Mutex
	counts map[string]int
}

func (m *toolBudgetMiddleware) BeforeAgent(ctx context.Context, runCtx *adk.ChatModelAgentContext) (context.Context, *adk.ChatModelAgentContext, error) {
	m.mu.Lock()
	m.counts = map[string]int{}
	m.mu.Unlock()
	return ctx, runCtx, nil
}

func (m *toolBudgetMiddleware) BeforeModelRewriteState(ctx context.Context, state *adk.ChatModelAgentState, mc *adk.ModelContext) (context.Context, *adk.ChatModelAgentState, error) {
	if state == nil || len(state.Messages) == 0 {
		return ctx, state, nil
	}
	toolCalls, repeatedTool := toolBudgetSignal(state.Messages)
	warnAt := m.warnAtToolCalls()
	if toolCalls < warnAt && repeatedTool == "" {
		return ctx, state, nil
	}
	if toolBudgetWarningAlreadyPresent(state.Messages) {
		return ctx, state, nil
	}
	reason := fmt.Sprintf("%d completed tool calls", toolCalls)
	if repeatedTool != "" {
		reason = fmt.Sprintf("%s called repeatedly", repeatedTool)
	}
	warning := fmt.Sprintf("%s: Tool budget pressure detected (%s). If existing evidence can answer the user's question, stop calling tools and produce the final answer with explicit caveats and evidence references. If proof is still missing, make at most one targeted query that directly closes the gap; do not repeat equivalent searches or broad raw-document probes.", toolBudgetWarningMarker, reason)
	next := *state
	next.Messages = append(append([]adk.Message(nil), state.Messages...), schema.SystemMessage(warning))
	return ctx, &next, nil
}

func (m *toolBudgetMiddleware) WrapInvokableToolCall(ctx context.Context, endpoint adk.InvokableToolCallEndpoint, tCtx *adk.ToolContext) (adk.InvokableToolCallEndpoint, error) {
	return func(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
		name := ""
		if tCtx != nil {
			name = tCtx.Name
		}
		if name == "" {
			return endpoint(ctx, argumentsInJSON, opts...)
		}
		total, repeated, ok := m.recordToolBudgetCall(ctx, name)
		if ok && m.shouldReturnBudgetGuard(total, repeated) {
			return toolBudgetGuardOutput(name, total, repeated), nil
		}
		return endpoint(ctx, argumentsInJSON, opts...)
	}, nil
}

func (m *toolBudgetMiddleware) recordToolBudgetCall(ctx context.Context, name string) (int, int, bool) {
	if name == "" {
		return 0, 0, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.counts == nil {
		m.counts = map[string]int{}
	}
	m.counts[toolBudgetTotalKey]++
	m.counts[name]++
	return m.counts[toolBudgetTotalKey], m.counts[name], true
}

func (m *toolBudgetMiddleware) shouldReturnBudgetGuard(total int, repeated int) bool {
	return repeated > 3 || total > m.warnAtToolCalls()+6
}

func (m *toolBudgetMiddleware) warnAtToolCalls() int {
	if m.maxIterations > 0 && m.maxIterations < 12 {
		if half := m.maxIterations / 2; half > 2 {
			return half
		}
		return 3
	}
	return 6
}

func toolBudgetSignal(messages []adk.Message) (int, string) {
	total := 0
	byName := map[string]int{}
	for _, msg := range messages {
		if msg == nil || msg.Role != schema.Tool {
			continue
		}
		total++
		name := firstNonEmptyString(msg.ToolName, toolNameFromToolMessage(msg))
		if name == "" {
			continue
		}
		byName[name]++
		if byName[name] >= 3 {
			return total, name
		}
	}
	return total, ""
}

func toolNameFromToolMessage(msg adk.Message) string {
	if msg == nil || msg.Content == "" {
		return ""
	}
	var value map[string]any
	if err := json.Unmarshal([]byte(msg.Content), &value); err != nil {
		return ""
	}
	if name, ok := value["name"].(string); ok {
		return name
	}
	if name, ok := value["tool"].(string); ok {
		return name
	}
	return ""
}

func toolBudgetWarningAlreadyPresent(messages []adk.Message) bool {
	for _, msg := range messages {
		if msg != nil && msg.Role == schema.System && strings.Contains(msg.Content, toolBudgetWarningMarker) {
			return true
		}
	}
	return false
}

func toolBudgetGuardOutput(name string, total int, repeated int) string {
	return string(jsonRaw(map[string]any{
		"budgetGuard": true,
		"tool":        name,
		"totalCalls":  total,
		"repeatCalls": repeated,
		"summary":     fmt.Sprintf("tool budget guard: %s has been called %d times in this run", name, repeated),
		"message":     "Stop repeating equivalent tool calls. If the existing evidence can answer the user, produce the final answer now with caveats and evidence references. If one proof gap remains, use a different, narrower query shape.",
	}))
}
