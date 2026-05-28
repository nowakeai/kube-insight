package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

func TestScriptedQueryToolRunsDependentSQLAndGroupsRows(t *testing.T) {
	ctx := context.Background()
	sqlTool := &fakeScriptedSQLTool{
		results: map[string]any{
			"select namespace, memory from facts": map[string]any{
				"rows": []map[string]any{
					{"namespace": "default", "memory": 256},
					{"namespace": "default", "memory": 512},
					{"namespace": "ops", "memory": 128},
				},
				"rowCount": 3,
			},
			"select count() as nodes from facts where kind = 'Node'": map[string]any{
				"rows":     []map[string]any{{"nodes": 18}},
				"rowCount": 1,
			},
		},
	}
	scripted := NewScriptedQueryTool(sqlTool).(tool.InvokableTool)
	out, err := scripted.InvokableRun(ctx, `{
		"script": "const usage = sql('select namespace, memory from facts', 20); const grouped = Object.entries(ki.groupBy(rows(usage), 'namespace')).map(([namespace, rows]) => ({namespace, memory: ki.sumBy(rows, 'memory')})); const nodes = rows(sql(\"select count() as nodes from facts where kind = 'Node'\", 1))[0].nodes; return {nodes, grouped: ki.sortBy(grouped, 'namespace')};"
	}`)
	if err != nil {
		t.Fatal(err)
	}
	var result scriptedQueryResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Queries) != 2 {
		t.Fatalf("queries = %#v", result.Queries)
	}
	var payload struct {
		Nodes   float64 `json:"nodes"`
		Grouped []struct {
			Namespace string  `json:"namespace"`
			Memory    float64 `json:"memory"`
		} `json:"grouped"`
	}
	if err := json.Unmarshal(result.Result, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Nodes != 18 || len(payload.Grouped) != 2 || payload.Grouped[0].Namespace != "default" || payload.Grouped[0].Memory != 768 {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestScriptedQueryToolSQLResultIsIterableRows(t *testing.T) {
	ctx := context.Background()
	sqlTool := &fakeScriptedSQLTool{results: map[string]any{
		"select n from numbers": map[string]any{
			"rows":     []map[string]any{{"n": 1}, {"n": 2}},
			"rowCount": 2,
		},
	}}
	scripted := NewScriptedQueryTool(sqlTool).(tool.InvokableTool)
	out, err := scripted.InvokableRun(ctx, `{
		"script": "const result = sql('select n from numbers', 10); let total = 0; for (const row of result) total += row.n; return {total, rowCount: result.rowCount, rowsLength: result.rows.length, rowsHelperLength: rows(result).length};"
	}`)
	if err != nil {
		t.Fatal(err)
	}
	var result scriptedQueryResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	var payload map[string]float64
	if err := json.Unmarshal(result.Result, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["total"] != 3 || payload["rowCount"] != 2 || payload["rowsLength"] != 2 || payload["rowsHelperLength"] != 2 {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestScriptedQueryToolRunsSQLAll(t *testing.T) {
	ctx := context.Background()
	sqlTool := &fakeScriptedSQLTool{results: map[string]any{
		"select 1": map[string]any{"rows": []map[string]any{{"n": 1}}, "rowCount": 1},
		"select 2": map[string]any{"rows": []map[string]any{{"n": 2}}, "rowCount": 1},
	}}
	scripted := NewScriptedQueryTool(sqlTool).(tool.InvokableTool)
	out, err := scripted.InvokableRun(ctx, `{
		"script": "const out = sqlAll([{name: 'one', sql: 'select 1', maxRows: 1}, {name: 'two', sql: 'select 2', maxRows: 1}]); const [oneResult, twoResult] = out; return {one: rows(out.one)[0].n, two: rows(out.two)[0].n, first: rows(oneResult)[0].n, second: rows(twoResult)[0].n, aliasOne: oneResult.one[0].n, aliasTwo: twoResult.two[0].n, itemCount: out._items.length};"
	}`)
	if err != nil {
		t.Fatal(err)
	}
	var result scriptedQueryResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	var payload map[string]float64
	if err := json.Unmarshal(result.Result, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["one"] != 1 || payload["two"] != 2 || payload["first"] != 1 || payload["second"] != 2 || payload["aliasOne"] != 1 || payload["aliasTwo"] != 2 || payload["itemCount"] != 2 {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestScriptedQueryToolRunsSQLAllConcurrently(t *testing.T) {
	ctx := context.Background()
	sqlTool := &fakeScriptedSQLTool{
		delay: 50 * time.Millisecond,
		results: map[string]any{
			"select 1": map[string]any{"rows": []map[string]any{{"n": 1}}, "rowCount": 1},
			"select 2": map[string]any{"rows": []map[string]any{{"n": 2}}, "rowCount": 1},
			"select 3": map[string]any{"rows": []map[string]any{{"n": 3}}, "rowCount": 1},
		},
	}
	scripted := NewScriptedQueryTool(sqlTool).(tool.InvokableTool)
	_, err := scripted.InvokableRun(ctx, `{
		"script": "const out = sqlAll([{name: 'one', sql: 'select 1', maxRows: 1}, {name: 'two', sql: 'select 2', maxRows: 1}, {name: 'three', sql: 'select 3', maxRows: 1}]); return {total: rows(out.one)[0].n + rows(out.two)[0].n + rows(out.three)[0].n};"
	}`)
	if err != nil {
		t.Fatal(err)
	}
	sqlTool.mu.Lock()
	maxActive := sqlTool.maxActive
	sqlTool.mu.Unlock()
	if maxActive < 2 {
		t.Fatalf("sqlAll did not run queries concurrently, max active calls = %d", maxActive)
	}
}

func TestScriptedQueryToolAllowsAwaitForSynchronousSQL(t *testing.T) {
	ctx := context.Background()
	sqlTool := &fakeScriptedSQLTool{results: map[string]any{
		"select 1": map[string]any{"rows": []map[string]any{{"n": 1}}, "rowCount": 1},
	}}
	scripted := NewScriptedQueryTool(sqlTool).(tool.InvokableTool)
	out, err := scripted.InvokableRun(ctx, `{
		"script": "const result = await sql('select 1', 1); return rows(result)[0];"
	}`)
	if err != nil {
		t.Fatal(err)
	}
	var result scriptedQueryResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	var row map[string]any
	if err := json.Unmarshal(result.Result, &row); err != nil {
		t.Fatal(err)
	}
	if row["n"] != float64(1) {
		t.Fatalf("row = %#v", row)
	}
}

func TestScriptedQueryToolScratchHelpersUseSessionScopedTempStore(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	session, err := store.CreateSession(ctx, CreateSessionInput{Title: "scratch"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.CreateRun(ctx, session.ID, CreateRunInput{Input: "scratch test"})
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempScratchStore{scope: scratchScope{SessionID: session.ID}}.root())
	ctx = withRunExecutionContext(ctx, RunExecutionContext{Store: store, RunID: run.ID})

	scripted := NewScriptedQueryTool(&fakeScriptedSQLTool{}).(tool.InvokableTool)
	out, err := scripted.InvokableRun(ctx, `{
		"script": "const handle = scratch.write('/rows/top.json', {rows: [{namespace: 'default', cpu: 1}], note: 'kept out of context'}, {source: 'test'}); const loaded = scratch.read(handle.path); const listed = scratch.list('/rows'); return {path: handle.path, bytes: handle.bytes, sessionId: handle.sessionId, runId: handle.runId, loadedRows: loaded.value.rows.length, listed: listed.length, metadataSource: handle.metadata.source};"
	}`)
	if err != nil {
		t.Fatal(err)
	}
	var result scriptedQueryResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Path           string  `json:"path"`
		Bytes          float64 `json:"bytes"`
		SessionID      string  `json:"sessionId"`
		RunID          string  `json:"runId"`
		LoadedRows     float64 `json:"loadedRows"`
		Listed         float64 `json:"listed"`
		MetadataSource string  `json:"metadataSource"`
	}
	if err := json.Unmarshal(result.Result, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Path != "/rows/top.json" || payload.Bytes == 0 || payload.SessionID != session.ID || payload.RunID != run.ID || payload.LoadedRows != 1 || payload.Listed != 1 || payload.MetadataSource != "test" {
		t.Fatalf("payload = %#v", payload)
	}
	if len(result.ScratchHandles) != 1 || result.ScratchHandles[0]["path"] != "/rows/top.json" || result.ScratchHandles[0]["sessionId"] != session.ID || result.ScratchHandles[0]["runId"] != run.ID {
		t.Fatalf("scratch handles = %#v", result.ScratchHandles)
	}
}

func TestScriptedQueryToolRecordsScratchWritesEvenWhenHandleIsNotReturned(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	session, err := store.CreateSession(ctx, CreateSessionInput{Title: "scratch"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.CreateRun(ctx, session.ID, CreateRunInput{Input: "scratch test"})
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempScratchStore{scope: scratchScope{SessionID: session.ID}}.root())
	ctx = withRunExecutionContext(ctx, RunExecutionContext{Store: store, RunID: run.ID})

	scripted := NewScriptedQueryTool(&fakeScriptedSQLTool{}).(tool.InvokableTool)
	out, err := scripted.InvokableRun(ctx, `{
		"script": "scratch.write('/rows/hidden.json', {rows: [{namespace: 'default', cpu: 1}]}, {source: 'test'}); return {ok: true, row_count: 1};"
	}`)
	if err != nil {
		t.Fatal(err)
	}
	var result scriptedQueryResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	var payload struct {
		OK       bool    `json:"ok"`
		RowCount float64 `json:"row_count"`
	}
	if err := json.Unmarshal(result.Result, &payload); err != nil {
		t.Fatal(err)
	}
	if !payload.OK || payload.RowCount != 1 {
		t.Fatalf("payload = %#v", payload)
	}
	if len(result.ScratchHandles) != 1 || result.ScratchHandles[0]["path"] != "/rows/hidden.json" || result.ScratchHandles[0]["sessionId"] != session.ID || result.ScratchHandles[0]["runId"] != run.ID {
		t.Fatalf("scratch handles = %#v", result.ScratchHandles)
	}
}

func TestScriptedQueryToolScratchPersistsAcrossToolRuns(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	session, err := store.CreateSession(ctx, CreateSessionInput{Title: "scratch"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.CreateRun(ctx, session.ID, CreateRunInput{Input: "scratch test"})
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempScratchStore{scope: scratchScope{SessionID: session.ID}}.root())
	ctx = withRunExecutionContext(ctx, RunExecutionContext{Store: store, RunID: run.ID})

	scripted := NewScriptedQueryTool(&fakeScriptedSQLTool{}).(tool.InvokableTool)
	firstOut, err := scripted.InvokableRun(ctx, `{
		"script": "scratch.write('/rows/shared.json', {rows: [{namespace: 'default', cpu: 1}, {namespace: 'ops', cpu: 2}]}, {source: 'first'}); return {written: true};"
	}`)
	if err != nil {
		t.Fatal(err)
	}
	var first scriptedQueryResult
	if err := json.Unmarshal([]byte(firstOut), &first); err != nil {
		t.Fatal(err)
	}
	if len(first.ScratchHandles) != 1 || first.ScratchHandles[0]["path"] != "/rows/shared.json" {
		t.Fatalf("first scratch handles = %#v", first.ScratchHandles)
	}

	secondOut, err := scripted.InvokableRun(ctx, `{
		"script": "const loaded = scratch.load('/rows/shared.json'); const envelope = scratch.read('/rows/shared.json'); return {path: envelope.path, bytes: envelope.bytes, row_count: loaded.rows.length, namespaces: loaded.rows.map(r => r.namespace)};"
	}`)
	if err != nil {
		t.Fatal(err)
	}
	var second scriptedQueryResult
	if err := json.Unmarshal([]byte(secondOut), &second); err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Path       string   `json:"path"`
		Bytes      float64  `json:"bytes"`
		RowCount   float64  `json:"row_count"`
		Namespaces []string `json:"namespaces"`
	}
	if err := json.Unmarshal(second.Result, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Path != "/rows/shared.json" || payload.Bytes == 0 || payload.RowCount != 2 || strings.Join(payload.Namespaces, ",") != "default,ops" {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestScratchHandlesFromToolOutputExtractsHandles(t *testing.T) {
	output := jsonRaw(map[string]any{
		"result": map[string]any{
			"top": []map[string]any{{"namespace": "default"}},
			"handle": map[string]any{
				"path":      "/rows/top.json",
				"mime":      "application/json",
				"bytes":     123,
				"sha256":    strings.Repeat("a", 64),
				"sessionId": "sess_1",
				"runId":     "run_1",
			},
		},
	})
	handles := scratchHandlesFromToolOutput(output)
	if len(handles) != 1 || handles[0]["path"] != "/rows/top.json" || handles[0]["sha256"] != strings.Repeat("a", 64) {
		t.Fatalf("handles = %#v", handles)
	}
}

func TestScriptedQueryToolEnforcesQueryLimit(t *testing.T) {
	ctx := context.Background()
	sqlTool := &fakeScriptedSQLTool{results: map[string]any{"select 1": map[string]any{"rows": []map[string]any{{"n": 1}}}}}
	scripted := NewScriptedQueryTool(sqlTool).(tool.InvokableTool)
	_, err := scripted.InvokableRun(ctx, `{
		"maxQueries": 1,
		"script": "sql('select 1'); sql('select 1'); return true;"
	}`)
	if err == nil || !strings.Contains(err.Error(), "query limit exceeded") {
		t.Fatalf("err = %v", err)
	}
}

func TestScriptedQueryToolDoesNotExposeHostCapabilities(t *testing.T) {
	ctx := context.Background()
	sqlTool := &fakeScriptedSQLTool{results: map[string]any{}}
	scripted := NewScriptedQueryTool(sqlTool).(tool.InvokableTool)
	out, err := scripted.InvokableRun(ctx, `{"script":"return {process: typeof process, require: typeof require, fetch: typeof fetch};"}`)
	if err != nil {
		t.Fatal(err)
	}
	var result scriptedQueryResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	var exported map[string]string
	if err := json.Unmarshal(result.Result, &exported); err != nil {
		t.Fatal(err)
	}
	for key, value := range exported {
		if value != "undefined" {
			t.Fatalf("%s exposed as %q", key, value)
		}
	}
}

func TestScriptedQueryToolSanitizesNonFiniteNumbers(t *testing.T) {
	ctx := context.Background()
	sqlTool := &fakeScriptedSQLTool{results: map[string]any{}}
	scripted := NewScriptedQueryTool(sqlTool).(tool.InvokableTool)
	out, err := scripted.InvokableRun(ctx, `{"script":"return {nan: Number.NaN, inf: Infinity, ok: 3};"}`)
	if err != nil {
		t.Fatal(err)
	}
	var result scriptedQueryResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(result.Result, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["nan"] != nil || payload["inf"] != nil || payload["ok"] != float64(3) {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestScriptedQueryToolAppliesMinimumOutputLimit(t *testing.T) {
	ctx := context.Background()
	sqlTool := &fakeScriptedSQLTool{results: map[string]any{}}
	scripted := NewScriptedQueryTool(sqlTool).(tool.InvokableTool)
	out, err := scripted.InvokableRun(ctx, `{
		"maxOutputBytes": 64,
		"script": "return {text: 'x'.repeat(4096)};"
	}`)
	if err != nil {
		t.Fatal(err)
	}
	var result scriptedQueryResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	var payload map[string]string
	if err := json.Unmarshal(result.Result, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload["text"]) != 4096 {
		t.Fatalf("payload text length = %d", len(payload["text"]))
	}
}

func TestScriptedQueryToolInfoEncouragesQueryPlanning(t *testing.T) {
	info, err := NewScriptedQueryTool(&fakeScriptedSQLTool{}).Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{scriptedQueryToolName, "sql(query, maxRows)", "sqlAll", "after kube_insight_schema", "dependent or parallel", "Do not use await", "Never write const sql", "history: history", "window_utc", "client_timezone", "do not treat one JS call as a correctness requirement", "Omit maxQueries", "Usually omit maxOutputBytes", "scratch.load(path)", "do not use comma-expression destructuring", "never call this tool only to reformat", "10000", "lifecycle peak", "PVC resize", "JSONExtractString", "parse units in JavaScript", "Date arithmetic", "current_node_capacity", "node_lifecycle_events", "not JSON.stringify", "{{evidence: Node capacity snapshot}}"} {
		if !strings.Contains(info.Name+" "+info.Desc, want) {
			t.Fatalf("info missing %q: %#v", want, info)
		}
	}
}

type fakeScriptedSQLTool struct {
	mu        sync.Mutex
	results   map[string]any
	calls     []string
	delay     time.Duration
	active    int
	maxActive int
}

func (t *fakeScriptedSQLTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: "kube_insight_sql", Desc: "fake sql"}, nil
}

func (t *fakeScriptedSQLTool) InvokableRun(_ context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	var args struct {
		SQL     string `json:"sql"`
		MaxRows int    `json:"maxRows"`
	}
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return "", err
	}
	t.mu.Lock()
	t.calls = append(t.calls, args.SQL)
	t.active++
	if t.active > t.maxActive {
		t.maxActive = t.active
	}
	result, ok := t.results[args.SQL]
	t.mu.Unlock()
	defer func() {
		t.mu.Lock()
		t.active--
		t.mu.Unlock()
	}()
	if t.delay > 0 {
		time.Sleep(t.delay)
	}
	if !ok {
		return "", fmt.Errorf("unexpected SQL %q", args.SQL)
	}
	data, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	wrapped, err := json.Marshal(map[string]any{
		"content": []map[string]any{{"type": "text", "text": string(data)}},
	})
	if err != nil {
		return "", err
	}
	return string(wrapped), nil
}
