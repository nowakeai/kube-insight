package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

const evidenceCondenserToolName = "evidence_condenser"

type EvidenceCondenserTool struct {
	model model.BaseChatModel
}

// NewEvidenceCondenserTool exposes a small specialist agent as a normal tool.
// The main agent decides when evidence is too large or noisy enough to justify
// the extra model call.
func NewEvidenceCondenserTool(ctx context.Context, mdl model.BaseChatModel) (tool.BaseTool, error) {
	if mdl == nil {
		return nil, ErrEinoModelRequired
	}
	return EvidenceCondenserTool{model: mdl}, nil
}

func (t EvidenceCondenserTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: evidenceCondenserToolName,
		Desc: "Condense large kube-insight evidence artifacts into compact cited findings. The request must include source artifact IDs/titles and relevant row or snippet excerpts.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"request": {
				Type:     schema.String,
				Required: true,
				Desc:     "Evidence condensation request with source artifact IDs/titles and relevant row or snippet excerpts.",
			},
		}),
	}, nil
}

func (t EvidenceCondenserTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	var args struct {
		Request string `json:"request"`
	}
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return "", fmt.Errorf("parse %s arguments: %w", evidenceCondenserToolName, err)
	}
	request := strings.TrimSpace(args.Request)
	parentRun, hasParentRun := RunExecutionContextFromContext(ctx)
	parentToolCall, _ := ToolCallExecutionContextFromContext(ctx)
	runner, err := NewEinoRunner(ctx, EinoRunnerConfig{
		Name:          evidenceCondenserToolName,
		Description:   "Condense large kube-insight evidence artifacts into compact cited findings.",
		Instruction:   EvidenceCondenserInstruction(),
		Model:         t.model,
		MaxIterations: 3,
	})
	if err != nil {
		return "", err
	}
	store := parentRun.Store
	runID := ""
	if hasParentRun {
		child, err := t.createChildRun(ctx, parentRun, parentToolCall, request)
		if err != nil {
			return "", err
		}
		runID = child.ID
		if err := appendChildRunCreatedEvent(ctx, store, runID, parentRun.RunID); err != nil {
			return "", err
		}
	}
	result, err := runner.Run(ctx, EinoRunInput{
		Messages: []Message{{Role: RoleUser, Content: request}},
		Store:    store,
		RunID:    runID,
		Provider: parentRun.Provider,
		Model:    parentRun.Model,
	})
	if err != nil {
		return "", err
	}
	return result.FinalAnswer, nil
}

func (t EvidenceCondenserTool) createChildRun(ctx context.Context, parent RunExecutionContext, parentToolCall ToolCallExecutionContext, input string) (Run, error) {
	parentRun, err := parent.Store.GetRun(ctx, parent.RunID)
	if err != nil {
		return Run{}, err
	}
	return parent.Store.CreateRun(ctx, parentRun.SessionID, CreateRunInput{
		Input:    input,
		Provider: parent.Provider,
		Model:    parent.Model,
		Metadata: jsonRaw(map[string]any{
			"parentRunId":       parent.RunID,
			"parentToolCallId":  parentToolCall.CallID,
			"parentToolName":    parentToolCall.Name,
			"subagentName":      evidenceCondenserToolName,
			"contextPolicy":     "child-full-parent-compact",
			"modelContextScope": "subagent",
		}),
	})
}

func EvidenceCondenserInstruction() string {
	return strings.TrimSpace(`
You are the kube-insight evidence condenser. Convert large or noisy kube-insight tool results into compact, human-readable findings for the main investigation agent.

Rules:
- Do not fetch new Kubernetes data and do not invent facts. Use only the artifact IDs, titles, snippets, rows, object identities, timestamps, and tool summaries provided in the request.
- Prefer exact Kubernetes identities: cluster, kind, namespace, name, uid, fact_key, fact_value, timestamps, row counts, version IDs, and change IDs.
- Drop unrelated or duplicate rows. Keep only evidence directly useful for the stated user question.
- Return concise Markdown with 3 to 8 bullets or a small table.
- Start with a short Sources line listing the source artifact IDs/titles present in the request. Each finding must name the source artifact or stable ID it came from.
- If the provided evidence is insufficient, say what is missing instead of guessing.
`)
}
