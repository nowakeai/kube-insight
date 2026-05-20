package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	"kube-insight/internal/storage"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

const SQLToolName = "kube_insight_sql"

const defaultSQLToolMaxRows = 200
const maxSQLToolMaxRows = 1000

type SQLTool struct {
	store storage.SQLQueryStore
}

type sqlToolArgs struct {
	SQL     string `json:"sql,omitempty"`
	MaxRows int    `json:"maxRows,omitempty"`
}

var _ tool.InvokableTool = (*SQLTool)(nil)

func NewSQLTool(store storage.SQLQueryStore) *SQLTool {
	return &SQLTool{store: store}
}

func (t *SQLTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: SQLToolName,
		Desc: "Run guarded read-only SQL against kube-insight evidence tables for advanced joins and proof queries. Use only SELECT, WITH, or EXPLAIN, prefer indexed tables such as object_facts, object_edges, object_changes, latest_index, object_observations, and versions before scanning blobs." + toolCitationGuidance(),
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"sql":     {Type: schema.String, Desc: "Single read-only SELECT/WITH/EXPLAIN statement.", Required: true},
			"maxRows": {Type: schema.Integer, Desc: fmt.Sprintf("Maximum rows to return. Defaults to %d and caps at %d.", defaultSQLToolMaxRows, maxSQLToolMaxRows)},
		}),
	}, nil
}

func (t *SQLTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	if t == nil || t.store == nil {
		return "", fmt.Errorf("%s store is not configured", SQLToolName)
	}
	args, err := parseSQLToolArgs(argumentsInJSON)
	if err != nil {
		return "", err
	}
	if err := validateAgentReadOnlySQL(args.SQL); err != nil {
		return "", err
	}
	result, err := t.store.QuerySQL(ctx, storage.SQLQueryOptions{SQL: args.SQL, MaxRows: sqlToolMaxRows(args.MaxRows)})
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func parseSQLToolArgs(argumentsInJSON string) (sqlToolArgs, error) {
	var args sqlToolArgs
	if strings.TrimSpace(argumentsInJSON) == "" {
		return args, fmt.Errorf("%s requires a sql argument", SQLToolName)
	}
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return sqlToolArgs{}, fmt.Errorf("invalid %s arguments: %w", SQLToolName, err)
	}
	if strings.TrimSpace(args.SQL) == "" {
		return sqlToolArgs{}, fmt.Errorf("%s requires a sql argument", SQLToolName)
	}
	return args, nil
}

func sqlToolMaxRows(requested int) int {
	if requested <= 0 {
		return defaultSQLToolMaxRows
	}
	if requested > maxSQLToolMaxRows {
		return maxSQLToolMaxRows
	}
	return requested
}

func validateAgentReadOnlySQL(query string) error {
	trimmed := strings.TrimSpace(query)
	trimmed = strings.TrimSuffix(trimmed, ";")
	if strings.Contains(trimmed, ";") {
		return fmt.Errorf("%s allows only one SQL statement", SQLToolName)
	}
	tokens := agentSQLTokens(trimmed)
	if len(tokens) == 0 {
		return fmt.Errorf("%s requires a sql argument", SQLToolName)
	}
	switch tokens[0] {
	case "select", "with":
	case "explain":
		if len(tokens) < 2 || (tokens[1] != "select" && tokens[1] != "query") {
			return fmt.Errorf("%s allows only EXPLAIN SELECT or EXPLAIN QUERY PLAN SELECT", SQLToolName)
		}
	default:
		return fmt.Errorf("%s allows only read-only SELECT/WITH/EXPLAIN SQL, got %q", SQLToolName, tokens[0])
	}
	for _, token := range tokens {
		if forbiddenAgentSQLToken(token) {
			return fmt.Errorf("%s rejected forbidden token %q", SQLToolName, token)
		}
	}
	return nil
}

func agentSQLTokens(query string) []string {
	return strings.FieldsFunc(strings.ToLower(query), func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_')
	})
}

func forbiddenAgentSQLToken(token string) bool {
	switch token {
	case "insert", "update", "delete", "drop", "alter", "create", "replace", "truncate", "attach", "detach", "vacuum", "reindex", "pragma":
		return true
	default:
		return false
	}
}
