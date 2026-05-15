package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"

	"kube-insight/internal/core"
	"kube-insight/internal/filter"
)

func (s *Store) PutFilterDecisions(ctx context.Context, obs core.Observation, decisions []filter.Decision) error {
	if len(decisions) == 0 {
		return nil
	}
	decisions = persistedFilterDecisions(decisions)
	if len(decisions) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)

	for _, decision := range decisions {
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
	return tx.Commit()
}

func persistedFilterDecisions(decisions []filter.Decision) []filter.Decision {
	out := make([]filter.Decision, 0, len(decisions))
	for _, decision := range decisions {
		filterName, _ := decision.Meta["filter"].(string)
		if shouldPersistFilterDecision(filterName, decision) {
			out = append(out, decision)
		}
	}
	return out
}

func shouldPersistFilterDecision(filterName string, decision filter.Decision) bool {
	switch decision.Outcome {
	case filter.DiscardChange, filter.DiscardResource:
		return true
	case filter.KeepModified:
		return isSecretPayloadRemoval(filterName, decision)
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
