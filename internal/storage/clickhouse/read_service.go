package clickhouse

import (
	"context"
	"fmt"
	"sort"

	"kube-insight/internal/storage"
)

func (s *Store) InvestigateServiceWithOptions(ctx context.Context, target storage.ObjectTarget, opts storage.InvestigationOptions) (storage.ServiceInvestigation, error) {
	if target.Kind == "" {
		target.Kind = "Service"
	}
	if target.Kind != "Service" {
		return storage.ServiceInvestigation{}, fmt.Errorf("service investigation requires kind Service")
	}
	serviceObject, err := s.findObject(ctx, target)
	if err != nil {
		return storage.ServiceInvestigation{}, err
	}
	bundleOpts := investigationBundleOptions(opts)
	maxObjects := boundedLimit(opts.MaxEvidenceObjects, 20, 1000)
	visited := map[string]bool{serviceObject.LogicalID: true}
	queued := []string{serviceObject.LogicalID}
	topologyRowsByKey := map[string]topologyEdgeRow{}
	recordsByID := map[string]storage.ObjectRecord{}
	for len(queued) > 0 {
		current := queued[0]
		queued = queued[1:]
		rows, err := s.relatedTopologyEdgeRows(ctx, current, opts)
		if err != nil {
			return storage.ServiceInvestigation{}, err
		}
		for _, row := range rows {
			topologyRowsByKey[row.key()] = row
			for _, record := range []storage.ObjectRecord{row.sourceRecord(), row.targetRecord()} {
				if record.LogicalID == "" || visited[record.LogicalID] {
					continue
				}
				if !serviceInvestigationKind(record.Kind) {
					continue
				}
				if len(recordsByID) >= maxObjects {
					continue
				}
				visited[record.LogicalID] = true
				recordsByID[record.LogicalID] = record
				if record.Kind == "EndpointSlice" || record.Kind == "Pod" {
					queued = append(queued, record.LogicalID)
				}
			}
		}
	}
	topologyRows := make([]topologyEdgeRow, 0, len(topologyRowsByKey))
	for _, row := range topologyRowsByKey {
		topologyRows = append(topologyRows, row)
	}
	topology, err := s.hydrateTopologyEdgeRows(ctx, topologyRows)
	if err != nil {
		return storage.ServiceInvestigation{}, err
	}
	hydratedRecordsByID := map[string]storage.ObjectRecord{}
	for _, edge := range topology {
		for _, record := range []storage.ObjectRecord{edge.Source, edge.Target} {
			if record.LogicalID == serviceObject.LogicalID || !serviceInvestigationKind(record.Kind) {
				continue
			}
			if _, ok := hydratedRecordsByID[record.LogicalID]; !ok && len(hydratedRecordsByID) >= maxObjects {
				continue
			}
			hydratedRecordsByID[record.LogicalID] = record
		}
	}
	recordsByID = hydratedRecordsByID
	records := make([]storage.ObjectRecord, 0, len(recordsByID)+1)
	records = append(records, serviceObject)
	for _, record := range recordsByID {
		records = append(records, record)
	}
	bundles, err := s.evidenceBundles(ctx, records, bundleOpts)
	if err != nil {
		return storage.ServiceInvestigation{}, err
	}
	if len(bundles) == 0 {
		return storage.ServiceInvestigation{}, fmt.Errorf("service evidence missing")
	}
	service := bundles[0]
	objects := bundles[1:]
	rankServiceEvidence(&service, objects)
	sort.Slice(topology, func(i, j int) bool {
		if topology[i].ValidFrom.Equal(topology[j].ValidFrom) {
			return topologyEdgeKey(topology[i]) < topologyEdgeKey(topology[j])
		}
		return topology[i].ValidFrom.Before(topology[j].ValidFrom)
	})
	out := storage.ServiceInvestigation{Service: service, Objects: objects, Topology: topology}
	out.Summary = summarizeServiceInvestigation(out)
	return out, nil
}

func investigationBundleOptions(opts storage.InvestigationOptions) storage.InvestigationOptions {
	out := opts
	out.MaxVersionsPerObject = boundedLimit(out.MaxVersionsPerObject, 3, 100)
	return out
}

func (s *Store) evidenceBundles(ctx context.Context, records []storage.ObjectRecord, opts storage.InvestigationOptions) ([]storage.EvidenceBundle, error) {
	if len(records) == 0 {
		return nil, nil
	}
	filtered := make([]storage.ObjectRecord, 0, len(records))
	objectIDs := make([]string, 0, len(records))
	seen := map[string]bool{}
	for _, record := range records {
		if record.LogicalID == "" || seen[record.LogicalID] {
			continue
		}
		seen[record.LogicalID] = true
		filtered = append(filtered, record)
		objectIDs = append(objectIDs, record.LogicalID)
	}
	if len(filtered) == 0 {
		return nil, nil
	}
	latestByObject, err := s.latestDocumentsByObject(ctx, objectIDs)
	if err != nil {
		return nil, err
	}
	factsByObject, err := s.factsByObject(ctx, objectIDs, opts)
	if err != nil {
		return nil, err
	}
	changesByObject, err := s.changesByObject(ctx, objectIDs, opts)
	if err != nil {
		return nil, err
	}
	edgesByObject, err := s.edgesByObject(ctx, objectIDs)
	if err != nil {
		return nil, err
	}
	versionsByObject := map[string][]storage.VersionEvidence{}
	if opts.MaxVersionsPerObject > 0 || !opts.From.IsZero() || !opts.To.IsZero() {
		versionsByObject, err = s.versionEvidenceByObject(ctx, objectIDs, opts)
		if err != nil {
			return nil, err
		}
	}
	out := make([]storage.EvidenceBundle, 0, len(filtered))
	for _, record := range filtered {
		latest, ok := latestByObject[record.LogicalID]
		if !ok {
			return nil, fmt.Errorf("latest document missing for %s", record.LogicalID)
		}
		facts := factsByObject[record.LogicalID]
		edges := edgesByObject[record.LogicalID]
		changes := changesByObject[record.LogicalID]
		versions := versionsByObject[record.LogicalID]
		bundle := storage.EvidenceBundle{
			Object:   record,
			Latest:   latest,
			Facts:    facts,
			Edges:    edges,
			Changes:  changes,
			Versions: versions,
			Summary: storage.BundleSummary{
				Facts:         len(facts),
				Edges:         len(edges),
				Changes:       len(changes),
				Versions:      len(versions),
				EvidenceScore: len(facts)*20 + len(edges)*10 + len(changes)*15,
			},
		}
		out = append(out, bundle)
	}
	return out, nil
}

func serviceInvestigationKind(kind string) bool {
	switch kind {
	case "EndpointSlice", "Pod", "Node", "Event":
		return true
	default:
		return false
	}
}

func summarizeServiceInvestigation(in storage.ServiceInvestigation) storage.ServiceInvestigationSummary {
	out := storage.ServiceInvestigationSummary{Objects: len(in.Objects) + 1, Edges: len(in.Topology), Facts: len(in.Service.Facts), Changes: len(in.Service.Changes), Versions: len(in.Service.Versions), VersionDiffs: len(in.Service.VersionDiffs)}
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

func rankServiceEvidence(service *storage.EvidenceBundle, objects []storage.EvidenceBundle) {
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
