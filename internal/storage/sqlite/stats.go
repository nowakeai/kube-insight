package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"
)

type Stats struct {
	Clusters                       int64            `json:"clusters"`
	APIResources                   int64            `json:"api_resources"`
	Objects                        int64            `json:"objects"`
	DeletedObjects                 int64            `json:"deleted_objects"`
	Observations                   int64            `json:"observations"`
	UnchangedObservations          int64            `json:"unchanged_observations"`
	Versions                       int64            `json:"versions"`
	Blobs                          int64            `json:"blobs"`
	Facts                          int64            `json:"facts"`
	Edges                          int64            `json:"edges"`
	Changes                        int64            `json:"changes"`
	FilterDecisions                int64            `json:"filter_decisions"`
	DestructiveFilterDecisions     int64            `json:"destructive_filter_decisions"`
	RedactedFields                 int64            `json:"redacted_fields"`
	RemovedFields                  int64            `json:"removed_fields"`
	SecretPayloadRemovals          int64            `json:"secret_payload_removals"`
	SecretPayloadViolations        int64            `json:"secret_payload_violations"`
	IngestionOffsets               int64            `json:"ingestion_offsets"`
	OffsetLagMS                    int64            `json:"offset_lag_ms"`
	RawBytes                       int64            `json:"raw_bytes"`
	BlobBytes                      int64            `json:"blob_bytes"`
	DatabaseBytes                  int64            `json:"database_bytes"`
	WALBytes                       int64            `json:"wal_bytes"`
	LiveBytes                      int64            `json:"live_bytes"`
	SlackBytes                     int64            `json:"slack_bytes"`
	PageSize                       int64            `json:"page_size"`
	PageCount                      int64            `json:"page_count"`
	FreelistPages                  int64            `json:"freelist_pages"`
	TableBytes                     int64            `json:"table_bytes"`
	IndexBytes                     int64            `json:"index_bytes"`
	VersionBytes                   int64            `json:"version_bytes"`
	LatestIndexBytes               int64            `json:"latest_index_bytes"`
	FactBytes                      int64            `json:"fact_bytes"`
	EdgeBytes                      int64            `json:"edge_bytes"`
	FilterDecisionBytes            int64            `json:"filter_decision_bytes"`
	LatestRows                     int64            `json:"latest_rows"`
	PodStatusChangeRows            int64            `json:"pod_status_change_rows"`
	NodeConditionFactRows          int64            `json:"node_condition_fact_rows"`
	EventMessageFingerprints       int64            `json:"event_message_fingerprints"`
	EventFactRows                  int64            `json:"event_fact_rows"`
	EndpointSliceEdgeRows          int64            `json:"endpointslice_edge_rows"`
	EndpointSliceChangeRows        int64            `json:"endpointslice_change_rows"`
	EndpointSliceReadinessFactRows int64            `json:"endpointslice_readiness_fact_rows"`
	ProcessingProfiles             int64            `json:"processing_profiles"`
	DisabledProcessingProfiles     int64            `json:"disabled_processing_profiles"`
	StoredVersionsByProfile        map[string]int64 `json:"stored_versions_by_profile"`
	APIResourcesByProfile          map[string]int64 `json:"api_resources_by_profile"`
}

func (s *Store) Stats(ctx context.Context) (Stats, error) {
	out := Stats{
		StoredVersionsByProfile: map[string]int64{},
		APIResourcesByProfile:   map[string]int64{},
	}
	queries := []struct {
		dst   *int64
		query string
	}{
		{&out.Clusters, `select count(*) from clusters`},
		{&out.APIResources, `select count(*) from api_resources`},
		{&out.Objects, `select count(*) from objects`},
		{&out.DeletedObjects, `select count(*) from objects where deleted_at is not null`},
		{&out.Observations, `select count(*) from object_observations`},
		{&out.UnchangedObservations, `select count(*) from object_observations where not content_changed`},
		{&out.Versions, `select count(*) from versions`},
		{&out.Blobs, `select count(*) from blobs`},
		{&out.Facts, `select count(*) from object_facts`},
		{&out.Edges, `select count(*) from object_edges`},
		{&out.Changes, `select count(*) from object_changes`},
		{&out.FilterDecisions, `select count(*) from filter_decisions`},
		{&out.DestructiveFilterDecisions, `select count(*) from filter_decisions where destructive`},
		{&out.ProcessingProfiles, `select count(*) from resource_processing_profiles`},
		{&out.DisabledProcessingProfiles, `select count(*) from resource_processing_profiles where not enabled`},
		{&out.IngestionOffsets, `select count(*) from ingestion_offsets`},
		{&out.RawBytes, `select coalesce(sum(raw_size), 0) from versions`},
		{&out.BlobBytes, `select coalesce(sum(stored_size), 0) from blobs`},
		{&out.LatestRows, `select count(*) from latest_index`},
		{&out.PodStatusChangeRows, `select count(*)
from object_changes ch
join objects o on o.id = ch.object_id
join object_kinds ok on ok.id = o.kind_id
where ok.kind = 'Pod' and ch.change_family = 'status'`},
		{&out.NodeConditionFactRows, `select count(*) from object_facts where fact_key like 'node_condition.%'`},
		{&out.EventMessageFingerprints, `select count(distinct fact_value) from object_facts where fact_key = 'k8s_event.message_fingerprint'`},
		{&out.EventFactRows, `select count(*) from object_facts where fact_key like 'k8s_event.%'`},
		{&out.EndpointSliceEdgeRows, `select count(*) from object_edges where edge_type like 'endpointslice_%'`},
		{&out.EndpointSliceChangeRows, `select count(*)
from object_changes ch
join objects o on o.id = ch.object_id
join object_kinds ok on ok.id = o.kind_id
where ok.kind = 'EndpointSlice'`},
		{&out.EndpointSliceReadinessFactRows, `select count(*) from object_facts where fact_key in ('endpoint.ready', 'endpoint.serving', 'endpoint.terminating')`},
	}
	for _, item := range queries {
		if err := s.db.QueryRowContext(ctx, item.query).Scan(item.dst); err != nil {
			return Stats{}, err
		}
	}
	if err := s.addPageStats(ctx, &out); err != nil {
		return Stats{}, err
	}
	if err := s.addFileStats(ctx, &out); err != nil {
		return Stats{}, err
	}
	if err := s.addOffsetLag(ctx, &out); err != nil {
		return Stats{}, err
	}
	if err := s.addFilterDecisionStats(ctx, &out); err != nil {
		return Stats{}, err
	}
	if err := s.addProcessingProfileStats(ctx, &out); err != nil {
		return Stats{}, err
	}
	return out, nil
}

func (s *Store) addProcessingProfileStats(ctx context.Context, out *Stats) error {
	rows, err := s.db.QueryContext(ctx, `
select coalesce(rpp.profile, 'generic'), count(*)
from api_resources ar
left join resource_processing_profiles rpp on rpp.api_resource_id = ar.id
group by coalesce(rpp.profile, 'generic')`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var profile string
		var count int64
		if err := rows.Scan(&profile, &count); err != nil {
			return err
		}
		out.APIResourcesByProfile[profile] = count
	}
	if err := rows.Err(); err != nil {
		return err
	}

	rows, err = s.db.QueryContext(ctx, `
select coalesce(rpp.profile, 'generic'), count(v.id)
from versions v
join objects o on o.id = v.object_id
join object_kinds ok on ok.id = o.kind_id
join api_resources ar on ar.id = ok.api_resource_id
left join resource_processing_profiles rpp on rpp.api_resource_id = ar.id
group by coalesce(rpp.profile, 'generic')`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var profile string
		var count int64
		if err := rows.Scan(&profile, &count); err != nil {
			return err
		}
		out.StoredVersionsByProfile[profile] = count
	}
	return rows.Err()
}

func (s *Store) addFilterDecisionStats(ctx context.Context, out *Stats) error {
	rows, err := s.db.QueryContext(ctx, `select meta from filter_decisions where meta is not null and meta <> ''`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var meta string
		if err := rows.Scan(&meta); err != nil {
			return err
		}
		out.RedactedFields += jsonInt(meta, "redactedFields")
		out.RemovedFields += jsonInt(meta, "removedFields")
		if strings.Contains(meta, `"secretPayloadRemoved":true`) {
			out.SecretPayloadRemovals++
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return s.db.QueryRowContext(ctx, `
select count(*)
from latest_index li
join versions v on v.id = li.latest_version_id
join blobs b on b.digest = v.blob_ref
join object_kinds ok on ok.id = li.kind_id
where ok.kind = 'Secret' and (cast(b.data as text) like '%"data":%' or cast(b.data as text) like '%"stringData":%')`).Scan(&out.SecretPayloadViolations)
}

func (s *Store) addOffsetLag(ctx context.Context, out *Stats) error {
	var updatedAt int64
	if err := s.db.QueryRowContext(ctx, `select coalesce(max(updated_at), 0) from ingestion_offsets`).Scan(&updatedAt); err != nil {
		return err
	}
	if updatedAt > 0 {
		out.OffsetLagMS = millis(time.Now()) - updatedAt
		if out.OffsetLagMS < 0 {
			out.OffsetLagMS = 0
		}
	}
	return nil
}

func (s *Store) addPageStats(ctx context.Context, out *Stats) error {
	indexes, err := s.indexNames(ctx)
	if err != nil {
		return err
	}
	rows, err := s.db.QueryContext(ctx, `select name, coalesce(sum(pgsize), 0) from dbstat group by name`)
	if err != nil {
		if strings.Contains(err.Error(), "no such table: dbstat") {
			return nil
		}
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var name string
		var bytes int64
		if err := rows.Scan(&name, &bytes); err != nil {
			return err
		}
		if indexes[name] {
			out.IndexBytes += bytes
		} else {
			out.TableBytes += bytes
		}
		switch {
		case name == "versions":
			out.VersionBytes += bytes
		case name == "latest_index" || strings.HasPrefix(name, "latest_"):
			out.LatestIndexBytes += bytes
		case name == "object_facts" || strings.HasPrefix(name, "object_facts_"):
			out.FactBytes += bytes
		case name == "object_edges" || strings.HasPrefix(name, "object_edges_"):
			out.EdgeBytes += bytes
		case name == "filter_decisions" || strings.HasPrefix(name, "filter_decisions_"):
			out.FilterDecisionBytes += bytes
		}
	}
	return rows.Err()
}

func (s *Store) addFileStats(ctx context.Context, out *Stats) error {
	fileStats, err := databaseFileStats(s.path)
	if err != nil {
		return err
	}
	out.DatabaseBytes = fileStats.DatabaseBytes
	out.WALBytes = fileStats.WALBytes
	out.LiveBytes = out.TableBytes + out.IndexBytes
	if out.DatabaseBytes+out.WALBytes > out.LiveBytes {
		out.SlackBytes = out.DatabaseBytes + out.WALBytes - out.LiveBytes
	}
	out.PageSize, _ = s.pragmaInt64(ctx, "page_size")
	out.PageCount, _ = s.pragmaInt64(ctx, "page_count")
	out.FreelistPages, _ = s.pragmaInt64(ctx, "freelist_count")
	return nil
}

func jsonInt(doc, key string) int64 {
	pattern := `"` + key + `":`
	index := strings.Index(doc, pattern)
	if index < 0 {
		return 0
	}
	rest := strings.TrimLeft(doc[index+len(pattern):], " ")
	end := 0
	for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0
	}
	value, err := strconv.ParseInt(rest[:end], 10, 64)
	if err != nil {
		return 0
	}
	return value
}

func (s *Store) indexNames(ctx context.Context) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `select name from sqlite_master where type = 'index' and name is not null`)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return map[string]bool{}, nil
		}
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out[name] = true
	}
	return out, rows.Err()
}
