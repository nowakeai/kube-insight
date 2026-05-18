package clickhouse

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"kube-insight/internal/storage"
)

func (s *Store) SearchEvidence(ctx context.Context, opts storage.EvidenceSearchOptions) (storage.EvidenceSearchResult, error) {
	limit := boundedLimit(opts.Limit, 20, 1000)
	terms := searchTerms(opts.Query)
	hits, err := s.searchEvidenceHits(ctx, opts, terms)
	if err != nil {
		return storage.EvidenceSearchResult{}, err
	}
	merged := mergeClickHouseSearchHits(hits)
	sort.Slice(merged, func(i, j int) bool {
		if merged[i].Score != merged[j].Score {
			return merged[i].Score > merged[j].Score
		}
		return merged[i].ObjectID < merged[j].ObjectID
	})
	if len(merged) > limit {
		merged = merged[:limit]
	}
	objectIDs := make([]string, 0, len(merged))
	for _, hit := range merged {
		objectIDs = append(objectIDs, hit.ObjectID)
	}
	objects, err := s.objectsByID(ctx, objectIDs)
	if err != nil {
		return storage.EvidenceSearchResult{}, err
	}
	out := storage.EvidenceSearchResult{Input: opts}
	includeBundles := opts.IncludeBundles || opts.MaxVersionsPerObject > 0
	out.Matches = make([]storage.EvidenceSearchMatch, 0, len(merged))
	if includeBundles {
		out.Bundles = make([]storage.EvidenceBundle, 0, len(merged))
	}
	for _, hit := range merged {
		object, ok := objects[hit.ObjectID]
		if !ok {
			continue
		}
		reasons := append([]string(nil), hit.Reasons...)
		sort.Strings(reasons)
		out.Matches = append(out.Matches, storage.EvidenceSearchMatch{Object: object, Score: hit.Score, Reasons: reasons})
		if includeBundles {
			bundle, err := s.evidenceBundle(ctx, object, storage.InvestigationOptions{From: opts.From, To: opts.To, MaxVersionsPerObject: opts.MaxVersionsPerObject})
			if err != nil {
				return storage.EvidenceSearchResult{}, err
			}
			out.Bundles = append(out.Bundles, bundle)
		}
	}
	out.Summary = storage.EvidenceSearchSummary{Matches: len(out.Matches), Bundles: len(out.Bundles)}
	if opts.IncludeHealth {
		health, err := s.ResourceHealth(ctx, storage.ResourceHealthOptions{ClusterID: opts.ClusterID, StaleAfter: opts.HealthStaleAfter})
		if err != nil {
			return storage.EvidenceSearchResult{}, err
		}
		out.Coverage = &storage.EvidenceSearchCoverage{Summary: health.Summary, ByStatus: health.ByStatus}
	}
	return out, nil
}

func (s *Store) searchEvidenceHits(ctx context.Context, opts storage.EvidenceSearchOptions, terms []string) ([]clickHouseSearchHit, error) {
	var hits []clickHouseSearchHit
	factRows, err := s.searchRows(ctx, "facts", opts, terms)
	if err != nil {
		return nil, err
	}
	for _, row := range factRows {
		hits = append(hits, clickHouseSearchHit{ObjectID: stringValue(row["object_id"]), Score: 20 + int(int64Value(row["severity"])), Reasons: []string{"fact:" + stringValue(row["fact_key"])}})
	}
	changeRows, err := s.searchRows(ctx, "changes", opts, terms)
	if err != nil {
		return nil, err
	}
	for _, row := range changeRows {
		hits = append(hits, clickHouseSearchHit{ObjectID: stringValue(row["object_id"]), Score: 15 + int(int64Value(row["severity"])), Reasons: []string{"change:" + stringValue(row["path"])}})
	}
	return hits, nil
}

func (s *Store) searchRows(ctx context.Context, table string, opts storage.EvidenceSearchOptions, terms []string) ([]map[string]any, error) {
	var selectSQL string
	var textExpr string
	timeColumn := "ts"
	switch table {
	case "facts":
		selectSQL = "object_id, fact_key, severity"
		textExpr = "concat(fact_key, ' ', fact_value, ' ', detail)"
	case "changes":
		selectSQL = "object_id, path, severity"
		textExpr = "concat(change_family, ' ', path, ' ', old_scalar, ' ', new_scalar)"
	default:
		return nil, fmt.Errorf("unsupported search table %s", table)
	}
	where := []string{fmt.Sprintf("(%s = '' OR cluster_id = %s)", quoteString(opts.ClusterID), quoteString(opts.ClusterID)), fmt.Sprintf("(%s = '' OR kind = %s)", quoteString(opts.Kind), quoteString(opts.Kind)), fmt.Sprintf("(%s = '' OR namespace = %s)", quoteString(opts.Namespace), quoteString(opts.Namespace))}
	if !opts.From.IsZero() {
		where = append(where, fmt.Sprintf("%s >= %s", timeColumn, quoteString(clickHouseTime(opts.From))))
	}
	if !opts.To.IsZero() {
		where = append(where, fmt.Sprintf("%s <= %s", timeColumn, quoteString(clickHouseTime(opts.To))))
	}
	for _, term := range terms {
		where = append(where, fmt.Sprintf("positionCaseInsensitive(%s, %s) > 0", textExpr, quoteString(term)))
	}
	limit := boundedLimit(opts.Limit, 50, 500) * 20
	query := fmt.Sprintf("SELECT %s FROM %s.%s WHERE %s ORDER BY %s DESC LIMIT %d", selectSQL, q(s.database()), q(table), strings.Join(where, " AND "), timeColumn, limit)
	result, err := s.client().QueryJSON(ctx, query)
	if err != nil {
		return nil, err
	}
	return result.Data, nil
}

type clickHouseSearchHit struct {
	ObjectID string
	Score    int
	Reasons  []string
}

func mergeClickHouseSearchHits(hits []clickHouseSearchHit) []clickHouseSearchHit {
	byID := map[string]*clickHouseSearchHit{}
	for _, hit := range hits {
		if hit.ObjectID == "" {
			continue
		}
		merged := byID[hit.ObjectID]
		if merged == nil {
			copyHit := clickHouseSearchHit{ObjectID: hit.ObjectID}
			merged = &copyHit
			byID[hit.ObjectID] = merged
		}
		merged.Score += hit.Score
		merged.Reasons = append(merged.Reasons, hit.Reasons...)
	}
	out := make([]clickHouseSearchHit, 0, len(byID))
	for _, hit := range byID {
		hit.Reasons = uniqueStrings(hit.Reasons)
		out = append(out, *hit)
	}
	return out
}
