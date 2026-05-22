package clickhouse

import (
	"fmt"
	"regexp"
	"strings"
)

const defaultDatabase = "kube_insight"

var identifierPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

type SchemaOptions struct {
	Database         string
	Cluster          string
	UseJSONType      bool
	HotVolume        string
	ColdVolume       string
	ColdAfterSeconds int
}

func (o SchemaOptions) normalized() SchemaOptions {
	if o.Database == "" {
		o.Database = defaultDatabase
	}
	return o
}

func (o SchemaOptions) Validate() error {
	o = o.normalized()
	for label, value := range map[string]string{
		"database":   o.Database,
		"cluster":    o.Cluster,
		"hotVolume":  o.HotVolume,
		"coldVolume": o.ColdVolume,
	} {
		if value == "" {
			continue
		}
		if !identifierPattern.MatchString(value) {
			return fmt.Errorf("%s %q is not a valid ClickHouse identifier", label, value)
		}
	}
	if o.ColdAfterSeconds < 0 {
		return fmt.Errorf("coldAfterSeconds must be non-negative")
	}
	return nil
}

func CreateTableStatements(opts SchemaOptions) ([]string, error) {
	if err := opts.Validate(); err != nil {
		return nil, err
	}
	opts = opts.normalized()
	docType := "String CODEC(ZSTD(3))"
	if opts.UseJSONType {
		docType = "JSON"
	}
	statements := []string{
		fmt.Sprintf("CREATE DATABASE IF NOT EXISTS %s%s", q(opts.Database), clusterClause(opts.Cluster)),
		createAPIResources(opts),
		createObservations(opts, docType),
		createObjectAliases(opts),
		createVersions(opts, docType),
		createFacts(opts),
		createEdges(opts),
		createChanges(opts),
		createFilterDecisions(opts),
		createIngestionOffsets(opts),
	}
	statements = append(statements, CreateAgentTableStatements(opts)...)
	statements = append(statements, readPathIndexStatements(opts)...)
	return statements, nil
}

func CreateAgentTableStatements(opts SchemaOptions) []string {
	opts = opts.normalized()
	return []string{
		createAgentSessions(opts),
		createAgentRuns(opts),
		createAgentRunEvents(opts),
	}
}

func createAPIResources(opts SchemaOptions) string {
	return fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.api_resources%s (
    api_group LowCardinality(String),
    api_version LowCardinality(String),
    resource LowCardinality(String),
    kind LowCardinality(String),
    namespaced Bool,
    verbs String CODEC(ZSTD(3)),
    last_discovered_at DateTime64(3, 'UTC')
)
ENGINE = ReplacingMergeTree(last_discovered_at)
ORDER BY (api_group, api_version, resource)
%s`, q(opts.Database), clusterClause(opts.Cluster), ttlClause(opts, "last_discovered_at"))
}

func createObservations(opts SchemaOptions, docType string) string {
	return fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.observations%s (
    cluster_id LowCardinality(String),
    observed_at DateTime64(3, 'UTC'),
    observation_type LowCardinality(String),
    api_group LowCardinality(String),
    api_version LowCardinality(String),
    resource LowCardinality(String),
    kind LowCardinality(String),
    namespace String,
    name String,
    uid String,
    resource_version String,
    partial Bool,
    doc_hash String,
    doc %s
)
ENGINE = MergeTree
PARTITION BY toYYYYMM(observed_at)
ORDER BY (cluster_id, kind, namespace, name, observed_at, uid)
%s`, q(opts.Database), clusterClause(opts.Cluster), docType, ttlClause(opts, "observed_at"))
}

func createObjectAliases(opts SchemaOptions) string {
	return fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.object_aliases%s (
    cluster_id LowCardinality(String),
    object_id String,
    alias_id String,
    kind LowCardinality(String),
    namespace String,
    name String,
    observed_at DateTime64(3, 'UTC')
)
ENGINE = ReplacingMergeTree(observed_at)
PARTITION BY toYYYYMM(observed_at)
ORDER BY (cluster_id, alias_id, object_id)
%s`, q(opts.Database), clusterClause(opts.Cluster), ttlClause(opts, "observed_at"))
}

func createVersions(opts SchemaOptions, docType string) string {
	return fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.versions%s (
    cluster_id LowCardinality(String),
    object_id String,
    seq UInt64,
    observed_at DateTime64(3, 'UTC'),
    api_group LowCardinality(String),
    api_version LowCardinality(String),
    resource LowCardinality(String),
    kind LowCardinality(String),
    namespace String,
    name String,
    uid String,
    resource_version String,
    doc_hash String,
    materialization LowCardinality(String),
    blob_ref String,
    raw_size UInt64,
    stored_size UInt64,
    doc %s
)
ENGINE = MergeTree
PARTITION BY toYYYYMM(observed_at)
ORDER BY (cluster_id, object_id, seq, observed_at)
%s`, q(opts.Database), clusterClause(opts.Cluster), docType, ttlClause(opts, "observed_at"))
}

func createFacts(opts SchemaOptions) string {
	return fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.facts%s (
    cluster_id LowCardinality(String),
    ts DateTime64(3, 'UTC'),
    object_id String,
    kind LowCardinality(String),
    namespace String,
    name String,
    fact_key LowCardinality(String),
    fact_value String,
    numeric_value Nullable(Float64),
    severity Int16,
    detail String CODEC(ZSTD(3))
)
ENGINE = MergeTree
PARTITION BY toYYYYMM(ts)
ORDER BY (cluster_id, fact_key, fact_value, ts, object_id)
%s`, q(opts.Database), clusterClause(opts.Cluster), ttlClause(opts, "ts"))
}

func createEdges(opts SchemaOptions) string {
	return fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.edges%s (
    cluster_id LowCardinality(String),
    edge_type LowCardinality(String),
    src_id String,
    dst_id String,
    src_kind LowCardinality(String),
    dst_kind LowCardinality(String),
    valid_from DateTime64(3, 'UTC'),
    valid_to DateTime64(3, 'UTC'),
    valid_from_ms Int64,
    valid_to_ms Int64,
    detail String CODEC(ZSTD(3))
)
ENGINE = MergeTree
PARTITION BY toYYYYMM(valid_from)
ORDER BY (cluster_id, edge_type, src_id, valid_from_ms, dst_id)
%s`, q(opts.Database), clusterClause(opts.Cluster), ttlClause(opts, "valid_from"))
}

func createChanges(opts SchemaOptions) string {
	return fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.changes%s (
    cluster_id LowCardinality(String),
    ts DateTime64(3, 'UTC'),
    object_id String,
    kind LowCardinality(String),
    namespace String,
    name String,
    change_family LowCardinality(String),
    path String,
    op LowCardinality(String),
    old_scalar String,
    new_scalar String,
    severity Int16
)
ENGINE = MergeTree
PARTITION BY toYYYYMM(ts)
ORDER BY (cluster_id, change_family, path, ts, object_id)
%s`, q(opts.Database), clusterClause(opts.Cluster), ttlClause(opts, "ts"))
}

func createFilterDecisions(opts SchemaOptions) string {
	return fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.filter_decisions%s (
    ts DateTime64(3, 'UTC'),
    cluster_id LowCardinality(String),
    api_group LowCardinality(String),
    api_version LowCardinality(String),
    resource LowCardinality(String),
    kind LowCardinality(String),
    namespace String,
    name String,
    uid String,
    resource_version String,
    observation_type LowCardinality(String),
    filter_name LowCardinality(String),
    outcome LowCardinality(String),
    reason String,
    destructive Bool,
    meta String CODEC(ZSTD(3))
)
ENGINE = MergeTree
PARTITION BY toYYYYMM(ts)
ORDER BY (cluster_id, resource, namespace, name, ts, outcome, filter_name)
%s`, q(opts.Database), clusterClause(opts.Cluster), ttlClause(opts, "ts"))
}

func createIngestionOffsets(opts SchemaOptions) string {
	return fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.ingestion_offsets%s (
    cluster_id LowCardinality(String),
    api_group LowCardinality(String),
    api_version LowCardinality(String),
    resource LowCardinality(String),
    kind LowCardinality(String),
    namespaced Bool,
    namespace String,
    resource_version String,
    event LowCardinality(String),
    status LowCardinality(String),
    error String CODEC(ZSTD(3)),
    last_list_at Nullable(DateTime64(3, 'UTC')),
    last_watch_at Nullable(DateTime64(3, 'UTC')),
    last_bookmark_at Nullable(DateTime64(3, 'UTC')),
    updated_at DateTime64(3, 'UTC')
)
ENGINE = ReplacingMergeTree(updated_at)
PARTITION BY toYYYYMM(updated_at)
ORDER BY (cluster_id, api_group, api_version, resource, namespace, event)
%s`, q(opts.Database), clusterClause(opts.Cluster), ttlClause(opts, "updated_at"))
}

func createAgentSessions(opts SchemaOptions) string {
	return fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.agent_sessions%s (
    id String,
    title String,
    provider String,
    model String,
    created_at DateTime64(3, 'UTC'),
    updated_at DateTime64(3, 'UTC')
)
ENGINE = ReplacingMergeTree(updated_at)
ORDER BY id`, q(opts.Database), clusterClause(opts.Cluster))
}

func createAgentRuns(opts SchemaOptions) string {
	return fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.agent_runs%s (
    id String,
    session_id String,
    status LowCardinality(String),
    input String CODEC(ZSTD(3)),
    provider String,
    model String,
    created_at DateTime64(3, 'UTC'),
    started_at Nullable(DateTime64(3, 'UTC')),
    completed_at Nullable(DateTime64(3, 'UTC')),
    error String CODEC(ZSTD(3)),
    metadata String CODEC(ZSTD(3)),
    updated_at DateTime64(3, 'UTC')
)
ENGINE = ReplacingMergeTree(updated_at)
ORDER BY id`, q(opts.Database), clusterClause(opts.Cluster))
}

func createAgentRunEvents(opts SchemaOptions) string {
	return fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.agent_run_events%s (
    id String,
    run_id String,
    sequence UInt64,
    type LowCardinality(String),
    created_at DateTime64(3, 'UTC'),
    data String CODEC(ZSTD(3))
)
ENGINE = MergeTree
ORDER BY (run_id, sequence, id)`, q(opts.Database), clusterClause(opts.Cluster))
}

func readPathIndexStatements(opts SchemaOptions) []string {
	return []string{
		addBloomIndex(opts, "versions", "versions_object_id_bf", "object_id"),
		addBloomIndex(opts, "versions", "versions_uid_bf", "uid"),
		addBloomIndex(opts, "versions", "versions_name_bf", "name"),
		addBloomIndex(opts, "observations", "observations_uid_bf", "uid"),
		addBloomIndex(opts, "facts", "facts_object_id_bf", "object_id"),
		addBloomIndex(opts, "changes", "changes_object_id_bf", "object_id"),
		addBloomIndex(opts, "edges", "edges_src_id_bf", "src_id"),
		addBloomIndex(opts, "edges", "edges_dst_id_bf", "dst_id"),
	}
}

func addBloomIndex(opts SchemaOptions, table, name, column string) string {
	return fmt.Sprintf("ALTER TABLE %s.%s%s ADD INDEX IF NOT EXISTS %s %s TYPE bloom_filter(0.01) GRANULARITY 4", q(opts.Database), q(table), clusterClause(opts.Cluster), q(name), q(column))
}

func clusterClause(cluster string) string {
	if cluster == "" {
		return ""
	}
	return " ON CLUSTER " + q(cluster)
}

func ttlClause(opts SchemaOptions, column string) string {
	if opts.ColdAfterSeconds <= 0 || opts.ColdVolume == "" {
		return ""
	}
	return fmt.Sprintf("TTL toDateTime(%s) + INTERVAL %d SECOND TO VOLUME %s", column, opts.ColdAfterSeconds, quoteString(opts.ColdVolume))
}

// q and quoteString are the only low-level SQL escaping helpers used by the
// ClickHouse HTTP queries. Identifiers are backtick-escaped and values are
// single-quote escaped before being interpolated into query templates.
func q(identifier string) string {
	return "`" + strings.ReplaceAll(identifier, "`", "``") + "`"
}

func quoteString(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func boundedLimit(value, fallback, max int) int {
	if fallback <= 0 {
		fallback = 1
	}
	if value <= 0 {
		value = fallback
	}
	if max > 0 && value > max {
		return max
	}
	return value
}
