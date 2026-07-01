package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	kiagent "kube-insight/internal/agent"
)

const jsToolName = "kube_insight_js"

type mcpSQLTool struct {
	server *Server
}

func (t mcpSQLTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: "kube_insight_sql", Desc: "Run bounded read-only kube-insight SQL."}, nil
}

func (t mcpSQLTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	var args sqlArguments
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return "", fmt.Errorf("invalid sql arguments: %w", err)
	}
	value, err := t.server.querySQL(ctx, args)
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (s *Server) queryJS(ctx context.Context, raw json.RawMessage) (string, error) {
	jsTool, ok := kiagent.NewJSInterpreterTool(mcpSQLTool{server: s}).(tool.InvokableTool)
	if !ok {
		return "", fmt.Errorf("%s is not invokable", jsToolName)
	}
	return jsTool.InvokableRun(ctx, string(raw))
}

func jsToolSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"script": map[string]any{
				"type":        "string",
				"description": "JavaScript statements. Available helpers include input, inputRows, rows(value), json(value), console.log, print, ki/_ data helpers, scratch.write/load/read/list, and synchronous sql(query, maxRows) and sqlAll([{name, sql, maxRows}]). Call kube_insight_health and kube_insight_schema before using SQL-shaped JS for current-state, historical, ranking, aggregation, or absence claims. Do not use await, network, filesystem, process, environment, Date arithmetic for relative-time now, or const sql = ... because that shadows the sql helper.",
			},
			"input": map[string]any{
				"type":        "object",
				"description": "Optional bounded JSON object from prior tool output. The script can read it as input; inputRows aliases input.rows or input when it is an array.",
			},
			"timeoutMillis": map[string]any{
				"type":        "integer",
				"description": "Optional timeout in milliseconds. Defaults to 3000 and is capped at 8000.",
			},
			"maxOutputBytes": map[string]any{
				"type":        "integer",
				"description": "Optional maximum JSON output size. Defaults to 131072, has a 16384 minimum, and is capped at 524288.",
			},
			"maxQueries": map[string]any{
				"type":        "integer",
				"description": "Optional maximum SQL calls allowed from the script. Defaults to 6 and is hard-capped at 10.",
			},
			"defaultMaxRows": map[string]any{
				"type":        "integer",
				"description": "Optional default maxRows for sql calls. Defaults to 100 and is capped at 10000.",
			},
		},
		"required": []string{"script"},
	}
}

func jsToolDescription() string {
	return "Run kube-insight's bounded JavaScript interpreter for investigation aggregation, JSON transformation, and schema-guided read-only SQL via synchronous sql(query, maxRows) or sqlAll([{name, sql, maxRows}]). Use after kube_insight_health and kube_insight_schema for code-shaped tasks such as dependent profile/proof queries, latest-per-object selection, JSON field extraction, Kubernetes unit normalization, time bucketing, top-N ranking, start/end snapshot comparison, Pod-count peak reconstruction, PVC resize history, or compact answer-ready summarization. Do not use it merely to reformat previous rows. Scripts have no filesystem, network, process, or environment access; SQL calls are bounded and read-only."
}
