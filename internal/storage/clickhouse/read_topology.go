package clickhouse

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"kube-insight/internal/storage"
)

type topologyEdgeRow struct {
	Type      string
	SrcID     string
	DstID     string
	SrcKind   string
	DstKind   string
	Direction string
	ValidFrom time.Time
	ValidTo   *time.Time
}

func (r topologyEdgeRow) key() string {
	return r.Type + "\x00" + r.SrcID + "\x00" + r.DstID
}

func (r topologyEdgeRow) sourceRecord() storage.ObjectRecord {
	return storage.ObjectRecord{LogicalID: r.SrcID, Kind: r.SrcKind}
}

func (r topologyEdgeRow) targetRecord() storage.ObjectRecord {
	return storage.ObjectRecord{LogicalID: r.DstID, Kind: r.DstKind}
}

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
	rows, err := s.relatedTopologyEdgeRows(ctx, rootID, opts)
	if err != nil {
		return nil, err
	}
	return s.hydrateTopologyEdgeRows(ctx, rows)
}

func (s *Store) relatedTopologyEdgeRows(ctx context.Context, rootID string, opts storage.InvestigationOptions) ([]topologyEdgeRow, error) {
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
	result, err := s.client().QueryTSV(ctx, query)
	if err != nil {
		return nil, err
	}
	rows := make([]topologyEdgeRow, 0, len(result.Rows))
	for _, row := range result.Rows {
		srcID := row[1]
		dstID := row[2]
		validFrom := timeValue(row[5])
		validTo := timeValue(row[6])
		out := topologyEdgeRow{Type: row[0], SrcID: srcID, DstID: dstID, SrcKind: row[3], DstKind: row[4], ValidFrom: validFrom}
		if !validTo.IsZero() && validTo.Before(farFutureTime()) {
			out.ValidTo = &validTo
		}
		if rootIDSet[srcID] {
			out.Direction = "out"
		} else {
			out.Direction = "in"
		}
		rows = append(rows, out)
	}
	return rows, nil
}

func (s *Store) hydrateTopologyEdgeRows(ctx context.Context, rows []topologyEdgeRow) ([]storage.TopologyEdge, error) {
	ids := make([]string, 0, len(rows)*2)
	for _, row := range rows {
		ids = append(ids, row.SrcID, row.DstID)
	}
	objects, err := s.objectsByID(ctx, uniqueStrings(ids))
	if err != nil {
		return nil, err
	}
	edges := make([]storage.TopologyEdge, 0, len(rows))
	for _, row := range rows {
		source := objects[row.SrcID]
		if source.LogicalID == "" {
			source = row.sourceRecord()
		}
		target := objects[row.DstID]
		if target.LogicalID == "" {
			target = row.targetRecord()
		}
		edges = append(edges, storage.TopologyEdge{Type: row.Type, Source: source, Target: target, Direction: row.Direction, ValidFrom: row.ValidFrom, ValidTo: row.ValidTo})
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
