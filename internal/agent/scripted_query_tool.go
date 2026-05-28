package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/dop251/goja"
)

const (
	jsInterpreterToolName          = "kube_insight_js"
	scriptedQueryToolName          = jsInterpreterToolName
	legacyScriptedQueryToolName    = "kube_insight_scripted_query"
	legacyJSTransformToolName      = "artifact_transform_js"
	defaultScriptedQueryTimeout    = 3 * time.Second
	minScriptedQueryOutputSize     = 16 * 1024
	maxScriptedQueryTimeout        = 8 * time.Second
	defaultScriptedQueryOutputSize = 128 * 1024
	maxScriptedQueryOutputSize     = 512 * 1024
	maxScriptedQueryScriptSize     = 24 * 1024
	defaultScriptedQueryMaxQueries = 6
	maxScriptedQueryMaxQueries     = 10
	defaultScriptedQueryMaxRows    = 100
	maxScriptedQueryMaxRows        = 10000
)

type ScriptedQueryTool struct {
	sqlTool tool.InvokableTool
}

type scriptedQueryArguments struct {
	Script         string          `json:"script"`
	Input          json.RawMessage `json:"input,omitempty"`
	TimeoutMillis  int             `json:"timeoutMillis,omitempty"`
	MaxOutputBytes int             `json:"maxOutputBytes,omitempty"`
	MaxQueries     int             `json:"maxQueries,omitempty"`
	DefaultMaxRows int             `json:"defaultMaxRows,omitempty"`
}

type scriptedQueryResult struct {
	Result         json.RawMessage    `json:"result,omitempty"`
	Queries        []scriptedQueryLog `json:"queries,omitempty"`
	Logs           []string           `json:"logs,omitempty"`
	ScratchHandles []map[string]any   `json:"scratchHandles,omitempty"`
	Error          string             `json:"error,omitempty"`
	Detail         map[string]any     `json:"detail,omitempty"`
}

type scriptedQueryLog struct {
	SQL       string  `json:"sql"`
	MaxRows   int     `json:"maxRows"`
	RowCount  int     `json:"rowCount,omitempty"`
	Truncated bool    `json:"truncated,omitempty"`
	ElapsedMS float64 `json:"elapsedMs,omitempty"`
}

func NewScriptedQueryTool(sqlTool tool.InvokableTool) tool.BaseTool {
	return NewJSInterpreterTool(sqlTool)
}

func NewJSInterpreterTool(sqlTool tool.InvokableTool) tool.BaseTool {
	return ScriptedQueryTool{sqlTool: sqlTool}
}

func (t ScriptedQueryTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: scriptedQueryToolName,
		Desc: "Run a bounded JavaScript interpreter for kube-insight investigation. Use it both to transform JSON already returned in this run via input/rows and to call kube_insight_sql through synchronous helpers sql(query, maxRows) or sqlAll([{name, sql, maxRows}]) after kube_insight_schema. For Kubernetes current-state, historical lookback, ranking, aggregation, or absence claims, gather collector coverage with kube_insight_health before or alongside schema/JS; do not skip health just because schema is enough to write SQL. Do not use await with sql/sqlAll. Never write const sql = `...`; that shadows the sql helper and makes sql(sql, maxRows) fail. Use const query = `...` or const sqlText = `...`, then call sql(query, maxRows). Do not call sql with an object argument. sql(query) and each sqlAll named result return arrays directly; read results.name or rows(result), never results.rows(...). For named sqlAll results, use const result = sqlAll([...]); const rows = result.name; do not use comma-expression destructuring from multiple sqlAll calls. Prefer this single JS tool surface when code-shaped aggregation is clearer: dependent or parallel profile -> proof SQL, several independent aggregates, latest-per-object selection, JSON field extraction, Kubernetes CPU/memory unit normalization, grouping, sorting, filtering, counting, time bucketing, top-N ranking, per-object ordered previous/next diffing such as PVC resize, or answer-ready summarization. When practical, include tightly related profiling, proof SQL, and aggregation together to reduce latency and context size, but do not treat one JS call as a correctness requirement. For relative-time prompts, use literal UTC bounds already computed from client context; do not use JavaScript Date arithmetic, kube_insight_health checked_at, SQL now(), or server time as now. When a JS result covers a relative window, return both window_utc and window_client or client_timezone fields so the final answer can show the client-local time zone, not UTC only. For unscoped namespace resource-ranking scripts, do not hard-code the first cluster_id from health output; keep all dataful clusters, preserve cluster_id in rows, and return per-cluster plus explicitly labeled global rankings when useful. For namespace resource-delta scripts, query coverage/min/max, compute startWindow/endWindow from those coverage rows, then build start/end snapshot SQL; if a previous step exposed a bad, sparse, or stale window, a focused follow-up JS/SQL step may correct it. For Node inventory plus lifecycle scripts, fetch current capacity rows and lifecycle rows together, then fill missing lifecycle instance_type/nodepool from same-name lifecycle rows with known labels and current capacity rows before returning; if any lifecycle label is still missing, run a focused fallback JS/SQL step before answering. Return separate compact sections such as current_node_capacity and node_lifecycle_events so the final answer can cite both. For PVC resize scripts, if the result contains expansions_found > 0 or PVC records with before/after requested/capacity values, answer from that result instead of running a broad lag/window SQL query that returns raw PVC rows, and label normalized binary quantities as GiB in the final answer. For Pod-count peak scripts, use an event sweep over sorted +1/-1 lifecycle events, not nested bucket-by-pod scans. For multi-cluster or multi-metric aggregation, cover relevant clusters, requested ranking dimensions, totals, samples, coverage, row_count, and truncated flags; split into staged JS calls only when that makes planning clearer or keeps intermediate data bounded. Choose conservative bounded LIMIT/maxRows values up front, up to 10000 rows when needed; for lifecycle peak or broad historical scans that can exceed 1000 rows, choose a useful cap instead of probing repeatedly. Avoid repeated equivalent JS/SQL probes after enough evidence exists, and never call this tool only to reformat, translate, hard-code, summarize, or present rows from a previous tool result; write the final Markdown answer directly. Use a follow-up only when the prior result failed, was empty, was truncated and cannot support even a caveated answer, or missed a field explicitly required by the user. Omit maxQueries unless you intentionally need a lower cap; the default allows 6 SQL calls, and any explicit maxQueries must be at least the number of sql plus sqlAll item calls in the script. Usually omit maxOutputBytes; return compact top rows and scratch handles instead of forcing a tiny output limit. The script has no filesystem, network, process, or environment access beyond bounded read-only SQL helpers and session-scoped scratch helpers. Use scratch.write(path, value, metadata), scratch.load(path), scratch.read(path, options), and scratch.list(prefix) to share large JSON between steps by virtual path instead of returning it to the LLM context. Use scratch.load(path) when a later JS step needs the full JSON value for aggregation; use scratch.read(path, options) only when you need metadata, preview, or chunked content. The tool also records scratch.write handles in the top-level scratchHandles result, but best practice is still to assign const handle = scratch.write(...) and return that handle near the compact result. Fetch Kubernetes CPU/memory/storage quantity strings as strings in SQL and parse units in JavaScript; do not divide JSONExtractString or replaceAll string expressions by 1000, 1024, or 1048576 inside SQL. Return a compact object/array, not JSON.stringify of raw rows. Never return broad raw arrays such as {history: history} or {rows: rows}; aggregate them, slice to proof rows, or write them to scratch and return the handle. Include only top rows, totals, caveats, coverage, row_count, truncated, scratch handles, and source IDs needed for the answer. Before returning, make sure every returned property references a variable defined in the script. A complete interpreter result over relevant rows is usually enough evidence to answer. SQL integer columns may arrive as strings, so use Number(value) or ki.sumBy(rows, key) for totals to avoid string concatenation. When the final answer uses this result, add nearby evidence labels such as {{evidence: Node capacity snapshot}} and {{evidence: Node lifecycle events}} so the server can verify citations.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"script": {
				Type:     schema.String,
				Required: true,
				Desc:     "JavaScript statements. Available helpers: input, inputRows, sql(query, maxRows), sqlAll([{name, sql, maxRows}]) which returns an object keyed by name plus _items when all specs are named, rows(result), scratch.write/load/read/list for session-scoped JSON handles, ki/_ helpers groupBy/countBy/sumBy/sortBy/uniqBy/pick, console.log, print, and json(value). sql and sqlAll are synchronous; call const podRows = sql(query, 100), not await sql(...) and not sql({sql: query, maxRows: 100}). Never create const sql = `...`; that hides the helper and makes sql(sql, maxRows) fail. Use const query = `...` or const sqlText = `...` for SQL strings. sql(query) and each sqlAll named result return arrays directly; use const result = sqlAll([...]); const rows = result.name; never results.rows(...) and never comma-expression destructuring from two sqlAll calls. Use literal UTC bounds computed before the tool call; do not use Date arithmetic or SQL now() inside the script for relative windows. Fetch Kubernetes CPU/memory/storage quantity strings as strings in SQL and parse m/Ki/Mi/Gi/Ti units in JavaScript; do not divide JSONExtractString or replaceAll string expressions by 1000, 1024, or 1048576 inside SQL. For Node lifecycle, fill missing instance_type/nodepool from same-name known lifecycle rows or current capacity rows before returning. Use scratch.load(path) to load the full JSON value from a previous scratch.write for in-script aggregation; scratch.read(path, options) returns a metadata envelope and is mainly for previews or chunks. For resource deltas, derive startWindow/endWindow from coverage before snapshot SQL. For bucket peaks, use event-sweep cumulative counts instead of nested lifecycle scans. Use Number(value) or ki.sumBy(rows, key) before summing SQL numeric columns, because ClickHouse integers can arrive as strings. Return a compact JSON-serializable object/array with explicit fields such as top, totals, coverage, row_count, truncated, and scratch handles; do not return JSON.stringify(rawRows), and never return broad raw arrays such as {history: history} or {rows: rows}.",
			},
			"input": {
				Type: schema.Object,
				Desc: "Optional bounded JSON object/array/scalar from prior tool output. The script can read it as input; rows aliases input.rows or input when it is an array.",
			},
			"timeoutMillis": {
				Type: schema.Integer,
				Desc: "Optional timeout in milliseconds. Defaults to 3000 and is capped at 8000.",
			},
			"maxOutputBytes": {
				Type: schema.Integer,
				Desc: "Optional maximum JSON output size. Usually omit this; defaults to 131072, has a 16384 minimum, and is capped at 524288. Prefer compact top rows plus scratch handles over tiny limits.",
			},
			"maxQueries": {
				Type: schema.Integer,
				Desc: "Optional maximum SQL calls allowed from the script. Usually omit this and use the default 6. If set, it must be at least the number of sql() calls plus sqlAll() specs the script will run; it is capped at 10.",
			},
			"defaultMaxRows": {
				Type: schema.Integer,
				Desc: "Optional default maxRows for sql calls. Defaults to 100 and is capped at 10000. Prefer per-call maxRows for large lifecycle scans and keep returned output compact.",
			},
		}),
	}, nil
}

func (t ScriptedQueryTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
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
	if len(args.Input) > maxJSTransformInputSize {
		return "", fmt.Errorf("%s input too large: %d bytes > %d", scriptedQueryToolName, len(args.Input), maxJSTransformInputSize)
	}
	var input any
	if len(args.Input) > 0 {
		if err := json.Unmarshal(args.Input, &input); err != nil {
			return "", fmt.Errorf("%s input must be valid JSON: %w", scriptedQueryToolName, err)
		}
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
	_ = vm.Set("input", input)
	_ = vm.Set("inputRows", jsTransformRowsAlias(input))
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
		return jsSQLResultValue(vm, result, "")
	})
	_ = vm.Set("sqlAll", func(call goja.FunctionCall) goja.Value {
		result, err := state.sqlAll(vm, call)
		if err != nil {
			panic(vm.ToValue(err.Error()))
		}
		if named, ok := result.(scriptedQueryAllResult); ok {
			array := vm.NewArray()
			for i, item := range named.Items {
				alias := ""
				if i < len(named.Names) {
					alias = named.Names[i]
				}
				if err := array.Set(strconv.Itoa(i), jsSQLResultValue(vm, item, alias)); err != nil {
					panic(vm.ToValue(err.Error()))
				}
			}
			for name, item := range named.ByName {
				if err := array.Set(name, jsSQLResultValue(vm, item, name)); err != nil {
					panic(vm.ToValue(err.Error()))
				}
			}
			if err := array.Set("_items", named.Items); err != nil {
				panic(vm.ToValue(err.Error()))
			}
			return array
		}
		return vm.ToValue(result)
	})
	_ = vm.Set("json", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return vm.ToValue("null")
		}
		data, err := json.Marshal(call.Arguments[0].Export())
		if err != nil {
			panic(vm.ToValue(err.Error()))
		}
		return vm.ToValue(string(data))
	})
	_ = vm.Set("rows", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return vm.ToValue(jsTransformRowsAlias(input))
		}
		return vm.ToValue(sqlRows(call.Arguments[0].Export()))
	})
	if err := installJSDataHelpers(vm); err != nil {
		return "", fmt.Errorf("%s failed to install helpers: %w", scriptedQueryToolName, err)
	}
	scratchHandles := []map[string]any{}
	if err := installScratchHelpers(ctx, vm, &scratchHandles); err != nil {
		return "", fmt.Errorf("%s failed to install scratch helpers: %w", scriptedQueryToolName, err)
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
	out := scriptedQueryResult{Result: resultData, Queries: state.logs(), Logs: logs, ScratchHandles: scratchHandles}
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

type scriptedQueryAllResult struct {
	Items  []any
	Names  []string
	ByName map[string]any
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
	resultNames := make([]string, len(items))
	resultsByName := map[string]any{}
	named := make([]bool, len(items))
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
				results[i] = result
				resultNames[i] = name
				named[i] = true
				s.mu.Lock()
				resultsByName[name] = result
				s.mu.Unlock()
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
	allNamed := true
	for _, ok := range named {
		if !ok {
			allNamed = false
			break
		}
	}
	if allNamed {
		return scriptedQueryAllResult{Items: results, Names: resultNames, ByName: resultsByName}, nil
	}
	return results, nil
}

func (s *scriptedQueryState) runQuery(query string, maxRows int) (any, error) {
	if s.sqlTool == nil {
		return nil, errors.New("sql helpers are unavailable because kube_insight_sql is not registered")
	}
	if err := s.reserveQueries(1); err != nil {
		return nil, err
	}
	return s.runReservedQuery(query, maxRows)
}

func (s *scriptedQueryState) reserveQueries(count int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.queries+count > s.maxQueries {
		return fmt.Errorf("script query limit exceeded: %d requested > %d allowed; omit maxQueries to use the default or set maxQueries >= the total sql() calls plus sqlAll() specs", s.queries+count, s.maxQueries)
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
	if rows, ok := value.([]any); ok {
		return rows
	}
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

func jsSQLResultValue(vm *goja.Runtime, value any, alias string) goja.Value {
	rows := sqlRows(value)
	rowItems, ok := rows.([]any)
	if !ok {
		return vm.ToValue(value)
	}
	array := vm.NewArray()
	for i, row := range rowItems {
		_ = array.Set(strconv.Itoa(i), row)
	}
	_ = array.Set("rows", rowItems)
	if object, ok := value.(map[string]any); ok {
		for _, key := range []string{"rowCount", "truncated", "elapsedMs", "columns", "data"} {
			if item, ok := object[key]; ok {
				_ = array.Set(key, item)
			}
		}
		_ = array.Set("_result", object)
	}
	if alias != "" {
		_ = array.Set(alias, rowItems)
	}
	return array
}

func jsTransformRowsAlias(input any) any {
	if rows, ok := input.([]any); ok {
		return rows
	}
	if object, ok := input.(map[string]any); ok {
		if rows, ok := object["rows"]; ok {
			return rows
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
	if limit < minScriptedQueryOutputSize {
		return minScriptedQueryOutputSize
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

func jsLogArgs(args []goja.Value) string {
	parts := make([]string, 0, len(args))
	for _, arg := range args {
		parts = append(parts, fmt.Sprint(arg.Export()))
	}
	return strings.Join(parts, " ")
}
