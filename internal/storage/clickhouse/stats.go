package clickhouse

import (
	"context"
	"fmt"
	"time"
)

type Stats struct {
	Clusters               int64
	APIResources           int64
	Objects                int64
	DeletedObjects         int64
	Observations           int64
	Versions               int64
	Facts                  int64
	Edges                  int64
	Changes                int64
	LatestRows             int64
	IngestionOffsets       int64
	OffsetLagMS            int64
	RawBytes               int64
	StoredBytes            int64
	CompressedBytes        int64
	UncompressedBytes      int64
	ProofCompressedBytes   int64
	DerivedCompressedBytes int64
	CompressedBytesPerRow  float64
	CompressionRatio       float64
	TableParts             []TablePartStats
	Footprint              []FootprintStats
}

type TablePartStats struct {
	Table             string
	Rows              int64
	BytesOnDisk       int64
	CompressedBytes   int64
	UncompressedBytes int64
}

type FootprintStats struct {
	Database                 string
	State                    string
	Parts                    int64
	Rows                     int64
	BytesOnDisk              int64
	CompressedBytes          int64
	UncompressedBytes        int64
	OldestInactiveAgeSeconds int64
}

func (s *Store) Stats(ctx context.Context) (Stats, error) {
	if s == nil {
		return Stats{}, nil
	}
	db := q(s.database())
	out := Stats{}
	queries := []struct {
		dst   *int64
		query string
	}{
		{&out.Clusters, fmt.Sprintf("SELECT uniqExact(cluster_id) AS value FROM %s.observations", db)},
		{&out.APIResources, fmt.Sprintf("SELECT uniqExact(concat(api_group, '\\0', api_version, '\\0', resource)) AS value FROM %s.api_resources", db)},
		{&out.Observations, fmt.Sprintf("SELECT count() AS value FROM %s.observations", db)},
		{&out.Versions, fmt.Sprintf("SELECT count() AS value FROM %s.versions", db)},
		{&out.Facts, fmt.Sprintf("SELECT count() AS value FROM %s.facts", db)},
		{&out.Edges, fmt.Sprintf("SELECT count() AS value FROM %s.edges", db)},
		{&out.Changes, fmt.Sprintf("SELECT count() AS value FROM %s.changes", db)},
		{&out.IngestionOffsets, fmt.Sprintf("SELECT count() AS value FROM (SELECT cluster_id, api_group, api_version, resource, namespace FROM %s.ingestion_offsets GROUP BY cluster_id, api_group, api_version, resource, namespace)", db)},
		{&out.RawBytes, fmt.Sprintf("SELECT coalesce(sum(raw_size), 0) AS value FROM %s.versions", db)},
		{&out.StoredBytes, fmt.Sprintf("SELECT coalesce(sum(stored_size), 0) AS value FROM %s.versions", db)},
	}
	for _, item := range queries {
		value, err := s.queryInt64(ctx, item.query)
		if err != nil {
			return Stats{}, err
		}
		*item.dst = value
	}
	objects, deleted, err := s.objectStateCounts(ctx)
	if err != nil {
		return Stats{}, err
	}
	out.Objects = objects
	out.DeletedObjects = deleted
	out.LatestRows = objects
	lag, err := s.offsetLagMS(ctx)
	if err != nil {
		return Stats{}, err
	}
	out.OffsetLagMS = lag
	parts, err := s.tablePartStats(ctx)
	if err != nil {
		return Stats{}, err
	}
	out.TableParts = parts
	footprint, err := s.footprintStats(ctx)
	if err != nil {
		return Stats{}, err
	}
	out.Footprint = footprint
	var rows int64
	for _, part := range parts {
		rows += part.Rows
		out.CompressedBytes += part.CompressedBytes
		out.UncompressedBytes += part.UncompressedBytes
		switch part.Table {
		case "observations", "versions":
			out.ProofCompressedBytes += part.CompressedBytes
		case "facts", "edges", "changes":
			out.DerivedCompressedBytes += part.CompressedBytes
		}
	}
	if out.CompressedBytes > 0 {
		out.CompressionRatio = float64(out.UncompressedBytes) / float64(out.CompressedBytes)
	}
	if rows > 0 {
		out.CompressedBytesPerRow = float64(out.CompressedBytes) / float64(rows)
	}
	return out, nil
}

func (s *Store) queryInt64(ctx context.Context, query string) (int64, error) {
	result, err := s.client().QueryJSON(ctx, query)
	if err != nil {
		return 0, err
	}
	if len(result.Data) == 0 {
		return 0, nil
	}
	return int64Value(result.Data[0]["value"]), nil
}

func (s *Store) objectStateCounts(ctx context.Context) (int64, int64, error) {
	query := fmt.Sprintf(`
SELECT
  countIf(latest_type != 'DELETED') AS objects,
  countIf(latest_type = 'DELETED') AS deleted_objects
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
  GROUP BY cluster_id, api_group, api_version, resource, namespace, identity
)`, q(s.database()))
	result, err := s.client().QueryJSON(ctx, query)
	if err != nil {
		return 0, 0, err
	}
	if len(result.Data) == 0 {
		return 0, 0, nil
	}
	row := result.Data[0]
	return int64Value(row["objects"]), int64Value(row["deleted_objects"]), nil
}

func (s *Store) offsetLagMS(ctx context.Context) (int64, error) {
	updatedMS, err := s.queryInt64(ctx, fmt.Sprintf("SELECT ifNull(max(toUnixTimestamp64Milli(updated_at)), 0) AS value FROM %s.ingestion_offsets", q(s.database())))
	if err != nil || updatedMS <= 0 {
		return 0, err
	}
	lag := time.Now().UnixMilli() - updatedMS
	if lag < 0 {
		return 0, nil
	}
	return lag, nil
}

func (s *Store) tablePartStats(ctx context.Context) ([]TablePartStats, error) {
	query := fmt.Sprintf(`
SELECT
  table,
  sum(rows) AS rows,
  sum(bytes_on_disk) AS bytes_on_disk,
  sum(data_compressed_bytes) AS compressed_bytes,
  sum(data_uncompressed_bytes) AS uncompressed_bytes
FROM system.parts
WHERE active AND database = %s AND table IN (%s)
GROUP BY table
ORDER BY table`, quoteString(s.database()), businessTableSQLList())
	result, err := s.client().QueryJSON(ctx, query)
	if err != nil {
		return nil, err
	}
	out := make([]TablePartStats, 0, len(result.Data))
	for _, row := range result.Data {
		out = append(out, TablePartStats{
			Table:             stringValue(row["table"]),
			Rows:              int64Value(row["rows"]),
			BytesOnDisk:       int64Value(row["bytes_on_disk"]),
			CompressedBytes:   int64Value(row["compressed_bytes"]),
			UncompressedBytes: int64Value(row["uncompressed_bytes"]),
		})
	}
	return out, nil
}

func businessTableSQLList() string {
	names := make([]string, 0, len(ExpectedTableSchemas()))
	for _, table := range ExpectedTableSchemas() {
		names = append(names, table.Name)
	}
	return sqlStringList(names)
}

func (s *Store) footprintStats(ctx context.Context) ([]FootprintStats, error) {
	query := `
SELECT
  database,
  if(active = 1, 'active', 'inactive') AS state,
  count() AS parts,
  sum(rows) AS rows,
  sum(bytes_on_disk) AS bytes_on_disk,
  sum(data_compressed_bytes) AS compressed_bytes,
  sum(data_uncompressed_bytes) AS uncompressed_bytes,
  max(if(active = 0, dateDiff('second', remove_time, now()), 0)) AS oldest_inactive_age_seconds
FROM system.parts
WHERE database NOT IN ('INFORMATION_SCHEMA', 'information_schema')
GROUP BY database, state
ORDER BY bytes_on_disk DESC`
	result, err := s.client().QueryJSON(ctx, query)
	if err != nil {
		return nil, err
	}
	out := make([]FootprintStats, 0, len(result.Data))
	for _, row := range result.Data {
		out = append(out, FootprintStats{
			Database:                 stringValue(row["database"]),
			State:                    stringValue(row["state"]),
			Parts:                    int64Value(row["parts"]),
			Rows:                     int64Value(row["rows"]),
			BytesOnDisk:              int64Value(row["bytes_on_disk"]),
			CompressedBytes:          int64Value(row["compressed_bytes"]),
			UncompressedBytes:        int64Value(row["uncompressed_bytes"]),
			OldestInactiveAgeSeconds: int64Value(row["oldest_inactive_age_seconds"]),
		})
	}
	return out, nil
}
