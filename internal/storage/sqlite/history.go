package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

type ObjectHistoryOptions struct {
	From            time.Time
	To              time.Time
	MaxVersions     int
	MaxObservations int
	IncludeDocs     bool
	IncludeDiffs    bool
}

type ObjectHistory struct {
	Object       ObjectRecord         `json:"object"`
	Versions     []HistoryVersion     `json:"versions"`
	Observations []HistoryObservation `json:"observations,omitempty"`
	VersionDiffs []VersionDiff        `json:"versionDiffs,omitempty"`
	Summary      ObjectHistorySummary `json:"summary"`
}

type HistoryVersion struct {
	ID                         int64          `json:"id"`
	Sequence                   int64          `json:"sequence"`
	ObservedAt                 time.Time      `json:"observedAt"`
	ResourceVersion            string         `json:"resourceVersion,omitempty"`
	DocumentHash               string         `json:"documentHash"`
	Materialization            string         `json:"materialization"`
	Strategy                   string         `json:"strategy"`
	ReplayDepth                int            `json:"replayDepth"`
	RawSize                    int64          `json:"rawSize"`
	StoredSize                 int64          `json:"storedSize"`
	ObservationCount           int64          `json:"observationCount"`
	ContentChangedObservations int64          `json:"contentChangedObservations"`
	UnchangedObservations      int64          `json:"unchangedObservations"`
	FirstObservedAt            time.Time      `json:"firstObservedAt"`
	LastObservedAt             time.Time      `json:"lastObservedAt"`
	LatestResourceVersion      string         `json:"latestResourceVersion,omitempty"`
	Document                   map[string]any `json:"document,omitempty"`
}

type HistoryObservation struct {
	ID              int64     `json:"id"`
	ObservedAt      time.Time `json:"observedAt"`
	ObservationType string    `json:"observationType"`
	ResourceVersion string    `json:"resourceVersion,omitempty"`
	VersionID       int64     `json:"versionId,omitempty"`
	VersionSequence int64     `json:"versionSequence,omitempty"`
	DocumentHash    string    `json:"documentHash,omitempty"`
	ContentChanged  bool      `json:"contentChanged"`
}

type ObjectHistorySummary struct {
	Versions                   int   `json:"versions"`
	VersionDiffs               int   `json:"versionDiffs,omitempty"`
	Observations               int64 `json:"observations"`
	ReturnedObservations       int   `json:"returnedObservations"`
	ContentChangedObservations int64 `json:"contentChangedObservations"`
	UnchangedObservations      int64 `json:"unchangedObservations"`
}

func (s *Store) ObjectHistory(ctx context.Context, target ObjectTarget, opts ObjectHistoryOptions) (ObjectHistory, error) {
	object, dbObjectID, err := s.FindObject(ctx, target)
	if err != nil {
		return ObjectHistory{}, err
	}
	if opts.MaxVersions <= 0 {
		opts.MaxVersions = 50
	}
	if opts.MaxObservations <= 0 {
		opts.MaxObservations = 100
	}

	versions, diffEvidence, err := s.historyVersions(ctx, dbObjectID, opts)
	if err != nil {
		return ObjectHistory{}, err
	}
	observations, err := s.historyObservations(ctx, dbObjectID, opts)
	if err != nil {
		return ObjectHistory{}, err
	}
	summary, err := s.historySummary(ctx, dbObjectID, opts)
	if err != nil {
		return ObjectHistory{}, err
	}
	out := ObjectHistory{
		Object:       object,
		Versions:     versions,
		Observations: observations,
		Summary:      summary,
	}
	out.Summary.Versions = len(versions)
	out.Summary.ReturnedObservations = len(observations)
	if opts.IncludeDiffs {
		out.VersionDiffs = compactVersionDiffs(diffEvidence)
		out.Summary.VersionDiffs = len(out.VersionDiffs)
	}
	return out, nil
}

func (s *Store) historyVersions(ctx context.Context, dbObjectID int64, opts ObjectHistoryOptions) ([]HistoryVersion, []VersionEvidence, error) {
	from := millis(opts.From)
	to := millis(opts.To)
	rows, err := s.db.QueryContext(ctx, `
with version_obs as (
  select
    version_id,
    count(*) as observation_count,
    sum(case when content_changed then 1 else 0 end) as changed_count,
    sum(case when not content_changed then 1 else 0 end) as unchanged_count,
    min(observed_at) as first_observed_at,
    max(observed_at) as last_observed_at
  from object_observations
  where object_id = ?
    and version_id is not null
    and (? = 0 or observed_at >= ?)
    and (? = 0 or observed_at <= ?)
  group by version_id
)
select
  v.id,
  v.seq,
  v.observed_at,
  coalesce(v.resource_version, ''),
  v.doc_hash,
  v.materialization,
  v.strategy,
  v.replay_depth,
  v.raw_size,
  v.stored_size,
  coalesce(vo.observation_count, 0),
  coalesce(vo.changed_count, 0),
  coalesce(vo.unchanged_count, 0),
  coalesce(vo.first_observed_at, v.observed_at),
  coalesce(vo.last_observed_at, v.observed_at),
  coalesce((
    select oo.resource_version
    from object_observations oo
    where oo.version_id = v.id
      and (? = 0 or oo.observed_at >= ?)
      and (? = 0 or oo.observed_at <= ?)
    order by oo.observed_at desc, oo.id desc
    limit 1
  ), ''),
  b.data
from versions v
join blobs b on b.digest = v.blob_ref
left join version_obs vo on vo.version_id = v.id
where v.object_id = ?
  and (
    (? = 0 and ? = 0)
    or vo.observation_count is not null
    or ((? = 0 or v.observed_at >= ?) and (? = 0 or v.observed_at <= ?))
  )
order by v.observed_at desc, v.seq desc
limit ?`,
		dbObjectID, from, from, to, to,
		from, from, to, to,
		dbObjectID, from, to, from, from, to, to, opts.MaxVersions)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	var versions []HistoryVersion
	var diffEvidence []VersionEvidence
	for rows.Next() {
		var version HistoryVersion
		var observedAt int64
		var firstObservedAt int64
		var lastObservedAt int64
		var docBytes []byte
		if err := rows.Scan(
			&version.ID,
			&version.Sequence,
			&observedAt,
			&version.ResourceVersion,
			&version.DocumentHash,
			&version.Materialization,
			&version.Strategy,
			&version.ReplayDepth,
			&version.RawSize,
			&version.StoredSize,
			&version.ObservationCount,
			&version.ContentChangedObservations,
			&version.UnchangedObservations,
			&firstObservedAt,
			&lastObservedAt,
			&version.LatestResourceVersion,
			&docBytes,
		); err != nil {
			return nil, nil, err
		}
		version.ObservedAt = time.UnixMilli(observedAt)
		version.FirstObservedAt = time.UnixMilli(firstObservedAt)
		version.LastObservedAt = time.UnixMilli(lastObservedAt)
		var document map[string]any
		if len(docBytes) > 0 {
			if err := json.Unmarshal(docBytes, &document); err != nil {
				return nil, nil, err
			}
		}
		if opts.IncludeDocs {
			version.Document = document
		}
		diffEvidence = append(diffEvidence, VersionEvidence{
			ID:              version.ID,
			Sequence:        version.Sequence,
			ObservedAt:      version.ObservedAt,
			ResourceVersion: version.ResourceVersion,
			DocumentHash:    version.DocumentHash,
			Materialization: version.Materialization,
			Strategy:        version.Strategy,
			ReplayDepth:     version.ReplayDepth,
			Document:        document,
		})
		versions = append(versions, version)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return versions, diffEvidence, nil
}

func (s *Store) historyObservations(ctx context.Context, dbObjectID int64, opts ObjectHistoryOptions) ([]HistoryObservation, error) {
	from := millis(opts.From)
	to := millis(opts.To)
	rows, err := s.db.QueryContext(ctx, `
select
  oo.id,
  oo.observed_at,
  oo.observation_type,
  coalesce(oo.resource_version, ''),
  coalesce(oo.version_id, 0),
  coalesce(v.seq, 0),
  coalesce(v.doc_hash, ''),
  oo.content_changed
from object_observations oo
left join versions v on v.id = oo.version_id
where oo.object_id = ?
  and (? = 0 or oo.observed_at >= ?)
  and (? = 0 or oo.observed_at <= ?)
order by oo.observed_at desc, oo.id desc
limit ?`, dbObjectID, from, from, to, to, opts.MaxObservations)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []HistoryObservation
	for rows.Next() {
		var observedAt int64
		var observation HistoryObservation
		if err := rows.Scan(
			&observation.ID,
			&observedAt,
			&observation.ObservationType,
			&observation.ResourceVersion,
			&observation.VersionID,
			&observation.VersionSequence,
			&observation.DocumentHash,
			&observation.ContentChanged,
		); err != nil {
			return nil, err
		}
		observation.ObservedAt = time.UnixMilli(observedAt)
		out = append(out, observation)
	}
	return out, rows.Err()
}

func (s *Store) historySummary(ctx context.Context, dbObjectID int64, opts ObjectHistoryOptions) (ObjectHistorySummary, error) {
	from := millis(opts.From)
	to := millis(opts.To)
	var summary ObjectHistorySummary
	var changed sql.NullInt64
	var unchanged sql.NullInt64
	err := s.db.QueryRowContext(ctx, `
select
  count(*),
  sum(case when content_changed then 1 else 0 end),
  sum(case when not content_changed then 1 else 0 end)
from object_observations
where object_id = ?
  and (? = 0 or observed_at >= ?)
  and (? = 0 or observed_at <= ?)`, dbObjectID, from, from, to, to).Scan(
		&summary.Observations,
		&changed,
		&unchanged,
	)
	if err != nil {
		return ObjectHistorySummary{}, err
	}
	if changed.Valid {
		summary.ContentChangedObservations = changed.Int64
	}
	if unchanged.Valid {
		summary.UnchangedObservations = unchanged.Int64
	}
	return summary, nil
}
