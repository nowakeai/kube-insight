package agent_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	openaimodel "github.com/cloudwego/eino-ext/components/model/openai"
	einomcp "github.com/cloudwego/eino-ext/components/tool/mcp"
	"github.com/cloudwego/eino/components/tool"
	markclient "github.com/mark3labs/mcp-go/client"
	markmcp "github.com/mark3labs/mcp-go/mcp"

	kiagent "kube-insight/internal/agent"
	insightmcp "kube-insight/internal/mcp"
)

const realPromptLiveEvalEnabledEnv = "KUBE_INSIGHT_AGENT_REAL_PROMPT_EVAL"

func TestRealDBPromptContextEvaluation(t *testing.T) {
	if os.Getenv(realPromptLiveEvalEnabledEnv) != "1" {
		t.Skipf("set %s=1 to run live LLM evaluation against a real kube-insight DB", realPromptLiveEvalEnabledEnv)
	}
	dbPath := strings.TrimSpace(os.Getenv("KUBE_INSIGHT_AGENT_REAL_EVAL_DB"))
	if dbPath == "" {
		t.Fatal("KUBE_INSIGHT_AGENT_REAL_EVAL_DB is required")
	}
	questions := realPromptEvalQuestions()
	if len(questions) == 0 {
		t.Fatal("KUBE_INSIGHT_AGENT_REAL_EVAL_QUESTIONS is required")
	}
	specs, err := realPromptEvalModelSpecsFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	richContext := realPromptEvalContext(t)
	modes := realPromptEvalModes(richContext)

	ctx, cancel := context.WithTimeout(context.Background(), realPromptEvalTimeout())
	defer cancel()

	allReports := []realPromptEvalReport{}
	for _, spec := range specs {
		t.Run(spec.Name, func(t *testing.T) {
			model, err := openaimodel.NewChatModel(ctx, &openaimodel.ChatModelConfig{
				APIKey:  os.Getenv(spec.APIKeyEnv),
				BaseURL: os.Getenv(spec.BaseURLEnv),
				Model:   spec.Model,
				Timeout: realPromptEvalTimeout(),
			})
			if err != nil {
				t.Fatal(err)
			}
			tools := realPromptEvalTools(t, ctx, dbPath)
			for _, mode := range modes {
				t.Run(mode, func(t *testing.T) {
					runner, err := kiagent.NewEinoRunner(ctx, kiagent.EinoRunnerConfig{
						Description:        "Kubernetes investigation assistant real DB prompt-context evaluation runner.",
						Instruction:        realPromptEvalInstruction(mode, richContext),
						Model:              model,
						Tools:              tools,
						EmitInternalEvents: true,
						EnableStreaming:    true,
						MaxIterations:      realPromptEvalMaxIterations(),
					})
					if err != nil {
						t.Fatal(err)
					}
					for _, question := range questions {
						t.Run(realPromptEvalCaseName(question), func(t *testing.T) {
							report := runRealPromptEvalCase(t, ctx, runner, spec, mode, question)
							allReports = append(allReports, report)
							if report.Error != "" {
								t.Fatalf("runner failed: %s", report.Error)
							}
							if strings.TrimSpace(report.FinalAnswer) == "" {
								t.Fatal("final answer is empty")
							}
							if report.ToolCallCount == 0 {
								t.Fatal("expected at least one evidence tool call")
							}
						})
					}
				})
			}
		})
	}
	writeRealPromptEvalReports(t, allReports)
}

type realPromptEvalModelSpec struct {
	Name       string
	Model      string
	APIKeyEnv  string
	BaseURLEnv string
}

type realPromptEvalReport struct {
	Model         string                       `json:"model"`
	Mode          string                       `json:"mode"`
	Question      string                       `json:"question"`
	DurationMS    int64                        `json:"durationMs"`
	ToolCallCount int                          `json:"toolCallCount"`
	ToolCalls     []kiagent.EvaluationToolCall `json:"toolCalls"`
	Citations     int                          `json:"citations"`
	ArtifactKinds []string                     `json:"artifactKinds"`
	FinalAnswer   string                       `json:"finalAnswer"`
	Error         string                       `json:"error,omitempty"`
}

func runRealPromptEvalCase(t *testing.T, ctx context.Context, runner *kiagent.EinoRunner, spec realPromptEvalModelSpec, mode, question string) realPromptEvalReport {
	t.Helper()
	store := kiagent.NewMemoryStore()
	session, err := store.CreateSession(ctx, kiagent.CreateSessionInput{Title: question, Provider: "openai-compatible", Model: spec.Model})
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.CreateRun(ctx, session.ID, kiagent.CreateRunInput{Input: question, Provider: "openai-compatible", Model: spec.Model})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, runErr := runner.Run(ctx, kiagent.EinoRunInput{
		Messages: []kiagent.Message{{Role: kiagent.RoleUser, Content: question}},
		Store:    store,
		RunID:    run.ID,
	})
	events, err := store.ListRunEvents(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	report := realPromptEvalReport{
		Model:         spec.Name,
		Mode:          mode,
		Question:      question,
		DurationMS:    time.Since(start).Milliseconds(),
		ToolCalls:     realPromptEvalToolCalls(events),
		Citations:     realPromptEvalCountEvents(events, kiagent.EventCitation),
		ArtifactKinds: realPromptEvalArtifactKinds(events),
		FinalAnswer:   realPromptEvalFinalAnswer(events),
	}
	report.ToolCallCount = len(report.ToolCalls)
	if runErr != nil {
		report.Error = runErr.Error()
	}
	return report
}

func realPromptEvalTools(t *testing.T, ctx context.Context, dbPath string) []tool.BaseTool {
	t.Helper()
	srv, err := insightmcp.NewServer(insightmcp.ServerOptions{DBPath: dbPath})
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(srv.StreamableHTTPHandler(30 * time.Minute))
	t.Cleanup(httpServer.Close)
	cli, err := markclient.NewStreamableHttpClient(httpServer.URL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cli.Close() })
	if err := cli.Start(ctx); err != nil {
		t.Fatal(err)
	}
	init := markmcp.InitializeRequest{}
	init.Params.ProtocolVersion = markmcp.LATEST_PROTOCOL_VERSION
	init.Params.ClientInfo = markmcp.Implementation{Name: "kube-insight-real-prompt-eval", Version: "0.0.1"}
	if _, err := cli.Initialize(ctx, init); err != nil {
		t.Fatal(err)
	}
	tools, err := einomcp.GetTools(ctx, &einomcp.Config{Cli: cli})
	if err != nil {
		t.Fatal(err)
	}
	return tools
}

func realPromptEvalInstruction(mode, richContext string) string {
	instruction := kiagent.DefaultAgentInstruction()
	if mode != "rich" || strings.TrimSpace(richContext) == "" {
		return instruction
	}
	return instruction + `

Runtime kube-insight context:
- The following context is a cache of already-collected kube-insight evidence for this run.
- Use it to choose the smallest useful tool plan and avoid rediscovering obvious cluster scope, resource coverage, and high-value object names.
- Treat it as routing and orientation data, not proof. Current-state claims still need evidence from kube-insight tools, and final answers must cite tool-backed artifacts.

` + richContext
}

func realPromptEvalContext(t *testing.T) string {
	t.Helper()
	path := strings.TrimSpace(os.Getenv("KUBE_INSIGHT_AGENT_REAL_EVAL_CONTEXT_FILE"))
	if path == "" {
		return strings.TrimSpace(os.Getenv("KUBE_INSIGHT_AGENT_REAL_EVAL_CONTEXT"))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read context file: %v", err)
	}
	return strings.TrimSpace(string(data))
}

func realPromptEvalQuestions() []string {
	raw := strings.TrimSpace(os.Getenv("KUBE_INSIGHT_AGENT_REAL_EVAL_QUESTIONS"))
	parts := strings.Split(raw, ";;")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func realPromptEvalModelSpecsFromEnv() ([]realPromptEvalModelSpec, error) {
	raw := strings.TrimSpace(os.Getenv("KUBE_INSIGHT_AGENT_LIVE_EVAL_MODELS"))
	if raw == "" {
		model := strings.TrimSpace(os.Getenv("KUBE_INSIGHT_AGENT_LIVE_EVAL_MODEL"))
		if model == "" {
			model = strings.TrimSpace(os.Getenv("MODEL"))
		}
		if model == "" {
			model = "gpt-5.2"
		}
		raw = fmt.Sprintf("default|%s|OPENAI_API_KEY|OPENAI_BASE_URL", model)
	}
	parts := strings.Split(raw, ";")
	specs := make([]realPromptEvalModelSpec, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		fields := strings.Split(part, "|")
		if len(fields) < 2 || len(fields) > 4 {
			return nil, fmt.Errorf("invalid model spec %q: expected name|model|api_key_env|base_url_env", part)
		}
		spec := realPromptEvalModelSpec{Name: strings.TrimSpace(fields[0]), Model: strings.TrimSpace(fields[1]), APIKeyEnv: "OPENAI_API_KEY", BaseURLEnv: "OPENAI_BASE_URL"}
		if len(fields) >= 3 && strings.TrimSpace(fields[2]) != "" {
			spec.APIKeyEnv = strings.TrimSpace(fields[2])
		}
		if len(fields) == 4 && strings.TrimSpace(fields[3]) != "" {
			spec.BaseURLEnv = strings.TrimSpace(fields[3])
		}
		if spec.Name == "" || spec.Model == "" {
			return nil, fmt.Errorf("invalid model spec %q: name and model are required", part)
		}
		if os.Getenv(spec.APIKeyEnv) == "" {
			return nil, fmt.Errorf("model spec %q requires %s to be set", spec.Name, spec.APIKeyEnv)
		}
		specs = append(specs, spec)
	}
	return specs, nil
}

func realPromptEvalModes(richContext string) []string {
	raw := strings.TrimSpace(os.Getenv("KUBE_INSIGHT_AGENT_REAL_EVAL_MODES"))
	if raw == "" {
		if strings.TrimSpace(richContext) == "" {
			return []string{"baseline"}
		}
		return []string{"baseline", "rich"}
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if part == "rich" && strings.TrimSpace(richContext) == "" {
			continue
		}
		out = append(out, part)
	}
	return out
}

func realPromptEvalMaxIterations() int {
	raw := strings.TrimSpace(os.Getenv("KUBE_INSIGHT_AGENT_LIVE_EVAL_MAX_ITERATIONS"))
	if raw == "" {
		return 12
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed <= 0 {
		return 12
	}
	return parsed
}

func realPromptEvalTimeout() time.Duration {
	raw := strings.TrimSpace(os.Getenv("KUBE_INSIGHT_AGENT_LIVE_EVAL_TIMEOUT"))
	if raw == "" {
		return 3 * time.Minute
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil || parsed <= 0 {
		return 3 * time.Minute
	}
	return parsed
}

func realPromptEvalCaseName(question string) string {
	name := strings.ToLower(question)
	replacer := strings.NewReplacer("/", "-", " ", "-", "?", "", ":", "", ",", "", ".", "", "\n", "-")
	name = replacer.Replace(name)
	if len(name) > 64 {
		name = name[:64]
	}
	name = strings.Trim(name, "-")
	if name == "" {
		return "question"
	}
	return name
}

func writeRealPromptEvalReports(t *testing.T, reports []realPromptEvalReport) {
	t.Helper()
	outputDir := strings.TrimSpace(os.Getenv("KUBE_INSIGHT_AGENT_LIVE_EVAL_OUTPUT"))
	if outputDir == "" {
		return
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		t.Fatalf("create live eval output dir: %v", err)
	}
	data, err := json.MarshalIndent(reports, "", "  ")
	if err != nil {
		t.Fatalf("marshal real prompt eval reports: %v", err)
	}
	path := filepath.Join(outputDir, "real-prompt-context-report.json")
	if err := os.WriteFile(path, append(data, byte(10)), 0o644); err != nil {
		t.Fatalf("write real prompt eval report: %v", err)
	}
	t.Logf("wrote real prompt eval report: %s", path)
}

func realPromptEvalCountEvents(events []kiagent.RunEvent, eventType kiagent.RunEventType) int {
	count := 0
	for _, event := range events {
		if event.Type == eventType {
			count++
		}
	}
	return count
}

func realPromptEvalToolCalls(events []kiagent.RunEvent) []kiagent.EvaluationToolCall {
	callsByID := map[string]kiagent.EvaluationToolCall{}
	order := []string{}
	for _, event := range events {
		if event.Type != kiagent.EventToolAudit && event.Type != kiagent.EventToolCompleted && event.Type != kiagent.EventToolFailed {
			continue
		}
		var data kiagent.ToolCallEventData
		if event.Type == kiagent.EventToolAudit {
			var audit kiagent.ToolAuditEventData
			if err := json.Unmarshal(event.Data, &audit); err != nil {
				continue
			}
			data = kiagent.ToolCallEventData{ToolCallID: audit.ToolCallID, Name: audit.Name, Status: audit.Status, DurationMS: audit.DurationMS, Error: audit.Error}
		} else if err := json.Unmarshal(event.Data, &data); err != nil {
			continue
		}
		key := data.ToolCallID
		if key == "" {
			key = fmt.Sprintf("%s-%d", data.Name, len(order)+1)
		}
		if _, ok := callsByID[key]; !ok {
			order = append(order, key)
		}
		callsByID[key] = kiagent.EvaluationToolCall{ID: data.ToolCallID, Name: data.Name, Status: data.Status, DurationMS: data.DurationMS, Error: data.Error}
	}
	calls := make([]kiagent.EvaluationToolCall, 0, len(order))
	for _, key := range order {
		calls = append(calls, callsByID[key])
	}
	return calls
}

func realPromptEvalArtifactKinds(events []kiagent.RunEvent) []string {
	seen := map[string]bool{}
	kinds := []string{}
	for _, event := range events {
		if event.Type != kiagent.EventArtifact && event.Type != kiagent.EventArtifactUpdate {
			continue
		}
		var data kiagent.ArtifactEventData
		if err := json.Unmarshal(event.Data, &data); err != nil || data.Artifact.Kind == "" {
			continue
		}
		if !seen[data.Artifact.Kind] {
			seen[data.Artifact.Kind] = true
			kinds = append(kinds, data.Artifact.Kind)
		}
	}
	sort.Strings(kinds)
	return kinds
}

func realPromptEvalFinalAnswer(events []kiagent.RunEvent) string {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type != kiagent.EventFinalAnswer && events[i].Type != kiagent.EventMessageDone {
			continue
		}
		var data kiagent.MessageEventData
		if err := json.Unmarshal(events[i].Data, &data); err == nil && data.Content != "" {
			return data.Content
		}
	}
	return ""
}
