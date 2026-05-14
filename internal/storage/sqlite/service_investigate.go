package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"kube-insight/internal/core"
)

type ServiceInvestigation struct {
	Service  EvidenceBundle              `json:"service"`
	Objects  []EvidenceBundle            `json:"objects"`
	Topology []TopologyEdge              `json:"topology"`
	Summary  ServiceInvestigationSummary `json:"summary"`
}

type ServiceInvestigationSummary struct {
	Objects        int `json:"objects"`
	EndpointSlices int `json:"endpointSlices"`
	Pods           int `json:"pods"`
	Nodes          int `json:"nodes"`
	Events         int `json:"events"`
	Facts          int `json:"facts"`
	Changes        int `json:"changes"`
	Edges          int `json:"edges"`
	Versions       int `json:"versions"`
	VersionDiffs   int `json:"versionDiffs"`
}

type InvestigationOptions struct {
	From                 time.Time
	To                   time.Time
	MaxEvidenceObjects   int
	MaxVersionsPerObject int
}

func (s *Store) InvestigateService(ctx context.Context, target ObjectTarget) (ServiceInvestigation, error) {
	return s.InvestigateServiceWithOptions(ctx, target, InvestigationOptions{})
}

func (s *Store) InvestigateServiceWithOptions(ctx context.Context, target ObjectTarget, opts InvestigationOptions) (ServiceInvestigation, error) {
	if target.Kind == "" {
		target.Kind = "Service"
	}
	if target.Kind != "Service" {
		return ServiceInvestigation{}, fmt.Errorf("service investigation requires kind Service")
	}
	serviceRecord, serviceID, err := s.FindObject(ctx, target)
	if err != nil {
		return ServiceInvestigation{}, err
	}
	service, err := s.bundleForRecordWithOptions(ctx, serviceRecord, opts)
	if err != nil {
		return ServiceInvestigation{}, err
	}

	visited := map[string]bool{serviceRecord.LogicalID: true}
	queued := []int64{serviceID}
	topologyByKey := map[string]TopologyEdge{}
	recordsByID := map[string]ObjectRecord{}
	for len(queued) > 0 {
		nextID := queued[0]
		queued = queued[1:]
		edges, err := s.relatedEdges(ctx, nextID)
		if err != nil {
			return ServiceInvestigation{}, err
		}
		for _, edge := range edges {
			if !topologyEdgeInWindow(edge, opts) {
				continue
			}
			topologyByKey[topologyEdgeKey(edge)] = edge
			for _, record := range []ObjectRecord{edge.Source, edge.Target} {
				if record.LogicalID == "" || visited[record.LogicalID] {
					continue
				}
				if !serviceInvestigationKind(record.Kind) {
					continue
				}
				visited[record.LogicalID] = true
				recordsByID[record.LogicalID] = record
				if record.Kind == "EndpointSlice" || record.Kind == "Pod" {
					if _, dbID, err := s.FindObject(ctx, targetForRecord(record)); err == nil {
						queued = append(queued, dbID)
					}
				}
			}
		}
	}

	objects := make([]EvidenceBundle, 0, len(recordsByID))
	for _, record := range recordsByID {
		bundle, err := s.bundleForRecordWithOptions(ctx, record, opts)
		if err != nil {
			if errors.Is(err, errLatestDocumentMissing) {
				continue
			}
			return ServiceInvestigation{}, err
		}
		objects = append(objects, bundle)
	}
	topology := make([]TopologyEdge, 0, len(topologyByKey))
	for _, edge := range topologyByKey {
		topology = append(topology, edge)
	}
	rankServiceEvidence(&service, objects)
	if err := s.attachServiceVersions(ctx, &service, objects, opts); err != nil {
		return ServiceInvestigation{}, err
	}
	sort.Slice(topology, func(i, j int) bool {
		if topology[i].ValidFrom.Equal(topology[j].ValidFrom) {
			return topologyEdgeKey(topology[i]) < topologyEdgeKey(topology[j])
		}
		return topology[i].ValidFrom.Before(topology[j].ValidFrom)
	})
	out := ServiceInvestigation{
		Service:  service,
		Objects:  objects,
		Topology: topology,
	}
	out.Summary = summarizeServiceInvestigation(out)
	return out, nil
}

var errLatestDocumentMissing = errors.New("latest document missing")

func (s *Store) bundleForRecord(ctx context.Context, record ObjectRecord) (EvidenceBundle, error) {
	return s.bundleForRecordWithOptions(ctx, record, InvestigationOptions{})
}

func (s *Store) bundleForRecordWithOptions(ctx context.Context, record ObjectRecord, opts InvestigationOptions) (EvidenceBundle, error) {
	latestRecord, dbID, err := s.FindObject(ctx, targetForRecord(record))
	if err != nil {
		return EvidenceBundle{}, err
	}
	latest, err := s.LatestDocument(ctx, dbID)
	if err != nil {
		return EvidenceBundle{}, errLatestDocumentMissing
	}
	facts, err := s.GetFacts(ctx, latestRecord.LogicalID)
	if err != nil {
		return EvidenceBundle{}, err
	}
	edges, err := s.GetEdges(ctx, latestRecord.LogicalID)
	if err != nil {
		return EvidenceBundle{}, err
	}
	changes, err := s.GetChanges(ctx, latestRecord.LogicalID)
	if err != nil {
		return EvidenceBundle{}, err
	}
	facts = factsInWindow(facts, opts)
	edges = edgesInWindow(edges, opts)
	changes = changesInWindow(changes, opts)
	return EvidenceBundle{
		Object:  latestRecord,
		Latest:  latest,
		Facts:   facts,
		Edges:   edges,
		Changes: changes,
		Summary: BundleSummary{
			Facts:         len(facts),
			Edges:         len(edges),
			Changes:       len(changes),
			EvidenceScore: evidenceScore(facts, edges, changes),
		},
	}, nil
}

func targetForRecord(record ObjectRecord) ObjectTarget {
	return ObjectTarget{
		ClusterID: record.ClusterID,
		Kind:      record.Kind,
		Namespace: record.Namespace,
		Name:      record.Name,
		UID:       record.UID,
	}
}

func serviceInvestigationKind(kind string) bool {
	switch kind {
	case "EndpointSlice", "Pod", "Node", "Event":
		return true
	default:
		return false
	}
}

func topologyEdgeKey(edge TopologyEdge) string {
	return edge.Type + "\x00" + edge.Source.LogicalID + "\x00" + edge.Target.LogicalID
}

func summarizeServiceInvestigation(in ServiceInvestigation) ServiceInvestigationSummary {
	out := ServiceInvestigationSummary{
		Objects:      len(in.Objects) + 1,
		Edges:        len(in.Topology),
		Facts:        len(in.Service.Facts),
		Changes:      len(in.Service.Changes),
		Versions:     len(in.Service.Versions),
		VersionDiffs: len(in.Service.VersionDiffs),
	}
	for _, bundle := range in.Objects {
		out.Facts += len(bundle.Facts)
		out.Changes += len(bundle.Changes)
		out.Versions += len(bundle.Versions)
		out.VersionDiffs += len(bundle.VersionDiffs)
		switch bundle.Object.Kind {
		case "EndpointSlice":
			out.EndpointSlices++
		case "Pod":
			out.Pods++
		case "Node":
			out.Nodes++
		case "Event":
			out.Events++
		}
	}
	return out
}

func (s *Store) attachServiceVersions(ctx context.Context, service *EvidenceBundle, objects []EvidenceBundle, opts InvestigationOptions) error {
	maxObjects, maxVersions := investigationVersionLimits(opts)
	if err := s.attachBundleVersions(ctx, service, opts, maxVersions); err != nil {
		return err
	}
	for i := range objects {
		if i >= maxObjects {
			break
		}
		if err := s.attachBundleVersions(ctx, &objects[i], opts, maxVersions); err != nil {
			return err
		}
	}
	return nil
}

func investigationVersionLimits(opts InvestigationOptions) (int, int) {
	maxObjects := opts.MaxEvidenceObjects
	if maxObjects <= 0 {
		maxObjects = 5
	}
	maxVersions := opts.MaxVersionsPerObject
	if maxVersions <= 0 {
		maxVersions = 3
	}
	return maxObjects, maxVersions
}

func (s *Store) attachBundleVersions(ctx context.Context, bundle *EvidenceBundle, opts InvestigationOptions, limit int) error {
	if limit <= 0 {
		return nil
	}
	versions, err := s.versionEvidence(ctx, targetForRecord(bundle.Object), opts, limit)
	if err != nil {
		return err
	}
	bundle.Versions = versions
	bundle.Summary.Versions = len(versions)
	bundle.VersionDiffs = compactVersionDiffs(versions)
	bundle.Summary.VersionDiffs = len(bundle.VersionDiffs)
	return nil
}

func (s *Store) versionEvidence(ctx context.Context, target ObjectTarget, opts InvestigationOptions, limit int) ([]VersionEvidence, error) {
	_, dbObjectID, err := s.FindObject(ctx, target)
	if err != nil {
		return nil, err
	}
	query := `
select
  v.id,
  v.seq,
  v.observed_at,
  coalesce(v.resource_version, ''),
  v.doc_hash,
  v.materialization,
  v.strategy,
  v.replay_depth,
  coalesce(v.summary, ''),
  b.data
from versions v
join blobs b on b.digest = v.blob_ref
where v.object_id = ?
  and (? = 0 or v.observed_at >= ?)
  and (? = 0 or v.observed_at <= ?)
order by v.observed_at desc, v.seq desc
limit ?`
	from := millis(opts.From)
	to := millis(opts.To)
	rows, err := s.db.QueryContext(ctx, query, dbObjectID, from, from, to, to, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []VersionEvidence
	for rows.Next() {
		var observedAt int64
		var summaryText string
		var docBytes []byte
		var evidence VersionEvidence
		if err := rows.Scan(
			&evidence.ID,
			&evidence.Sequence,
			&observedAt,
			&evidence.ResourceVersion,
			&evidence.DocumentHash,
			&evidence.Materialization,
			&evidence.Strategy,
			&evidence.ReplayDepth,
			&summaryText,
			&docBytes,
		); err != nil {
			return nil, err
		}
		evidence.ObservedAt = time.UnixMilli(observedAt)
		if summaryText != "" {
			_ = json.Unmarshal([]byte(summaryText), &evidence.Summary)
		}
		if err := json.Unmarshal(docBytes, &evidence.Document); err != nil {
			return nil, err
		}
		out = append(out, evidence)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) > 0 || !hasInvestigationWindow(opts) {
		return out, nil
	}
	return s.latestVersionEvidence(ctx, dbObjectID)
}

func (s *Store) latestVersionEvidence(ctx context.Context, dbObjectID int64) ([]VersionEvidence, error) {
	row := s.db.QueryRowContext(ctx, `
select
  v.id,
  v.seq,
  v.observed_at,
  coalesce(v.resource_version, ''),
  v.doc_hash,
  v.materialization,
  v.strategy,
  v.replay_depth,
  coalesce(v.summary, ''),
  b.data
from versions v
join blobs b on b.digest = v.blob_ref
where v.object_id = ?
order by v.observed_at desc, v.seq desc
limit 1`, dbObjectID)
	var observedAt int64
	var summaryText string
	var docBytes []byte
	var evidence VersionEvidence
	if err := row.Scan(
		&evidence.ID,
		&evidence.Sequence,
		&observedAt,
		&evidence.ResourceVersion,
		&evidence.DocumentHash,
		&evidence.Materialization,
		&evidence.Strategy,
		&evidence.ReplayDepth,
		&summaryText,
		&docBytes,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	evidence.ObservedAt = time.UnixMilli(observedAt)
	if summaryText != "" {
		_ = json.Unmarshal([]byte(summaryText), &evidence.Summary)
	}
	if err := json.Unmarshal(docBytes, &evidence.Document); err != nil {
		return nil, err
	}
	return []VersionEvidence{evidence}, nil
}

func rankServiceEvidence(service *EvidenceBundle, objects []EvidenceBundle) {
	service.Summary.Rank = 1
	sort.SliceStable(objects, func(i, j int) bool {
		left := objects[i]
		right := objects[j]
		if left.Summary.EvidenceScore != right.Summary.EvidenceScore {
			return left.Summary.EvidenceScore > right.Summary.EvidenceScore
		}
		if serviceKindPriority(left.Object.Kind) != serviceKindPriority(right.Object.Kind) {
			return serviceKindPriority(left.Object.Kind) < serviceKindPriority(right.Object.Kind)
		}
		if !left.Object.LatestObservedAt.Equal(right.Object.LatestObservedAt) {
			return left.Object.LatestObservedAt.After(right.Object.LatestObservedAt)
		}
		return left.Object.LogicalID < right.Object.LogicalID
	})
	for i := range objects {
		objects[i].Summary.Rank = i + 2
	}
}

func serviceKindPriority(kind string) int {
	switch kind {
	case "EndpointSlice":
		return 0
	case "Pod":
		return 1
	case "Node":
		return 2
	case "Event":
		return 3
	default:
		return 4
	}
}

func evidenceScore(facts []core.Fact, edges []core.Edge, changes []core.Change) int {
	score := len(edges)
	for _, fact := range facts {
		score += 1 + fact.Severity
	}
	for _, change := range changes {
		score += 3 + change.Severity
	}
	return score
}

func factsInWindow(facts []core.Fact, opts InvestigationOptions) []core.Fact {
	if !hasInvestigationWindow(opts) {
		return facts
	}
	out := facts[:0]
	for _, fact := range facts {
		if timeInWindow(fact.Time, opts) {
			out = append(out, fact)
		}
	}
	return out
}

func changesInWindow(changes []core.Change, opts InvestigationOptions) []core.Change {
	if !hasInvestigationWindow(opts) {
		return changes
	}
	out := changes[:0]
	for _, change := range changes {
		if timeInWindow(change.Time, opts) {
			out = append(out, change)
		}
	}
	return out
}

func edgesInWindow(edges []core.Edge, opts InvestigationOptions) []core.Edge {
	if !hasInvestigationWindow(opts) {
		return edges
	}
	out := edges[:0]
	for _, edge := range edges {
		if edgeInWindow(edge.ValidFrom, edge.ValidTo, opts) {
			out = append(out, edge)
		}
	}
	return out
}

func topologyEdgeInWindow(edge TopologyEdge, opts InvestigationOptions) bool {
	if !hasInvestigationWindow(opts) {
		return true
	}
	return edgeInWindow(edge.ValidFrom, edge.ValidTo, opts)
}

func edgeInWindow(validFrom time.Time, validTo *time.Time, opts InvestigationOptions) bool {
	if !opts.To.IsZero() && validFrom.After(opts.To) {
		return false
	}
	if !opts.From.IsZero() && validTo != nil && validTo.Before(opts.From) {
		return false
	}
	return true
}

func timeInWindow(t time.Time, opts InvestigationOptions) bool {
	if !opts.From.IsZero() && t.Before(opts.From) {
		return false
	}
	if !opts.To.IsZero() && t.After(opts.To) {
		return false
	}
	return true
}

func hasInvestigationWindow(opts InvestigationOptions) bool {
	return !opts.From.IsZero() || !opts.To.IsZero()
}

const maxDocumentDiffChanges = 50

func compactVersionDiffs(versions []VersionEvidence) []VersionDiff {
	if len(versions) < 2 {
		return nil
	}
	ascending := append([]VersionEvidence(nil), versions...)
	sort.Slice(ascending, func(i, j int) bool {
		if ascending[i].ObservedAt.Equal(ascending[j].ObservedAt) {
			return ascending[i].Sequence < ascending[j].Sequence
		}
		return ascending[i].ObservedAt.Before(ascending[j].ObservedAt)
	})
	out := make([]VersionDiff, 0, len(ascending)-1)
	for i := 1; i < len(ascending); i++ {
		diff := VersionDiff{
			FromVersionID: ascending[i-1].ID,
			ToVersionID:   ascending[i].ID,
			FromSequence:  ascending[i-1].Sequence,
			ToSequence:    ascending[i].Sequence,
		}
		diffDocument("", ascending[i-1].Document, ascending[i].Document, &diff.Changes, &diff.Truncated)
		out = append(out, diff)
	}
	return out
}

func diffDocument(path string, before, after any, changes *[]DocumentDiffChange, truncated *bool) {
	if *truncated {
		return
	}
	if len(*changes) >= maxDocumentDiffChanges {
		*truncated = true
		return
	}
	beforeMap, beforeIsMap := before.(map[string]any)
	afterMap, afterIsMap := after.(map[string]any)
	if beforeIsMap && afterIsMap {
		keys := make([]string, 0, len(beforeMap)+len(afterMap))
		seen := map[string]bool{}
		for key := range beforeMap {
			seen[key] = true
			keys = append(keys, key)
		}
		for key := range afterMap {
			if !seen[key] {
				keys = append(keys, key)
			}
		}
		sort.Strings(keys)
		for _, key := range keys {
			child := path + "/" + escapeJSONPointer(key)
			beforeValue, beforeOK := beforeMap[key]
			afterValue, afterOK := afterMap[key]
			switch {
			case !beforeOK:
				appendDocumentDiff(child, nil, afterValue, changes, truncated)
			case !afterOK:
				appendDocumentDiff(child, beforeValue, nil, changes, truncated)
			default:
				diffDocument(child, beforeValue, afterValue, changes, truncated)
			}
			if *truncated {
				return
			}
		}
		return
	}
	if documentScalar(before) == documentScalar(after) {
		return
	}
	appendDocumentDiff(path, before, after, changes, truncated)
}

func appendDocumentDiff(path string, before, after any, changes *[]DocumentDiffChange, truncated *bool) {
	if len(*changes) >= maxDocumentDiffChanges {
		*truncated = true
		return
	}
	if path == "" {
		path = "/"
	}
	*changes = append(*changes, DocumentDiffChange{
		Path:   path,
		Before: documentScalar(before),
		After:  documentScalar(after),
	})
}

func documentScalar(value any) string {
	if value == nil {
		return ""
	}
	switch v := value.(type) {
	case string:
		return v
	case float64, bool:
		return fmt.Sprint(v)
	default:
		data, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprint(v)
		}
		return string(data)
	}
}

func escapeJSONPointer(in string) string {
	in = strings.ReplaceAll(in, "~", "~0")
	return strings.ReplaceAll(in, "/", "~1")
}
