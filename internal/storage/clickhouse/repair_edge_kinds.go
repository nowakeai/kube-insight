package clickhouse

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

type EdgeKindRepairOptions struct {
	Database  string `json:"database"`
	ClusterID string `json:"clusterId,omitempty"`
	DryRun    bool   `json:"dryRun"`
}

type EdgeKindRepairReport struct {
	StartedAt     time.Time `json:"startedAt"`
	FinishedAt    time.Time `json:"finishedAt"`
	Database      string    `json:"database"`
	ClusterID     string    `json:"clusterId,omitempty"`
	DryRun        bool      `json:"dryRun"`
	SrcMissing    int64     `json:"srcMissing"`
	DstMissing    int64     `json:"dstMissing"`
	SrcRepairable int64     `json:"srcRepairable"`
	DstRepairable int64     `json:"dstRepairable"`
	Statements    []string  `json:"statements"`
	Applied       int       `json:"applied"`
}

func RepairEdgeKinds(ctx context.Context, runner QueryRunner, applier SchemaApplier, opts EdgeKindRepairOptions) (EdgeKindRepairReport, error) {
	if runner == nil {
		return EdgeKindRepairReport{}, fmt.Errorf("clickhouse query runner is required")
	}
	if opts.Database == "" {
		opts.Database = defaultDatabase
	}
	if err := (SchemaOptions{Database: opts.Database}).Validate(); err != nil {
		return EdgeKindRepairReport{}, err
	}
	report := EdgeKindRepairReport{
		StartedAt:  time.Now().UTC(),
		Database:   opts.Database,
		ClusterID:  opts.ClusterID,
		DryRun:     opts.DryRun,
		Statements: edgeKindRepairStatements(opts),
	}
	counts, err := runner.QueryJSON(ctx, edgeKindRepairCountQuery(opts))
	if err != nil {
		return EdgeKindRepairReport{}, err
	}
	if len(counts.Data) > 0 {
		row := counts.Data[0]
		report.SrcMissing = rowInt64(row, "src_missing")
		report.DstMissing = rowInt64(row, "dst_missing")
		report.SrcRepairable = rowInt64(row, "src_repairable")
		report.DstRepairable = rowInt64(row, "dst_repairable")
	}
	if !opts.DryRun && (report.SrcRepairable > 0 || report.DstRepairable > 0) {
		if applier == nil {
			return EdgeKindRepairReport{}, fmt.Errorf("clickhouse schema applier is required")
		}
		result, err := applier.ApplySchema(ctx, report.Statements)
		report.Applied = result.Applied
		if err != nil {
			return report, err
		}
	}
	report.FinishedAt = time.Now().UTC()
	return report, nil
}

func edgeKindRepairCountQuery(opts EdgeKindRepairOptions) string {
	srcExpr := edgeKindSQLExpression("src_id")
	dstExpr := edgeKindSQLExpression("dst_id")
	where := edgeKindRepairWhere(opts.ClusterID)
	return fmt.Sprintf(`SELECT
  countIf(src_kind = '') AS src_missing,
  countIf(dst_kind = '') AS dst_missing,
  countIf(src_kind = '' AND %s != '') AS src_repairable,
  countIf(dst_kind = '' AND %s != '') AS dst_repairable
FROM %s.edges
WHERE %s`, srcExpr, dstExpr, q(opts.Database), where)
}

func edgeKindRepairStatements(opts EdgeKindRepairOptions) []string {
	srcExpr := edgeKindSQLExpression("src_id")
	dstExpr := edgeKindSQLExpression("dst_id")
	where := edgeKindRepairWhere(opts.ClusterID)
	return []string{
		fmt.Sprintf("ALTER TABLE %s.edges UPDATE src_kind = %s WHERE src_kind = '' AND %s != '' AND %s", q(opts.Database), srcExpr, srcExpr, where),
		fmt.Sprintf("ALTER TABLE %s.edges UPDATE dst_kind = %s WHERE dst_kind = '' AND %s != '' AND %s", q(opts.Database), dstExpr, dstExpr, where),
	}
}

func edgeKindRepairWhere(clusterID string) string {
	if clusterID == "" {
		return "1"
	}
	return "cluster_id = " + quoteString(clusterID)
}

func edgeKindSQLExpression(idColumn string) string {
	segments := make([]string, 0, len(resourceKindBySegment))
	for resource := range resourceKindBySegment {
		segments = append(segments, resource)
	}
	sort.Strings(segments)
	parts := make([]string, 0, len(segments)*2+1)
	for _, resource := range segments {
		kind := resourceKindBySegment[resource]
		condition := fmt.Sprintf("(arrayElement(splitByChar('/', %s), 2) = %s OR arrayElement(splitByChar('/', %s), 3) = %s)", idColumn, quoteString(resource), idColumn, quoteString(resource))
		parts = append(parts, condition, quoteString(kind))
	}
	parts = append(parts, "''")
	return "multiIf(" + strings.Join(parts, ", ") + ")"
}

func rowInt64(row map[string]any, key string) int64 {
	switch value := row[key].(type) {
	case float64:
		return int64(value)
	case int64:
		return value
	case uint64:
		return int64(value)
	case jsonNumber:
		parsed, _ := value.Int64()
		return parsed
	case string:
		var parsed int64
		_, _ = fmt.Sscan(value, &parsed)
		return parsed
	default:
		return 0
	}
}

type jsonNumber interface {
	Int64() (int64, error)
}
