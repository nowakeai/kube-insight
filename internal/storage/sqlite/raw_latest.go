package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"

	"kube-insight/internal/core"
)

func (s *Store) PutRawLatest(ctx context.Context, obs core.Observation) error {
	if obs.Ref.Name == "" {
		return nil
	}
	doc, err := json.Marshal(obs.Object)
	if err != nil {
		return err
	}
	docHash := digest(doc)
	observedAt := millis(obs.ObservedAt)
	deletedAt := sql.NullInt64{}
	if obs.Type == core.ObservationDeleted {
		deletedAt = sql.NullInt64{Int64: observedAt, Valid: true}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)

	clusterID, err := ensureCluster(ctx, tx, obs.Ref.ClusterID, observedAt)
	if err != nil {
		return err
	}
	_, kindID, err := ensureKind(ctx, tx, obs.Ref, observedAt, s.profileForResource)
	if err != nil {
		return err
	}
	objectRecord, err := ensureObjectRecord(ctx, tx, clusterID, kindID, obs.Ref, observedAt, deletedAt)
	if err != nil {
		return err
	}
	if obs.Type == core.ObservationDeleted {
		if err := deleteRawLatest(ctx, tx, objectRecord.ID); err != nil {
			return err
		}
		return tx.Commit()
	}
	if err := upsertRawLatest(ctx, tx, objectRecord.ID, clusterID, kindID, obs, observedAt, docHash, doc); err != nil {
		return err
	}
	return tx.Commit()
}

func deleteRawLatest(ctx context.Context, tx *sql.Tx, objectID int64) error {
	_, err := tx.ExecContext(ctx, `delete from latest_raw_index where object_id = ?`, objectID)
	return err
}

func (s *Store) DeleteRawLatestForDeletedObjects(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx, `
delete from latest_raw_index
where object_id in (
  select id
  from objects
  where deleted_at is not null
)`)
	if err != nil {
		return 0, err
	}
	rows, _ := res.RowsAffected()
	return rows, nil
}

func upsertRawLatest(ctx context.Context, tx *sql.Tx, objectID, clusterID, kindID int64, obs core.Observation, observedAt int64, docHash string, doc []byte) error {
	_, err := tx.ExecContext(ctx, `
insert into latest_raw_index(
  object_id, cluster_id, kind_id, namespace, name, uid, observed_at,
  observation_type, resource_version, generation, doc_hash, raw_size, doc
)
values(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
on conflict(object_id) do update set
  cluster_id = excluded.cluster_id,
  kind_id = excluded.kind_id,
  namespace = excluded.namespace,
  name = excluded.name,
  uid = excluded.uid,
  observed_at = excluded.observed_at,
  observation_type = excluded.observation_type,
  resource_version = excluded.resource_version,
  generation = excluded.generation,
  doc_hash = excluded.doc_hash,
  raw_size = excluded.raw_size,
  doc = excluded.doc`,
		objectID,
		clusterID,
		kindID,
		nullable(obs.Ref.Namespace),
		obs.Ref.Name,
		nullable(obs.Ref.UID),
		observedAt,
		string(obs.Type),
		nullable(obs.ResourceVersion),
		generation(obs.Object),
		docHash,
		len(doc),
		doc)
	return err
}
