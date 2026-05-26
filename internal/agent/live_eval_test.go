package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	openaimodel "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

const liveEvalEnabledEnv = "KUBE_INSIGHT_AGENT_LIVE_EVAL"

func TestLiveLLMEvaluation(t *testing.T) {
	if os.Getenv(liveEvalEnabledEnv) != "1" {
		t.Skipf("set %s=1 to run live LLM agent evaluation", liveEvalEnabledEnv)
	}
	specs, err := liveEvalModelSpecsFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) == 0 {
		t.Fatal("no live evaluation model specs configured")
	}

	baseCtx := context.Background()

	allReports := []liveEvalReport{}
	for _, spec := range specs {
		t.Run(spec.Name, func(t *testing.T) {
			model, err := openaimodel.NewChatModel(baseCtx, &openaimodel.ChatModelConfig{
				APIKey:  os.Getenv(spec.APIKeyEnv),
				BaseURL: os.Getenv(spec.BaseURLEnv),
				Model:   spec.Model,
				Timeout: liveEvalTimeout(),
			})
			if err != nil {
				t.Fatal(err)
			}
			runner, err := NewEinoRunner(baseCtx, EinoRunnerConfig{
				Description:        "Kubernetes investigation assistant live evaluation runner.",
				Model:              model,
				Tools:              liveEvalTools(),
				EmitInternalEvents: true,
				EnableStreaming:    true,
				MaxIterations:      liveEvalMaxIterations(),
			})
			if err != nil {
				t.Fatal(err)
			}
			for _, tc := range liveEvalCases(t) {
				t.Run(tc.ID, func(t *testing.T) {
					caseCtx, cancel := context.WithTimeout(baseCtx, liveEvalTimeout())
					defer cancel()
					store := NewMemoryStore()
					session, err := store.CreateSession(caseCtx, CreateSessionInput{Title: tc.Question, Provider: "openai-compatible", Model: spec.Model})
					if err != nil {
						t.Fatal(err)
					}
					run, err := store.CreateRun(caseCtx, session.ID, CreateRunInput{Input: tc.Question, Provider: "openai-compatible", Model: spec.Model})
					if err != nil {
						t.Fatal(err)
					}
					_, runErr := runner.Run(caseCtx, EinoRunInput{
						Messages: []Message{{Role: RoleUser, Content: tc.Question}},
						Store:    store,
						RunID:    run.ID,
					})
					events, err := store.ListRunEvents(context.Background(), run.ID)
					if err != nil {
						t.Fatal(err)
					}
					report := EvaluateRunEvents(tc, events)
					if runErr != nil {
						report.Passed = false
						report.addCheck("runner", false, runErr.Error())
					}
					allReports = append(allReports, liveEvalReport{Model: spec.Name, EvaluationReport: report})
					writeLiveEvalReports(t, allReports)
					if runErr != nil {
						t.Errorf("runner failed for model %s case %s: %v", spec.Name, tc.ID, runErr)
					}
					if !report.Passed {
						t.Errorf("live eval failed for model %s case %s: %+v", spec.Name, tc.ID, report)
					}
				})
			}
		})
	}
	writeLiveEvalReports(t, allReports)
}

type liveEvalModelSpec struct {
	Name       string
	Model      string
	APIKeyEnv  string
	BaseURLEnv string
}

type liveEvalReport struct {
	Model string `json:"model"`
	EvaluationReport
}

func liveEvalModelSpecsFromEnv() ([]liveEvalModelSpec, error) {
	raw := strings.TrimSpace(os.Getenv("KUBE_INSIGHT_AGENT_LIVE_EVAL_MODELS"))
	if raw == "" {
		model := strings.TrimSpace(os.Getenv("KUBE_INSIGHT_AGENT_LIVE_EVAL_MODEL"))
		if model == "" {
			model = "gpt-5.2"
		}
		raw = fmt.Sprintf("default|%s|OPENAI_API_KEY|OPENAI_BASE_URL", model)
	}
	parts := strings.Split(raw, ";")
	specs := make([]liveEvalModelSpec, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		fields := strings.Split(part, "|")
		if len(fields) < 2 || len(fields) > 4 {
			return nil, fmt.Errorf("invalid model spec %q: expected name|model|api_key_env|base_url_env", part)
		}
		spec := liveEvalModelSpec{
			Name:       strings.TrimSpace(fields[0]),
			Model:      strings.TrimSpace(fields[1]),
			APIKeyEnv:  "OPENAI_API_KEY",
			BaseURLEnv: "OPENAI_BASE_URL",
		}
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

func liveEvalCases(t *testing.T) []EvaluationCase {
	t.Helper()
	cases := DefaultEvaluationCases()
	raw := strings.TrimSpace(os.Getenv("KUBE_INSIGHT_AGENT_LIVE_EVAL_CASES"))
	if raw == "" {
		return cases
	}
	wanted := map[string]bool{}
	for _, part := range strings.Split(raw, ",") {
		id := strings.TrimSpace(part)
		if id != "" {
			wanted[id] = true
		}
	}
	if len(wanted) == 0 {
		return cases
	}
	filtered := make([]EvaluationCase, 0, len(wanted))
	for _, tc := range cases {
		if wanted[tc.ID] {
			filtered = append(filtered, tc)
			delete(wanted, tc.ID)
		}
	}
	if len(wanted) > 0 {
		missing := make([]string, 0, len(wanted))
		for id := range wanted {
			missing = append(missing, id)
		}
		t.Fatalf("unknown live eval case ids: %s", strings.Join(missing, ", "))
	}
	return filtered
}

func liveEvalMaxIterations() int {
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

func liveEvalTimeout() time.Duration {
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

func writeLiveEvalReports(t *testing.T, reports []liveEvalReport) {
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
		t.Fatalf("marshal live eval reports: %v", err)
	}
	path := filepath.Join(outputDir, "report.json")
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		t.Fatalf("write live eval report: %v", err)
	}
	t.Logf("wrote live eval report: %s", path)
}

func liveEvalTools() []tool.BaseTool {
	sqlTool := liveEvalSQLTool{}
	tools := []tool.BaseTool{
		liveEvalTool{
			name: "kube_insight_health",
			desc: "Check collector and current health before current-state claims. Use this first for health questions. Returns compact resource coverage and freshness.",
			output: map[string]any{
				"summary": map[string]any{"status": "watching", "problems": 0, "resources": 4},
				"resources": []any{
					map[string]any{"kind": "Service", "resource": "services", "status": "watching", "clusterId": "eval-cluster"},
					map[string]any{"kind": "Pod", "resource": "pods", "status": "watching", "clusterId": "eval-cluster"},
					map[string]any{"kind": "Node", "resource": "nodes", "status": "watching", "clusterId": "eval-cluster"},
					map[string]any{"kind": "EndpointSlice", "resource": "endpointslices", "status": "watching", "clusterId": "eval-cluster"},
				},
			},
		},
		liveEvalTool{
			name: "kube_insight_search",
			desc: "Discover candidate Kubernetes objects from names, symptoms, facts, changes, events, labels, and namespaces. Use this before topology or history when the target is broad or symptom-based. Do not use search when the user already gave an exact kind plus namespace/name target; use the target-specific typed tool directly.",
			params: map[string]*schema.ParameterInfo{
				"query":     {Type: schema.String, Desc: "Search text such as api, OOMKilled, restart, change, topology, or default namespace."},
				"namespace": {Type: schema.String, Desc: "Optional namespace."},
				"kind":      {Type: schema.String, Desc: "Optional Kubernetes kind."},
			},
			output: map[string]any{
				"input":   map[string]any{"query": "eval evidence"},
				"summary": map[string]any{"matches": 3},
				"bundles": []any{
					map[string]any{"object": liveEvalObject("Service", "default", "api"), "summary": map[string]any{"facts": 2, "edges": 3, "changes": 1, "versions": 2, "evidenceScore": 9}},
					map[string]any{"object": liveEvalObject("Pod", "default", "api-0"), "summary": map[string]any{"facts": 3, "edges": 2, "changes": 2, "versions": 3, "evidenceScore": 10}, "facts": []any{map[string]any{"factKey": "pod_status.last_reason", "factValue": "OOMKilled"}}},
					map[string]any{"object": liveEvalObject("EndpointSlice", "default", "api-abc"), "summary": map[string]any{"facts": 1, "edges": 2, "versions": 1, "evidenceScore": 7}},
				},
			},
		},
		liveEvalTool{
			name: "kube_insight_history",
			desc: "Load retained versions and changes after a target object is known. Use this for recent changes, OOMKilled/restart proof, and history diffs. In this eval, one history call is enough when it returns the relevant changes; answer instead of checking related objects.",
			params: map[string]*schema.ParameterInfo{
				"kind":      {Type: schema.String, Required: true, Desc: "Target kind."},
				"namespace": {Type: schema.String, Desc: "Target namespace."},
				"name":      {Type: schema.String, Required: true, Desc: "Target object name."},
				"diffs":     {Type: schema.Boolean, Desc: "Include retained version diffs when useful."},
			},
			output: map[string]any{
				"object": liveEvalObject("Pod", "default", "api-0"),
				"versions": []any{
					map[string]any{"versionId": "ver-api-0-1", "observedAt": "2026-05-24T10:00:00Z", "changeSummary": "ready pod"},
					map[string]any{"versionId": "ver-api-0-2", "observedAt": "2026-05-24T10:05:00Z", "changeSummary": "container api lastState reason OOMKilled; restartCount 1"},
				},
				"diffs": []any{
					map[string]any{"fromVersionId": "ver-api-0-1", "toVersionId": "ver-api-0-2", "path": "status.containerStatuses.api.lastState.reason", "before": "", "after": "OOMKilled"},
					map[string]any{"fromVersionId": "ver-api-0-1", "toVersionId": "ver-api-0-2", "path": "status.containerStatuses.api.restartCount", "before": 0, "after": 1},
				},
				"changes": []any{
					map[string]any{"changeId": "chg-api-0-oom", "path": "status.containerStatuses.api.lastState.reason", "after": "OOMKilled", "observedAt": "2026-05-24T10:05:00Z"},
					map[string]any{"changeId": "chg-default-api", "path": "metadata.resourceVersion", "after": "8123", "observedAt": "2026-05-24T10:06:00Z"},
				},
			},
		},
		liveEvalTool{
			name: "kube_insight_topology",
			desc: "Load retained topology around a known object. Use after search identifies Service, EndpointSlice, Pod, Deployment, or namespace candidates. One call around the best root is enough for this eval topology map; do not call topology repeatedly for every returned node unless the graph is incomplete.",
			params: map[string]*schema.ParameterInfo{
				"kind":      {Type: schema.String, Required: true, Desc: "Root object kind."},
				"namespace": {Type: schema.String, Desc: "Root namespace."},
				"name":      {Type: schema.String, Required: true, Desc: "Root object name."},
			},
			output: liveEvalTopologyOutput(),
		},
		liveEvalTool{
			name: "kube_insight_service_investigation",
			desc: "Load compact Service evidence for an exact Service namespace/name. Use this for Service health after health confirms coverage. In this eval, health plus service investigation is complete terminal evidence; after this tool returns, answer immediately and do not call search, schema, SQL, history, or topology unless this bundle is missing Service, EndpointSlice, Pod, or Event evidence. Do not use this tool just to answer recent-changes questions when search and history already returned changes.",
			params: map[string]*schema.ParameterInfo{
				"namespace": {Type: schema.String, Required: true, Desc: "Service namespace."},
				"name":      {Type: schema.String, Required: true, Desc: "Service name."},
			},
			output: map[string]any{
				"summary": map[string]any{"completeForServiceHealth": true, "endpointSlices": 1, "pods": 1, "readyEndpointPods": 0, "terminalReason": "Service api has one endpoint Pod api-0 and the bundle includes endpoint and pod evidence. Answer from this bundle without extra tools."},
				"service": map[string]any{"object": liveEvalObject("Service", "default", "api"), "summary": map[string]any{"facts": 2, "edges": 3, "versions": 2, "evidenceScore": 9}, "facts": []any{map[string]any{"factKey": "service.type", "factValue": "ClusterIP"}}},
				"objects": []any{
					map[string]any{"object": liveEvalObject("EndpointSlice", "default", "api-abc"), "summary": map[string]any{"facts": 2, "edges": 2, "versions": 1}, "facts": []any{map[string]any{"factKey": "endpoints.ready", "factValue": "false"}}},
					map[string]any{"object": liveEvalObject("Pod", "default", "api-0"), "summary": map[string]any{"facts": 3, "edges": 2, "versions": 3}, "facts": []any{map[string]any{"factKey": "pod_status.last_reason", "factValue": "OOMKilled"}, map[string]any{"factKey": "pod_status.restart_count", "factValue": "1"}}},
				},
				"topology": liveEvalTopologyEdges(),
			},
		},
		liveEvalTool{
			name: "kube_insight_schema",
			desc: "Return compact schema DSL before SQL. Use this before kube_insight_sql. Do not call schema when health, search, history, topology, or service investigation already provides enough evidence.",
			output: map[string]any{
				"backend": "sqlite",
				"tables":  []any{"objects", "facts", "edges", "changes", "versions", "latest_index"},
				"recipes": []any{"Query facts where fact_key = 'pod_status.last_reason' and fact_value = 'OOMKilled'.", "For allocation/configuration follow-ups, profile available fact keys before querying request/limit rows.", "For Node capacity, query node_capacity.cpu and node_capacity.memory facts, first taking the latest numeric_value per cluster_id/name/fact_key before summing."},
			},
		},
		sqlTool,
	}
	tools = append(tools, WrapRecoverableToolErrors([]tool.BaseTool{NewScriptedQueryTool(sqlTool), NewJSTransformTool()})...)
	return tools
}

type liveEvalSQLTool struct{}

func (t liveEvalSQLTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "kube_insight_sql",
		Desc: "Run bounded read-only SQL after calling kube_insight_schema. Use only for aggregates or proof rows that typed tools cannot already provide. Terminal rule: when SQL returns rows for OOM ranking, allocation requests/limits, or exact recent changes, answer from those rows immediately; do not call search, history, topology, or more SQL unless rows are empty or the user explicitly asks for root cause/impact/raw proof. If the user asked to use artifact_transform_js, run only one proof SQL query before the transform. Do not use SQL to re-confirm facts, changes, versions, or topology already returned by typed tools.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"sql":     {Type: schema.String, Required: true, Desc: "Read-only SQL."},
			"maxRows": {Type: schema.Integer, Desc: "Maximum rows."},
		}),
	}, nil
}

func (t liveEvalSQLTool) InvokableRun(_ context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	var args struct {
		SQL string `json:"sql"`
	}
	_ = json.Unmarshal([]byte(argumentsInJSON), &args)
	query := strings.ToLower(args.SQL)
	switch {
	case strings.Contains(query, "node_capacity") || (strings.Contains(query, "node") && strings.Contains(query, "capacity")):
		return string(jsonRaw(map[string]any{
			"columns": []any{"cluster_id", "node_count", "total_capacity_cpu_cores", "total_capacity_memory_bytes", "last_seen"},
			"rows": []any{
				map[string]any{"cluster_id": "eval-cluster", "node_count": 3, "total_capacity_cpu_cores": 24, "total_capacity_memory_bytes": 103079215104, "last_seen": "2026-05-24T10:10:00Z"},
			},
			"rowIds": []any{"sqlrow-node-capacity"},
		})), nil
	case strings.Contains(query, "fact_key") && strings.Contains(query, "node"):
		return string(jsonRaw(map[string]any{
			"columns": []any{"kind", "fact_key", "sample_value"},
			"rows": []any{
				map[string]any{"kind": "Node", "fact_key": "node_capacity.cpu", "sample_value": "8"},
				map[string]any{"kind": "Node", "fact_key": "node_capacity.memory", "sample_value": "34359738368"},
				map[string]any{"kind": "Node", "fact_key": "node_allocatable.cpu", "sample_value": "7900m"},
				map[string]any{"kind": "Node", "fact_key": "node_allocatable.memory", "sample_value": "31820896Ki"},
			},
			"rowIds": []any{"sqlrow-node-key-profile"},
		})), nil
	case strings.Contains(query, "oom") || strings.Contains(query, "oomkilled") || strings.Contains(query, "last_reason"):
		return string(jsonRaw(map[string]any{
			"columns": []any{"kind", "namespace", "name", "fact_key", "fact_value", "fact_count", "last_seen"},
			"rows": []any{
				[]any{"Pod", "default", "api-0", "pod_status.last_reason", "OOMKilled", 1, "2026-05-24T10:05:00Z"},
			},
			"rowIds": []any{"sqlrow-oom-1"},
		})), nil
	case strings.Contains(query, "changes") || strings.Contains(query, "change_family") || strings.Contains(query, "deployment"):
		return string(jsonRaw(map[string]any{
			"columns": []any{"kind", "namespace", "name", "change_family", "path", "changes", "first_seen", "last_seen", "sample_old", "sample_new"},
			"rows": []any{
				[]any{"Deployment", "default", "api", "spec", "spec.template.spec.containers[api].resources.requests.memory", 1, "2026-05-24T10:05:00Z", "2026-05-24T10:05:00Z", "128Mi", "256Mi"},
				[]any{"Deployment", "default", "api", "spec", "spec.template.spec.containers[api].resources.limits.memory", 1, "2026-05-24T10:06:00Z", "2026-05-24T10:06:00Z", "256Mi", "512Mi"},
			},
			"rowIds": []any{"sqlrow-change-request-memory", "sqlrow-change-limit-memory"},
		})), nil
	case strings.Contains(query, "request") || strings.Contains(query, "limit") || strings.Contains(query, "allocation") || strings.Contains(query, "resource"):
		return string(jsonRaw(map[string]any{
			"columns": []any{"kind", "namespace", "name", "fact_key", "fact_value"},
			"rows": []any{
				[]any{"Deployment", "default", "api", "container.resources.requests.memory", "256Mi"},
				[]any{"Deployment", "default", "api", "container.resources.limits.memory", "512Mi"},
			},
			"rowIds": []any{"sqlrow-alloc-requests", "sqlrow-alloc-limits"},
		})), nil
	default:
		return string(jsonRaw(map[string]any{
			"columns": []any{"kind", "namespace", "name", "fact_key", "fact_value"},
			"rows": []any{
				[]any{"Pod", "default", "api-0", "pod_status.last_reason", "OOMKilled"},
				[]any{"Deployment", "default", "api", "container.resources.requests.memory", "256Mi"},
				[]any{"Deployment", "default", "api", "container.resources.limits.memory", "512Mi"},
			},
			"rowIds": []any{"sqlrow-oom-1", "sqlrow-alloc-requests", "sqlrow-alloc-limits"},
		})), nil
	}
}

type liveEvalTool struct {
	name   string
	desc   string
	params map[string]*schema.ParameterInfo
	output any
}

func (t liveEvalTool) Info(context.Context) (*schema.ToolInfo, error) {
	info := &schema.ToolInfo{Name: t.name, Desc: t.desc}
	if len(t.params) > 0 {
		info.ParamsOneOf = schema.NewParamsOneOfByParams(t.params)
	}
	return info, nil
}

func (t liveEvalTool) InvokableRun(context.Context, string, ...tool.Option) (string, error) {
	return string(jsonRaw(t.output)), nil
}

func liveEvalObject(kind, namespace, name string) map[string]any {
	return map[string]any{
		"clusterId": "eval-cluster",
		"group":     "",
		"version":   "v1",
		"resource":  strings.ToLower(kind) + "s",
		"kind":      kind,
		"namespace": namespace,
		"name":      name,
		"uid":       "uid-" + strings.ToLower(kind) + "-" + name,
	}
}

func liveEvalTopologyOutput() map[string]any {
	return map[string]any{
		"root":  liveEvalObject("Service", "default", "api"),
		"nodes": []any{liveEvalObject("Service", "default", "api"), liveEvalObject("EndpointSlice", "default", "api-abc"), liveEvalObject("Pod", "default", "api-0")},
		"edges": liveEvalTopologyEdges(),
	}
}

func liveEvalTopologyEdges() []any {
	return []any{
		map[string]any{"type": "service_selects_endpointslice", "src": liveEvalObject("Service", "default", "api"), "dst": liveEvalObject("EndpointSlice", "default", "api-abc"), "validFrom": "2026-05-24T10:00:00Z"},
		map[string]any{"type": "endpointslice_targets_pod", "src": liveEvalObject("EndpointSlice", "default", "api-abc"), "dst": liveEvalObject("Pod", "default", "api-0"), "validFrom": "2026-05-24T10:00:00Z"},
	}
}
