package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"kube-insight/internal/kubeapi"
	"kube-insight/internal/resourceprofile"
)

func (s *Store) UpsertAPIResources(ctx context.Context, resources []kubeapi.ResourceInfo, discoveredAt time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)

	now := millis(discoveredAt)
	if now == 0 {
		now = millis(time.Now())
	}
	for _, resource := range resources {
		apiResourceID, err := upsertAPIResource(ctx, tx, resource, now)
		if err != nil {
			return err
		}
		if apiResourceID > 0 {
			if err := upsertProcessingProfile(ctx, tx, apiResourceID, s.profileForResource(resource)); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func (s *Store) ResourceProcessingProfiles(ctx context.Context) (map[string]int64, error) {
	rows, err := s.db.QueryContext(ctx, `
select profile, count(*)
from resource_processing_profiles
group by profile
order by profile`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var profile string
		var count int64
		if err := rows.Scan(&profile, &count); err != nil {
			return nil, err
		}
		out[profile] = count
	}
	return out, rows.Err()
}

func (s *Store) APIResources(ctx context.Context) ([]kubeapi.ResourceInfo, error) {
	rows, err := s.db.QueryContext(ctx, `
select api_group, api_version, resource, kind, namespaced
from api_resources
where removed_at is null
order by api_group, api_version, resource`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []kubeapi.ResourceInfo
	for rows.Next() {
		var info kubeapi.ResourceInfo
		if err := rows.Scan(&info.Group, &info.Version, &info.Resource, &info.Kind, &info.Namespaced); err != nil {
			return nil, err
		}
		out = append(out, info)
	}
	return out, rows.Err()
}

func upsertAPIResource(ctx context.Context, tx *sql.Tx, resource kubeapi.ResourceInfo, discoveredAt int64) (int64, error) {
	if resource.Resource == "" || resource.Kind == "" {
		return 0, nil
	}
	verbs, err := json.Marshal(resource.Verbs)
	if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `
insert into api_resources(api_group, api_version, resource, kind, namespaced, verbs, last_discovered_at, preferred_version, storage_version, removed_at)
values(?, ?, ?, ?, ?, ?, ?, ?, ?, null)
on conflict(api_group, api_version, resource) do update set
  kind = excluded.kind,
  namespaced = excluded.namespaced,
  verbs = excluded.verbs,
  last_discovered_at = excluded.last_discovered_at,
  preferred_version = coalesce(api_resources.preferred_version, excluded.preferred_version),
  storage_version = coalesce(api_resources.storage_version, excluded.storage_version),
  removed_at = null`,
		resource.Group,
		resource.Version,
		resource.Resource,
		resource.Kind,
		resource.Namespaced,
		string(verbs),
		discoveredAt,
		resource.Version,
		resource.Version,
	); err != nil {
		return 0, err
	}
	var id int64
	err = tx.QueryRowContext(ctx, `
select id from api_resources
where api_group = ? and api_version = ? and resource = ?`,
		resource.Group, resource.Version, resource.Resource).Scan(&id)
	return id, err
}

func upsertProcessingProfile(ctx context.Context, tx *sql.Tx, apiResourceID int64, profile resourceprofile.Profile) error {
	_, err := tx.ExecContext(ctx, `
insert into resource_processing_profiles(api_resource_id, profile, retention_class, filter_chain, extractor_set, compaction_strategy, priority, max_event_buffer, enabled)
values(?, ?, ?, ?, ?, ?, ?, ?, ?)
on conflict(api_resource_id) do update set
  profile = excluded.profile,
  retention_class = excluded.retention_class,
  filter_chain = excluded.filter_chain,
  extractor_set = excluded.extractor_set,
  compaction_strategy = excluded.compaction_strategy,
  priority = excluded.priority,
  max_event_buffer = excluded.max_event_buffer,
  enabled = excluded.enabled`,
		apiResourceID,
		profile.Name,
		profile.RetentionPolicy,
		profile.FilterChain,
		profile.ExtractorSet,
		profile.CompactionStrategy,
		profile.Priority,
		profile.MaxEventBuffer,
		profile.Enabled,
	)
	return err
}
