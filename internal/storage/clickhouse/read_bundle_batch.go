package clickhouse

import (
	"context"
	"fmt"

	"kube-insight/internal/core"
	"kube-insight/internal/storage"
)

func (s *Store) latestDocumentsByObject(ctx context.Context, objectIDs []string) (map[string]map[string]any, error) {
	out := map[string]map[string]any{}
	objectIDs = uniqueStrings(objectIDs)
	if len(objectIDs) == 0 {
		return out, nil
	}
	query := fmt.Sprintf(`
SELECT object_id, doc
FROM (
  SELECT object_id, doc, observed_at, seq
  FROM %s.versions
  WHERE object_id IN (%s)
  ORDER BY object_id ASC, observed_at DESC, seq DESC
  LIMIT 1 BY object_id
)
ORDER BY object_id`, q(s.database()), sqlStringList(objectIDs))
	result, err := s.client().QueryTSV(ctx, query)
	if err != nil {
		return nil, err
	}
	for _, row := range result.Rows {
		objectID := row[0]
		if objectID != "" {
			out[objectID] = jsonMap(row[1])
		}
	}
	return out, nil
}

func (s *Store) factsByObject(ctx context.Context, objectIDs []string, opts storage.InvestigationOptions) (map[string][]core.Fact, error) {
	out := map[string][]core.Fact{}
	objectIDs = uniqueStrings(objectIDs)
	if len(objectIDs) == 0 {
		return out, nil
	}
	limit := boundedLimit(opts.MaxFactsPerObject, 1000, 5000)
	query := fmt.Sprintf(`
SELECT ts, object_id, fact_key, fact_value, numeric_value, severity, detail
FROM %s.facts
WHERE object_id IN (%s)%s
ORDER BY object_id ASC, ts DESC, fact_key ASC
LIMIT %d BY object_id`, q(s.database()), sqlStringList(objectIDs), timeFilter("ts", opts.From, opts.To), limit)
	result, err := s.client().QueryTSV(ctx, query)
	if err != nil {
		return nil, err
	}
	for _, row := range result.Rows {
		objectID := row[1]
		if objectID == "" {
			continue
		}
		out[objectID] = append(out[objectID], core.Fact{Time: timeValue(row[0]), ObjectID: objectID, Key: row[2], Value: row[3], NumericValue: numericPointer(row[4]), Severity: int(int64Value(row[5])), Detail: jsonMap(row[6])})
	}
	return out, nil
}

func (s *Store) changesByObject(ctx context.Context, objectIDs []string, opts storage.InvestigationOptions) (map[string][]core.Change, error) {
	out := map[string][]core.Change{}
	objectIDs = uniqueStrings(objectIDs)
	if len(objectIDs) == 0 {
		return out, nil
	}
	limit := boundedLimit(opts.MaxChangesPerObject, 1000, 5000)
	query := fmt.Sprintf(`
SELECT ts, object_id, change_family, path, op, old_scalar, new_scalar, severity
FROM %s.changes
WHERE object_id IN (%s)%s
ORDER BY object_id ASC, ts DESC, path ASC
LIMIT %d BY object_id`, q(s.database()), sqlStringList(objectIDs), timeFilter("ts", opts.From, opts.To), limit)
	result, err := s.client().QueryTSV(ctx, query)
	if err != nil {
		return nil, err
	}
	for _, row := range result.Rows {
		objectID := row[1]
		if objectID == "" {
			continue
		}
		out[objectID] = append(out[objectID], core.Change{Time: timeValue(row[0]), ObjectID: objectID, Family: row[2], Path: row[3], Op: row[4], Old: row[5], New: row[6], Severity: int(int64Value(row[7]))})
	}
	return out, nil
}

func (s *Store) edgeSourceIDsByObject(ctx context.Context, objectIDs []string) (map[string][]string, error) {
	objectIDs = uniqueStrings(objectIDs)
	out := make(map[string][]string, len(objectIDs))
	for _, objectID := range objectIDs {
		out[objectID] = []string{objectID}
	}
	if len(objectIDs) == 0 {
		return out, nil
	}
	query := fmt.Sprintf(`
SELECT object_id, alias_id
FROM %s.object_aliases
WHERE object_id IN (%s)
GROUP BY object_id, alias_id
ORDER BY object_id, alias_id`, q(s.database()), sqlStringList(objectIDs))
	result, err := s.client().QueryTSV(ctx, query)
	if err != nil {
		return nil, err
	}
	for _, row := range result.Rows {
		objectID := row[0]
		aliasID := row[1]
		if objectID != "" && aliasID != "" {
			out[objectID] = append(out[objectID], aliasID)
		}
	}
	for objectID, ids := range out {
		out[objectID] = uniqueStrings(ids)
	}
	return out, nil
}

func (s *Store) edgesByObject(ctx context.Context, objectIDs []string) (map[string][]core.Edge, error) {
	out := map[string][]core.Edge{}
	objectIDs = uniqueStrings(objectIDs)
	if len(objectIDs) == 0 {
		return out, nil
	}
	sourceIDsByObject, err := s.edgeSourceIDsByObject(ctx, objectIDs)
	if err != nil {
		return nil, err
	}
	ownerBySourceID := map[string]string{}
	allSourceIDs := make([]string, 0, len(objectIDs))
	for objectID, sourceIDs := range sourceIDsByObject {
		for _, sourceID := range sourceIDs {
			ownerBySourceID[sourceID] = objectID
			allSourceIDs = append(allSourceIDs, sourceID)
		}
	}
	allSourceIDs = uniqueStrings(allSourceIDs)
	if len(allSourceIDs) == 0 {
		return out, nil
	}
	query := fmt.Sprintf(`
SELECT edge_type, src_id, dst_id, valid_from, if(valid_to_ms >= 9223372036854770000, '', toString(valid_to)) AS valid_to, detail
FROM %s.edges
WHERE src_id IN (%s)
ORDER BY src_id ASC, valid_from ASC, edge_type ASC, dst_id ASC`, q(s.database()), sqlStringList(allSourceIDs))
	result, err := s.client().QueryTSV(ctx, query)
	if err != nil {
		return nil, err
	}
	for _, row := range result.Rows {
		sourceID := row[1]
		ownerID := ownerBySourceID[sourceID]
		if ownerID == "" {
			continue
		}
		edge := core.Edge{Type: row[0], SourceID: sourceID, TargetID: row[2], ValidFrom: timeValue(row[3]), Detail: jsonMap(row[5])}
		if validTo := timeValue(row[4]); !validTo.IsZero() {
			edge.ValidTo = &validTo
		}
		out[ownerID] = append(out[ownerID], edge)
	}
	return out, nil
}

func (s *Store) versionEvidenceByObject(ctx context.Context, objectIDs []string, opts storage.InvestigationOptions) (map[string][]storage.VersionEvidence, error) {
	out := map[string][]storage.VersionEvidence{}
	objectIDs = uniqueStrings(objectIDs)
	if len(objectIDs) == 0 {
		return out, nil
	}
	maxVersions := boundedLimit(opts.MaxVersionsPerObject, 3, 100)
	query := fmt.Sprintf(`
SELECT object_id, seq, observed_at, resource_version, doc_hash, materialization, raw_size, stored_size
FROM (
  SELECT object_id, seq, observed_at, resource_version, doc_hash, materialization, raw_size, stored_size,
    row_number() OVER (PARTITION BY object_id ORDER BY observed_at DESC, seq DESC) AS rn
  FROM %s.versions
  WHERE object_id IN (%s)%s
)
WHERE rn <= %d
ORDER BY object_id ASC, observed_at DESC, seq DESC`, q(s.database()), sqlStringList(objectIDs), timeFilter("observed_at", opts.From, opts.To), maxVersions)
	result, err := s.client().QueryTSV(ctx, query)
	if err != nil {
		return nil, err
	}
	for _, row := range result.Rows {
		objectID := row[0]
		if objectID == "" {
			continue
		}
		seq := int64Value(row[1])
		out[objectID] = append(out[objectID], storage.VersionEvidence{ID: seq, Sequence: seq, ObservedAt: timeValue(row[2]), ResourceVersion: row[3], DocumentHash: row[4], Materialization: row[5], Strategy: "full"})
	}
	return out, nil
}
