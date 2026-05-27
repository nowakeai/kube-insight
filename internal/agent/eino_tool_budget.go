package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

const toolBudgetWarningMarker = "KUBE_INSIGHT_TOOL_BUDGET_WARNING"

type toolBudgetMiddleware struct {
	adk.BaseChatModelAgentMiddleware
	maxIterations int
}

func (m *toolBudgetMiddleware) BeforeAgent(ctx context.Context, runCtx *adk.ChatModelAgentContext) (context.Context, *adk.ChatModelAgentContext, error) {
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
	warning := fmt.Sprintf("%s: Tool budget pressure detected (%s). Before calling another tool, audit the evidence already in this conversation: what claim can be answered now, what exact proof is still missing, and whether the next tool call is materially different from previous calls. If existing evidence is enough, answer with caveats and evidence references. If proof is missing, choose the smallest targeted query that closes that specific gap; avoid repeating equivalent SQL, search, or raw-document probes.", toolBudgetWarningMarker, reason)
	next := *state
	next.Messages = append(append([]adk.Message(nil), state.Messages...), schema.SystemMessage(warning))
	return ctx, &next, nil
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
		if byName[name] >= repeatedToolWarningThreshold(name) {
			return total, name
		}
	}
	return total, ""
}

func repeatedToolWarningThreshold(name string) int {
	switch name {
	case "kube_insight_sql", scriptedQueryToolName:
		return 4
	default:
		return 3
	}
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
