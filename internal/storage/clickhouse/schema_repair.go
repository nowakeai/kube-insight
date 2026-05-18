package clickhouse

import (
	"context"
	"fmt"
	"strings"
)

const ingestionOffsetsTable = "ingestion_offsets"

type SchemaRepairPlan struct {
	Database    string   `json:"database"`
	Needed      bool     `json:"needed"`
	Table       string   `json:"table,omitempty"`
	NewTable    string   `json:"newTable,omitempty"`
	BackupTable string   `json:"backupTable,omitempty"`
	Reason      string   `json:"reason,omitempty"`
	Statements  []string `json:"statements,omitempty"`
}

func PlanIngestionOffsetsRepair(status SchemaStatusResult, opts SchemaOptions, suffix string) (SchemaRepairPlan, error) {
	if err := opts.Validate(); err != nil {
		return SchemaRepairPlan{}, err
	}
	opts = opts.normalized()
	plan := SchemaRepairPlan{Database: opts.Database, Table: ingestionOffsetsTable}
	table, ok := schemaTableStatus(status, ingestionOffsetsTable)
	if !ok || !table.Exists {
		plan.Reason = "ingestion_offsets table is missing; run clickhouse init instead"
		return plan, nil
	}
	if table.OK {
		plan.Reason = "ingestion_offsets schema already matches expected engine and sorting key"
		return plan, nil
	}
	if suffix == "" {
		return SchemaRepairPlan{}, fmt.Errorf("repair suffix is required")
	}
	newTable := "ingestion_offsets_repair_" + suffix
	backupTable := "ingestion_offsets_backup_" + suffix
	for label, value := range map[string]string{"newTable": newTable, "backupTable": backupTable} {
		if !identifierPattern.MatchString(value) {
			return SchemaRepairPlan{}, fmt.Errorf("%s %q is not a valid ClickHouse identifier", label, value)
		}
	}
	plan.Needed = true
	plan.NewTable = newTable
	plan.BackupTable = backupTable
	plan.Reason = strings.Join(table.Problems, "; ")
	plan.Statements = []string{
		createIngestionOffsetsNamed(opts, newTable),
		copyLatestIngestionOffsets(opts.Database, ingestionOffsetsTable, newTable),
		fmt.Sprintf("RENAME TABLE %s.%s TO %s.%s, %s.%s TO %s.%s",
			q(opts.Database), q(ingestionOffsetsTable),
			q(opts.Database), q(backupTable),
			q(opts.Database), q(newTable),
			q(opts.Database), q(ingestionOffsetsTable),
		),
	}
	return plan, nil
}

func schemaTableStatus(status SchemaStatusResult, name string) (TableSchemaStatus, bool) {
	for _, table := range status.Tables {
		if table.Name == name {
			return table, true
		}
	}
	return TableSchemaStatus{}, false
}

func createIngestionOffsetsNamed(opts SchemaOptions, table string) string {
	statement := createIngestionOffsets(opts)
	from := fmt.Sprintf("%s.%s", q(opts.Database), ingestionOffsetsTable)
	to := fmt.Sprintf("%s.%s", q(opts.Database), q(table))
	return strings.Replace(statement, from, to, 1)
}

func copyLatestIngestionOffsets(database, fromTable, toTable string) string {
	return fmt.Sprintf(`INSERT INTO %s.%s
SELECT
    cluster_id,
    api_group,
    api_version,
    resource,
    kind,
    namespaced,
    namespace,
    resource_version,
    event,
    status,
    error,
    last_list_at,
    last_watch_at,
    last_bookmark_at,
    latest_updated_at AS updated_at
FROM (
    SELECT
        cluster_id,
        api_group,
        api_version,
        resource,
        argMax(kind, updated_at) AS kind,
        argMax(namespaced, updated_at) AS namespaced,
        namespace,
        argMax(resource_version, updated_at) AS resource_version,
        event,
        argMax(status, updated_at) AS status,
        argMax(error, updated_at) AS error,
        argMax(last_list_at, updated_at) AS last_list_at,
        argMax(last_watch_at, updated_at) AS last_watch_at,
        argMax(last_bookmark_at, updated_at) AS last_bookmark_at,
        max(updated_at) AS latest_updated_at
    FROM %s.%s
    GROUP BY cluster_id, api_group, api_version, resource, namespace, event
)`,
		q(database), q(toTable),
		q(database), q(fromTable),
	)
}

type RepairArtifact struct {
	Name         string `json:"name"`
	Kind         string `json:"kind"`
	Engine       string `json:"engine"`
	Rows         int64  `json:"rows"`
	Bytes        int64  `json:"bytes"`
	DropEligible bool   `json:"dropEligible"`
}

type RepairArtifactCleanupPlan struct {
	Database   string           `json:"database"`
	Artifacts  []RepairArtifact `json:"artifacts"`
	Statements []string         `json:"statements,omitempty"`
}

func ListRepairArtifacts(ctx context.Context, runner QueryRunner, opts SchemaOptions) ([]RepairArtifact, error) {
	if runner == nil {
		return nil, fmt.Errorf("clickhouse query runner is required")
	}
	if err := opts.Validate(); err != nil {
		return nil, err
	}
	opts = opts.normalized()
	query := fmt.Sprintf(`SELECT name, engine, coalesce(total_rows, 0) AS rows, coalesce(total_bytes, 0) AS bytes
FROM system.tables
WHERE database = %s
  AND (startsWith(name, 'ingestion_offsets_repair_') OR startsWith(name, 'ingestion_offsets_backup_'))
ORDER BY name`, quoteString(opts.Database))
	result, err := runner.QueryJSON(ctx, query)
	if err != nil {
		return nil, err
	}
	artifacts := make([]RepairArtifact, 0, len(result.Data))
	for _, row := range result.Data {
		artifact := RepairArtifact{
			Name:   stringValue(row["name"]),
			Engine: stringValue(row["engine"]),
			Rows:   int64Value(row["rows"]),
			Bytes:  int64Value(row["bytes"]),
		}
		switch {
		case strings.HasPrefix(artifact.Name, "ingestion_offsets_repair_"):
			artifact.Kind = "repair"
			artifact.DropEligible = artifact.Rows == 0
		case strings.HasPrefix(artifact.Name, "ingestion_offsets_backup_"):
			artifact.Kind = "backup"
		}
		artifacts = append(artifacts, artifact)
	}
	return artifacts, nil
}

func PlanRepairArtifactCleanup(artifacts []RepairArtifact, opts SchemaOptions) (RepairArtifactCleanupPlan, error) {
	if err := opts.Validate(); err != nil {
		return RepairArtifactCleanupPlan{}, err
	}
	opts = opts.normalized()
	plan := RepairArtifactCleanupPlan{Database: opts.Database, Artifacts: artifacts}
	for _, artifact := range artifacts {
		if !artifact.DropEligible {
			continue
		}
		if !strings.HasPrefix(artifact.Name, "ingestion_offsets_repair_") || artifact.Rows != 0 {
			continue
		}
		if !identifierPattern.MatchString(artifact.Name) {
			return RepairArtifactCleanupPlan{}, fmt.Errorf("repair artifact %q is not a valid ClickHouse identifier", artifact.Name)
		}
		plan.Statements = append(plan.Statements, fmt.Sprintf("DROP TABLE IF EXISTS %s.%s", q(opts.Database), q(artifact.Name)))
	}
	return plan, nil
}
