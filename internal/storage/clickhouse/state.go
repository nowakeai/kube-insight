package clickhouse

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"kube-insight/internal/core"
	"kube-insight/internal/kubeapi"
	"kube-insight/internal/storage"
)

func (s *Store) UpsertAPIResources(ctx context.Context, resources []kubeapi.ResourceInfo, discoveredAt time.Time) error {
	if s == nil || len(resources) == 0 {
		return nil
	}
	if discoveredAt.IsZero() {
		discoveredAt = time.Now().UTC()
	}
	rows := make([]map[string]any, 0, len(resources))
	for _, resource := range resources {
		if resource.Resource == "" || resource.Kind == "" {
			continue
		}
		verbs, err := json.Marshal(resource.Verbs)
		if err != nil {
			return err
		}
		rows = append(rows, map[string]any{
			"api_group":          resource.Group,
			"api_version":        resource.Version,
			"resource":           resource.Resource,
			"kind":               resource.Kind,
			"namespaced":         resource.Namespaced,
			"verbs":              string(verbs),
			"last_discovered_at": clickHouseTime(discoveredAt),
		})
	}
	return s.client().InsertRows(ctx, s.database(), "api_resources", rows)
}

func (s *Store) APIResources(ctx context.Context) ([]kubeapi.ResourceInfo, error) {
	if s == nil {
		return nil, nil
	}
	query := fmt.Sprintf(`
SELECT
  api_group,
  api_version,
  resource,
  argMax(kind, last_discovered_at) AS kind,
  argMax(namespaced, last_discovered_at) AS namespaced,
  argMax(verbs, last_discovered_at) AS verbs
FROM %s.api_resources
GROUP BY api_group, api_version, resource
ORDER BY api_group, api_version, resource`, q(s.database()))
	result, err := s.client().QueryJSON(ctx, query)
	if err != nil {
		return nil, err
	}
	out := make([]kubeapi.ResourceInfo, 0, len(result.Data))
	for _, row := range result.Data {
		var verbs []string
		if raw := stringValue(row["verbs"]); raw != "" {
			_ = json.Unmarshal([]byte(raw), &verbs)
		}
		out = append(out, kubeapi.ResourceInfo{
			Group:      stringValue(row["api_group"]),
			Version:    stringValue(row["api_version"]),
			Resource:   stringValue(row["resource"]),
			Kind:       stringValue(row["kind"]),
			Namespaced: boolValue(row["namespaced"]),
			Verbs:      verbs,
		})
	}
	return out, nil
}

func (s *Store) UpsertIngestionOffset(ctx context.Context, offset storage.IngestionOffset) error {
	if s == nil || offset.ClusterID == "" || offset.Resource.Resource == "" {
		return nil
	}
	if offset.Status == "" {
		offset.Status = "ok"
	}
	if offset.At.IsZero() {
		offset.At = time.Now().UTC()
	}
	row := map[string]any{
		"cluster_id":       offset.ClusterID,
		"api_group":        offset.Resource.Group,
		"api_version":      offset.Resource.Version,
		"resource":         offset.Resource.Resource,
		"kind":             offset.Resource.Kind,
		"namespaced":       offset.Resource.Namespaced,
		"namespace":        offset.Namespace,
		"resource_version": offset.ResourceVersion,
		"event":            string(offset.Event),
		"status":           offset.Status,
		"error":            offset.Error,
		"last_list_at":     nil,
		"last_watch_at":    nil,
		"last_bookmark_at": nil,
		"updated_at":       clickHouseTime(offset.At),
	}
	switch offset.Event {
	case storage.OffsetEventList:
		row["last_list_at"] = clickHouseTime(offset.At)
	case storage.OffsetEventWatch:
		row["last_watch_at"] = clickHouseTime(offset.At)
	case storage.OffsetEventBookmark:
		row["last_bookmark_at"] = clickHouseTime(offset.At)
	}
	s.mu.Lock()
	if s.pendingOffsets == nil {
		s.pendingOffsets = map[string]map[string]any{}
	}
	s.pendingOffsets[ingestionOffsetKey(offset)] = row
	if s.pendingRowsLocked() >= s.batchSizeLocked() {
		batch := s.drainLocked()
		s.mu.Unlock()
		return s.insertPending(ctx, batch)
	}
	s.armFlushTimerLocked()
	s.mu.Unlock()
	return nil
}

func ingestionOffsetKey(offset storage.IngestionOffset) string {
	resource := offset.Resource
	return strings.Join([]string{
		offset.ClusterID,
		resource.Group,
		resource.Version,
		resource.Resource,
		offset.Namespace,
		string(offset.Event),
	}, "\x00")
}

func (s *Store) LatestResourceRefs(ctx context.Context, clusterID string, resource kubeapi.ResourceInfo, namespace string) ([]core.ResourceRef, error) {
	if s == nil {
		return nil, nil
	}
	if err := s.Flush(ctx); err != nil {
		return nil, err
	}
	query := fmt.Sprintf(`
SELECT
  api_group,
  api_version,
  resource,
  argMax(kind, observed_at) AS kind,
  namespace,
  name,
  identity,
  argMax(uid, observed_at) AS uid,
  argMax(observation_type, observed_at) AS latest_type
FROM
(
  SELECT
    api_group,
    api_version,
    resource,
    kind,
    namespace,
    name,
    if(uid != '', concat('uid:', uid), concat('name:', namespace, '/', name)) AS identity,
    uid,
    observation_type,
    observed_at
  FROM %s.observations
  WHERE cluster_id = %s
    AND api_group = %s
    AND api_version = %s
    AND resource = %s
    AND (%s = '' OR namespace = %s)
)
GROUP BY api_group, api_version, resource, namespace, name, identity
HAVING latest_type != 'DELETED'
ORDER BY namespace, name`,
		q(s.database()),
		quoteString(clusterID),
		quoteString(resource.Group),
		quoteString(resource.Version),
		quoteString(resource.Resource),
		quoteString(namespace),
		quoteString(namespace),
	)
	result, err := s.client().QueryJSON(ctx, query)
	if err != nil {
		return nil, err
	}
	out := make([]core.ResourceRef, 0, len(result.Data))
	for _, row := range result.Data {
		out = append(out, core.ResourceRef{
			ClusterID: clusterID,
			Group:     stringValue(row["api_group"]),
			Version:   stringValue(row["api_version"]),
			Resource:  stringValue(row["resource"]),
			Kind:      stringValue(row["kind"]),
			Namespace: stringValue(row["namespace"]),
			Name:      stringValue(row["name"]),
			UID:       stringValue(row["uid"]),
		})
	}
	return out, nil
}

func (s *Store) database() string {
	if s.Database == "" {
		return defaultDatabase
	}
	return s.Database
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	if s, ok := value.(string); ok {
		return s
	}
	return fmt.Sprint(value)
}

func (s *Store) ResourceHealth(ctx context.Context, opts storage.ResourceHealthOptions) (storage.ResourceHealthReport, error) {
	if s == nil {
		return storage.ResourceHealthReport{}, nil
	}
	if err := s.Flush(ctx); err != nil {
		return storage.ResourceHealthReport{}, err
	}
	resources, err := s.APIResources(ctx)
	if err != nil {
		return storage.ResourceHealthReport{}, err
	}
	latestCounts, err := s.latestObjectCounts(ctx, opts.ClusterID)
	if err != nil {
		return storage.ResourceHealthReport{}, err
	}
	offsets, err := s.resourceHealthOffsets(ctx, opts.ClusterID)
	if err != nil {
		return storage.ResourceHealthReport{}, err
	}

	clusters := healthClusters(opts.ClusterID, latestCounts, offsets)
	resourceOffsets := map[string]bool{}
	candidates := make([]storage.ResourceHealthRecord, 0, len(offsets)+len(resources))
	for _, record := range offsets {
		key := healthKey(record.ClusterID, record.Group, record.Version, record.Resource, record.Namespace)
		record.LatestObjects = latestCounts[key]
		candidates = append(candidates, record)
		resourceOffsets[healthResourceKey(record.ClusterID, record.Group, record.Version, record.Resource)] = true
	}
	for _, clusterID := range clusters {
		for _, resource := range resources {
			if !watchableResource(resource) {
				continue
			}
			if resourceOffsets[healthResourceKey(clusterID, resource.Group, resource.Version, resource.Resource)] {
				continue
			}
			candidates = append(candidates, storage.ResourceHealthRecord{
				ClusterID:     clusterID,
				Group:         resource.Group,
				Version:       resource.Version,
				Resource:      resource.Resource,
				Kind:          resource.Kind,
				Namespaced:    resource.Namespaced,
				Status:        "not_started",
				LatestObjects: latestCounts[healthKey(clusterID, resource.Group, resource.Version, resource.Resource, "")],
			})
		}
	}

	now := time.Now().UTC()
	report := storage.ResourceHealthReport{CheckedAt: now, ByStatus: map[string]int{}}
	for _, record := range candidates {
		if record.UpdatedAt != nil {
			record.AgeSeconds = int64(now.Sub(*record.UpdatedAt).Seconds())
			if record.AgeSeconds < 0 {
				record.AgeSeconds = 0
			}
			record.Stale = resourceHealthCanBecomeStale(record) && opts.StaleAfter > 0 && now.Sub(*record.UpdatedAt) > opts.StaleAfter
		}
		if resourceHealthExcluded(record, opts.ExcludeResources) {
			report.Summary.Skipped++
			if !opts.IncludeExcluded {
				continue
			}
			record.Status = "skipped"
			record.Error = ""
			record.Skipped = true
		}
		if !resourceHealthMatches(record, opts) {
			continue
		}
		if opts.Limit <= 0 || len(report.Resources) < opts.Limit {
			report.Resources = append(report.Resources, record)
		}
		report.ByStatus[record.Status]++
		report.Summary.Resources++
		summarizeResourceHealth(&report.Summary, record)
	}
	report.Summary = finalizeResourceHealthSummary(report.Summary, opts)
	return report, nil
}

func (s *Store) resourceHealthOffsets(ctx context.Context, clusterID string) ([]storage.ResourceHealthRecord, error) {
	query := fmt.Sprintf(`
WITH latest_clusters AS (
  SELECT
    name,
    argMax(uid, created_at) AS cluster_uid,
    argMax(source, created_at) AS cluster_source
  FROM %s.clusters
  GROUP BY name
)
SELECT
  io.cluster_id AS cluster_id,
  any(lc.cluster_uid) AS cluster_uid,
  any(lc.cluster_source) AS cluster_source,
  api_group,
  api_version,
  resource,
  argMax(kind, updated_at) AS kind,
  argMax(namespaced, updated_at) AS namespaced,
  namespace,
  argMax(status, updated_at) AS status,
  argMax(error, updated_at) AS error,
  argMax(resource_version, updated_at) AS resource_version,
  maxIf(toUnixTimestamp64Milli(updated_at), event = 'list') AS last_list_ms,
  maxIf(toUnixTimestamp64Milli(updated_at), event = 'watch') AS last_watch_ms,
  maxIf(toUnixTimestamp64Milli(updated_at), event = 'bookmark') AS last_bookmark_ms,
  max(toUnixTimestamp64Milli(updated_at)) AS updated_ms
FROM %s.ingestion_offsets io
LEFT JOIN latest_clusters lc ON lc.name = io.cluster_id
WHERE (%s = '' OR io.cluster_id = %s)
GROUP BY io.cluster_id, api_group, api_version, resource, namespace
ORDER BY updated_ms DESC`,
		q(s.database()),
		q(s.database()),
		quoteString(clusterID),
		quoteString(clusterID),
	)
	result, err := s.client().QueryJSON(ctx, query)
	if err != nil {
		return nil, err
	}
	out := make([]storage.ResourceHealthRecord, 0, len(result.Data))
	for _, row := range result.Data {
		record := storage.ResourceHealthRecord{
			ClusterID:       stringValue(row["cluster_id"]),
			ClusterUID:      stringValue(row["cluster_uid"]),
			ClusterSource:   stringValue(row["cluster_source"]),
			Group:           stringValue(row["api_group"]),
			Version:         stringValue(row["api_version"]),
			Resource:        stringValue(row["resource"]),
			Kind:            stringValue(row["kind"]),
			Namespaced:      boolValue(row["namespaced"]),
			Namespace:       stringValue(row["namespace"]),
			Status:          stringValue(row["status"]),
			Error:           stringValue(row["error"]),
			ResourceVersion: stringValue(row["resource_version"]),
			LastListAt:      msTime(row["last_list_ms"]),
			LastWatchAt:     msTime(row["last_watch_ms"]),
			LastBookmarkAt:  msTime(row["last_bookmark_ms"]),
			UpdatedAt:       msTime(row["updated_ms"]),
		}
		out = append(out, record)
	}
	return out, nil
}

func (s *Store) latestObjectCounts(ctx context.Context, clusterID string) (map[string]int, error) {
	query := fmt.Sprintf(`
SELECT cluster_id, api_group, api_version, resource, namespace, count() AS latest_objects
FROM
(
  SELECT
    cluster_id,
    api_group,
    api_version,
    resource,
    namespace,
    if(uid != '', concat('uid:', uid), concat('name:', namespace, '/', name)) AS identity,
    argMax(observation_type, observed_at) AS latest_type
  FROM %s.observations
  WHERE (%s = '' OR cluster_id = %s)
  GROUP BY cluster_id, api_group, api_version, resource, namespace, identity
  HAVING latest_type != 'DELETED'
)
GROUP BY cluster_id, api_group, api_version, resource, namespace`, q(s.database()), quoteString(clusterID), quoteString(clusterID))
	result, err := s.client().QueryJSON(ctx, query)
	if err != nil {
		return nil, err
	}
	out := map[string]int{}
	for _, row := range result.Data {
		clusterID := stringValue(row["cluster_id"])
		group := stringValue(row["api_group"])
		version := stringValue(row["api_version"])
		resource := stringValue(row["resource"])
		namespace := stringValue(row["namespace"])
		count := int(int64Value(row["latest_objects"]))
		out[healthKey(clusterID, group, version, resource, namespace)] = count
		if namespace != "" {
			out[healthKey(clusterID, group, version, resource, "")] += count
		}
	}
	return out, nil
}

func healthClusters(clusterID string, latestCounts map[string]int, offsets []storage.ResourceHealthRecord) []string {
	seen := map[string]bool{}
	if clusterID != "" {
		seen[clusterID] = true
	}
	for key := range latestCounts {
		parts := strings.SplitN(key, "\x00", 2)
		if parts[0] != "" {
			seen[parts[0]] = true
		}
	}
	for _, record := range offsets {
		if record.ClusterID != "" {
			seen[record.ClusterID] = true
		}
	}
	out := make([]string, 0, len(seen))
	for cluster := range seen {
		out = append(out, cluster)
	}
	sort.Strings(out)
	return out
}

func watchableResource(resource kubeapi.ResourceInfo) bool {
	return hasVerb(resource.Verbs, "list") && hasVerb(resource.Verbs, "watch")
}

func hasVerb(verbs []string, want string) bool {
	for _, verb := range verbs {
		if verb == want {
			return true
		}
	}
	return false
}

func summarizeResourceHealth(summary *storage.ResourceHealthSummary, record storage.ResourceHealthRecord) {
	switch {
	case record.Status == "not_started":
		summary.NotStarted++
	case record.Status == "queued":
		summary.Queued++
	case record.Status == "skipped":
	case isResourceHealthError(record):
		summary.Errors++
	case isResourceHealthUnstable(record):
		summary.Unstable++
	case record.Stale:
		summary.Stale++
	default:
		summary.Healthy++
	}
}

func finalizeResourceHealthSummary(summary storage.ResourceHealthSummary, opts storage.ResourceHealthOptions) storage.ResourceHealthSummary {
	summary.Complete = summary.Errors == 0 && summary.Unstable == 0 && summary.Stale == 0 && summary.NotStarted == 0
	if summary.Resources == 0 && summary.Skipped == 0 && opts.Status == "" && !opts.ErrorsOnly {
		summary.Complete = false
		summary.Warnings = append(summary.Warnings, "no discovered resources in health report")
	}
	if summary.Errors > 0 {
		summary.Warnings = append(summary.Warnings, fmt.Sprintf("%d resource stream(s) have errors", summary.Errors))
	}
	if summary.Unstable > 0 {
		summary.Warnings = append(summary.Warnings, fmt.Sprintf("%d resource stream(s) are retrying", summary.Unstable))
	}
	if summary.Stale > 0 {
		summary.Warnings = append(summary.Warnings, fmt.Sprintf("%d resource stream(s) are stale", summary.Stale))
	}
	if summary.NotStarted > 0 {
		summary.Warnings = append(summary.Warnings, fmt.Sprintf("%d resource stream(s) have not started", summary.NotStarted))
	}
	return summary
}

func resourceHealthExcluded(record storage.ResourceHealthRecord, excludes []string) bool {
	if len(excludes) == 0 {
		return false
	}
	candidates := resourceHealthCandidates(record)
	for _, exclude := range excludes {
		exclude = strings.ToLower(strings.TrimSpace(exclude))
		if exclude == "" {
			continue
		}
		for _, candidate := range candidates {
			if candidate == exclude {
				return true
			}
		}
	}
	return false
}

func resourceHealthCandidates(record storage.ResourceHealthRecord) []string {
	resource := strings.ToLower(strings.TrimSpace(record.Resource))
	group := strings.ToLower(strings.TrimSpace(record.Group))
	version := strings.ToLower(strings.TrimSpace(record.Version))
	var candidates []string
	if group != "" {
		candidates = append(candidates, resource+"."+group)
	} else {
		candidates = append(candidates, resource)
	}
	if version != "" {
		if group == "" {
			candidates = append(candidates, version+"/"+resource)
		} else {
			candidates = append(candidates, group+"/"+version+"/"+resource)
		}
	}
	return candidates
}

func resourceHealthMatches(record storage.ResourceHealthRecord, opts storage.ResourceHealthOptions) bool {
	if opts.Status != "" && record.Status != opts.Status {
		return false
	}
	if opts.ErrorsOnly && !isResourceHealthError(record) {
		return false
	}
	return true
}

func isResourceHealthError(record storage.ResourceHealthRecord) bool {
	return record.Status == "watch_error" || record.Status == "list_error"
}

func isResourceHealthUnstable(record storage.ResourceHealthRecord) bool {
	return record.Status == "retrying"
}

func resourceHealthCanBecomeStale(record storage.ResourceHealthRecord) bool {
	switch record.Status {
	case "not_started", "queued", "listed", "skipped":
		return false
	default:
		return true
	}
}

func healthKey(clusterID, group, version, resource, namespace string) string {
	return healthResourceKey(clusterID, group, version, resource) + "\x00" + namespace
}

func healthResourceKey(clusterID, group, version, resource string) string {
	return clusterID + "\x00" + group + "\x00" + version + "\x00" + resource
}

func msTime(value any) *time.Time {
	ms := int64Value(value)
	if ms <= 0 {
		return nil
	}
	t := time.UnixMilli(ms).UTC()
	return &t
}

func int64Value(value any) int64 {
	switch v := value.(type) {
	case nil:
		return 0
	case int64:
		return v
	case int:
		return int64(v)
	case float64:
		return int64(v)
	case json.Number:
		out, _ := v.Int64()
		return out
	case string:
		out, _ := strconv.ParseInt(v, 10, 64)
		return out
	default:
		return 0
	}
}

func boolValue(value any) bool {
	switch v := value.(type) {
	case bool:
		return v
	case float64:
		return v != 0
	case string:
		out, _ := strconv.ParseBool(v)
		return out
	default:
		return false
	}
}
