package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/dop251/goja"
)

const (
	scriptedQueryToolName          = "kube_insight_scripted_query"
	defaultScriptedQueryTimeout    = 3 * time.Second
	maxScriptedQueryTimeout        = 8 * time.Second
	defaultScriptedQueryOutputSize = 128 * 1024
	maxScriptedQueryOutputSize     = 512 * 1024
	maxScriptedQueryScriptSize     = 24 * 1024
	defaultScriptedQueryMaxQueries = 6
	maxScriptedQueryMaxQueries     = 10
	defaultScriptedQueryMaxRows    = 100
	maxScriptedQueryMaxRows        = 1000
)

type ScriptedQueryTool struct {
	sqlTool tool.InvokableTool
}

type scriptedQueryArguments struct {
	Script         string `json:"script"`
	TimeoutMillis  int    `json:"timeoutMillis,omitempty"`
	MaxOutputBytes int    `json:"maxOutputBytes,omitempty"`
	MaxQueries     int    `json:"maxQueries,omitempty"`
	DefaultMaxRows int    `json:"defaultMaxRows,omitempty"`
}

type scriptedQueryResult struct {
	Result  json.RawMessage    `json:"result,omitempty"`
	Queries []scriptedQueryLog `json:"queries,omitempty"`
	Logs    []string           `json:"logs,omitempty"`
	Error   string             `json:"error,omitempty"`
	Detail  map[string]any     `json:"detail,omitempty"`
}

type scriptedQueryLog struct {
	SQL       string  `json:"sql"`
	MaxRows   int     `json:"maxRows"`
	RowCount  int     `json:"rowCount,omitempty"`
	Truncated bool    `json:"truncated,omitempty"`
	ElapsedMS float64 `json:"elapsedMs,omitempty"`
}

func NewScriptedQueryTool(sqlTool tool.InvokableTool) tool.BaseTool {
	return ScriptedQueryTool{sqlTool: sqlTool}
}

func (t ScriptedQueryTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: scriptedQueryToolName,
		Desc: "Run bounded JavaScript that can call kube_insight_sql through sql(query, maxRows) and sqlAll([{name, sql, maxRows}]) for dependent or parallel read-only SQL investigation. Use after kube_insight_schema when one tool call should perform a small query plan, such as profile -> proof, several independent aggregates, or SQL rows plus compact grouping. The script has no filesystem, network, process, or environment access. Prefer returning compact JSON and answer from one successful scripted query instead of calling repeated SQL plus artifact_transform_js. When the final answer uses this result, add a nearby evidence label such as {{evidence: Node capacity facts}} so the server can verify citations.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"script": {
				Type:     schema.String,
				Required: true,
				Desc:     "JavaScript statements. Available helpers: sql(query, maxRows), sqlAll([{name, sql, maxRows}]), rows(result), ki/_ helpers groupBy/countBy/sumBy/sortBy/uniqBy/pick, input logs via console.log. Return JSON-serializable data.",
			},
			"timeoutMillis": {
				Type: schema.Integer,
				Desc: "Optional timeout in milliseconds. Defaults to 3000 and is capped at 8000.",
			},
			"maxOutputBytes": {
				Type: schema.Integer,
				Desc: "Optional maximum JSON output size. Defaults to 131072 and is capped at 524288.",
			},
			"maxQueries": {
				Type: schema.Integer,
				Desc: "Optional maximum SQL calls allowed from the script. Defaults to 6 and is capped at 10.",
			},
			"defaultMaxRows": {
				Type: schema.Integer,
				Desc: "Optional default maxRows for sql calls. Defaults to 100 and is capped at 1000.",
			},
		}),
	}, nil
}

func (t ScriptedQueryTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	if t.sqlTool == nil {
		return "", fmt.Errorf("%s requires a kube_insight_sql tool", scriptedQueryToolName)
	}
	var args scriptedQueryArguments
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return "", fmt.Errorf("parse %s arguments: %w", scriptedQueryToolName, err)
	}
	script := strings.TrimSpace(args.Script)
	if script == "" {
		return "", fmt.Errorf("%s requires a script", scriptedQueryToolName)
	}
	if len(script) > maxScriptedQueryScriptSize {
		return "", fmt.Errorf("%s script too large: %d bytes > %d", scriptedQueryToolName, len(script), maxScriptedQueryScriptSize)
	}
	timeout := boundedScriptedQueryTimeout(args.TimeoutMillis)
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	state := &scriptedQueryState{
		ctx:            runCtx,
		sqlTool:        t.sqlTool,
		maxQueries:     boundedScriptedQueryMaxQueries(args.MaxQueries),
		defaultMaxRows: boundedScriptedQueryMaxRows(args.DefaultMaxRows),
	}
	outputLimit := boundedScriptedQueryOutputLimit(args.MaxOutputBytes)
	vm := goja.New()
	logs := []string{}
	_ = vm.Set("console", map[string]any{
		"log": func(call goja.FunctionCall) goja.Value {
			logs = append(logs, compactText(jsLogArgs(call.Arguments), 500))
			return goja.Undefined()
		},
	})
	_ = vm.Set("print", func(call goja.FunctionCall) goja.Value {
		logs = append(logs, compactText(jsLogArgs(call.Arguments), 500))
		return goja.Undefined()
	})
	_ = vm.Set("sql", func(call goja.FunctionCall) goja.Value {
		result, err := state.sql(vm, call)
		if err != nil {
			panic(vm.ToValue(err.Error()))
		}
		return vm.ToValue(result)
	})
	_ = vm.Set("sqlAll", func(call goja.FunctionCall) goja.Value {
		result, err := state.sqlAll(vm, call)
		if err != nil {
			panic(vm.ToValue(err.Error()))
		}
		return vm.ToValue(result)
	})
	_ = vm.Set("rows", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return vm.ToValue([]any{})
		}
		return vm.ToValue(sqlRows(call.Arguments[0].Export()))
	})
	if err := installJSDataHelpers(vm); err != nil {
		return "", fmt.Errorf("%s failed to install helpers: %w", scriptedQueryToolName, err)
	}
	timer := time.AfterFunc(timeout, func() {
		vm.Interrupt("timeout")
	})
	defer timer.Stop()
	value, err := vm.RunString(wrapScriptedQueryScript(script))
	if err != nil {
		return "", fmt.Errorf("%s failed: %w", scriptedQueryToolName, err)
	}
	value, err = resolveScriptedQueryValue(value)
	if err != nil {
		return "", err
	}
	select {
	case <-runCtx.Done():
		return "", runCtx.Err()
	default:
	}
	resultData, err := json.Marshal(sanitizeJSExportForJSON(value.Export()))
	if err != nil {
		return "", fmt.Errorf("%s result is not JSON serializable: %w", scriptedQueryToolName, err)
	}
	out := scriptedQueryResult{Result: resultData, Queries: state.logs(), Logs: logs}
	encoded, err := json.Marshal(out)
	if err != nil {
		return "", err
	}
	if len(encoded) > outputLimit {
		return "", fmt.Errorf("%s output too large: %d bytes > %d", scriptedQueryToolName, len(encoded), outputLimit)
	}
	return string(encoded), nil
}

func wrapScriptedQueryScript(script string) string {
	return "\"use strict\";\n(async () => {\n" + script + "\n})()"
}

func resolveScriptedQueryValue(value goja.Value) (goja.Value, error) {
	if promise, ok := value.Export().(*goja.Promise); ok {
		switch promise.State() {
		case goja.PromiseStateFulfilled:
			return promise.Result(), nil
		case goja.PromiseStateRejected:
			return nil, fmt.Errorf("%s rejected: %v", scriptedQueryToolName, promise.Result())
		default:
			return nil, fmt.Errorf("%s returned a pending promise; asynchronous timers or external async operations are not supported", scriptedQueryToolName)
		}
	}
	return value, nil
}

type scriptedQueryState struct {
	ctx            context.Context
	sqlTool        tool.InvokableTool
	maxQueries     int
	defaultMaxRows int

	mu       sync.Mutex
	queries  int
	queryLog []scriptedQueryLog
}

func (s *scriptedQueryState) sql(vm *goja.Runtime, call goja.FunctionCall) (any, error) {
	if len(call.Arguments) == 0 {
		return nil, errors.New("sql(query, maxRows) requires a query string")
	}
	query := strings.TrimSpace(call.Arguments[0].String())
	if query == "" {
		return nil, errors.New("sql query is required")
	}
	maxRows := s.maxRowsFromValue(call.Argument(1))
	return s.runQuery(query, maxRows)
}

func (s *scriptedQueryState) sqlAll(vm *goja.Runtime, call goja.FunctionCall) (any, error) {
	if len(call.Arguments) == 0 {
		return nil, errors.New("sqlAll requires an array of query specs")
	}
	raw := call.Arguments[0].Export()
	items, ok := raw.([]any)
	if !ok {
		return nil, errors.New("sqlAll argument must be an array")
	}
	if len(items) == 0 {
		return []any{}, nil
	}
	if err := s.reserveQueries(len(items)); err != nil {
		return nil, err
	}
	results := make([]any, len(items))
	errs := make([]error, len(items))
	var wg sync.WaitGroup
	for i, item := range items {
		i, item := i, item
		wg.Add(1)
		go func() {
			defer wg.Done()
			spec, ok := item.(map[string]any)
			if !ok {
				errs[i] = fmt.Errorf("sqlAll item %d must be an object", i)
				return
			}
			query, _ := spec["sql"].(string)
			query = strings.TrimSpace(query)
			if query == "" {
				errs[i] = fmt.Errorf("sqlAll item %d missing sql", i)
				return
			}
			maxRows := s.maxRowsFromAny(spec["maxRows"])
			result, err := s.runReservedQuery(query, maxRows)
			if err != nil {
				errs[i] = err
				return
			}
			if name, _ := spec["name"].(string); name != "" {
				results[i] = map[string]any{"name": name, "result": result}
				return
			}
			results[i] = result
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	return results, nil
}

func (s *scriptedQueryState) runQuery(query string, maxRows int) (any, error) {
	if err := s.reserveQueries(1); err != nil {
		return nil, err
	}
	return s.runReservedQuery(query, maxRows)
}

func (s *scriptedQueryState) reserveQueries(count int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.queries+count > s.maxQueries {
		return fmt.Errorf("script query limit exceeded: %d requested > %d allowed", s.queries+count, s.maxQueries)
	}
	s.queries += count
	return nil
}

func (s *scriptedQueryState) runReservedQuery(query string, maxRows int) (any, error) {
	maxRows = boundedScriptedQueryMaxRows(maxRows)
	payload, err := json.Marshal(map[string]any{"sql": query, "maxRows": maxRows})
	if err != nil {
		return nil, err
	}
	start := time.Now()
	output, err := s.sqlTool.InvokableRun(s.ctx, string(payload))
	if err != nil {
		return nil, err
	}
	value, err := decodeSQLToolOutput(output)
	if err != nil {
		return nil, err
	}
	log := scriptedQueryLog{SQL: query, MaxRows: maxRows, ElapsedMS: float64(time.Since(start).Microseconds()) / 1000}
	if object, ok := value.(map[string]any); ok {
		log.RowCount = intFromAny(object["rowCount"])
		log.Truncated = boolFromAny(object["truncated"])
		if elapsed := floatFromAny(object["elapsedMs"]); elapsed > 0 {
			log.ElapsedMS = elapsed
		}
	}
	s.mu.Lock()
	s.queryLog = append(s.queryLog, log)
	s.mu.Unlock()
	return value, nil
}

func (s *scriptedQueryState) logs() []scriptedQueryLog {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]scriptedQueryLog(nil), s.queryLog...)
}

func (s *scriptedQueryState) maxRowsFromValue(value goja.Value) int {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return s.defaultMaxRows
	}
	return s.maxRowsFromAny(value.Export())
}

func (s *scriptedQueryState) maxRowsFromAny(value any) int {
	switch typed := value.(type) {
	case int:
		return boundedScriptedQueryMaxRows(typed)
	case int64:
		return boundedScriptedQueryMaxRows(int(typed))
	case float64:
		return boundedScriptedQueryMaxRows(int(typed))
	case map[string]any:
		return s.maxRowsFromAny(typed["maxRows"])
	default:
		return s.defaultMaxRows
	}
}

func decodeSQLToolOutput(output string) (any, error) {
	var value any
	if err := json.Unmarshal([]byte(output), &value); err != nil {
		return output, nil
	}
	if object, ok := value.(map[string]any); ok {
		if boolFromAny(object["isError"]) {
			return nil, fmt.Errorf("%v", scriptedQueryFirstNonNil(object["error"], object["message"], object["summary"], output))
		}
		if text, ok := firstToolText(object); ok {
			return decodeSQLToolOutput(text)
		}
	}
	return value, nil
}

func firstToolText(object map[string]any) (string, bool) {
	content, ok := object["content"].([]any)
	if !ok {
		return "", false
	}
	for _, item := range content {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if text, _ := entry["text"].(string); text != "" {
			return text, true
		}
	}
	return "", false
}

func sqlRows(value any) any {
	if object, ok := value.(map[string]any); ok {
		if rows, ok := object["rows"]; ok {
			return rows
		}
		if data, ok := object["data"]; ok {
			return data
		}
	}
	return []any{}
}

func boundedScriptedQueryTimeout(ms int) time.Duration {
	if ms <= 0 {
		return defaultScriptedQueryTimeout
	}
	timeout := time.Duration(ms) * time.Millisecond
	if timeout > maxScriptedQueryTimeout {
		return maxScriptedQueryTimeout
	}
	return timeout
}

func boundedScriptedQueryOutputLimit(limit int) int {
	if limit <= 0 {
		return defaultScriptedQueryOutputSize
	}
	if limit > maxScriptedQueryOutputSize {
		return maxScriptedQueryOutputSize
	}
	return limit
}

func boundedScriptedQueryMaxQueries(limit int) int {
	if limit <= 0 {
		return defaultScriptedQueryMaxQueries
	}
	if limit > maxScriptedQueryMaxQueries {
		return maxScriptedQueryMaxQueries
	}
	return limit
}

func boundedScriptedQueryMaxRows(limit int) int {
	if limit <= 0 {
		return defaultScriptedQueryMaxRows
	}
	if limit > maxScriptedQueryMaxRows {
		return maxScriptedQueryMaxRows
	}
	return limit
}

func intFromAny(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	default:
		return 0
	}
}

func floatFromAny(value any) float64 {
	switch typed := value.(type) {
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	case float64:
		return typed
	default:
		return 0
	}
}

func boolFromAny(value any) bool {
	typed, _ := value.(bool)
	return typed
}

func scriptedQueryFirstNonNil(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}
