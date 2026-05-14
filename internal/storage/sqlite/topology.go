package sqlite

import (
	"context"
	"database/sql"
	"time"
)

type TopologyGraph struct {
	Root    ObjectRecord    `json:"root"`
	Nodes   []ObjectRecord  `json:"nodes"`
	Edges   []TopologyEdge  `json:"edges"`
	Summary TopologySummary `json:"summary"`
}

type TopologyEdge struct {
	Type      string       `json:"type"`
	Source    ObjectRecord `json:"source"`
	Target    ObjectRecord `json:"target"`
	Direction string       `json:"direction"`
	ValidFrom time.Time    `json:"validFrom"`
	ValidTo   *time.Time   `json:"validTo,omitempty"`
}

type TopologySummary struct {
	Nodes int `json:"nodes"`
	Edges int `json:"edges"`
}

func (s *Store) Topology(ctx context.Context, target ObjectTarget) (TopologyGraph, error) {
	root, rootID, err := s.FindObject(ctx, target)
	if err != nil {
		return TopologyGraph{}, err
	}
	edges, err := s.relatedEdges(ctx, rootID)
	if err != nil {
		return TopologyGraph{}, err
	}

	nodesByID := map[string]ObjectRecord{root.LogicalID: root}
	for _, edge := range edges {
		nodesByID[edge.Source.LogicalID] = edge.Source
		nodesByID[edge.Target.LogicalID] = edge.Target
	}
	nodes := make([]ObjectRecord, 0, len(nodesByID))
	nodes = append(nodes, root)
	for id, node := range nodesByID {
		if id == root.LogicalID {
			continue
		}
		nodes = append(nodes, node)
	}

	return TopologyGraph{
		Root:  root,
		Nodes: nodes,
		Edges: edges,
		Summary: TopologySummary{
			Nodes: len(nodes),
			Edges: len(edges),
		},
	}, nil
}

func (s *Store) relatedEdges(ctx context.Context, rootID int64) ([]TopologyEdge, error) {
	rows, err := s.db.QueryContext(ctx, `
select
  e.edge_type,
  min(e.valid_from),
  max(e.valid_to),
  case when e.src_id = ? then 'out' else 'in' end as direction,
  src.id, src_cluster.name, src_ar.api_group, src_ar.api_version, src_ar.resource, src_kind.kind,
  coalesce(src.namespace, ''), src.name, coalesce(src.uid, ''), coalesce(src_latest.observed_at, src.last_seen_at),
  dst.id, dst_cluster.name, dst_ar.api_group, dst_ar.api_version, dst_ar.resource, dst_kind.kind,
  coalesce(dst.namespace, ''), dst.name, coalesce(dst.uid, ''), coalesce(dst_latest.observed_at, dst.last_seen_at)
from object_edges e
join objects src on src.id = e.src_id
join clusters src_cluster on src_cluster.id = src.cluster_id
join object_kinds src_kind on src_kind.id = src.kind_id
join api_resources src_ar on src_ar.id = src_kind.api_resource_id
left join latest_index src_latest on src_latest.object_id = src.id
join objects dst on dst.id = e.dst_id
join clusters dst_cluster on dst_cluster.id = dst.cluster_id
join object_kinds dst_kind on dst_kind.id = dst.kind_id
join api_resources dst_ar on dst_ar.id = dst_kind.api_resource_id
left join latest_index dst_latest on dst_latest.object_id = dst.id
where e.src_id = ? or e.dst_id = ?
group by e.edge_type, e.src_id, e.dst_id
order by min(e.valid_from), min(e.id)`, rootID, rootID, rootID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var edges []TopologyEdge
	for rows.Next() {
		var validFrom int64
		var validTo int64
		var sourceDBID int64
		var targetDBID int64
		var sourceObservedAt int64
		var targetObservedAt int64
		var edge TopologyEdge
		if err := rows.Scan(
			&edge.Type,
			&validFrom,
			&validTo,
			&edge.Direction,
			&sourceDBID,
			&edge.Source.ClusterID,
			&edge.Source.Group,
			&edge.Source.Version,
			&edge.Source.Resource,
			&edge.Source.Kind,
			&edge.Source.Namespace,
			&edge.Source.Name,
			&edge.Source.UID,
			&sourceObservedAt,
			&targetDBID,
			&edge.Target.ClusterID,
			&edge.Target.Group,
			&edge.Target.Version,
			&edge.Target.Resource,
			&edge.Target.Kind,
			&edge.Target.Namespace,
			&edge.Target.Name,
			&edge.Target.UID,
			&targetObservedAt,
		); err != nil {
			return nil, err
		}
		_ = sourceDBID
		_ = targetDBID
		edge.Source.LatestObservedAt = time.UnixMilli(sourceObservedAt)
		edge.Source.LogicalID = logicalID(edge.Source)
		edge.Target.LatestObservedAt = time.UnixMilli(targetObservedAt)
		edge.Target.LogicalID = logicalID(edge.Target)
		edge.ValidFrom = time.UnixMilli(validFrom)
		if validTo != maxValidTo {
			t := time.UnixMilli(validTo)
			edge.ValidTo = &t
		}
		edges = append(edges, edge)
	}
	return edges, rows.Err()
}

const maxValidTo = int64(9223372036854775807)

func nullTimeMillis(value sql.NullInt64) *time.Time {
	if !value.Valid {
		return nil
	}
	t := time.UnixMilli(value.Int64)
	return &t
}
