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
	service, err := s.evidenceBundle(ctx, serviceObject, bundleOpts)
	if err != nil {
		return storage.ServiceInvestigation{}, err
	}
	maxObjects := boundedLimit(opts.MaxEvidenceObjects, 20, 1000)
	visited := map[string]bool{serviceObject.LogicalID: true}
	queued := []string{serviceObject.LogicalID}
	topologyByKey := map[string]storage.TopologyEdge{}
	recordsByID := map[string]storage.ObjectRecord{}
	for len(queued) > 0 {
		current := queued[0]
		queued = queued[1:]
		edges, err := s.relatedTopologyEdges(ctx, current, opts)
		if err != nil {
			return storage.ServiceInvestigation{}, err
		}
		for _, edge := range edges {
			topologyByKey[topologyEdgeKey(edge)] = edge
			for _, record := range []storage.ObjectRecord{edge.Source, edge.Target} {
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
	objects := make([]storage.EvidenceBundle, 0, len(recordsByID))
	for _, record := range recordsByID {
		bundle, err := s.evidenceBundle(ctx, record, bundleOpts)
		if err != nil {
			return storage.ServiceInvestigation{}, err
		}
		objects = append(objects, bundle)
	}
	topology := make([]storage.TopologyEdge, 0, len(topologyByKey))
	for _, edge := range topologyByKey {
		topology = append(topology, edge)
	}
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
