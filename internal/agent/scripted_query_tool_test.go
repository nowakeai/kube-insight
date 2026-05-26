package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

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

func TestScriptedQueryToolRunsSQLAll(t *testing.T) {
	ctx := context.Background()
	sqlTool := &fakeScriptedSQLTool{results: map[string]any{
		"select 1": map[string]any{"rows": []map[string]any{{"n": 1}}, "rowCount": 1},
		"select 2": map[string]any{"rows": []map[string]any{{"n": 2}}, "rowCount": 1},
	}}
	scripted := NewScriptedQueryTool(sqlTool).(tool.InvokableTool)
	out, err := scripted.InvokableRun(ctx, `{
		"script": "const out = sqlAll([{name: 'one', sql: 'select 1', maxRows: 1}, {name: 'two', sql: 'select 2', maxRows: 1}]); return out.map(x => ({name: x.name, n: rows(x.result)[0].n}));"
	}`)
	if err != nil {
		t.Fatal(err)
	}
	var result scriptedQueryResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	var rows []map[string]any
	if err := json.Unmarshal(result.Result, &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0]["name"] != "one" || rows[1]["n"] != float64(2) {
		t.Fatalf("rows = %#v", rows)
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

func TestScriptedQueryToolInfoEncouragesQueryPlanning(t *testing.T) {
	info, err := NewScriptedQueryTool(&fakeScriptedSQLTool{}).Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{scriptedQueryToolName, "sql(query, maxRows)", "sqlAll", "after kube_insight_schema", "dependent or parallel", "{{evidence: Node capacity facts}}"} {
		if !strings.Contains(info.Name+" "+info.Desc, want) {
			t.Fatalf("info missing %q: %#v", want, info)
		}
	}
}

type fakeScriptedSQLTool struct {
	mu      sync.Mutex
	results map[string]any
	calls   []string
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
	result, ok := t.results[args.SQL]
	t.mu.Unlock()
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
