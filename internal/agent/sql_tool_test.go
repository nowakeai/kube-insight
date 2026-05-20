package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"kube-insight/internal/storage"
)

func TestSQLToolInfo(t *testing.T) {
	tool := NewSQLTool(&fakeSQLStore{})
	info, err := tool.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != SQLToolName || !strings.Contains(info.Desc, "guarded read-only SQL") || info.ParamsOneOf == nil {
		t.Fatalf("tool info = %#v", info)
	}
}

func TestSQLToolInvokableRun(t *testing.T) {
	store := &fakeSQLStore{result: storage.SQLQueryResult{
		SQL:      "select name from latest_index",
		Columns:  []string{"name"},
		Rows:     []map[string]any{{"name": "api-0"}},
		RowCount: 1,
		MaxRows:  12,
	}}
	tool := NewSQLTool(store)
	out, err := tool.InvokableRun(context.Background(), `{"sql":"select name from latest_index","maxRows":9999}`)
	if err != nil {
		t.Fatal(err)
	}
	if store.opts.SQL != "select name from latest_index" || store.opts.MaxRows != maxSQLToolMaxRows {
		t.Fatalf("opts = %#v", store.opts)
	}
	var result storage.SQLQueryResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	if result.RowCount != 1 || result.Rows[0]["name"] != "api-0" {
		t.Fatalf("result = %#v", result)
	}
}

func TestSQLToolDefaultsAndArgumentErrors(t *testing.T) {
	store := &fakeSQLStore{}
	tool := NewSQLTool(store)
	if _, err := tool.InvokableRun(context.Background(), `{"sql":"with recent as (select 1 as n) select n from recent"}`); err != nil {
		t.Fatal(err)
	}
	if store.opts.MaxRows != defaultSQLToolMaxRows {
		t.Fatalf("default max rows = %d", store.opts.MaxRows)
	}
	bad := []string{
		"",
		`{`,
		`{"sql":""}`,
		`{"sql":"select 1; select 2"}`,
		`{"sql":"delete from objects"}`,
		`{"sql":"pragma table_info(objects)"}`,
		`{"sql":"explain delete from objects"}`,
	}
	for _, args := range bad {
		if _, err := tool.InvokableRun(context.Background(), args); err == nil {
			t.Fatalf("expected error for args %q", args)
		}
	}
}

type fakeSQLStore struct {
	opts   storage.SQLQueryOptions
	result storage.SQLQueryResult
}

func (s *fakeSQLStore) QuerySQL(_ context.Context, opts storage.SQLQueryOptions) (storage.SQLQueryResult, error) {
	s.opts = opts
	return s.result, nil
}

func (s *fakeSQLStore) QuerySchema(context.Context) (storage.SQLSchema, error) {
	return storage.SQLSchema{}, nil
}
