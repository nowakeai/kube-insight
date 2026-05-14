package sqlite

import (
	"context"
	"database/sql"
)

func (s *Store) migrate(ctx context.Context) error {
	hasDoc, err := latestIndexHasDocColumnDB(ctx, s.db)
	if err != nil {
		return err
	}
	if !hasDoc {
		return s.backfillObjectObservations(ctx)
	}
	if err := s.migrateLatestIndexWithoutDoc(ctx); err != nil {
		return err
	}
	return s.backfillObjectObservations(ctx)
}

func latestIndexHasDocColumnDB(ctx context.Context, db *sql.DB) (bool, error) {
	rows, err := db.QueryContext(ctx, `pragma table_info(latest_index)`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name string
		var typ string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return false, err
		}
		if name == "doc" {
			return true, nil
		}
	}
	return false, rows.Err()
}

func (s *Store) migrateLatestIndexWithoutDoc(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)

	for _, stmt := range []string{
		`drop view if exists latest_documents`,
		`create table latest_index_next (
  object_id integer primary key references objects(id),
  cluster_id integer not null references clusters(id),
  kind_id integer not null references object_kinds(id),
  namespace text,
  name text not null,
  uid text,
  latest_version_id integer not null,
  observed_at integer not null
)`,
		`insert into latest_index_next(
  object_id, cluster_id, kind_id, namespace, name, uid, latest_version_id, observed_at
)
select object_id, cluster_id, kind_id, namespace, name, uid, latest_version_id, observed_at
from latest_index`,
		`drop table latest_index`,
		`alter table latest_index_next rename to latest_index`,
		`create index if not exists latest_kind_ns_name_idx
on latest_index(cluster_id, kind_id, namespace, name)`,
		`create view if not exists latest_documents as
select
  li.object_id,
  li.cluster_id,
  li.kind_id,
  li.namespace,
  li.name,
  li.uid,
  li.latest_version_id,
  li.observed_at,
  cast(b.data as text) as doc
from latest_index li
join versions v on v.id = li.latest_version_id
join blobs b on b.digest = v.blob_ref
where b.codec = 'identity'`,
	} {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) backfillObjectObservations(ctx context.Context) error {
	var observations, versions int64
	if err := s.db.QueryRowContext(ctx, `select count(*) from object_observations`).Scan(&observations); err != nil {
		return err
	}
	if observations > 0 {
		return nil
	}
	if err := s.db.QueryRowContext(ctx, `select count(*) from versions`).Scan(&versions); err != nil {
		return err
	}
	if versions == 0 {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `
insert into object_observations(
  cluster_id, object_id, observed_at, observation_type, resource_version, version_id, content_changed
)
select
  o.cluster_id,
  v.object_id,
  v.observed_at,
  'modified',
  v.resource_version,
  (
    select canonical.id
    from versions canonical
    where canonical.object_id = v.object_id
      and canonical.blob_ref = v.blob_ref
      and canonical.seq <= v.seq
      and not exists (
        select 1
        from versions breaker
        where breaker.object_id = v.object_id
          and breaker.seq > canonical.seq
          and breaker.seq <= v.seq
          and breaker.blob_ref <> v.blob_ref
      )
    order by canonical.seq
    limit 1
  ),
  case when (
    select canonical.id
    from versions canonical
    where canonical.object_id = v.object_id
      and canonical.blob_ref = v.blob_ref
      and canonical.seq <= v.seq
      and not exists (
        select 1
        from versions breaker
        where breaker.object_id = v.object_id
          and breaker.seq > canonical.seq
          and breaker.seq <= v.seq
          and breaker.blob_ref <> v.blob_ref
      )
    order by canonical.seq
    limit 1
  ) = v.id then 1 else 0 end
from versions v
join objects o on o.id = v.object_id
order by v.object_id, v.seq`)
	return err
}
