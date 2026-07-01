package api

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"kube-insight/internal/storage"
)

type storageStatsResponse struct {
	CheckedAt   time.Time               `json:"checkedAt"`
	Backend     string                  `json:"backend"`
	Summary     storageStatsSummary     `json:"summary"`
	Tables      []storageTableStat      `json:"tables,omitempty"`
	ObjectKinds []storageObjectKindStat `json:"objectKinds,omitempty"`
	Warnings    []string                `json:"warnings,omitempty"`
}

type storageStatsSummary struct {
	Clusters          int64   `json:"clusters"`
	APIResources      int64   `json:"apiResources"`
	Objects           int64   `json:"objects"`
	DeletedObjects    int64   `json:"deletedObjects"`
	LatestObjects     int64   `json:"latestObjects"`
	Observations      int64   `json:"observations"`
	Versions          int64   `json:"versions"`
	Blobs             int64   `json:"blobs"`
	Facts             int64   `json:"facts"`
	Edges             int64   `json:"edges"`
	Changes           int64   `json:"changes"`
	FilterDecisions   int64   `json:"filterDecisions"`
	IngestionOffsets  int64   `json:"ingestionOffsets"`
	RawBytes          int64   `json:"rawBytes"`
	StoredBytes       int64   `json:"storedBytes"`
	DatabaseBytes     int64   `json:"databaseBytes"`
	BytesOnDisk       int64   `json:"bytesOnDisk"`
	CompressedBytes   int64   `json:"compressedBytes"`
	UncompressedBytes int64   `json:"uncompressedBytes"`
	CompressionRatio  float64 `json:"compressionRatio"`
}

type storageTableStat struct {
	Name              string `json:"name"`
	Rows              int64  `json:"rows"`
	Bytes             int64  `json:"bytes,omitempty"`
	BytesOnDisk       int64  `json:"bytesOnDisk,omitempty"`
	CompressedBytes   int64  `json:"compressedBytes,omitempty"`
	UncompressedBytes int64  `json:"uncompressedBytes,omitempty"`
}

type storageObjectKindStat struct {
	Group         string `json:"group,omitempty"`
	Version       string `json:"version,omitempty"`
	Resource      string `json:"resource,omitempty"`
	Kind          string `json:"kind"`
	Objects       int64  `json:"objects"`
	LatestObjects int64  `json:"latestObjects"`
	Versions      int64  `json:"versions"`
	RawBytes      int64  `json:"rawBytes"`
	StoredBytes   int64  `json:"storedBytes"`
}

func (s *Server) handleStorageStats(w http.ResponseWriter, r *http.Request) {
	store, err := s.openStore(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer s.closeReadStore(store)
	queryStore, ok := store.(storage.SQLQueryStore)
	if !ok {
		writeUnsupported(w, "storage stats")
		return
	}
	stats, err := buildStorageStats(r.Context(), queryStore)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func buildStorageStats(ctx context.Context, store storage.SQLQueryStore) (storageStatsResponse, error) {
	schema, err := store.QuerySchema(ctx)
	if err != nil {
		return storageStatsResponse{}, err
	}
	switch detectStorageStatsBackend(schema) {
	case "sqlite":
		return buildSQLiteStorageStats(ctx, store)
	case "clickhouse":
		return buildClickHouseStorageStats(ctx, store)
	default:
		return storageStatsResponse{}, fmt.Errorf("storage stats are unsupported for this schema")
	}
}

func detectStorageStatsBackend(schema storage.SQLSchema) string {
	tables := map[string]bool{}
	for _, table := range schema.Tables {
		tables[table.Name] = true
	}
	if tables["objects"] && tables["object_observations"] && tables["latest_index"] {
		return "sqlite"
	}
	if tables["observations"] && tables["versions"] && tables["facts"] {
		return "clickhouse"
	}
	notes := strings.ToLower(strings.Join(schema.Notes, "\n"))
	if strings.Contains(notes, "clickhouse") {
		return "clickhouse"
	}
	if strings.Contains(notes, "sqlite") {
		return "sqlite"
	}
	return ""
}

func buildSQLiteStorageStats(ctx context.Context, store storage.SQLQueryStore) (storageStatsResponse, error) {
	out := storageStatsResponse{CheckedAt: time.Now().UTC(), Backend: "sqlite"}
	summary, err := queryOne(ctx, store, `
select
  (select count(*) from clusters) as clusters,
  (select count(*) from api_resources) as api_resources,
  (select count(*) from objects) as objects,
  (select count(*) from objects where deleted_at is not null) as deleted_objects,
  (select count(*) from latest_index) as latest_objects,
  (select count(*) from object_observations) as observations,
  (select count(*) from versions) as versions,
  (select count(*) from blobs) as blobs,
  (select count(*) from object_facts) as facts,
  (select count(*) from object_edges) as edges,
  (select count(*) from object_changes) as changes,
  (select count(*) from filter_decisions) as filter_decisions,
  (select count(*) from ingestion_offsets) as ingestion_offsets,
  (select coalesce(sum(raw_size), 0) from versions) as raw_bytes,
  (select coalesce(sum(stored_size), 0) from blobs) as stored_bytes`)
	if err != nil {
		return out, err
	}
	out.Summary = storageSummaryFromRow(summary)
	if row, err := queryOne(ctx, store, `select coalesce(sum(pgsize), 0) as database_bytes from dbstat`); err == nil {
		out.Summary.DatabaseBytes = int64Value(row["database_bytes"])
	} else {
		out.Warnings = append(out.Warnings, "SQLite dbstat virtual table is unavailable; database byte size is omitted.")
	}
	out.Summary.CompressionRatio = ratio(out.Summary.RawBytes, out.Summary.StoredBytes)
	tables, err := queryRows(ctx, store, sqliteTableStatsSQL())
	if err != nil {
		return out, err
	}
	out.Tables = tableStatsFromRows(tables)
	if rows, err := queryRows(ctx, store, `select name, coalesce(sum(pgsize), 0) as bytes from dbstat group by name`); err == nil {
		mergeSQLiteTableBytes(out.Tables, rows)
	}
	objectKinds, err := queryRows(ctx, store, `
select
  ok.api_group as api_group,
  ok.api_version as api_version,
  ar.resource as resource,
  ok.kind as kind,
  count(distinct o.id) as objects,
  count(distinct li.object_id) as latest_objects,
  count(v.id) as versions,
  coalesce(sum(v.raw_size), 0) as raw_bytes,
  coalesce(sum(v.stored_size), 0) as stored_bytes
from objects o
join object_kinds ok on ok.id = o.kind_id
left join api_resources ar on ar.id = ok.api_resource_id
left join versions v on v.object_id = o.id
left join latest_index li on li.object_id = o.id
group by ok.api_group, ok.api_version, ar.resource, ok.kind
order by raw_bytes desc, versions desc, objects desc
limit 20`)
	if err != nil {
		return out, err
	}
	out.ObjectKinds = objectKindStatsFromRows(objectKinds)
	return out, nil
}

func buildClickHouseStorageStats(ctx context.Context, store storage.SQLQueryStore) (storageStatsResponse, error) {
	out := storageStatsResponse{CheckedAt: time.Now().UTC(), Backend: "clickhouse"}
	database, err := clickHouseStatsDatabase(ctx, store)
	if err != nil {
		return out, err
	}
	table := func(name string) string {
		if database == "" {
			return clickHouseQuoteIdent(name)
		}
		return clickHouseQuoteIdent(database) + "." + clickHouseQuoteIdent(name)
	}
	items := []struct {
		dst   *int64
		query string
	}{
		{&out.Summary.Clusters, fmt.Sprintf(`select uniqExact(cluster_id) as value from %s`, table("observations"))},
		{&out.Summary.APIResources, fmt.Sprintf(`select uniqExact(concat(api_group, '\0', api_version, '\0', resource)) as value from %s`, table("api_resources"))},
		{&out.Summary.Observations, fmt.Sprintf(`select count() as value from %s`, table("observations"))},
		{&out.Summary.Versions, fmt.Sprintf(`select count() as value from %s`, table("versions"))},
		{&out.Summary.Facts, fmt.Sprintf(`select count() as value from %s`, table("facts"))},
		{&out.Summary.Edges, fmt.Sprintf(`select count() as value from %s`, table("edges"))},
		{&out.Summary.Changes, fmt.Sprintf(`select count() as value from %s`, table("changes"))},
		{&out.Summary.FilterDecisions, fmt.Sprintf(`select count() as value from %s`, table("filter_decisions"))},
		{&out.Summary.IngestionOffsets, fmt.Sprintf(`select count() as value from %s`, table("ingestion_offsets"))},
		{&out.Summary.RawBytes, fmt.Sprintf(`select coalesce(sum(raw_size), 0) as value from %s`, table("versions"))},
		{&out.Summary.StoredBytes, fmt.Sprintf(`select coalesce(sum(stored_size), 0) as value from %s`, table("versions"))},
	}
	for _, item := range items {
		value, err := queryInt64(ctx, store, item.query)
		if err != nil {
			return out, err
		}
		*item.dst = value
	}
	state, err := queryOne(ctx, store, fmt.Sprintf(`
select
  countIf(latest_type != 'DELETED') as objects,
  countIf(latest_type = 'DELETED') as deleted_objects
from
(
  select
    cluster_id,
    api_group,
    api_version,
    resource,
    namespace,
    if(uid != '', concat('uid:', uid), concat('name:', namespace, '/', name)) as identity,
    argMax(observation_type, observed_at) as latest_type
  from %s
  group by cluster_id, api_group, api_version, resource, namespace, identity
)`, table("observations")))
	if err != nil {
		return out, err
	}
	out.Summary.Objects = int64Value(state["objects"])
	out.Summary.DeletedObjects = int64Value(state["deleted_objects"])
	out.Summary.LatestObjects = out.Summary.Objects
	physical, err := queryOne(ctx, store, fmt.Sprintf(`
select
  coalesce(sum(bytes_on_disk), 0) as bytes_on_disk,
  coalesce(sum(data_compressed_bytes), 0) as compressed_bytes,
  coalesce(sum(data_uncompressed_bytes), 0) as uncompressed_bytes
from system.parts
where active
  and database = %s
  and table in ('api_resources', 'observations', 'object_aliases', 'versions', 'facts', 'edges', 'changes', 'filter_decisions', 'ingestion_offsets')`, clickHouseQuoteString(database)))
	if err == nil {
		out.Summary.BytesOnDisk = int64Value(physical["bytes_on_disk"])
		out.Summary.CompressedBytes = int64Value(physical["compressed_bytes"])
		out.Summary.UncompressedBytes = int64Value(physical["uncompressed_bytes"])
	} else {
		out.Warnings = append(out.Warnings, "ClickHouse system.parts is unavailable; physical byte size is omitted.")
	}
	if out.Summary.CompressedBytes > 0 {
		out.Summary.CompressionRatio = ratio(out.Summary.UncompressedBytes, out.Summary.CompressedBytes)
	} else {
		out.Summary.CompressionRatio = ratio(out.Summary.RawBytes, out.Summary.StoredBytes)
	}
	tableRows, err := queryRows(ctx, store, fmt.Sprintf(`
select
  table as name,
  sum(rows) as rows,
  sum(bytes_on_disk) as bytes_on_disk,
  sum(data_compressed_bytes) as compressed_bytes,
  sum(data_uncompressed_bytes) as uncompressed_bytes
from system.parts
where active
  and database = %s
  and table in ('api_resources', 'observations', 'object_aliases', 'versions', 'facts', 'edges', 'changes', 'filter_decisions', 'ingestion_offsets')
group by table
order by bytes_on_disk desc`, clickHouseQuoteString(database)))
	if err == nil {
		out.Tables = tableStatsFromRows(tableRows)
	} else {
		out.Warnings = append(out.Warnings, "ClickHouse table part stats are unavailable.")
	}
	objectKinds, err := queryRows(ctx, store, fmt.Sprintf(`
select
  api_group,
  api_version,
  resource,
  kind,
  uniqExact(object_id) as objects,
  uniqExact(object_id) as latest_objects,
  count() as versions,
  coalesce(sum(raw_size), 0) as raw_bytes,
  coalesce(sum(stored_size), 0) as stored_bytes
from %s
group by api_group, api_version, resource, kind
order by raw_bytes desc, versions desc, objects desc
limit 20`, table("versions")))
	if err != nil {
		return out, err
	}
	out.ObjectKinds = objectKindStatsFromRows(objectKinds)
	return out, nil
}

func clickHouseStatsDatabase(ctx context.Context, store storage.SQLQueryStore) (string, error) {
	if _, err := queryInt64(ctx, store, `select count() as value from observations`); err == nil {
		row, currentErr := queryOne(ctx, store, `select currentDatabase() as database`)
		if currentErr == nil {
			return stringValue(row["database"]), nil
		}
		return "", nil
	}
	row, err := queryOne(ctx, store, `
select database
from system.tables
where name = 'observations'
order by if(database = currentDatabase(), 0, 1), database
limit 1`)
	if err != nil {
		return "", err
	}
	database := stringValue(row["database"])
	if database == "" {
		return "", fmt.Errorf("clickhouse-compatible observations table was not found")
	}
	return database, nil
}

func clickHouseQuoteIdent(value string) string {
	return "`" + strings.ReplaceAll(value, "`", "``") + "`"
}

func clickHouseQuoteString(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func queryOne(ctx context.Context, store storage.SQLQueryStore, query string) (map[string]any, error) {
	result, err := store.QuerySQL(ctx, storage.SQLQueryOptions{SQL: query, MaxRows: 1})
	if err != nil {
		return nil, err
	}
	if len(result.Rows) == 0 {
		return map[string]any{}, nil
	}
	return result.Rows[0], nil
}

func queryRows(ctx context.Context, store storage.SQLQueryStore, query string) ([]map[string]any, error) {
	result, err := store.QuerySQL(ctx, storage.SQLQueryOptions{SQL: query, MaxRows: 100})
	if err != nil {
		return nil, err
	}
	return result.Rows, nil
}

func queryInt64(ctx context.Context, store storage.SQLQueryStore, query string) (int64, error) {
	row, err := queryOne(ctx, store, query)
	if err != nil {
		return 0, err
	}
	return int64Value(row["value"]), nil
}

func storageSummaryFromRow(row map[string]any) storageStatsSummary {
	return storageStatsSummary{
		Clusters:         int64Value(row["clusters"]),
		APIResources:     int64Value(row["api_resources"]),
		Objects:          int64Value(row["objects"]),
		DeletedObjects:   int64Value(row["deleted_objects"]),
		LatestObjects:    int64Value(row["latest_objects"]),
		Observations:     int64Value(row["observations"]),
		Versions:         int64Value(row["versions"]),
		Blobs:            int64Value(row["blobs"]),
		Facts:            int64Value(row["facts"]),
		Edges:            int64Value(row["edges"]),
		Changes:          int64Value(row["changes"]),
		FilterDecisions:  int64Value(row["filter_decisions"]),
		IngestionOffsets: int64Value(row["ingestion_offsets"]),
		RawBytes:         int64Value(row["raw_bytes"]),
		StoredBytes:      int64Value(row["stored_bytes"]),
	}
}

func tableStatsFromRows(rows []map[string]any) []storageTableStat {
	out := make([]storageTableStat, 0, len(rows))
	for _, row := range rows {
		out = append(out, storageTableStat{
			Name:              stringValue(row["name"]),
			Rows:              int64Value(row["rows"]),
			Bytes:             int64Value(row["bytes"]),
			BytesOnDisk:       int64Value(row["bytes_on_disk"]),
			CompressedBytes:   int64Value(row["compressed_bytes"]),
			UncompressedBytes: int64Value(row["uncompressed_bytes"]),
		})
	}
	return out
}

func objectKindStatsFromRows(rows []map[string]any) []storageObjectKindStat {
	out := make([]storageObjectKindStat, 0, len(rows))
	for _, row := range rows {
		rawBytes := int64Value(row["raw_bytes"])
		storedBytes := int64Value(row["stored_bytes"])
		out = append(out, storageObjectKindStat{
			Group:         stringValue(row["api_group"]),
			Version:       stringValue(row["api_version"]),
			Resource:      stringValue(row["resource"]),
			Kind:          stringValue(row["kind"]),
			Objects:       int64Value(row["objects"]),
			LatestObjects: int64Value(row["latest_objects"]),
			Versions:      int64Value(row["versions"]),
			RawBytes:      rawBytes,
			StoredBytes:   storedBytes,
		})
	}
	return out
}

func mergeSQLiteTableBytes(tables []storageTableStat, rows []map[string]any) {
	bytesByName := map[string]int64{}
	for _, row := range rows {
		bytesByName[stringValue(row["name"])] = int64Value(row["bytes"])
	}
	for i := range tables {
		tables[i].Bytes = bytesByName[tables[i].Name]
	}
}

func sqliteTableStatsSQL() string {
	names := []string{
		"clusters",
		"api_resources",
		"object_kinds",
		"objects",
		"blobs",
		"versions",
		"object_observations",
		"latest_index",
		"latest_raw_index",
		"object_edges",
		"object_facts",
		"object_changes",
		"filter_decisions",
		"filter_decision_rollups",
		"ingestion_offsets",
	}
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, fmt.Sprintf("select '%s' as name, count(*) as rows from %s", name, name))
	}
	return strings.Join(parts, "\nunion all\n")
}

func ratio(numerator, denominator int64) float64 {
	if denominator <= 0 {
		return 0
	}
	value := float64(numerator) / float64(denominator)
	return math.Round(value*100) / 100
}

func int64Value(value any) int64 {
	switch v := value.(type) {
	case nil:
		return 0
	case int:
		return int64(v)
	case int8:
		return int64(v)
	case int16:
		return int64(v)
	case int32:
		return int64(v)
	case int64:
		return v
	case uint:
		return int64(v)
	case uint8:
		return int64(v)
	case uint16:
		return int64(v)
	case uint32:
		return int64(v)
	case uint64:
		if v > math.MaxInt64 {
			return math.MaxInt64
		}
		return int64(v)
	case float32:
		return int64(v)
	case float64:
		return int64(v)
	case json.Number:
		if i, err := v.Int64(); err == nil {
			return i
		}
		f, _ := v.Float64()
		return int64(f)
	case string:
		if v == "" {
			return 0
		}
		i, err := strconv.ParseInt(v, 10, 64)
		if err == nil {
			return i
		}
		f, _ := strconv.ParseFloat(v, 64)
		return int64(f)
	default:
		return 0
	}
}

func stringValue(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	case []byte:
		return string(v)
	default:
		return fmt.Sprint(v)
	}
}
