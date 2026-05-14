package sqlite

import (
	"context"
	"database/sql"
	"time"

	"kube-insight/internal/resourceprofile"
	"kube-insight/internal/storage"
)

func (s *Store) UpsertIngestionOffset(ctx context.Context, offset storage.IngestionOffset) error {
	if offset.ClusterID == "" || offset.Resource.Resource == "" {
		return nil
	}
	if offset.Status == "" {
		offset.Status = "ok"
	}
	if offset.At.IsZero() {
		offset.At = time.Now()
	}
	now := millis(offset.At)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)

	clusterID, err := ensureCluster(ctx, tx, offset.ClusterID, now)
	if err != nil {
		return err
	}
	apiResourceID, err := upsertAPIResource(ctx, tx, offset.Resource, now)
	if err != nil {
		return err
	}
	if apiResourceID == 0 {
		return nil
	}
	if err := upsertProcessingProfile(ctx, tx, apiResourceID, resourceprofile.ForResource(offset.Resource)); err != nil {
		return err
	}

	listAt, watchAt, bookmarkAt := offsetTimes(offset.Event, now)
	if _, err := tx.ExecContext(ctx, `
insert into ingestion_offsets(
  cluster_id, api_resource_id, namespace, resource_version,
  last_list_at, last_watch_at, last_bookmark_at, status, error, updated_at
) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
on conflict(cluster_id, api_resource_id, namespace) do update set
  resource_version = excluded.resource_version,
  last_list_at = coalesce(excluded.last_list_at, ingestion_offsets.last_list_at),
  last_watch_at = coalesce(excluded.last_watch_at, ingestion_offsets.last_watch_at),
  last_bookmark_at = coalesce(excluded.last_bookmark_at, ingestion_offsets.last_bookmark_at),
  status = excluded.status,
  error = excluded.error,
  updated_at = excluded.updated_at`,
		clusterID,
		apiResourceID,
		offset.Namespace,
		offset.ResourceVersion,
		listAt,
		watchAt,
		bookmarkAt,
		offset.Status,
		nullable(offset.Error),
		now,
	); err != nil {
		return err
	}
	return tx.Commit()
}

func offsetTimes(event storage.OffsetEvent, now int64) (sql.NullInt64, sql.NullInt64, sql.NullInt64) {
	value := sql.NullInt64{Int64: now, Valid: true}
	switch event {
	case storage.OffsetEventList:
		return value, sql.NullInt64{}, sql.NullInt64{}
	case storage.OffsetEventBookmark:
		return sql.NullInt64{}, sql.NullInt64{}, value
	default:
		return sql.NullInt64{}, value, sql.NullInt64{}
	}
}
