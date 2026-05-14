package sqlite

import (
	"context"
	"database/sql"

	"kube-insight/internal/core"
)

func putObservationTrace(ctx context.Context, tx *sql.Tx, clusterID, objectID int64, obs core.Observation, versionID int64, contentChanged bool) error {
	_, err := tx.ExecContext(ctx, `
insert into object_observations(
  cluster_id, object_id, observed_at, observation_type, resource_version, version_id, content_changed
)
values(?, ?, ?, ?, ?, ?, ?)`,
		clusterID, objectID, millis(obs.ObservedAt), string(obs.Type), nullable(obs.ResourceVersion), nullInt64(versionID), contentChanged)
	return err
}

func nullInt64(value int64) sql.NullInt64 {
	if value == 0 {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: value, Valid: true}
}
