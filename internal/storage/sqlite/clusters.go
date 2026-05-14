package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"kube-insight/internal/logging"
	"kube-insight/internal/storage"
)

func (s *Store) UpsertCluster(ctx context.Context, cluster storage.ClusterRecord) error {
	if cluster.Name == "" {
		return fmt.Errorf("cluster name is required")
	}
	now := millis(time.Now().UTC())
	_, err := s.db.ExecContext(ctx, `
insert into clusters(name, uid, source, created_at)
values (?, nullif(?, ''), nullif(?, ''), ?)
on conflict(name) do update set
  uid = coalesce(nullif(excluded.uid, ''), clusters.uid),
  source = coalesce(nullif(excluded.source, ''), clusters.source)`,
		cluster.Name, cluster.UID, cluster.Source, now)
	if err == nil {
		logging.FromContext(ctx).With("component", "storage").Debug("upserted cluster", "cluster", cluster.Name, "uid", cluster.UID, "source", cluster.Source)
	}
	return err
}

func (s *Store) ListClusters(ctx context.Context) ([]storage.ClusterRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
select
  c.name,
  coalesce(c.uid, ''),
  coalesce(c.source, ''),
  c.created_at,
  count(distinct o.id) as objects,
  count(distinct v.id) as versions,
  count(distinct li.object_id) as latest
from clusters c
left join objects o on o.cluster_id = c.id
left join versions v on v.object_id = o.id
left join latest_index li on li.cluster_id = c.id
group by c.id
order by c.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []storage.ClusterRecord{}
	for rows.Next() {
		var cluster storage.ClusterRecord
		var createdAt int64
		if err := rows.Scan(&cluster.Name, &cluster.UID, &cluster.Source, &createdAt, &cluster.Objects, &cluster.Versions, &cluster.Latest); err != nil {
			return nil, err
		}
		cluster.CreatedAt = time.UnixMilli(createdAt)
		out = append(out, cluster)
	}
	return out, rows.Err()
}

func (s *Store) DeleteCluster(ctx context.Context, name string) (storage.ClusterRecord, error) {
	if name == "" {
		return storage.ClusterRecord{}, fmt.Errorf("cluster name is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return storage.ClusterRecord{}, err
	}
	defer rollback(tx)

	cluster, dbID, err := clusterByName(ctx, tx, name)
	if err != nil {
		return storage.ClusterRecord{}, err
	}
	if _, err := tx.ExecContext(ctx, `delete from object_edges where cluster_id = ?`, dbID); err != nil {
		return cluster, err
	}
	if _, err := tx.ExecContext(ctx, `delete from object_facts where cluster_id = ?`, dbID); err != nil {
		return cluster, err
	}
	if _, err := tx.ExecContext(ctx, `delete from object_changes where cluster_id = ?`, dbID); err != nil {
		return cluster, err
	}
	if _, err := tx.ExecContext(ctx, `delete from latest_index where cluster_id = ?`, dbID); err != nil {
		return cluster, err
	}
	if _, err := tx.ExecContext(ctx, `delete from ingestion_offsets where cluster_id = ?`, dbID); err != nil {
		return cluster, err
	}
	if _, err := tx.ExecContext(ctx, `delete from maintenance_runs where cluster_id = ?`, dbID); err != nil {
		return cluster, err
	}
	if _, err := tx.ExecContext(ctx, `delete from object_observations where cluster_id = ?`, dbID); err != nil {
		return cluster, err
	}
	if _, err := tx.ExecContext(ctx, `
delete from versions
where object_id in (select id from objects where cluster_id = ?)`, dbID); err != nil {
		return cluster, err
	}
	if _, err := tx.ExecContext(ctx, `delete from objects where cluster_id = ?`, dbID); err != nil {
		return cluster, err
	}
	if _, err := tx.ExecContext(ctx, `delete from filter_decisions where cluster_name = ?`, name); err != nil {
		return cluster, err
	}
	if _, err := tx.ExecContext(ctx, `delete from clusters where id = ?`, dbID); err != nil {
		return cluster, err
	}
	if err := tx.Commit(); err != nil {
		return cluster, err
	}
	logging.FromContext(ctx).With("component", "storage").Info("deleted cluster", "cluster", cluster.Name, "objects", cluster.Objects, "versions", cluster.Versions)
	return cluster, nil
}

func clusterByName(ctx context.Context, tx *sql.Tx, name string) (storage.ClusterRecord, int64, error) {
	var cluster storage.ClusterRecord
	var id int64
	var createdAt int64
	err := tx.QueryRowContext(ctx, `
select
  c.id,
  c.name,
  coalesce(c.uid, ''),
  coalesce(c.source, ''),
  c.created_at,
  (select count(*) from objects where cluster_id = c.id),
  (select count(*) from versions v join objects o on o.id = v.object_id where o.cluster_id = c.id),
  (select count(*) from latest_index where cluster_id = c.id)
from clusters c
where c.name = ?`, name).Scan(
		&id,
		&cluster.Name,
		&cluster.UID,
		&cluster.Source,
		&createdAt,
		&cluster.Objects,
		&cluster.Versions,
		&cluster.Latest,
	)
	if err == sql.ErrNoRows {
		return storage.ClusterRecord{}, 0, fmt.Errorf("cluster %q not found", name)
	}
	if err != nil {
		return storage.ClusterRecord{}, 0, err
	}
	cluster.CreatedAt = time.UnixMilli(createdAt)
	return cluster, id, nil
}
