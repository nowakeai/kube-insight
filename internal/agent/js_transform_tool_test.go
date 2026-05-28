package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/tool"
)

func TestJSInterpreterToolGroupsRows(t *testing.T) {
	ctx := context.Background()
	transform := NewJSInterpreterTool(nil)
	info, err := transform.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != jsInterpreterToolName || !strings.Contains(info.Desc, "JavaScript") {
		t.Fatalf("tool info = %#v", info)
	}
	invokable, ok := transform.(tool.InvokableTool)
	if !ok {
		t.Fatalf("transform is not invokable: %T", transform)
	}
	out, err := invokable.InvokableRun(ctx, `{
		"input": {
			"rows": [
				{"namespace": "default", "memory": 256},
				{"namespace": "default", "memory": 512},
				{"namespace": "ops", "memory": 128}
			]
		},
		"script": "const byNs = {}; for (const row of inputRows) { byNs[row.namespace] = (byNs[row.namespace] || 0) + row.memory; } return byNs;"
	}`)
	if err != nil {
		t.Fatal(err)
	}
	var result scriptedQueryResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	var grouped map[string]float64
	if err := json.Unmarshal(result.Result, &grouped); err != nil {
		t.Fatal(err)
	}
	if grouped["default"] != 768 || grouped["ops"] != 128 {
		t.Fatalf("grouped = %#v", grouped)
	}
}

func TestJSInterpreterToolExposesDataHelpers(t *testing.T) {
	ctx := context.Background()
	transform := NewJSInterpreterTool(nil).(tool.InvokableTool)
	out, err := transform.InvokableRun(ctx, `{
		"input": {"rows": [{"namespace":"default","cpu":1},{"namespace":"default","cpu":2},{"namespace":"ops","cpu":4}]},
		"script": "const grouped = ki.groupBy(inputRows, 'namespace'); return {counts: _.countBy(inputRows, 'namespace'), defaultCPU: ki.sumBy(grouped.default, 'cpu')};"
	}`)
	if err != nil {
		t.Fatal(err)
	}
	var result scriptedQueryResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Counts     map[string]float64 `json:"counts"`
		DefaultCPU float64            `json:"defaultCPU"`
	}
	if err := json.Unmarshal(result.Result, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Counts["default"] != 2 || payload.DefaultCPU != 3 {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestJSInterpreterToolDoesNotExposeHostCapabilities(t *testing.T) {
	ctx := context.Background()
	transform := NewJSInterpreterTool(nil).(tool.InvokableTool)
	out, err := transform.InvokableRun(ctx, `{"script":"return {process: typeof process, require: typeof require, fetch: typeof fetch};"}`)
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

func TestJSInterpreterToolSanitizesNonFiniteNumbers(t *testing.T) {
	ctx := context.Background()
	transform := NewJSInterpreterTool(nil).(tool.InvokableTool)
	out, err := transform.InvokableRun(ctx, `{"script":"return {nan: Number.NaN, inf: Infinity, ok: 3};"}`)
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

func TestJSInterpreterToolRejectsLargeInput(t *testing.T) {
	ctx := context.Background()
	transform := NewJSInterpreterTool(nil).(tool.InvokableTool)
	_, err := transform.InvokableRun(ctx, `{"input":"`+strings.Repeat("x", maxJSTransformInputSize+1)+`","script":"return input"}`)
	if err == nil || !strings.Contains(err.Error(), "input too large") {
		t.Fatalf("err = %v", err)
	}
}

func TestJSInterpreterToolTimesOut(t *testing.T) {
	ctx := context.Background()
	transform := NewJSInterpreterTool(nil).(tool.InvokableTool)
	_, err := transform.InvokableRun(ctx, `{"script":"while (true) {}","timeoutMillis":1}`)
	if err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("err = %v", err)
	}
}
