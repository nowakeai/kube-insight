package clickhouse

import (
	"context"
	"fmt"
	"sort"
)

type ExpectedTableSchema struct {
	Name       string `json:"name"`
	Engine     string `json:"engine"`
	SortingKey string `json:"sortingKey"`
}

type TableSchemaStatus struct {
	Name               string   `json:"name"`
	Exists             bool     `json:"exists"`
	OK                 bool     `json:"ok"`
	Engine             string   `json:"engine,omitempty"`
	ExpectedEngine     string   `json:"expectedEngine"`
	SortingKey         string   `json:"sortingKey,omitempty"`
	ExpectedSortingKey string   `json:"expectedSortingKey"`
	Problems           []string `json:"problems,omitempty"`
}

type SchemaStatusResult struct {
	Database string              `json:"database"`
	OK       bool                `json:"ok"`
	Tables   []TableSchemaStatus `json:"tables"`
}

func ExpectedTableSchemas() []ExpectedTableSchema {
	return []ExpectedTableSchema{
		{Name: "api_resources", Engine: "ReplacingMergeTree", SortingKey: "api_group, api_version, resource"},
		{Name: "observations", Engine: "MergeTree", SortingKey: "cluster_id, kind, namespace, name, observed_at, uid"},
		{Name: "object_aliases", Engine: "ReplacingMergeTree", SortingKey: "cluster_id, alias_id, object_id"},
		{Name: "versions", Engine: "MergeTree", SortingKey: "cluster_id, object_id, seq, observed_at"},
		{Name: "facts", Engine: "MergeTree", SortingKey: "cluster_id, fact_key, fact_value, ts, object_id"},
		{Name: "edges", Engine: "MergeTree", SortingKey: "cluster_id, edge_type, src_id, valid_from_ms, dst_id"},
		{Name: "changes", Engine: "MergeTree", SortingKey: "cluster_id, change_family, path, ts, object_id"},
		{Name: "filter_decisions", Engine: "MergeTree", SortingKey: "cluster_id, resource, namespace, name, ts, outcome, filter_name"},
		{Name: "ingestion_offsets", Engine: "ReplacingMergeTree", SortingKey: "cluster_id, api_group, api_version, resource, namespace, event"},
	}
}

func CheckSchemaStatus(ctx context.Context, runner QueryRunner, opts SchemaOptions) (SchemaStatusResult, error) {
	if runner == nil {
		return SchemaStatusResult{}, fmt.Errorf("clickhouse query runner is required")
	}
	if err := opts.Validate(); err != nil {
		return SchemaStatusResult{}, err
	}
	opts = opts.normalized()
	expected := ExpectedTableSchemas()
	names := make([]string, 0, len(expected))
	for _, table := range expected {
		names = append(names, table.Name)
	}
	query := fmt.Sprintf(`
SELECT name, engine, sorting_key
FROM system.tables
WHERE database = %s AND name IN (%s)
ORDER BY name`, quoteString(opts.Database), sqlStringList(names))
	result, err := runner.QueryJSON(ctx, query)
	if err != nil {
		return SchemaStatusResult{}, err
	}
	actual := map[string]TableSchemaStatus{}
	for _, row := range result.Data {
		name := stringValue(row["name"])
		actual[name] = TableSchemaStatus{
			Name:       name,
			Exists:     true,
			Engine:     stringValue(row["engine"]),
			SortingKey: stringValue(row["sorting_key"]),
		}
	}
	out := SchemaStatusResult{Database: opts.Database, OK: true, Tables: make([]TableSchemaStatus, 0, len(expected))}
	for _, want := range expected {
		status := actual[want.Name]
		if status.Name == "" {
			status.Name = want.Name
		}
		status.ExpectedEngine = want.Engine
		status.ExpectedSortingKey = want.SortingKey
		if !status.Exists {
			status.Problems = append(status.Problems, "missing")
		}
		if status.Exists && status.Engine != want.Engine {
			status.Problems = append(status.Problems, fmt.Sprintf("engine=%s want=%s", status.Engine, want.Engine))
		}
		if status.Exists && status.SortingKey != want.SortingKey {
			status.Problems = append(status.Problems, fmt.Sprintf("sorting_key=%s want=%s", status.SortingKey, want.SortingKey))
		}
		status.OK = len(status.Problems) == 0
		if !status.OK {
			out.OK = false
		}
		out.Tables = append(out.Tables, status)
	}
	sort.SliceStable(out.Tables, func(i, j int) bool {
		return out.Tables[i].Name < out.Tables[j].Name
	})
	return out, nil
}
