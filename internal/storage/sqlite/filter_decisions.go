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
			decision.Outcome == filter.KeepModified || decision.Outcome == filter.DiscardChange || decision.Outcome == filter.DiscardResource,
			sql.NullString{String: string(meta), Valid: string(meta) != "null"},
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}
