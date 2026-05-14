package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"kube-insight/internal/resourceprofile"
	"kube-insight/internal/storage"
)

type ResourceHealthOptions struct {
	ClusterID  string
	Status     string
	ErrorsOnly bool
	StaleAfter time.Duration
	Limit      int
}

type ResourceHealthReport struct {
	CheckedAt time.Time              `json:"checkedAt"`
	Summary   ResourceHealthSummary  `json:"summary"`
	ByStatus  map[string]int         `json:"byStatus"`
	Resources []ResourceHealthRecord `json:"resources"`
}

type ResourceHealthSummary struct {
	Resources  int      `json:"resources"`
	Healthy    int      `json:"healthy"`
	Errors     int      `json:"errors"`
	Stale      int      `json:"stale"`
	NotStarted int      `json:"notStarted"`
	Complete   bool     `json:"complete"`
	Warnings   []string `json:"warnings,omitempty"`
}

type ResourceHealthRecord struct {
	ClusterID       string     `json:"clusterId"`
	Group           string     `json:"group,omitempty"`
	Version         string     `json:"version"`
	Resource        string     `json:"resource"`
	Kind            string     `json:"kind"`
	Namespaced      bool       `json:"namespaced"`
	Namespace       string     `json:"namespace,omitempty"`
	Status          string     `json:"status"`
	Error           string     `json:"error,omitempty"`
	ResourceVersion string     `json:"resourceVersion,omitempty"`
	LastListAt      *time.Time `json:"lastListAt,omitempty"`
	LastWatchAt     *time.Time `json:"lastWatchAt,omitempty"`
	LastBookmarkAt  *time.Time `json:"lastBookmarkAt,omitempty"`
	UpdatedAt       *time.Time `json:"updatedAt,omitempty"`
	AgeSeconds      int64      `json:"ageSeconds,omitempty"`
	Stale           bool       `json:"stale,omitempty"`
	LatestObjects   int        `json:"latestObjects"`
}

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

func (s *Store) ResourceHealth(ctx context.Context, opts ResourceHealthOptions) (ResourceHealthReport, error) {
	now := time.Now().UTC()
	rows, err := s.db.QueryContext(ctx, `
select
  c.name,
  ar.api_group,
  ar.api_version,
  ar.resource,
  ar.kind,
  ar.namespaced,
  coalesce(io.namespace, ''),
  coalesce(io.status, 'not_started'),
  coalesce(io.error, ''),
  coalesce(io.resource_version, ''),
  io.last_list_at,
  io.last_watch_at,
  io.last_bookmark_at,
  io.updated_at,
  (
    select count(*)
    from latest_index li
    join object_kinds ok on ok.id = li.kind_id
    where li.cluster_id = c.id
      and ok.api_resource_id = ar.id
      and (coalesce(io.namespace, '') = '' or coalesce(li.namespace, '') = coalesce(io.namespace, ''))
  ) as latest_objects
from clusters c
join api_resources ar on ar.removed_at is null
left join ingestion_offsets io on io.cluster_id = c.id and io.api_resource_id = ar.id
where (? = '' or c.name = ?)
order by
  case coalesce(io.status, 'not_started')
    when 'watch_error' then 0
    when 'list_error' then 1
    when 'not_started' then 2
    when 'listed' then 3
    when 'watching' then 4
    when 'bookmark' then 5
    else 6
  end,
  c.name,
  ar.api_group,
  ar.api_version,
  ar.resource,
  coalesce(io.namespace, '')`, opts.ClusterID, opts.ClusterID)
	if err != nil {
		return ResourceHealthReport{}, err
	}
	defer rows.Close()

	report := ResourceHealthReport{
		CheckedAt: now,
		ByStatus:  map[string]int{},
	}
	for rows.Next() {
		record, err := scanResourceHealthRecord(rows, now, opts.StaleAfter)
		if err != nil {
			return ResourceHealthReport{}, err
		}
		if !resourceHealthMatches(record, opts) {
			continue
		}
		if opts.Limit <= 0 || len(report.Resources) < opts.Limit {
			report.Resources = append(report.Resources, record)
		}
		report.ByStatus[record.Status]++
		report.Summary.Resources++
		switch {
		case record.Status == "not_started":
			report.Summary.NotStarted++
		case isResourceHealthError(record):
			report.Summary.Errors++
		case record.Stale:
			report.Summary.Stale++
		default:
			report.Summary.Healthy++
		}
	}
	if err := rows.Err(); err != nil {
		return ResourceHealthReport{}, err
	}
	report.Summary = finalizeResourceHealthSummary(report.Summary, opts)
	return report, nil
}

func finalizeResourceHealthSummary(summary ResourceHealthSummary, opts ResourceHealthOptions) ResourceHealthSummary {
	summary.Complete = summary.Errors == 0 && summary.Stale == 0 && summary.NotStarted == 0
	if summary.Resources == 0 && opts.Status == "" && !opts.ErrorsOnly {
		summary.Complete = false
		summary.Warnings = append(summary.Warnings, "no discovered resources in health report")
	}
	if summary.Errors > 0 {
		summary.Warnings = append(summary.Warnings, fmt.Sprintf("%d resource stream(s) have errors", summary.Errors))
	}
	if summary.Stale > 0 {
		summary.Warnings = append(summary.Warnings, fmt.Sprintf("%d resource stream(s) are stale", summary.Stale))
	}
	if summary.NotStarted > 0 {
		summary.Warnings = append(summary.Warnings, fmt.Sprintf("%d resource stream(s) have not started", summary.NotStarted))
	}
	return summary
}

type resourceHealthScanner interface {
	Scan(dest ...any) error
}

func scanResourceHealthRecord(rows resourceHealthScanner, now time.Time, staleAfter time.Duration) (ResourceHealthRecord, error) {
	var lastListAt sql.NullInt64
	var lastWatchAt sql.NullInt64
	var lastBookmarkAt sql.NullInt64
	var updatedAt sql.NullInt64
	var record ResourceHealthRecord
	if err := rows.Scan(
		&record.ClusterID,
		&record.Group,
		&record.Version,
		&record.Resource,
		&record.Kind,
		&record.Namespaced,
		&record.Namespace,
		&record.Status,
		&record.Error,
		&record.ResourceVersion,
		&lastListAt,
		&lastWatchAt,
		&lastBookmarkAt,
		&updatedAt,
		&record.LatestObjects,
	); err != nil {
		return ResourceHealthRecord{}, err
	}
	record.LastListAt = nullMillisTime(lastListAt)
	record.LastWatchAt = nullMillisTime(lastWatchAt)
	record.LastBookmarkAt = nullMillisTime(lastBookmarkAt)
	record.UpdatedAt = nullMillisTime(updatedAt)
	if record.UpdatedAt != nil {
		record.AgeSeconds = int64(now.Sub(*record.UpdatedAt).Seconds())
		if record.AgeSeconds < 0 {
			record.AgeSeconds = 0
		}
		record.Stale = staleAfter > 0 && now.Sub(*record.UpdatedAt) > staleAfter
	}
	return record, nil
}

func nullMillisTime(value sql.NullInt64) *time.Time {
	if !value.Valid || value.Int64 <= 0 {
		return nil
	}
	t := time.UnixMilli(value.Int64).UTC()
	return &t
}

func resourceHealthMatches(record ResourceHealthRecord, opts ResourceHealthOptions) bool {
	if opts.Status != "" && record.Status != opts.Status {
		return false
	}
	if opts.ErrorsOnly && !isResourceHealthError(record) {
		return false
	}
	return true
}

func isResourceHealthError(record ResourceHealthRecord) bool {
	return record.Error != "" || record.Status == "watch_error" || record.Status == "list_error"
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
