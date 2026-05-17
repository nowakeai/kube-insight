package clickhouse

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"kube-insight/internal/storage"
)

func (s *Store) Topology(ctx context.Context, target storage.ObjectTarget) (storage.TopologyGraph, error) {
	root, err := s.findObject(ctx, target)
	if err != nil {
		return storage.TopologyGraph{}, err
	}
	edges, err := s.relatedTopologyEdges(ctx, root.LogicalID, storage.InvestigationOptions{})
	if err != nil {
		return storage.TopologyGraph{}, err
	}
	nodesByID := map[string]storage.ObjectRecord{root.LogicalID: root}
	for _, edge := range edges {
		nodesByID[edge.Source.LogicalID] = edge.Source
		nodesByID[edge.Target.LogicalID] = edge.Target
	}
	nodes := make([]storage.ObjectRecord, 0, len(nodesByID))
	nodes = append(nodes, root)
	for id, node := range nodesByID {
		if id == root.LogicalID {
			continue
		}
		nodes = append(nodes, node)
	}
	sort.Slice(nodes[1:], func(i, j int) bool { return nodes[i+1].LogicalID < nodes[j+1].LogicalID })
	return storage.TopologyGraph{Root: root, Nodes: nodes, Edges: edges, Summary: storage.TopologySummary{Nodes: len(nodes), Edges: len(edges)}}, nil
}

func (s *Store) relatedTopologyEdges(ctx context.Context, rootID string, opts storage.InvestigationOptions) ([]storage.TopologyEdge, error) {
	rootIDs, err := s.objectAndAliasIDs(ctx, rootID)
	if err != nil {
		return nil, err
	}
	rootIDSet := map[string]bool{}
	for _, id := range rootIDs {
		rootIDSet[id] = true
	}
	query := fmt.Sprintf(`
SELECT edge_type, src_id, dst_id, argMax(src_kind, valid_from) AS src_kind, argMax(dst_kind, valid_from) AS dst_kind, min(valid_from) AS edge_valid_from, max(valid_to) AS edge_valid_to
FROM %s.edges
WHERE (src_id IN (%s) OR dst_id IN (%s))%s
GROUP BY edge_type, src_id, dst_id
ORDER BY edge_valid_from`, q(s.database()), sqlStringList(rootIDs), sqlStringList(rootIDs), edgeWindowFilter(opts))
	result, err := s.client().QueryJSON(ctx, query)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(result.Data)*2)
	for _, row := range result.Data {
		ids = append(ids, stringValue(row["src_id"]), stringValue(row["dst_id"]))
	}
	objects, err := s.objectsByID(ctx, uniqueStrings(ids))
	if err != nil {
		return nil, err
	}
	edges := make([]storage.TopologyEdge, 0, len(result.Data))
	for _, row := range result.Data {
		srcID := stringValue(row["src_id"])
		dstID := stringValue(row["dst_id"])
		source := objects[srcID]
		if source.LogicalID == "" {
			source = storage.ObjectRecord{LogicalID: srcID, Kind: stringValue(row["src_kind"])}
		}
		target := objects[dstID]
		if target.LogicalID == "" {
			target = storage.ObjectRecord{LogicalID: dstID, Kind: stringValue(row["dst_kind"])}
		}
		validFrom := timeValue(row["edge_valid_from"])
		if validFrom.IsZero() {
			validFrom = timeValue(row["valid_from"])
		}
		validTo := timeValue(row["edge_valid_to"])
		if validTo.IsZero() {
			validTo = timeValue(row["valid_to"])
		}
		edge := storage.TopologyEdge{Type: stringValue(row["edge_type"]), Source: source, Target: target, ValidFrom: validFrom}
		if !validTo.IsZero() && validTo.Before(farFutureTime()) {
			edge.ValidTo = &validTo
		}
		if rootIDSet[srcID] {
			edge.Direction = "out"
		} else {
			edge.Direction = "in"
		}
		edges = append(edges, edge)
	}
	return edges, nil
}

func edgeWindowFilter(opts storage.InvestigationOptions) string {
	var parts []string
	if !opts.To.IsZero() {
		parts = append(parts, fmt.Sprintf("valid_from <= %s", quoteString(clickHouseTime(opts.To))))
	}
	if !opts.From.IsZero() {
		parts = append(parts, fmt.Sprintf("valid_to >= %s", quoteString(clickHouseTime(opts.From))))
	}
	if len(parts) == 0 {
		return ""
	}
	return " AND " + strings.Join(parts, " AND ")
}

func topologyEdgeKey(edge storage.TopologyEdge) string {
	return edge.Type + "\x00" + edge.Source.LogicalID + "\x00" + edge.Target.LogicalID
}
