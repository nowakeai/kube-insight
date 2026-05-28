package clickhouse

import (
	"context"
	"fmt"
	"time"

	"kube-insight/internal/storage"
)

func (s *Store) UpsertCluster(ctx context.Context, cluster storage.ClusterRecord) error {
	if cluster.Name == "" {
		return fmt.Errorf("cluster name is required")
	}
	createdAt := cluster.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	return s.client().InsertRows(ctx, s.database(), "clusters", []map[string]any{{
		"name":       cluster.Name,
		"uid":        cluster.UID,
		"source":     cluster.Source,
		"created_at": clickHouseTime(createdAt),
	}})
}

func (s *Store) ListClusters(ctx context.Context) ([]storage.ClusterRecord, error) {
	query := fmt.Sprintf(`
WITH
latest_clusters AS (
  SELECT
    name,
    argMax(uid, created_at) AS uid,
    argMax(source, created_at) AS source,
    max(created_at) AS latest_created_at
  FROM %s.clusters
  GROUP BY name
),
object_counts AS (
  SELECT cluster_id AS name, uniqExact(object_id) AS objects
  FROM %s.versions
  GROUP BY cluster_id
),
version_counts AS (
  SELECT cluster_id AS name, count() AS versions
  FROM %s.versions
  GROUP BY cluster_id
),
	latest_counts AS (
	  SELECT cluster_id AS name, uniqExact(object_id) AS latest
	  FROM %s.versions
	  GROUP BY cluster_id
	),
	cluster_names AS (
	  SELECT name FROM latest_clusters WHERE name != ''
	  UNION DISTINCT SELECT name FROM object_counts WHERE name != ''
	  UNION DISTINCT SELECT name FROM version_counts WHERE name != ''
	  UNION DISTINCT SELECT name FROM latest_counts WHERE name != ''
	)
SELECT
	  cn.name,
	  lc.uid,
	  lc.source,
	  lc.latest_created_at AS created_at,
	  ifNull(oc.objects, 0) AS objects,
	  ifNull(vc.versions, 0) AS versions,
	  ifNull(lc2.latest, 0) AS latest
FROM cluster_names cn
LEFT JOIN latest_clusters lc ON lc.name = cn.name
LEFT JOIN object_counts oc ON oc.name = cn.name
LEFT JOIN version_counts vc ON vc.name = cn.name
LEFT JOIN latest_counts lc2 ON lc2.name = cn.name
ORDER BY cn.name`, q(s.database()), q(s.database()), q(s.database()), q(s.database()))
	result, err := s.client().QueryJSON(ctx, query)
	if err != nil {
		return nil, err
	}
	out := make([]storage.ClusterRecord, 0, len(result.Data))
	for _, row := range result.Data {
		out = append(out, storage.ClusterRecord{
			Name:      stringValue(row["name"]),
			UID:       stringValue(row["uid"]),
			Source:    stringValue(row["source"]),
			CreatedAt: timeValue(row["created_at"]),
			Objects:   int64Value(row["objects"]),
			Versions:  int64Value(row["versions"]),
			Latest:    int64Value(row["latest"]),
		})
	}
	return out, nil
}
