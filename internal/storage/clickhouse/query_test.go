package clickhouse

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"kube-insight/internal/storage"
)

func TestHTTPClientQueryJSON(t *testing.T) {
	var body string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		body = string(data)
		_, _ = w.Write([]byte(`{"meta":[{"name":"name","type":"String"}],"data":[{"name":"api"}],"rows":1,"statistics":{"elapsed":0.01,"rows_read":1,"bytes_read":10}}`))
	}))
	defer server.Close()

	result, err := (HTTPClient{Endpoint: server.URL, HTTPClient: server.Client()}).QueryJSON(context.Background(), "SELECT name FROM t")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "FORMAT JSON") {
		t.Fatalf("query body = %s", body)
	}
	if result.Rows != 1 || result.Data[0]["name"] != "api" || result.Statistics["rowsRead"] != 1 {
		t.Fatalf("result = %#v", result)
	}
}

func TestHTTPClientQueryServiceEvidence(t *testing.T) {
	var queries []string
	edgeCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		query := string(data)
		queries = append(queries, query)
		switch {
		case strings.Contains(query, "FROM `ki`.object_aliases"):
			var rows []string
			if strings.Contains(query, "c1/svc-uid") {
				rows = append(rows, `{"object_id":"c1/svc-uid","alias_id":"c1/services/default/api","kind":"Service"}`)
			}
			if strings.Contains(query, "c1/eps-uid") {
				rows = append(rows, `{"object_id":"c1/eps-uid","alias_id":"c1/endpointslices/default/api-abc","kind":"EndpointSlice"}`)
			}
			if strings.Contains(query, "c1/pod-uid") {
				rows = append(rows, `{"object_id":"c1/pod-uid","alias_id":"c1/pods/default/api-0","kind":"Pod"}`)
			}
			_, _ = w.Write([]byte(`{"data":[` + strings.Join(rows, ",") + `],"rows":` + fmt.Sprintf("%d", len(rows)) + `}`))
		case strings.Contains(query, "FROM `ki`.versions") && strings.Contains(query, "kind = 'Service'"):
			_, _ = w.Write([]byte(`{"data":[{"cluster_id":"c1","object_id":"c1/svc-uid","kind":"Service","namespace":"default","name":"api","uid":"svc-uid"}],"rows":1}`))
		case strings.Contains(query, "FROM `ki`.edges"):
			edgeCalls++
			if edgeCalls == 1 {
				_, _ = w.Write([]byte(`{"data":[{"edge_type":"endpointslice_for_service","src_id":"c1/eps-uid","dst_id":"c1/svc-uid"}],"rows":1}`))
				return
			}
			_, _ = w.Write([]byte(`{"data":[{"edge_type":"endpointslice_for_service","src_id":"c1/eps-uid","dst_id":"c1/svc-uid"},{"edge_type":"endpointslice_targets_pod","src_id":"c1/eps-uid","dst_id":"c1/pod-uid"}],"rows":2}`))
		case strings.Contains(query, "FROM `ki`.versions") && strings.Contains(query, "object_id IN"):
			_, _ = w.Write([]byte(`{"data":[{"object_id":"c1/svc-uid","kind":"Service"},{"object_id":"c1/eps-uid","kind":"EndpointSlice"},{"object_id":"c1/pod-uid","kind":"Pod"}],"rows":3}`))
		case strings.Contains(query, "FROM `ki`.facts"):
			_, _ = w.Write([]byte(`{"data":[{"object_id":"c1/pod-uid","fact_key":"pod.phase","fact_value":"Running"}],"rows":1}`))
		case strings.Contains(query, "FROM `ki`.changes"):
			_, _ = w.Write([]byte(`{"data":[{"object_id":"c1/pod-uid","path":"status.phase"}],"rows":1}`))
		default:
			t.Fatalf("unexpected query: %s", query)
		}
	}))
	defer server.Close()

	result, err := (HTTPClient{Endpoint: server.URL, HTTPClient: server.Client()}).QueryServiceEvidence(context.Background(), ServiceEvidenceOptions{
		Database:  "ki",
		ClusterID: "c1",
		Namespace: "default",
		Name:      "api",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(queries) < 10 || edgeCalls < 2 {
		t.Fatalf("queries = %#v edgeCalls=%d", queries, edgeCalls)
	}
	if result.Summary.Services != 1 || result.Summary.Objects != 3 || result.Summary.Edges != 2 || result.Summary.Facts != 1 || result.Summary.Changes != 1 {
		t.Fatalf("result = %#v", result)
	}
	if result.Edges[1]["dst_id"] != "c1/pod-uid" || result.Edges[1]["dst_kind"] != "Pod" {
		t.Fatalf("canonical edge = %#v", result.Edges[1])
	}
}

type queryRunnerFunc func(context.Context, string) (QueryResult, error)

func (fn queryRunnerFunc) QueryJSON(ctx context.Context, query string) (QueryResult, error) {
	return fn(ctx, query)
}

func TestQueryServiceEvidenceUsesQueryRunner(t *testing.T) {
	var queries []string
	runner := queryRunnerFunc(func(ctx context.Context, query string) (QueryResult, error) {
		queries = append(queries, query)
		return QueryResult{Data: nil, Rows: 0}, nil
	})

	result, err := QueryServiceEvidence(context.Background(), runner, ServiceEvidenceOptions{Database: "ki", Namespace: "default", Name: "api"})
	if err != nil {
		t.Fatal(err)
	}
	if len(queries) != 1 || !strings.Contains(queries[0], "FROM `ki`.versions") {
		t.Fatalf("queries = %#v", queries)
	}
	if result.Summary.Services != 0 || len(result.Service) != 0 {
		t.Fatalf("result = %#v", result)
	}
}

func TestCheckSchemaStatusReportsDrift(t *testing.T) {
	var query string
	runner := queryRunnerFunc(func(ctx context.Context, q string) (QueryResult, error) {
		query = q
		return QueryResult{Data: []map[string]any{
			{"name": "api_resources", "engine": "ReplacingMergeTree", "sorting_key": "api_group, api_version, resource"},
			{"name": "ingestion_offsets", "engine": "MergeTree", "sorting_key": "cluster_id, api_group, api_version, resource, namespace, updated_at"},
		}}, nil
	})

	status, err := CheckSchemaStatus(context.Background(), runner, SchemaOptions{Database: "ki"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(query, "FROM system.tables") || !strings.Contains(query, "'ingestion_offsets'") {
		t.Fatalf("query = %s", query)
	}
	if status.OK {
		t.Fatalf("status unexpectedly OK: %#v", status)
	}
	var offsets TableSchemaStatus
	for _, table := range status.Tables {
		if table.Name == "ingestion_offsets" {
			offsets = table
			break
		}
	}
	if offsets.OK || offsets.ExpectedEngine != "ReplacingMergeTree" || !strings.Contains(strings.Join(offsets.Problems, ";"), "engine=MergeTree") {
		t.Fatalf("offset status = %#v", offsets)
	}
}

func TestCheckSchemaStatusOK(t *testing.T) {
	rows := make([]map[string]any, 0, len(ExpectedTableSchemas()))
	for _, table := range ExpectedTableSchemas() {
		rows = append(rows, map[string]any{"name": table.Name, "engine": table.Engine, "sorting_key": table.SortingKey})
	}
	runner := queryRunnerFunc(func(ctx context.Context, query string) (QueryResult, error) {
		return QueryResult{Data: rows, Rows: len(rows)}, nil
	})

	status, err := CheckSchemaStatus(context.Background(), runner, SchemaOptions{Database: "ki"})
	if err != nil {
		t.Fatal(err)
	}
	if !status.OK || len(status.Tables) != len(ExpectedTableSchemas()) {
		t.Fatalf("status = %#v", status)
	}
}

func TestPlanIngestionOffsetsRepair(t *testing.T) {
	status := SchemaStatusResult{Database: "ki", Tables: []TableSchemaStatus{{
		Name:               "ingestion_offsets",
		Exists:             true,
		OK:                 false,
		Engine:             "MergeTree",
		ExpectedEngine:     "ReplacingMergeTree",
		SortingKey:         "cluster_id, api_group, api_version, resource, namespace, updated_at",
		ExpectedSortingKey: "cluster_id, api_group, api_version, resource, namespace, event",
		Problems:           []string{"engine=MergeTree want=ReplacingMergeTree"},
	}}}

	plan, err := PlanIngestionOffsetsRepair(status, SchemaOptions{Database: "ki"}, "20260516_130000")
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Needed || len(plan.Statements) != 3 {
		t.Fatalf("plan = %#v", plan)
	}
	joined := strings.Join(plan.Statements, "\n")
	for _, want := range []string{
		"CREATE TABLE IF NOT EXISTS `ki`.`ingestion_offsets_repair_20260516_130000`",
		"ENGINE = ReplacingMergeTree(updated_at)",
		"ORDER BY (cluster_id, api_group, api_version, resource, namespace, event)",
		"INSERT INTO `ki`.`ingestion_offsets_repair_20260516_130000`",
		"argMax(status, updated_at)",
		"GROUP BY cluster_id, api_group, api_version, resource, namespace, event",
		"RENAME TABLE `ki`.`ingestion_offsets` TO `ki`.`ingestion_offsets_backup_20260516_130000`",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("plan missing %q: %s", want, joined)
		}
	}
}

func TestPlanIngestionOffsetsRepairNoopWhenOK(t *testing.T) {
	status := SchemaStatusResult{Database: "ki", Tables: []TableSchemaStatus{{
		Name:   "ingestion_offsets",
		Exists: true,
		OK:     true,
	}}}

	plan, err := PlanIngestionOffsetsRepair(status, SchemaOptions{Database: "ki"}, "20260516_130000")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Needed || len(plan.Statements) != 0 || !strings.Contains(plan.Reason, "already matches") {
		t.Fatalf("plan = %#v", plan)
	}
}

func TestListRepairArtifactsAndPlanCleanup(t *testing.T) {
	var query string
	runner := queryRunnerFunc(func(ctx context.Context, q string) (QueryResult, error) {
		query = q
		return QueryResult{Data: []map[string]any{
			{"name": "ingestion_offsets_backup_20260516_195840", "engine": "MergeTree", "rows": "59015", "bytes": "1292433"},
			{"name": "ingestion_offsets_repair_20260516_195639", "engine": "ReplacingMergeTree", "rows": "0", "bytes": "0"},
			{"name": "ingestion_offsets_repair_20260516_200000", "engine": "ReplacingMergeTree", "rows": "12", "bytes": "100"},
		}}, nil
	})

	artifacts, err := ListRepairArtifacts(context.Background(), runner, SchemaOptions{Database: "ki"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(query, "FROM system.tables") || !strings.Contains(query, "ingestion_offsets_repair_") {
		t.Fatalf("query = %s", query)
	}
	if len(artifacts) != 3 {
		t.Fatalf("artifacts = %#v", artifacts)
	}
	if artifacts[0].Kind != "backup" || artifacts[0].DropEligible {
		t.Fatalf("backup artifact = %#v", artifacts[0])
	}
	if artifacts[1].Kind != "repair" || !artifacts[1].DropEligible {
		t.Fatalf("empty repair artifact = %#v", artifacts[1])
	}
	if artifacts[2].DropEligible {
		t.Fatalf("non-empty repair artifact = %#v", artifacts[2])
	}

	plan, err := PlanRepairArtifactCleanup(artifacts, SchemaOptions{Database: "ki"})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Statements) != 1 || !strings.Contains(plan.Statements[0], "DROP TABLE IF EXISTS `ki`.`ingestion_offsets_repair_20260516_195639`") {
		t.Fatalf("plan = %#v", plan)
	}
}

func TestStoreQuerySQLReturnsStorageResult(t *testing.T) {
	var body string
	var rawQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		body = string(data)
		rawQuery = r.URL.RawQuery
		_, _ = w.Write([]byte(`{"meta":[{"name":"name","type":"String"}],"data":[{"name":"api"},{"name":"worker"}],"rows":2,"rows_before_limit_at_least":2}`))
	}))
	defer server.Close()

	store := Store{Client: HTTPClient{Endpoint: server.URL, HTTPClient: server.Client()}, Database: "ki"}
	result, err := store.QuerySQL(context.Background(), storage.SQLQueryOptions{SQL: "select name from versions", MaxRows: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "FORMAT JSON") {
		t.Fatalf("query body = %s", body)
	}
	if !strings.Contains(rawQuery, "database=ki") {
		t.Fatalf("raw query = %s", rawQuery)
	}
	if len(result.Columns) != 1 || result.Columns[0] != "name" || result.RowCount != 1 || !result.Truncated {
		t.Fatalf("result = %#v", result)
	}
	if result.Rows[0]["name"] != "api" {
		t.Fatalf("rows = %#v", result.Rows)
	}
}

func TestValidateReadOnlyQueryRejectsMutations(t *testing.T) {
	allowed := []string{
		"select name from versions",
		"with recent as (select * from versions) select * from recent",
		"EXPLAIN indexes = 1 SELECT * FROM facts",
		"show tables",
		"describe versions",
		"select 'drop table versions' as text",
	}
	for _, query := range allowed {
		if err := validateReadOnlyQuery(query); err != nil {
			t.Fatalf("expected %q to be allowed: %v", query, err)
		}
	}

	rejected := []string{
		"drop table versions",
		"select * from versions; drop table versions",
		"insert into versions values (1)",
		"optimize table versions final",
		"system flush logs",
		"explain drop table versions",
	}
	for _, query := range rejected {
		if err := validateReadOnlyQuery(query); err == nil {
			t.Fatalf("expected %q to be rejected", query)
		}
	}
}

func TestStoreQuerySchemaReturnsClickHouseTables(t *testing.T) {
	var queries []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		query := string(data)
		queries = append(queries, query)
		switch {
		case strings.Contains(query, "FROM system.tables"):
			_, _ = w.Write([]byte(`{"data":[{"name":"versions","engine":"MergeTree"}],"rows":1}`))
		case strings.Contains(query, "FROM system.columns"):
			_, _ = w.Write([]byte(`{"data":[{"table":"versions","name":"object_id","type":"String"}],"rows":1}`))
		default:
			t.Fatalf("unexpected query: %s", query)
		}
	}))
	defer server.Close()

	store := Store{Client: HTTPClient{Endpoint: server.URL, HTTPClient: server.Client()}, Database: "ki"}
	schema, err := store.QuerySchema(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(queries) != 2 || len(schema.Tables) != 1 || schema.Tables[0].Name != "versions" || schema.Tables[0].Columns[0].Name != "object_id" {
		t.Fatalf("queries=%#v schema=%#v", queries, schema)
	}
	joinedNotes := strings.Join(schema.Notes, "\n")
	for _, want := range []string{"facts.ts", "Prefer sorted columns", "JSONExtractRaw", "do not use JSONExtract without an explicit return type"} {
		if !strings.Contains(joinedNotes, want) {
			t.Fatalf("schema notes missing %q: %#v", want, schema.Notes)
		}
	}
	var recipeNames string
	for _, recipe := range schema.Recipes {
		recipeNames += recipe.Name + "\n"
	}
	for _, want := range []string{"coverage_latest", "recent_fact_rollup", "recent_change_rollup", "container_resource_allocation_rollup", "raw_doc_field_profile", "object_proof_after_candidate"} {
		if !strings.Contains(recipeNames, want) {
			t.Fatalf("schema recipes missing %q: %#v", want, schema.Recipes)
		}
	}
}
