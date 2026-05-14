package sqlite

import (
	"context"

	"kube-insight/internal/core"
	"kube-insight/internal/kubeapi"
)

func (s *Store) LatestResourceRefs(ctx context.Context, clusterID string, resource kubeapi.ResourceInfo, namespace string) ([]core.ResourceRef, error) {
	rows, err := s.db.QueryContext(ctx, `
select
  ar.api_group,
  ar.api_version,
  ar.resource,
  ok.kind,
  coalesce(li.namespace, ''),
  li.name,
  coalesce(li.uid, '')
from latest_index li
join objects o on o.id = li.object_id
join clusters c on c.id = li.cluster_id
join object_kinds ok on ok.id = li.kind_id
join api_resources ar on ar.id = ok.api_resource_id
where c.name = ?
  and ar.api_group = ?
  and ar.api_version = ?
  and ar.resource = ?
  and o.deleted_at is null
  and (? = '' or coalesce(li.namespace, '') = ?)
order by li.namespace, li.name`,
		clusterID,
		resource.Group,
		resource.Version,
		resource.Resource,
		namespace,
		namespace,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []core.ResourceRef
	for rows.Next() {
		var ref core.ResourceRef
		ref.ClusterID = clusterID
		if err := rows.Scan(&ref.Group, &ref.Version, &ref.Resource, &ref.Kind, &ref.Namespace, &ref.Name, &ref.UID); err != nil {
			return nil, err
		}
		out = append(out, ref)
	}
	return out, rows.Err()
}
