package sqlite

import (
	"context"
	"database/sql"
	"fmt"
)

type PruneReport struct {
	Versions int64 `json:"versions"`
	Facts    int64 `json:"facts"`
	Changes  int64 `json:"changes"`
	Edges    int64 `json:"edges"`
	Blobs    int64 `json:"blobs"`
}

func (s *Store) PruneUnchangedVersions(ctx context.Context) (PruneReport, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PruneReport{}, err
	}
	defer rollback(tx)

	var versions, observations int64
	if err := tx.QueryRowContext(ctx, `select count(*) from versions`).Scan(&versions); err != nil {
		return PruneReport{}, err
	}
	if versions == 0 {
		return PruneReport{}, tx.Commit()
	}
	if err := tx.QueryRowContext(ctx, `select count(*) from object_observations`).Scan(&observations); err != nil {
		return PruneReport{}, err
	}
	if observations == 0 {
		return PruneReport{}, fmt.Errorf("cannot prune versions before object_observations is backfilled")
	}
	for _, stmt := range []string{
		`create temp table if not exists prune_versions(id integer primary key)`,
		`delete from prune_versions`,
		`insert into prune_versions(id)
select v.id
from versions v
left join (
  select distinct version_id
  from object_observations
  where version_id is not null
) retained on retained.version_id = v.id
where retained.version_id is null`,
		`update latest_index
set latest_version_id = (
  select oo.version_id
  from object_observations oo
  where oo.object_id = latest_index.object_id
    and oo.version_id is not null
  order by oo.observed_at desc, oo.id desc
  limit 1
)
where latest_version_id in (select id from prune_versions)`,
		`update objects
set latest_version_id = (
  select li.latest_version_id
  from latest_index li
  where li.object_id = objects.id
)
where latest_version_id in (select id from prune_versions)`,
		`update versions
set parent_version_id = null
where parent_version_id in (select id from prune_versions)`,
	} {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return PruneReport{}, err
		}
	}

	var report PruneReport
	if err := tx.QueryRowContext(ctx, `select count(*) from prune_versions`).Scan(&report.Versions); err != nil {
		return PruneReport{}, err
	}
	var res sql.Result
	if res, err = tx.ExecContext(ctx, `delete from object_facts where version_id in (select id from prune_versions)`); err != nil {
		return PruneReport{}, err
	}
	report.Facts, _ = res.RowsAffected()
	if res, err = tx.ExecContext(ctx, `delete from object_changes where version_id in (select id from prune_versions)`); err != nil {
		return PruneReport{}, err
	}
	report.Changes, _ = res.RowsAffected()
	if res, err = tx.ExecContext(ctx, `
delete from object_edges
where src_version_id in (select id from prune_versions)
   or dst_version_id in (select id from prune_versions)`); err != nil {
		return PruneReport{}, err
	}
	report.Edges, _ = res.RowsAffected()
	if _, err := tx.ExecContext(ctx, `delete from versions where id in (select id from prune_versions)`); err != nil {
		return PruneReport{}, err
	}
	if res, err = tx.ExecContext(ctx, `
delete from blobs
where digest not in (select distinct blob_ref from versions)`); err != nil {
		return PruneReport{}, err
	}
	report.Blobs, _ = res.RowsAffected()
	return report, tx.Commit()
}
