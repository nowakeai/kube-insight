package sqlite

import (
	"context"
	"sort"
	"strings"
	"time"

	"kube-insight/internal/logging"
)

type EvidenceSearchOptions struct {
	Query                string        `json:"query,omitempty"`
	ClusterID            string        `json:"clusterId,omitempty"`
	Kind                 string        `json:"kind,omitempty"`
	Namespace            string        `json:"namespace,omitempty"`
	From                 time.Time     `json:"from,omitempty"`
	To                   time.Time     `json:"to,omitempty"`
	Limit                int           `json:"limit,omitempty"`
	MaxVersionsPerObject int           `json:"maxVersionsPerObject,omitempty"`
	IncludeBundles       bool          `json:"includeBundles,omitempty"`
	IncludeHealth        bool          `json:"includeHealth,omitempty"`
	HealthStaleAfter     time.Duration `json:"-"`
}

type EvidenceSearchResult struct {
	Input    EvidenceSearchOptions   `json:"input"`
	Matches  []EvidenceSearchMatch   `json:"matches"`
	Bundles  []EvidenceBundle        `json:"bundles,omitempty"`
	Summary  EvidenceSearchSummary   `json:"summary"`
	Coverage *EvidenceSearchCoverage `json:"coverage,omitempty"`
}

type EvidenceSearchMatch struct {
	Object  ObjectRecord `json:"object"`
	Score   int          `json:"score"`
	Reasons []string     `json:"reasons"`
}

type EvidenceSearchSummary struct {
	Matches int `json:"matches"`
	Bundles int `json:"bundles"`
}

type EvidenceSearchCoverage struct {
	Summary  ResourceHealthSummary `json:"summary"`
	ByStatus map[string]int        `json:"byStatus,omitempty"`
}

type evidenceSearchHit struct {
	objectID int64
	reason   string
	score    int
}

func (s *Store) SearchEvidence(ctx context.Context, opts EvidenceSearchOptions) (EvidenceSearchResult, error) {
	logger := logging.FromContext(ctx).With("component", "query")
	limit := opts.Limit
	if limit <= 0 {
		limit = 20
	}
	includeBundles := opts.IncludeBundles || opts.MaxVersionsPerObject > 0
	opts.IncludeBundles = includeBundles
	terms := searchTerms(opts.Query)
	hits, err := s.searchEvidenceHits(ctx, opts, terms)
	if err != nil {
		return EvidenceSearchResult{}, err
	}
	scored := mergeEvidenceSearchHits(hits)
	sort.Slice(scored, func(i, j int) bool {
		if scored[i].Score != scored[j].Score {
			return scored[i].Score > scored[j].Score
		}
		return scored[i].objectID < scored[j].objectID
	})
	if len(scored) > limit {
		scored = scored[:limit]
	}

	matches := make([]EvidenceSearchMatch, 0, len(scored))
	var bundles []EvidenceBundle
	if includeBundles {
		bundles = make([]EvidenceBundle, 0, len(scored))
	}
	investigationOpts := InvestigationOptions{
		From:                 opts.From,
		To:                   opts.To,
		MaxVersionsPerObject: opts.MaxVersionsPerObject,
	}
	for _, candidate := range scored {
		record, err := s.objectRecordByDBID(ctx, candidate.objectID)
		if err != nil {
			return EvidenceSearchResult{}, err
		}
		reasons := append([]string(nil), candidate.Reasons...)
		sort.Strings(reasons)
		matches = append(matches, EvidenceSearchMatch{
			Object:  record,
			Score:   candidate.Score,
			Reasons: reasons,
		})
		if includeBundles {
			bundle, err := s.bundleForRecordWithOptions(ctx, record, investigationOpts)
			if err != nil {
				if err == errLatestDocumentMissing {
					continue
				}
				return EvidenceSearchResult{}, err
			}
			if opts.MaxVersionsPerObject > 0 || hasInvestigationWindow(investigationOpts) {
				limit := opts.MaxVersionsPerObject
				if limit <= 0 {
					limit = 3
				}
				if err := s.attachBundleVersions(ctx, &bundle, investigationOpts, limit); err != nil {
					return EvidenceSearchResult{}, err
				}
			}
			bundles = append(bundles, bundle)
		}
	}
	out := EvidenceSearchResult{
		Input:   opts,
		Matches: matches,
		Bundles: bundles,
		Summary: EvidenceSearchSummary{
			Matches: len(matches),
			Bundles: len(bundles),
		},
	}
	if opts.IncludeHealth {
		health, err := s.ResourceHealth(ctx, ResourceHealthOptions{
			ClusterID:  opts.ClusterID,
			StaleAfter: opts.HealthStaleAfter,
		})
		if err != nil {
			return EvidenceSearchResult{}, err
		}
		out.Coverage = &EvidenceSearchCoverage{
			Summary:  health.Summary,
			ByStatus: health.ByStatus,
		}
	}
	logger.Info("evidence search completed", "query", opts.Query, "cluster", opts.ClusterID, "kind", opts.Kind, "namespace", opts.Namespace, "matches", out.Summary.Matches)
	return out, nil
}

func (s *Store) searchEvidenceHits(ctx context.Context, opts EvidenceSearchOptions, terms []string) ([]evidenceSearchHit, error) {
	var hits []evidenceSearchHit
	factHits, err := s.searchFactHits(ctx, opts, terms)
	if err != nil {
		return nil, err
	}
	hits = append(hits, factHits...)
	changeHits, err := s.searchChangeHits(ctx, opts, terms)
	if err != nil {
		return nil, err
	}
	hits = append(hits, changeHits...)
	latestHits, err := s.searchLatestHits(ctx, opts, terms)
	if err != nil {
		return nil, err
	}
	hits = append(hits, latestHits...)
	return hits, nil
}

func (s *Store) searchFactHits(ctx context.Context, opts EvidenceSearchOptions, terms []string) ([]evidenceSearchHit, error) {
	query := `
select f.object_id, f.fact_key, coalesce(f.fact_value, ''), f.severity
from object_facts f
join clusters c on c.id = f.cluster_id
join object_kinds ok on ok.id = f.kind_id
where (? = '' or c.name = ?)
  and (? = '' or ok.kind = ?)
  and (? = '' or coalesce(f.namespace, '') = ?)
  and (? = 0 or f.ts >= ?)
  and (? = 0 or f.ts <= ?)`
	from := millis(opts.From)
	to := millis(opts.To)
	rows, err := s.db.QueryContext(ctx, query,
		opts.ClusterID, opts.ClusterID,
		opts.Kind, opts.Kind,
		opts.Namespace, opts.Namespace,
		from, from,
		to, to,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var hits []evidenceSearchHit
	for rows.Next() {
		var objectID int64
		var key string
		var value string
		var severity int
		if err := rows.Scan(&objectID, &key, &value, &severity); err != nil {
			return nil, err
		}
		text := key + " " + value
		if !matchesSearchTerms(text, terms) {
			continue
		}
		hits = append(hits, evidenceSearchHit{
			objectID: objectID,
			reason:   "fact:" + key,
			score:    20 + severity,
		})
	}
	return hits, rows.Err()
}

func (s *Store) searchChangeHits(ctx context.Context, opts EvidenceSearchOptions, terms []string) ([]evidenceSearchHit, error) {
	query := `
select ch.object_id, ch.change_family, ch.path, coalesce(ch.old_scalar, ''), coalesce(ch.new_scalar, ''), ch.severity
from object_changes ch
join clusters c on c.id = ch.cluster_id
join objects o on o.id = ch.object_id
join object_kinds ok on ok.id = o.kind_id
where (? = '' or c.name = ?)
  and (? = '' or ok.kind = ?)
  and (? = '' or coalesce(o.namespace, '') = ?)
  and (? = 0 or ch.ts >= ?)
  and (? = 0 or ch.ts <= ?)`
	from := millis(opts.From)
	to := millis(opts.To)
	rows, err := s.db.QueryContext(ctx, query,
		opts.ClusterID, opts.ClusterID,
		opts.Kind, opts.Kind,
		opts.Namespace, opts.Namespace,
		from, from,
		to, to,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var hits []evidenceSearchHit
	for rows.Next() {
		var objectID int64
		var family string
		var path string
		var oldScalar string
		var newScalar string
		var severity int
		if err := rows.Scan(&objectID, &family, &path, &oldScalar, &newScalar, &severity); err != nil {
			return nil, err
		}
		text := family + " " + path + " " + oldScalar + " " + newScalar
		if !matchesSearchTerms(text, terms) {
			continue
		}
		hits = append(hits, evidenceSearchHit{
			objectID: objectID,
			reason:   "change:" + path,
			score:    25 + severity,
		})
	}
	return hits, rows.Err()
}

func (s *Store) searchLatestHits(ctx context.Context, opts EvidenceSearchOptions, terms []string) ([]evidenceSearchHit, error) {
	query := `
select li.object_id, cast(b.data as text)
from latest_index li
join clusters c on c.id = li.cluster_id
join object_kinds ok on ok.id = li.kind_id
join versions v on v.id = li.latest_version_id
join blobs b on b.digest = v.blob_ref
where (? = '' or c.name = ?)
  and (? = '' or ok.kind = ?)
  and (? = '' or coalesce(li.namespace, '') = ?)
  and (? = 0 or li.observed_at >= ?)
  and (? = 0 or li.observed_at <= ?)`
	from := millis(opts.From)
	to := millis(opts.To)
	rows, err := s.db.QueryContext(ctx, query,
		opts.ClusterID, opts.ClusterID,
		opts.Kind, opts.Kind,
		opts.Namespace, opts.Namespace,
		from, from,
		to, to,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var hits []evidenceSearchHit
	for rows.Next() {
		var objectID int64
		var doc string
		if err := rows.Scan(&objectID, &doc); err != nil {
			return nil, err
		}
		if !matchesSearchTerms(doc, terms) {
			continue
		}
		hits = append(hits, evidenceSearchHit{
			objectID: objectID,
			reason:   "latest_json",
			score:    10,
		})
	}
	return hits, rows.Err()
}

type mergedEvidenceSearchHit struct {
	objectID int64
	Score    int
	Reasons  []string
}

func mergeEvidenceSearchHits(hits []evidenceSearchHit) []mergedEvidenceSearchHit {
	byObject := map[int64]*mergedEvidenceSearchHit{}
	seenReason := map[int64]map[string]bool{}
	for _, hit := range hits {
		merged := byObject[hit.objectID]
		if merged == nil {
			merged = &mergedEvidenceSearchHit{objectID: hit.objectID}
			byObject[hit.objectID] = merged
			seenReason[hit.objectID] = map[string]bool{}
		}
		merged.Score += hit.score
		if !seenReason[hit.objectID][hit.reason] {
			merged.Reasons = append(merged.Reasons, hit.reason)
			seenReason[hit.objectID][hit.reason] = true
		}
	}
	out := make([]mergedEvidenceSearchHit, 0, len(byObject))
	for _, hit := range byObject {
		out = append(out, *hit)
	}
	return out
}

func searchTerms(query string) []string {
	fields := strings.Fields(strings.ToLower(query))
	out := fields[:0]
	for _, field := range fields {
		field = strings.Trim(field, `"'`)
		if field != "" {
			out = append(out, field)
		}
	}
	return out
}

func matchesSearchTerms(text string, terms []string) bool {
	if len(terms) == 0 {
		return true
	}
	lower := strings.ToLower(text)
	for _, term := range terms {
		if !strings.Contains(lower, term) {
			return false
		}
	}
	return true
}

func (s *Store) objectRecordByDBID(ctx context.Context, objectID int64) (ObjectRecord, error) {
	row := s.db.QueryRowContext(ctx, `
select
  c.name,
  ar.api_group,
  ar.api_version,
  ar.resource,
  ok.kind,
  coalesce(o.namespace, ''),
  o.name,
  coalesce(o.uid, ''),
  coalesce(li.observed_at, o.last_seen_at)
from objects o
join clusters c on c.id = o.cluster_id
join object_kinds ok on ok.id = o.kind_id
join api_resources ar on ar.id = ok.api_resource_id
left join latest_index li on li.object_id = o.id
where o.id = ?`, objectID)
	var observedAt int64
	var record ObjectRecord
	if err := row.Scan(
		&record.ClusterID,
		&record.Group,
		&record.Version,
		&record.Resource,
		&record.Kind,
		&record.Namespace,
		&record.Name,
		&record.UID,
		&observedAt,
	); err != nil {
		return ObjectRecord{}, err
	}
	record.LatestObservedAt = time.UnixMilli(observedAt)
	record.LogicalID = logicalID(record)
	return record, nil
}
