package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"kube-insight/internal/core"
	"kube-insight/internal/filter"
	"kube-insight/internal/logging"
)

const filterDecisionRollupBucket = time.Hour

type FilterDecisionRollupReport struct {
	StartedAt       time.Time `json:"startedAt"`
	FinishedAt      time.Time `json:"finishedAt"`
	RowsRolledUp    int64     `json:"rowsRolledUp"`
	RollupsAffected int64     `json:"rollupsAffected"`
}

func (s *Store) PutFilterDecisions(ctx context.Context, obs core.Observation, decisions []filter.Decision) error {
	if len(decisions) == 0 {
		return nil
	}
	detailed, rollups := persistedFilterDecisions(decisions)
	if len(detailed) == 0 && len(rollups) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)

	for _, decision := range detailed {
		meta, err := json.Marshal(decision.Meta)
		if err != nil {
			return err
		}
		filterName, _ := decision.Meta["filter"].(string)
		if filterName == "" {
			filterName = "unknown"
		}
		if _, err := tx.ExecContext(ctx, `
insert into filter_decisions(
  ts, cluster_name, api_group, api_version, resource, kind, namespace, name, uid,
  resource_version, observation_type, filter_name, outcome, reason, destructive, meta
) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			millis(obs.ObservedAt),
			obs.Ref.ClusterID,
			obs.Ref.Group,
			obs.Ref.Version,
			obs.Ref.Resource,
			obs.Ref.Kind,
			nullable(obs.Ref.Namespace),
			obs.Ref.Name,
			nullable(obs.Ref.UID),
			nullable(obs.ResourceVersion),
			string(obs.Type),
			filterName,
			string(decision.Outcome),
			decision.Reason,
			isDestructiveFilterDecision(decision),
			sql.NullString{String: string(meta), Valid: string(meta) != "null"},
		); err != nil {
			return err
		}
	}
	for _, decision := range rollups {
		filterName, _ := decision.Meta["filter"].(string)
		if filterName == "" {
			filterName = "unknown"
		}
		bucketSeconds := int64(filterDecisionRollupBucket / time.Second)
		observedAt := millis(obs.ObservedAt)
		bucketStart := observedAt / (bucketSeconds * 1000) * (bucketSeconds * 1000)
		if _, err := tx.ExecContext(ctx, `
insert into filter_decision_rollups(
  bucket_start, bucket_seconds, cluster_name, api_group, api_version, resource, kind,
  observation_type, filter_name, outcome, reason, destructive, count, first_seen, last_seen
) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?)
on conflict(
  bucket_start, bucket_seconds, cluster_name, api_group, api_version, resource, kind,
  observation_type, filter_name, outcome, reason, destructive
) do update set
  count = count + 1,
  first_seen = min(first_seen, excluded.first_seen),
  last_seen = max(last_seen, excluded.last_seen)`,
			bucketStart,
			bucketSeconds,
			obs.Ref.ClusterID,
			obs.Ref.Group,
			obs.Ref.Version,
			obs.Ref.Resource,
			obs.Ref.Kind,
			string(obs.Type),
			filterName,
			string(decision.Outcome),
			decision.Reason,
			isDestructiveFilterDecision(decision),
			observedAt,
			observedAt,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func persistedFilterDecisions(decisions []filter.Decision) ([]filter.Decision, []filter.Decision) {
	detailed := make([]filter.Decision, 0, len(decisions))
	rollups := make([]filter.Decision, 0, len(decisions))
	for _, decision := range decisions {
		filterName, _ := decision.Meta["filter"].(string)
		switch {
		case shouldPersistFilterDecisionDetail(filterName, decision):
			detailed = append(detailed, decision)
		case shouldRollupFilterDecision(filterName, decision):
			rollups = append(rollups, decision)
		}
	}
	return detailed, rollups
}

func shouldPersistFilterDecisionDetail(filterName string, decision filter.Decision) bool {
	switch decision.Outcome {
	case filter.DiscardChange, filter.DiscardResource:
		return true
	case filter.KeepModified:
		return isSecretPayloadRemoval(filterName, decision) || !isKnownRollupFilterDecision(filterName, decision)
	default:
		return false
	}
}

func shouldRollupFilterDecision(filterName string, decision filter.Decision) bool {
	return decision.Outcome == filter.KeepModified && isKnownRollupFilterDecision(filterName, decision)
}

func isKnownRollupFilterDecision(filterName string, decision filter.Decision) bool {
	switch decision.Reason {
	case "managed_fields_removed", "resource_version_removed", "condition_timestamps_removed", "leader_annotation_removed":
		return true
	}
	switch filterName {
	case "metadata_normalization_filter",
		"resource_version_normalization_filter",
		"status_condition_timestamp_normalization_filter",
		"leader_election_configmap_normalization_filter":
		return true
	default:
		return false
	}
}

func isDestructiveFilterDecision(decision filter.Decision) bool {
	switch decision.Outcome {
	case filter.DiscardChange, filter.DiscardResource:
		return true
	case filter.KeepModified:
		filterName, _ := decision.Meta["filter"].(string)
		return isSecretPayloadRemoval(filterName, decision)
	default:
		return false
	}
}

func isSecretPayloadRemoval(filterName string, decision filter.Decision) bool {
	if decision.Reason == "secret_payload_removed" {
		return true
	}
	if filterName == "secret_redaction_filter" || filterName == "secret_metadata_only" {
		if value, _ := decision.Meta["secretPayloadRemoved"].(bool); value {
			return true
		}
		if count, ok := numericMeta(decision.Meta["redactedFields"]); ok && count > 0 {
			return true
		}
	}
	return false
}

func numericMeta(value any) (float64, bool) {
	switch typed := value.(type) {
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case float64:
		return typed, true
	default:
		return 0, false
	}
}

func (s *Store) RollupRoutineFilterDecisions(ctx context.Context) (FilterDecisionRollupReport, error) {
	report := FilterDecisionRollupReport{StartedAt: time.Now().UTC()}
	logger := logging.FromContext(ctx).With("component", "filter-rollup")
	logger.Info("starting routine filter decision rollup")
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return FilterDecisionRollupReport{}, err
	}
	defer rollback(tx)

	logger.Info("counting routine filter decisions")
	if err := tx.QueryRowContext(ctx, `select count(*) from filter_decisions where `+routineFilterDecisionSQL()).Scan(&report.RowsRolledUp); err != nil {
		return FilterDecisionRollupReport{}, err
	}
	logger.Info("counted routine filter decisions", "rows", report.RowsRolledUp)
	if report.RowsRolledUp == 0 {
		report.FinishedAt = time.Now().UTC()
		logger.Info("routine filter decision rollup skipped", "reason", "no_rows")
		return report, tx.Rollback()
	}
	logger.Info("inserting routine filter decision rollups")
	if _, err := tx.ExecContext(ctx, `
insert into filter_decision_rollups(
  bucket_start, bucket_seconds, cluster_name, api_group, api_version, resource, kind,
  observation_type, filter_name, outcome, reason, destructive, count, first_seen, last_seen
)
select
  (ts / 3600000) * 3600000 as bucket_start,
  3600 as bucket_seconds,
  cluster_name,
  api_group,
  api_version,
  resource,
  kind,
  observation_type,
  filter_name,
  outcome,
  reason,
  0 as destructive,
  count(*) as count,
  min(ts) as first_seen,
  max(ts) as last_seen
from filter_decisions
where `+routineFilterDecisionSQL()+`
group by bucket_start, cluster_name, api_group, api_version, resource, kind, observation_type, filter_name, outcome, reason
on conflict(
  bucket_start, bucket_seconds, cluster_name, api_group, api_version, resource, kind,
  observation_type, filter_name, outcome, reason, destructive
) do update set
  count = count + excluded.count,
  first_seen = min(first_seen, excluded.first_seen),
  last_seen = max(last_seen, excluded.last_seen)`); err != nil {
		return FilterDecisionRollupReport{}, err
	}
	logger.Info("counting filter decision rollup rows")
	if err := tx.QueryRowContext(ctx, `select count(*) from filter_decision_rollups`).Scan(&report.RollupsAffected); err != nil {
		return FilterDecisionRollupReport{}, err
	}
	logger.Info("deleting rolled up routine filter decision details", "rows", report.RowsRolledUp)
	if _, err := tx.ExecContext(ctx, `delete from filter_decisions where `+routineFilterDecisionSQL()); err != nil {
		return FilterDecisionRollupReport{}, err
	}
	logger.Info("committing routine filter decision rollup")
	if err := tx.Commit(); err != nil {
		return FilterDecisionRollupReport{}, err
	}
	report.FinishedAt = time.Now().UTC()
	logger.Info("routine filter decision rollup completed", "rowsRolledUp", report.RowsRolledUp, "rollupsAffected", report.RollupsAffected, "duration", report.FinishedAt.Sub(report.StartedAt).String())
	return report, nil
}

func routineFilterDecisionSQL() string {
	return `outcome = 'keep_modified' and reason in (
  'managed_fields_removed',
  'resource_version_removed',
  'condition_timestamps_removed',
  'leader_annotation_removed'
)`
}
