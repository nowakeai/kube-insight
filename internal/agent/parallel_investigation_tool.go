package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

const (
	parallelInvestigationToolName       = "parallel_investigation"
	maxParallelInvestigationBranches    = 3
	defaultParallelInvestigationTimeout = 90 * time.Second
	maxParallelInvestigationTimeout     = 3 * time.Minute
	maxParallelInvestigationAnswerRunes = 2400
)

type ParallelInvestigationTool struct {
	model model.BaseChatModel
	tools []tool.BaseTool
}

type parallelInvestigationArguments struct {
	Question      string                        `json:"question"`
	Branches      []parallelInvestigationBranch `json:"branches"`
	TimeoutMillis int                           `json:"timeoutMillis,omitempty"`
	Context       string                        `json:"context,omitempty"`
}

type parallelInvestigationBranch struct {
	Name      string `json:"name"`
	Objective string `json:"objective"`
	Focus     string `json:"focus,omitempty"`
}

type parallelInvestigationResult struct {
	Tool     string                              `json:"tool"`
	Summary  string                              `json:"summary"`
	Branches []parallelInvestigationBranchResult `json:"branches"`
}

type parallelInvestigationBranchResult struct {
	Name       string `json:"name"`
	Objective  string `json:"objective"`
	Status     string `json:"status"`
	DurationMS int64  `json:"durationMs"`
	Answer     string `json:"answer,omitempty"`
	Error      string `json:"error,omitempty"`
}

func NewParallelInvestigationTool(mdl model.BaseChatModel, evidenceTools []tool.BaseTool) tool.BaseTool {
	return ParallelInvestigationTool{model: mdl, tools: append([]tool.BaseTool(nil), evidenceTools...)}
}

func (t ParallelInvestigationTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: parallelInvestigationToolName,
		Desc: "Run 2-3 independent kube-insight investigation subagents concurrently and return compact branch findings. Use proactively for broad incidents, mixed symptom questions, cluster health overviews, namespace triage, or prompts that naturally split into independent branches such as OOM/restarts, recent changes, topology/impact, and collector coverage. Do not use for exact Service health or exact kind/namespace/name recent-change questions where a narrow typed tool path is sufficient.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"question": {
				Type:     schema.String,
				Required: true,
				Desc:     "Original user question or follow-up, including absolute time bounds and cluster/namespace scope when known.",
			},
			"branches": {
				Type:     schema.Array,
				Required: true,
				Desc:     "Two or three independent branches. Each item should include name, objective, and optional focus. Good branch names: collector_coverage, oom_restarts, recent_changes, topology_impact.",
			},
			"context": {
				Type: schema.String,
				Desc: "Optional concise transcript/evidence context needed by all branches. Do not include huge raw tool outputs.",
			},
			"timeoutMillis": {
				Type: schema.Integer,
				Desc: "Optional per-branch timeout in milliseconds. Defaults to 90000 and is capped at 180000.",
			},
		}),
	}, nil
}

func (t ParallelInvestigationTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	if t.model == nil {
		return "", errors.New("parallel_investigation requires a chat model")
	}
	var args parallelInvestigationArguments
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return "", fmt.Errorf("parse %s arguments: %w", parallelInvestigationToolName, err)
	}
	args.Question = strings.TrimSpace(args.Question)
	if args.Question == "" {
		return "", fmt.Errorf("%s requires a question", parallelInvestigationToolName)
	}
	branches := normalizeParallelInvestigationBranches(args.Branches)
	if len(branches) == 0 {
		return "", fmt.Errorf("%s requires at least one branch", parallelInvestigationToolName)
	}
	if len(branches) > maxParallelInvestigationBranches {
		return "", fmt.Errorf("%s supports at most %d branches", parallelInvestigationToolName, maxParallelInvestigationBranches)
	}
	timeout := boundedParallelInvestigationTimeout(args.TimeoutMillis)
	results := make([]parallelInvestigationBranchResult, len(branches))
	var wg sync.WaitGroup
	for i, branch := range branches {
		i, branch := i, branch
		wg.Add(1)
		go func() {
			defer wg.Done()
			branchCtx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			results[i] = t.runBranch(branchCtx, args.Question, args.Context, branch)
		}()
	}
	wg.Wait()
	out := parallelInvestigationResult{Tool: parallelInvestigationToolName, Summary: parallelInvestigationSummary(results), Branches: results}
	encoded, err := json.Marshal(out)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func normalizeParallelInvestigationBranches(branches []parallelInvestigationBranch) []parallelInvestigationBranch {
	out := make([]parallelInvestigationBranch, 0, len(branches))
	for i, branch := range branches {
		branch.Name = compactIdentifier(branch.Name)
		branch.Objective = strings.TrimSpace(branch.Objective)
		branch.Focus = strings.TrimSpace(branch.Focus)
		if branch.Name == "" {
			branch.Name = fmt.Sprintf("branch_%d", i+1)
		}
		if branch.Objective == "" {
			branch.Objective = branch.Focus
		}
		if branch.Objective == "" {
			continue
		}
		out = append(out, branch)
	}
	return out
}

func (t ParallelInvestigationTool) runBranch(ctx context.Context, question string, sharedContext string, branch parallelInvestigationBranch) parallelInvestigationBranchResult {
	start := time.Now()
	result := parallelInvestigationBranchResult{Name: branch.Name, Objective: branch.Objective, Status: "completed"}
	agentConfig := &adk.ChatModelAgentConfig{
		Name:          "kube_insight_" + branch.Name,
		Description:   "Specialist kube-insight investigation subagent for " + branch.Name,
		Instruction:   parallelInvestigationBranchInstruction(branch),
		Model:         t.model,
		MaxIterations: 6,
	}
	if len(t.tools) > 0 {
		agentConfig.ToolsConfig = adk.ToolsConfig{ToolsNodeConfig: compose.ToolsNodeConfig{Tools: t.tools}}
	}
	agent, err := adk.NewChatModelAgent(ctx, agentConfig)
	if err != nil {
		result.Status = "failed"
		result.Error = err.Error()
		result.DurationMS = toolAuditDurationMS(start)
		return result
	}
	runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: agent})
	iter := runner.Run(ctx, []adk.Message{schema.UserMessage(parallelInvestigationBranchPrompt(question, sharedContext, branch))})
	answer := ""
	for {
		event, ok := iter.Next()
		if !ok {
			break
		}
		if event == nil {
			continue
		}
		if event.Err != nil {
			result.Status = "failed"
			result.Error = event.Err.Error()
			result.DurationMS = toolAuditDurationMS(start)
			return result
		}
		if event.Output == nil || event.Output.MessageOutput == nil {
			continue
		}
		msg, err := event.Output.MessageOutput.GetMessage()
		if err != nil {
			result.Status = "failed"
			result.Error = err.Error()
			result.DurationMS = toolAuditDurationMS(start)
			return result
		}
		if msg != nil && msg.Role == schema.Assistant && strings.TrimSpace(msg.Content) != "" {
			answer = msg.Content
		}
	}
	if err := ctx.Err(); err != nil {
		result.Status = "failed"
		result.Error = err.Error()
	} else if answer == "" {
		result.Status = "failed"
		result.Error = "subagent returned no answer"
	} else {
		result.Answer = compactText(answer, maxParallelInvestigationAnswerRunes)
	}
	result.DurationMS = toolAuditDurationMS(start)
	return result
}

func parallelInvestigationBranchInstruction(branch parallelInvestigationBranch) string {
	return strings.TrimSpace(fmt.Sprintf(`You are a kube-insight specialist subagent for branch %q.

Rules:
- Investigate only this branch objective: %s
- Use kube-insight tools for evidence before making Kubernetes claims.
- Keep tool use small and bounded. Prefer health/search/schema/SQL summaries over raw YAML.
- Do not solve other branches. Mention only directly relevant uncertainty.
- Return concise Markdown with: Findings, Evidence, Gaps.
- Include stable identities, timestamps, cluster IDs, artifact IDs, SQL row counts, or exact object names when tools provide them.`, branch.Name, branch.Objective))
}

func parallelInvestigationBranchPrompt(question string, sharedContext string, branch parallelInvestigationBranch) string {
	var b strings.Builder
	b.WriteString("Original user question:\n")
	b.WriteString(question)
	b.WriteString("\n\nBranch name:\n")
	b.WriteString(branch.Name)
	b.WriteString("\n\nBranch objective:\n")
	b.WriteString(branch.Objective)
	if branch.Focus != "" {
		b.WriteString("\n\nBranch focus/hints:\n")
		b.WriteString(branch.Focus)
	}
	if strings.TrimSpace(sharedContext) != "" {
		b.WriteString("\n\nShared context:\n")
		b.WriteString(compactText(sharedContext, 2000))
	}
	return b.String()
}

func boundedParallelInvestigationTimeout(ms int) time.Duration {
	if ms <= 0 {
		return defaultParallelInvestigationTimeout
	}
	timeout := time.Duration(ms) * time.Millisecond
	if timeout > maxParallelInvestigationTimeout {
		return maxParallelInvestigationTimeout
	}
	return timeout
}

func parallelInvestigationSummary(results []parallelInvestigationBranchResult) string {
	completed := 0
	failed := 0
	for _, result := range results {
		if result.Status == "completed" {
			completed++
		} else {
			failed++
		}
	}
	return fmt.Sprintf("parallel investigation completed %d branch(es), failed %d branch(es)", completed, failed)
}

func compactIdentifier(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastUnderscore := false
	for _, r := range value {
		ok := r >= 'a' && r <= 'z' || r >= '0' && r <= '9'
		if ok {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore && b.Len() > 0 {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	return strings.Trim(b.String(), "_")
}
