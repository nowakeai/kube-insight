package clickhouse

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"kube-insight/internal/core"
	"kube-insight/internal/extractor"
)

const defaultServiceFactsBackfillBatchSize = 500

type ServiceFactsBackfillOptions struct {
	Database  string `json:"database"`
	ClusterID string `json:"clusterId,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	BatchSize int    `json:"batchSize"`
	DryRun    bool   `json:"dryRun"`
}

type ServiceFactsBackfillReport struct {
	StartedAt       time.Time `json:"startedAt"`
	FinishedAt      time.Time `json:"finishedAt"`
	Database        string    `json:"database"`
	DryRun          bool      `json:"dryRun"`
	Batches         int64     `json:"batches"`
	VersionsScanned int64     `json:"versionsScanned"`
	FactsInserted   int64     `json:"factsInserted"`
	ChangesInserted int64     `json:"changesInserted"`
}

type serviceBackfillCursor struct {
	ObjectID string
	Seq      uint64
}

func BackfillServiceFacts(ctx context.Context, runner QueryRunner, writer EvidenceWriter, opts ServiceFactsBackfillOptions) (ServiceFactsBackfillReport, error) {
	if runner == nil {
		return ServiceFactsBackfillReport{}, fmt.Errorf("clickhouse query runner is required")
	}
	if !opts.DryRun && writer == nil {
		return ServiceFactsBackfillReport{}, fmt.Errorf("clickhouse evidence writer is required")
	}
	if opts.Database == "" {
		opts.Database = defaultDatabase
	}
	if err := (SchemaOptions{Database: opts.Database}).Validate(); err != nil {
		return ServiceFactsBackfillReport{}, err
	}
	batchSize := opts.BatchSize
	if batchSize <= 0 {
		batchSize = defaultServiceFactsBackfillBatchSize
	}
	report := ServiceFactsBackfillReport{StartedAt: time.Now().UTC(), Database: opts.Database, DryRun: opts.DryRun}
	cursor := serviceBackfillCursor{}
	for {
		query := serviceFactsBackfillQuery(opts, cursor, batchSize)
		result, err := runner.QueryJSON(ctx, query)
		if err != nil {
			return ServiceFactsBackfillReport{}, err
		}
		if len(result.Data) == 0 {
			break
		}
		report.Batches++
		var facts []core.Fact
		var changes []core.Change
		var observations []core.Observation
		for _, row := range result.Data {
			obs, nextCursor, err := serviceBackfillObservation(row)
			if err != nil {
				return ServiceFactsBackfillReport{}, err
			}
			cursor = nextCursor
			report.VersionsScanned++
			evidence, err := (extractor.ServiceExtractor{}).Extract(ctx, obs)
			if err != nil {
				return ServiceFactsBackfillReport{}, err
			}
			if len(evidence.Facts) == 0 && len(evidence.Changes) == 0 {
				continue
			}
			observations = append(observations, obs)
			facts = append(facts, evidence.Facts...)
			changes = append(changes, evidence.Changes...)
		}
		report.FactsInserted += int64(len(facts))
		report.ChangesInserted += int64(len(changes))
		if opts.DryRun || (len(facts) == 0 && len(changes) == 0) {
			continue
		}
		batch, err := BuildEvidenceBatch(opts.Database, observations, facts, nil, changes)
		if err != nil {
			return ServiceFactsBackfillReport{}, err
		}
		if len(batch.Facts) > 0 {
			if err := writer.InsertRows(ctx, opts.Database, "facts", batch.Facts); err != nil {
				return ServiceFactsBackfillReport{}, err
			}
		}
		if len(batch.Changes) > 0 {
			if err := writer.InsertRows(ctx, opts.Database, "changes", batch.Changes); err != nil {
				return ServiceFactsBackfillReport{}, err
			}
		}
	}
	report.FinishedAt = time.Now().UTC()
	return report, nil
}

func serviceFactsBackfillQuery(opts ServiceFactsBackfillOptions, cursor serviceBackfillCursor, limit int) string {
	where := "v.kind = 'Service'"
	if opts.ClusterID != "" {
		where += " AND v.cluster_id = " + quoteString(opts.ClusterID)
	}
	if opts.Namespace != "" {
		where += " AND v.namespace = " + quoteString(opts.Namespace)
	}
	if cursor.ObjectID != "" {
		where += fmt.Sprintf(" AND (v.object_id > %s OR (v.object_id = %s AND v.seq > %d))", quoteString(cursor.ObjectID), quoteString(cursor.ObjectID), cursor.Seq)
	}
	return fmt.Sprintf(`SELECT
  v.cluster_id AS cluster_id,
  v.object_id,
  v.seq,
  v.observed_at AS observed_at,
  if(o.observation_type = '', 'MODIFIED', o.observation_type) AS observation_type,
  v.api_group,
  v.api_version,
  v.resource,
  v.kind AS kind,
  v.namespace AS namespace,
  v.name AS name,
  v.uid AS uid,
  v.resource_version,
  v.doc
FROM %s.versions v
LEFT JOIN (
  SELECT cluster_id, kind, namespace, name, uid, observed_at, any(observation_type) AS observation_type
  FROM %s.observations
  GROUP BY cluster_id, kind, namespace, name, uid, observed_at
) o ON o.cluster_id = v.cluster_id
  AND o.kind = v.kind
  AND o.namespace = v.namespace
  AND o.name = v.name
  AND o.uid = v.uid
  AND o.observed_at = v.observed_at
LEFT JOIN (
  SELECT object_id AS fact_object_id, ts AS fact_ts
  FROM %s.facts
  WHERE startsWith(fact_key, 'service.')
  GROUP BY fact_object_id, fact_ts
) sf ON sf.fact_object_id = v.object_id AND sf.fact_ts = v.observed_at
WHERE %s
  AND sf.fact_object_id = ''
ORDER BY v.object_id, v.seq
LIMIT %d`, q(opts.Database), q(opts.Database), q(opts.Database), where, limit)
}

func serviceBackfillObservation(row map[string]any) (core.Observation, serviceBackfillCursor, error) {
	seq, err := rowUint64(row, "seq")
	if err != nil {
		return core.Observation{}, serviceBackfillCursor{}, err
	}
	observedAt, err := parseClickHouseDateTime(rowString(row, "observed_at"))
	if err != nil {
		return core.Observation{}, serviceBackfillCursor{}, err
	}
	var object map[string]any
	if err := json.Unmarshal([]byte(rowString(row, "doc")), &object); err != nil {
		return core.Observation{}, serviceBackfillCursor{}, err
	}
	obs := core.Observation{
		Type:            observationTypeFromClickHouse(rowString(row, "observation_type")),
		ObservedAt:      observedAt,
		ResourceVersion: rowString(row, "resource_version"),
		Ref: core.ResourceRef{
			ClusterID: rowString(row, "cluster_id"),
			Group:     rowString(row, "api_group"),
			Version:   rowString(row, "api_version"),
			Resource:  rowString(row, "resource"),
			Kind:      rowString(row, "kind"),
			Namespace: rowString(row, "namespace"),
			Name:      rowString(row, "name"),
			UID:       rowString(row, "uid"),
		},
		Object: object,
	}
	return obs, serviceBackfillCursor{ObjectID: rowString(row, "object_id"), Seq: seq}, nil
}

func observationTypeFromClickHouse(value string) core.ObservationType {
	switch value {
	case string(core.ObservationAdded):
		return core.ObservationAdded
	case string(core.ObservationModified):
		return core.ObservationModified
	case string(core.ObservationDeleted):
		return core.ObservationDeleted
	case string(core.ObservationBookmark):
		return core.ObservationBookmark
	default:
		return core.ObservationModified
	}
}

func rowString(row map[string]any, key string) string {
	return stringValue(row[key])
}

func rowUint64(row map[string]any, key string) (uint64, error) {
	value := row[key]
	switch typed := value.(type) {
	case float64:
		return uint64(typed), nil
	case string:
		return strconv.ParseUint(typed, 10, 64)
	default:
		return 0, fmt.Errorf("row field %s has unsupported uint value %T", key, value)
	}
}

func parseClickHouseDateTime(value string) (time.Time, error) {
	for _, layout := range []string{
		time.RFC3339Nano,
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05.000",
		"2006-01-02 15:04:05",
	} {
		if parsed, err := time.ParseInLocation(layout, value, time.UTC); err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("parse ClickHouse DateTime %q", value)
}
