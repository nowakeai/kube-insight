package agent

import (
	"context"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
)

const evidenceCondenserToolName = "evidence_condenser"

// NewEvidenceCondenserTool exposes a small specialist agent as a normal tool.
// The main agent decides when evidence is too large or noisy enough to justify
// the extra model call.
func NewEvidenceCondenserTool(ctx context.Context, mdl model.BaseChatModel) (tool.BaseTool, error) {
	inner, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:          evidenceCondenserToolName,
		Description:   "Condense large kube-insight evidence artifacts into compact cited findings. The request must include source artifact IDs/titles and relevant row or snippet excerpts.",
		Instruction:   EvidenceCondenserInstruction(),
		Model:         mdl,
		MaxIterations: 3,
	})
	if err != nil {
		return nil, err
	}
	return adk.NewAgentTool(ctx, inner), nil
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
