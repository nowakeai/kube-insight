package clickhouse

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"kube-insight/internal/core"
	"kube-insight/internal/storage"
)

func (s *Store) findObject(ctx context.Context, target storage.ObjectTarget) (storage.ObjectRecord, error) {
	if target.UID == "" && (target.Kind == "" || target.Name == "") {
		return storage.ObjectRecord{}, fmt.Errorf("investigation target requires uid or kind and name")
	}
	query := fmt.Sprintf(`
SELECT object_id, cluster_id, api_group, api_version, resource, kind, namespace, name, uid, max(observed_at) AS latest_observed_at
FROM %s.versions
WHERE (%s = '' OR cluster_id = %s)
  AND ((%s != '' AND uid = %s) OR (%s = '' AND kind = %s AND namespace = %s AND name = %s))
GROUP BY object_id, cluster_id, api_group, api_version, resource, kind, namespace, name, uid
ORDER BY latest_observed_at DESC, object_id DESC
LIMIT 2`, q(s.database()), quoteString(target.ClusterID), quoteString(target.ClusterID), quoteString(target.UID), quoteString(target.UID), quoteString(target.UID), quoteString(target.Kind), quoteString(target.Namespace), quoteString(target.Name))
	result, err := s.client().QueryJSON(ctx, query)
	if err != nil {
		return storage.ObjectRecord{}, err
	}
	if len(result.Data) == 0 {
		return storage.ObjectRecord{}, fmt.Errorf("object not found")
	}
	if len(result.Data) > 1 && target.ClusterID == "" {
		return storage.ObjectRecord{}, fmt.Errorf("object is ambiguous across clusters; pass cluster")
	}
	return objectRecordFromRow(result.Data[0]), nil
}

func (s *Store) Investigate(ctx context.Context, target storage.ObjectTarget) (storage.EvidenceBundle, error) {
	return s.InvestigateWithOptions(ctx, target, storage.InvestigationOptions{})
}

func (s *Store) InvestigateWithOptions(ctx context.Context, target storage.ObjectTarget, opts storage.InvestigationOptions) (storage.EvidenceBundle, error) {
	object, err := s.findObject(ctx, target)
	if err != nil {
		return storage.EvidenceBundle{}, err
	}
	return s.evidenceBundle(ctx, object, opts)
}

func (s *Store) objectsByID(ctx context.Context, objectIDs []string) (map[string]storage.ObjectRecord, error) {
	out := map[string]storage.ObjectRecord{}
	objectIDs = uniqueStrings(objectIDs)
	if len(objectIDs) == 0 {
		return out, nil
	}
	canonicalByID, err := s.canonicalObjectIDs(ctx, objectIDs)
	if err != nil {
		return nil, err
	}
	canonicalIDs := make([]string, 0, len(canonicalByID))
	for _, canonicalID := range canonicalByID {
		canonicalIDs = append(canonicalIDs, canonicalID)
	}
	canonicalIDs = uniqueStrings(canonicalIDs)
	query := fmt.Sprintf(`
SELECT object_id, argMax(cluster_id, observed_at) AS cluster_id, argMax(api_group, observed_at) AS api_group, argMax(api_version, observed_at) AS api_version, argMax(resource, observed_at) AS resource, argMax(kind, observed_at) AS kind, argMax(namespace, observed_at) AS namespace, argMax(name, observed_at) AS name, argMax(uid, observed_at) AS uid, max(observed_at) AS latest_observed_at
FROM %s.versions
WHERE object_id IN (%s)
GROUP BY object_id`, q(s.database()), sqlStringList(canonicalIDs))
	result, err := s.client().QueryJSON(ctx, query)
	if err != nil {
		return nil, err
	}
	byCanonicalID := map[string]storage.ObjectRecord{}
	for _, row := range result.Data {
		object := objectRecordFromRow(row)
		byCanonicalID[object.LogicalID] = object
		out[object.LogicalID] = object
	}
	for _, objectID := range objectIDs {
		canonicalID := canonicalByID[objectID]
		if object := byCanonicalID[canonicalID]; object.LogicalID != "" {
			out[objectID] = object
		}
	}
	return out, nil
}

func (s *Store) canonicalObjectIDs(ctx context.Context, objectIDs []string) (map[string]string, error) {
	objectIDs = uniqueStrings(objectIDs)
	out := make(map[string]string, len(objectIDs))
	for _, objectID := range objectIDs {
		out[objectID] = objectID
	}
	if len(objectIDs) == 0 {
		return out, nil
	}
	query := fmt.Sprintf(`
SELECT alias_id, argMax(object_id, observed_at) AS object_id
FROM %s.object_aliases
WHERE alias_id IN (%s)
GROUP BY alias_id`, q(s.database()), sqlStringList(objectIDs))
	result, err := s.client().QueryJSON(ctx, query)
	if err != nil {
		return nil, err
	}
	for _, row := range result.Data {
		aliasID := stringValue(row["alias_id"])
		objectID := stringValue(row["object_id"])
		if aliasID != "" && objectID != "" {
			out[aliasID] = objectID
		}
	}
	return out, nil
}

func (s *Store) objectAndAliasIDs(ctx context.Context, objectID string) ([]string, error) {
	if objectID == "" {
		return nil, nil
	}
	canonicalByID, err := s.canonicalObjectIDs(ctx, []string{objectID})
	if err != nil {
		return nil, err
	}
	canonicalID := canonicalByID[objectID]
	if canonicalID == "" {
		canonicalID = objectID
	}
	out := []string{canonicalID}
	query := fmt.Sprintf(`
SELECT alias_id
FROM %s.object_aliases
WHERE object_id = %s
GROUP BY alias_id
ORDER BY alias_id`, q(s.database()), quoteString(canonicalID))
	result, err := s.client().QueryJSON(ctx, query)
	if err != nil {
		return nil, err
	}
	for _, row := range result.Data {
		if aliasID := stringValue(row["alias_id"]); aliasID != "" {
			out = append(out, aliasID)
		}
	}
	return uniqueStrings(out), nil
}

func (s *Store) evidenceBundle(ctx context.Context, object storage.ObjectRecord, opts storage.InvestigationOptions) (storage.EvidenceBundle, error) {
	latest, err := s.latestDocument(ctx, object.LogicalID)
	if err != nil {
		return storage.EvidenceBundle{}, err
	}
	facts, err := s.factsForObject(ctx, object.LogicalID, opts)
	if err != nil {
		return storage.EvidenceBundle{}, err
	}
	changes, err := s.changesForObject(ctx, object.LogicalID, opts)
	if err != nil {
		return storage.EvidenceBundle{}, err
	}
	edges, err := s.GetEdges(ctx, object.LogicalID)
	if err != nil {
		return storage.EvidenceBundle{}, err
	}
	bundle := storage.EvidenceBundle{Object: object, Latest: latest, Facts: facts, Edges: edges, Changes: changes, Summary: storage.BundleSummary{Facts: len(facts), Edges: len(edges), Changes: len(changes), EvidenceScore: len(facts)*20 + len(edges)*10 + len(changes)*15}}
	if opts.MaxVersionsPerObject > 0 || !opts.From.IsZero() || !opts.To.IsZero() {
		maxVersions := boundedLimit(opts.MaxVersionsPerObject, 3, 100)
		versions, err := s.historyVersions(ctx, object.LogicalID, storage.ObjectHistoryOptions{From: opts.From, To: opts.To, MaxVersions: maxVersions, IncludeDocs: false})
		if err != nil {
			return storage.EvidenceBundle{}, err
		}
		bundle.Versions = make([]storage.VersionEvidence, 0, len(versions))
		for _, version := range versions {
			bundle.Versions = append(bundle.Versions, storage.VersionEvidence{ID: version.ID, Sequence: version.Sequence, ObservedAt: version.ObservedAt, ResourceVersion: version.ResourceVersion, DocumentHash: version.DocumentHash, Materialization: version.Materialization, Strategy: version.Strategy, ReplayDepth: version.ReplayDepth})
		}
		bundle.Summary.Versions = len(bundle.Versions)
	}
	return bundle, nil
}

func (s *Store) latestDocument(ctx context.Context, objectID string) (map[string]any, error) {
	query := fmt.Sprintf("SELECT doc FROM %s.versions WHERE object_id = %s ORDER BY observed_at DESC, seq DESC LIMIT 1", q(s.database()), quoteString(objectID))
	result, err := s.client().QueryJSON(ctx, query)
	if err != nil {
		return nil, err
	}
	if len(result.Data) == 0 {
		return nil, fmt.Errorf("latest document missing")
	}
	return jsonMap(result.Data[0]["doc"]), nil
}

func (s *Store) factsForObject(ctx context.Context, objectID string, opts storage.InvestigationOptions) ([]core.Fact, error) {
	query := fmt.Sprintf("SELECT ts, object_id, fact_key, fact_value, numeric_value, severity, detail FROM %s.facts WHERE object_id = %s%s ORDER BY ts DESC LIMIT 1000", q(s.database()), quoteString(objectID), timeFilter("ts", opts.From, opts.To))
	result, err := s.client().QueryJSON(ctx, query)
	if err != nil {
		return nil, err
	}
	facts := make([]core.Fact, 0, len(result.Data))
	for _, row := range result.Data {
		facts = append(facts, core.Fact{Time: timeValue(row["ts"]), ObjectID: stringValue(row["object_id"]), Key: stringValue(row["fact_key"]), Value: stringValue(row["fact_value"]), NumericValue: numericPointer(row["numeric_value"]), Severity: int(int64Value(row["severity"])), Detail: jsonMap(row["detail"])})
	}
	return facts, nil
}

func (s *Store) changesForObject(ctx context.Context, objectID string, opts storage.InvestigationOptions) ([]core.Change, error) {
	query := fmt.Sprintf("SELECT ts, object_id, change_family, path, op, old_scalar, new_scalar, severity FROM %s.changes WHERE object_id = %s%s ORDER BY ts DESC LIMIT 1000", q(s.database()), quoteString(objectID), timeFilter("ts", opts.From, opts.To))
	result, err := s.client().QueryJSON(ctx, query)
	if err != nil {
		return nil, err
	}
	changes := make([]core.Change, 0, len(result.Data))
	for _, row := range result.Data {
		changes = append(changes, core.Change{Time: timeValue(row["ts"]), ObjectID: stringValue(row["object_id"]), Family: stringValue(row["change_family"]), Path: stringValue(row["path"]), Op: stringValue(row["op"]), Old: stringValue(row["old_scalar"]), New: stringValue(row["new_scalar"]), Severity: int(int64Value(row["severity"]))})
	}
	return changes, nil
}

func objectRecordFromRow(row map[string]any) storage.ObjectRecord {
	return storage.ObjectRecord{LogicalID: stringValue(row["object_id"]), ClusterID: stringValue(row["cluster_id"]), Group: stringValue(row["api_group"]), Version: stringValue(row["api_version"]), Resource: stringValue(row["resource"]), Kind: stringValue(row["kind"]), Namespace: stringValue(row["namespace"]), Name: stringValue(row["name"]), UID: stringValue(row["uid"]), LatestObservedAt: timeValue(row["latest_observed_at"])}
}

func clickHouseVersionDiffs(versions []storage.HistoryVersion) []storage.VersionDiff {
	if len(versions) < 2 {
		return nil
	}
	diffs := make([]storage.VersionDiff, 0, len(versions)-1)
	for i := 0; i+1 < len(versions); i++ {
		diffs = append(diffs, storage.VersionDiff{FromVersionID: versions[i+1].ID, ToVersionID: versions[i].ID, FromSequence: versions[i+1].Sequence, ToSequence: versions[i].Sequence, Truncated: true})
	}
	return diffs
}

func searchTerms(query string) []string {
	fields := strings.Fields(strings.ToLower(strings.TrimSpace(query)))
	return uniqueStrings(fields)
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func timeFilter(column string, from, to time.Time) string {
	var parts []string
	if !from.IsZero() {
		parts = append(parts, fmt.Sprintf("%s >= %s", column, quoteString(clickHouseTime(from))))
	}
	if !to.IsZero() {
		parts = append(parts, fmt.Sprintf("%s <= %s", column, quoteString(clickHouseTime(to))))
	}
	if len(parts) == 0 {
		return ""
	}
	return " AND " + strings.Join(parts, " AND ")
}

func jsonMap(value any) map[string]any {
	switch v := value.(type) {
	case nil:
		return nil
	case map[string]any:
		return v
	case string:
		var out map[string]any
		if err := json.Unmarshal([]byte(v), &out); err == nil {
			return out
		}
	}
	return nil
}

func timeValue(value any) time.Time {
	s := stringValue(value)
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{"2006-01-02 15:04:05.000", "2006-01-02 15:04:05", time.RFC3339Nano, time.RFC3339} {
		if t, err := time.ParseInLocation(layout, s, time.UTC); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

func numericPointer(value any) *float64 {
	switch v := value.(type) {
	case nil:
		return nil
	case float64:
		return &v
	case json.Number:
		if f, err := v.Float64(); err == nil {
			return &f
		}
	case string:
		if v == "" {
			return nil
		}
		var f float64
		if _, err := fmt.Sscan(v, &f); err == nil {
			return &f
		}
	}
	return nil
}
