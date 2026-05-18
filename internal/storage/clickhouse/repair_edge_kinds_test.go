package clickhouse

import (
	"context"
	"strings"
	"testing"
)

type edgeKindRepairFakeClient struct {
	query      string
	statements []string
}

func (c *edgeKindRepairFakeClient) QueryJSON(_ context.Context, query string) (QueryResult, error) {
	c.query = query
	return QueryResult{Data: []map[string]any{{
		"src_missing":    "2",
		"dst_missing":    "5",
		"src_repairable": "1",
		"dst_repairable": "4",
	}}}, nil
}

func (c *edgeKindRepairFakeClient) ApplySchema(_ context.Context, statements []string) (ApplyResult, error) {
	c.statements = statements
	return ApplyResult{Statements: len(statements), Applied: len(statements)}, nil
}

func TestRepairEdgeKindsDryRunPlansMutations(t *testing.T) {
	client := &edgeKindRepairFakeClient{}
	report, err := RepairEdgeKinds(context.Background(), client, client, EdgeKindRepairOptions{
		Database:  "ki",
		ClusterID: "c1",
		DryRun:    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.SrcMissing != 2 || report.DstRepairable != 4 || report.Applied != 0 {
		t.Fatalf("report = %#v", report)
	}
	if len(client.statements) != 0 {
		t.Fatalf("dry-run applied statements = %#v", client.statements)
	}
	for _, want := range []string{"FROM `ki`.edges", "cluster_id = 'c1'", "src_repairable", "dst_repairable"} {
		if !strings.Contains(client.query, want) {
			t.Fatalf("query missing %q: %s", want, client.query)
		}
	}
	if len(report.Statements) != 2 || !strings.Contains(report.Statements[1], "UPDATE dst_kind = multiIf") || !strings.Contains(report.Statements[1], "'secrets'") || !strings.Contains(report.Statements[1], "'Secret'") {
		t.Fatalf("statements = %#v", report.Statements)
	}
}

func TestRepairEdgeKindsApplyRunsPlannedMutations(t *testing.T) {
	client := &edgeKindRepairFakeClient{}
	report, err := RepairEdgeKinds(context.Background(), client, client, EdgeKindRepairOptions{
		Database: "ki",
		DryRun:   false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Applied != 2 || len(client.statements) != 2 {
		t.Fatalf("report = %#v statements = %#v", report, client.statements)
	}
	if !strings.Contains(client.statements[0], "WHERE src_kind = ''") || !strings.Contains(client.statements[1], "WHERE dst_kind = ''") {
		t.Fatalf("statements = %#v", client.statements)
	}
}
