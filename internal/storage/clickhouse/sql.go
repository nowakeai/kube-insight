package clickhouse

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode"

	"kube-insight/internal/storage"
)

func (s *Store) QuerySQL(ctx context.Context, opts storage.SQLQueryOptions) (storage.SQLQueryResult, error) {
	query := strings.TrimSpace(opts.SQL)
	if err := validateReadOnlyQuery(query); err != nil {
		return storage.SQLQueryResult{}, err
	}
	maxRows := opts.MaxRows
	if maxRows <= 0 {
		maxRows = 1000
	}
	execQuery := boundedClickHouseQuery(query, maxRows)
	start := time.Now()
	runner, err := s.sqlQueryRunner()
	if err != nil {
		return storage.SQLQueryResult{}, err
	}
	result, err := runner.QueryJSON(ctx, execQuery)
	if err != nil {
		return storage.SQLQueryResult{}, err
	}
	columns := make([]string, 0, len(result.Meta))
	for _, column := range result.Meta {
		columns = append(columns, column.Name)
	}
	rows := result.Data
	truncated := false
	if len(rows) > maxRows {
		rows = rows[:maxRows]
		truncated = true
	}
	return storage.SQLQueryResult{
		SQL:       query,
		Columns:   columns,
		Rows:      rows,
		RowCount:  len(rows),
		MaxRows:   maxRows,
		Truncated: truncated || result.RowsBefore > maxRows,
		ElapsedMS: float64(time.Since(start).Microseconds()) / 1000,
	}, nil
}

func boundedClickHouseQuery(query string, maxRows int) string {
	query = strings.TrimSpace(query)
	if maxRows <= 0 || !shouldWrapClickHouseQuery(query) {
		return query
	}
	query = strings.TrimRightFunc(query, func(r rune) bool {
		return unicode.IsSpace(r) || r == ';'
	})
	return fmt.Sprintf("SELECT * FROM (\n%s\n) LIMIT %d", query, maxRows+1)
}

func shouldWrapClickHouseQuery(query string) bool {
	first := firstSQLKeyword(query)
	switch first {
	case "SELECT", "WITH":
		return true
	default:
		return false
	}
}

func firstSQLKeyword(query string) string {
	sanitized := sanitizeClickHouseSQLForValidation(query)
	tokens := clickHouseSQLTokens(strings.TrimSpace(sanitized))
	if len(tokens) == 0 {
		return ""
	}
	return strings.ToUpper(tokens[0])
}

func (s *Store) sqlQueryRunner() (QueryRunner, error) {
	client := s.client()
	httpClient, ok := client.(HTTPClient)
	if !ok {
		return client, nil
	}
	endpoint, err := endpointWithDatabase(httpClient.Endpoint, s.database())
	if err != nil {
		return nil, err
	}
	httpClient.Endpoint = endpoint
	return httpClient, nil
}

func endpointWithDatabase(endpoint, database string) (string, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return "", fmt.Errorf("clickhouse endpoint is required")
	}
	database = strings.TrimSpace(database)
	if database == "" {
		database = defaultDatabase
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	values := parsed.Query()
	if values.Get("database") == "" {
		values.Set("database", database)
	}
	parsed.RawQuery = values.Encode()
	return parsed.String(), nil
}

func (s *Store) QuerySchema(ctx context.Context) (storage.SQLSchema, error) {
	database := s.database()
	tablesResult, err := s.client().QueryJSON(ctx, fmt.Sprintf(`
SELECT name, engine
FROM system.tables
WHERE database = %s
ORDER BY name`, quoteString(database)))
	if err != nil {
		return storage.SQLSchema{}, err
	}
	columnsResult, err := s.client().QueryJSON(ctx, fmt.Sprintf(`
SELECT table, name, type
FROM system.columns
WHERE database = %s
ORDER BY table, position`, quoteString(database)))
	if err != nil {
		return storage.SQLSchema{}, err
	}
	columnsByTable := map[string][]storage.SQLSchemaColumn{}
	for _, row := range columnsResult.Data {
		table := stringValue(row["table"])
		columnType := stringValue(row["type"])
		columnsByTable[table] = append(columnsByTable[table], storage.SQLSchemaColumn{
			Name:    stringValue(row["name"]),
			Type:    columnType,
			NotNull: !strings.HasPrefix(columnType, "Nullable("),
		})
	}
	schema := storage.SQLSchema{
		Notes: []string{
			"Active SQL backend: ClickHouse-compatible (ClickHouse or chDB).",
			"ClickHouse timestamps use DateTime64 UTC columns unless noted otherwise.",
			"Use observations and versions for proof; use facts, edges, and changes for investigation candidates.",
			"ClickHouse changes rows do not include uid. For object identity, use changes.object_id or query observations/versions by cluster_id, kind, namespace, and name to retrieve uid.",
			"When the requested semantic field is unknown, profile real data first: distinct fact_key/fact_value pairs, observation_type values, recent kind counts, cluster ids, and min/max timestamps with tight LIMITs.",
			"Use kube_insight_health cluster display map to render cluster_id as a human-readable cluster/context name in final answers.",
			"For recent/today/last-N queries, include UTC time bounds on facts.ts, changes.ts, observations.observed_at, versions.observed_at, edges.valid_from, or ingestion_offsets.updated_at.",
			"Prefer sorted columns before text search: facts(cluster_id,fact_key,fact_value,ts), changes(cluster_id,change_family,path,ts), observations(cluster_id,kind,namespace,name,observed_at), edges(cluster_id,edge_type,src_id,valid_from_ms).",
			"Avoid doc/detail text scans until candidates are narrowed by cluster, kind, namespace, name, exact fact/change/edge keys, and time.",
			"For configuration fields such as container requests/limits, profile facts first; if facts do not carry those fields, use one scoped observations.doc profile over recent rows to find the relevant kind/observation_type before fetching proof.",
			"For historical aggregation, ranking, bucketing, latest-per-object selection, or Kubernetes unit normalization, prefer kube_insight_js after schema when code-shaped aggregation is clearer than plain SQL. Run bounded SQL inside JS and return answer-ready JSON instead of chaining many hand-written kube_insight_sql calls. Fetch Kubernetes CPU/memory/storage quantity strings in SQL and parse units in JavaScript; do not divide JSONExtractString or replaceAll string expressions inside ClickHouse SQL.",
			"In kube_insight_js, sql(query, maxRows) and sqlAll([{name, sql, maxRows}]) are synchronous helpers. Do not use await, do not call sql with an object argument, do not name a variable sql, and return compact objects instead of JSON.stringify(rawRows). Combine tightly related profile, proof, and aggregation when practical, but do not treat one JS call as a correctness requirement. Usually omit maxQueries; if set, it must be at least the number of sql() calls plus sqlAll() specs.",
			"When a CTE defines aggregate aliases such as argMax(doc, observed_at) as latest_doc or argMax(observation_type, observed_at) as last_type, never reference those aliases in WHERE. Put latest_doc/last_type filters in HAVING or an outer SELECT.",
			"For ClickHouse JSON proof snippets, prefer JSONExtractRaw(doc, 'spec', 'containers') or selecting a bounded substring(doc, ...) sample; do not use JSONExtract without an explicit return type.",
			"SQL access is read-only; use SELECT/WITH/EXPLAIN/DESCRIBE/SHOW only.",
		},
		Recipes: clickHouseSchemaRecipes(),
	}
	for _, row := range tablesResult.Data {
		name := stringValue(row["name"])
		schema.Tables = append(schema.Tables, storage.SQLSchemaTable{
			Name:        name,
			Type:        "table",
			Description: clickHouseTableDescription(name),
			Columns:     columnsByTable[name],
		})
	}
	return schema, nil
}

func clickHouseSchemaRecipes() []storage.SQLSchemaRecipe {
	return []storage.SQLSchemaRecipe{
		{
			Name:        "coverage_latest",
			Description: "Check current collector coverage before claiming absence or current health. Keep cluster_id if returned rows show multiple clusters.",
			SQL: `select cluster_id, kind, resource, namespace,
       argMax(status, updated_at) as status,
       argMax(error, updated_at) as error,
       max(updated_at) as latest_updated_at
from ingestion_offsets
where cluster_id = 'CLUSTER_ID'
group by cluster_id, kind, resource, namespace
having status in ('not_started','retrying','list_error','watch_error')
order by status, resource, namespace
limit 50`,
		},
		{
			Name:        "recent_fact_rollup",
			Description: "Generic fast path for broad symptoms and status questions. Replace CLUSTER_ID, the time bound, and exact fact filters; prefer this before proof document scans.",
			SQL: `select kind, namespace, name, fact_key, fact_value,
       count() as rows,
       min(ts) as first_seen,
       max(ts) as last_seen
from facts
where cluster_id = 'CLUSTER_ID'
  and ts >= toDateTime64('2026-05-25 00:00:00', 3, 'UTC')
  and fact_key = 'EXACT_FACT_KEY'
  and fact_value = 'EXACT_FACT_VALUE'
group by kind, namespace, name, fact_key, fact_value
order by rows desc, last_seen desc
limit 50`,
		},
		{
			Name:        "recent_change_rollup",
			Description: "Generic fast path for recent changes. Use exact change_family/path/kind filters when known; avoid old_scalar/new_scalar text scans until narrowed. The changes table has object_id but no uid; join or follow up through observations/versions when uid is needed.",
			SQL: `select object_id, kind, namespace, name, change_family, path,
	       count() as changes,
	       min(ts) as first_seen,
	       max(ts) as last_seen
from changes
where cluster_id = 'CLUSTER_ID'
  and ts >= toDateTime64('2026-05-25 00:00:00', 3, 'UTC')
  and change_family in ('status','spec','topology')
	group by object_id, kind, namespace, name, change_family, path
	order by changes desc, last_seen desc
	limit 50`,
		},
		{
			Name:        "container_resource_allocation_rollup",
			Description: "Generic Kubernetes container requests/limits rollup for simple allocation/configuration summaries. Unless the user already scoped one exact cluster_id, keep all clusters and group by cluster_id plus namespace; do not pick the first cluster from health output. Returned sample strings are only examples; do not estimate total CPU or memory from samples. For top namespace ranking, unit normalization, or allocation-change comparisons, use pod_resource_rows_for_js inside kube_insight_js instead.",
			SQL: `with latest_pods as (
  select cluster_id, namespace, name, uid, doc,
         row_number() over (partition by cluster_id, kind, namespace, name, uid order by observed_at desc) as rn
  from observations
  where kind = 'Pod'
    and observed_at >= toDateTime64('2026-05-25 00:00:00', 3, 'UTC')
    and position(doc, '\"resources\"') > 0
), container_resources as (
  select cluster_id, namespace, name,
         arrayJoin(JSONExtractArrayRaw(doc, 'spec', 'containers')) as container_raw,
         JSONExtractString(container_raw, 'resources', 'requests', 'cpu') as cpu_request,
         JSONExtractString(container_raw, 'resources', 'requests', 'memory') as memory_request
  from latest_pods
  where rn = 1
)
select cluster_id,
       namespace,
       countDistinct(name) as pods,
       count() as containers,
       countIf(JSONExtractRaw(container_raw, 'resources', 'requests') != '') as containers_with_requests,
       countIf(JSONExtractRaw(container_raw, 'resources', 'limits') != '') as containers_with_limits,
       arrayStringConcat(arraySlice(groupUniqArrayIf(cpu_request, cpu_request != ''), 1, 5), ', ') as cpu_request_samples,
       arrayStringConcat(arraySlice(groupUniqArrayIf(memory_request, memory_request != ''), 1, 5), ', ') as memory_request_samples
from container_resources
group by cluster_id, namespace
order by containers_with_requests desc, containers desc
limit 25`,
		},
		{
			Name:        "pod_resource_rows_for_js",
			Description: "Row-level latest Pod container resource data for kube_insight_js. Use this for namespace Pod resource ranking or allocation-change questions. Unless the user already scoped one exact cluster_id, keep all clusters, preserve cluster_id in the rows, and rank by cluster_id plus namespace or by an explicitly stated global aggregation; do not pick the first cluster from health output. Normalize Kubernetes CPU/memory units, group, rank, and compare snapshots in JS when code-shaped aggregation is clearer. Copy the CTE shape: latest_doc is an aggregate alias, so keep position(latest_doc, ...) in HAVING or an outer SELECT, never WHERE. Do not estimate totals from sample strings, and do not count raw observation rows as resource usage.",
			SQL: `with latest_pods as (
  select cluster_id, namespace, name, uid,
         argMax(doc, observed_at) as latest_doc,
         argMax(observation_type, observed_at) as last_type,
         max(observed_at) as last_seen
  from observations
  where kind = 'Pod'
    and observed_at >= toDateTime64('2026-05-25 00:00:00', 3, 'UTC')
    and observed_at < toDateTime64('2026-05-26 00:00:00', 3, 'UTC')
  group by cluster_id, namespace, name, uid
  having position(latest_doc, '"resources"') > 0
), containers as (
  select cluster_id, namespace, name, uid, last_seen,
         arrayJoin(JSONExtractArrayRaw(latest_doc, 'spec', 'containers')) as container_raw
  from latest_pods
  where last_type != 'DELETED'
)
select cluster_id, namespace, name, uid, last_seen,
       JSONExtractString(container_raw, 'name') as container,
       JSONExtractString(container_raw, 'resources', 'requests', 'cpu') as cpu_request,
       JSONExtractString(container_raw, 'resources', 'requests', 'memory') as memory_request,
       JSONExtractString(container_raw, 'resources', 'limits', 'cpu') as cpu_limit,
       JSONExtractString(container_raw, 'resources', 'limits', 'memory') as memory_limit
from containers
limit 1000`,
		},
		{
			Name:        "namespace_resource_delta_for_js",
			Description: "Start/end Pod resource allocation rows for namespace resource-change questions. Query actual Pod coverage/min/max timestamps, compute startWindow/endWindow variables from that coverage, then fetch start_rows and end_rows with those variables. If coverage is per cluster, choose the dataful cluster/window before constructing snapshot SQL. Treat resource change as requests/limits allocation change unless the user explicitly asks for change-event volume or health churn. Normalize CPU/memory units and return start/end values, absolute deltas, percent deltas, final top ranking, and separate existing namespaces from newly observed namespaces when available. Do not query changes/status events or run key-namespace deep dives for allocation questions; if a chosen window is sparse or stale, use a focused follow-up coverage/query step rather than guessing.",
			SQL: `with latest_pods as (
  select cluster_id, namespace, name, uid,
         argMax(doc, observed_at) as latest_doc,
         argMax(observation_type, observed_at) as last_type,
         max(observed_at) as last_seen
  from observations
  where kind = 'Pod'
    and observed_at >= toDateTime64('2026-05-20 00:00:00', 3, 'UTC')
    and observed_at < toDateTime64('2026-05-21 00:00:00', 3, 'UTC')
  group by cluster_id, namespace, name, uid
  having last_type != 'DELETED'
     and position(latest_doc, '"resources"') > 0
), containers as (
  select cluster_id, namespace, name, uid, last_seen,
         arrayJoin(JSONExtractArrayRaw(latest_doc, 'spec', 'containers')) as container_raw
  from latest_pods
)
select cluster_id, namespace, name, uid, last_seen,
       JSONExtractString(container_raw, 'name') as container,
       JSONExtractString(container_raw, 'resources', 'requests', 'cpu') as cpu_request,
       JSONExtractString(container_raw, 'resources', 'requests', 'memory') as memory_request,
       JSONExtractString(container_raw, 'resources', 'limits', 'cpu') as cpu_limit,
       JSONExtractString(container_raw, 'resources', 'limits', 'memory') as memory_limit
from containers
limit 5000`,
		},
		{
			Name:        "pod_count_peak_intervals_for_js",
			Description: "Primary per-UID intervals recipe for kube_insight_js Pod-count peak and time-bucket questions. Use this final interval_rows SELECT instead of exporting raw Pod observation rows or separately querying baseline and window_events. It returns one row per Pod UID interval with interval_start, interval_end, and from_baseline, derived from a latest non-deleted baseline at the window start plus ADDED/MODIFIED/DELETED observations. ADDED and MODIFIED mean the UID exists after that timestamp; DELETED means it does not. Convert each returned row to at most two events: +1 at interval_start and -1 at interval_end when present, sort once, and sweep once while updating the peak. Never treat 1970-01-01 sentinel values as real deletes; this recipe nulls them before output. Do not request more than 10000 rows, do not scan all rows for every bucket, and do not raise maxRows above the cap; if this recipe truncates, split by cluster or time.",
			SQL: `with baseline as (
  select cluster_id, namespace, name, uid,
         max(observed_at) as baseline_observed_at,
         argMax(observation_type, observed_at) as last_type
  from observations
  where kind = 'Pod'
    and cluster_id = 'CLUSTER_ID'
    and observed_at < toDateTime64('2026-05-20 00:00:00', 3, 'UTC')
    and observation_type in ('ADDED', 'MODIFIED', 'DELETED')
  group by cluster_id, namespace, name, uid
  having last_type != 'DELETED'
), window_events as (
  select cluster_id, namespace, name, uid,
         minIf(observed_at, observation_type in ('ADDED','MODIFIED')) as first_present_at,
         minIf(observed_at, observation_type = 'DELETED') as first_deleted_at,
         count() as observation_rows
  from observations
  where kind = 'Pod'
    and cluster_id = 'CLUSTER_ID'
    and observed_at >= toDateTime64('2026-05-20 00:00:00', 3, 'UTC')
    and observed_at < toDateTime64('2026-05-27 00:00:00', 3, 'UTC')
    and observation_type in ('ADDED', 'MODIFIED', 'DELETED')
  group by cluster_id, namespace, name, uid
), interval_rows as (
  select cluster_id, namespace, name, uid,
         1 as from_baseline,
         toDateTime64('2026-05-20 00:00:00', 3, 'UTC') as interval_start,
         nullIf(first_deleted_at, toDateTime64('1970-01-01 00:00:00', 3, 'UTC')) as interval_end,
         baseline_observed_at,
         observation_rows
  from baseline
  left join window_events using (cluster_id, namespace, name, uid)
  union all
  select cluster_id, namespace, name, uid,
         0 as from_baseline,
         first_present_at as interval_start,
         nullIf(first_deleted_at, toDateTime64('1970-01-01 00:00:00', 3, 'UTC')) as interval_end,
         null as baseline_observed_at,
         observation_rows
  from window_events
  where first_present_at != toDateTime64('1970-01-01 00:00:00', 3, 'UTC')
    and uid not in (select uid from baseline)
)
select cluster_id, namespace, name, uid, from_baseline,
       interval_start, interval_end, baseline_observed_at, observation_rows
from interval_rows
order by interval_start, namespace, name
limit 10000`,
		},
		{
			Name:        "endpointslice_latest_for_js",
			Description: "Latest non-deleted EndpointSlice snapshots for kube_insight_js Service endpoint readiness checks. Use this when proving zero ready endpoints or empty endpoint arrays. Do not rely on arrayJoin(JSONExtractArrayRaw(doc, 'endpoints')) alone because ClickHouse drops rows for EndpointSlices whose endpoints array is empty; preserve each slice row and parse endpoint_count/ready counts in JavaScript or a SQL shape that keeps zero-endpoint slices.",
			SQL: `with latest_slices as (
  select cluster_id, namespace, name, uid,
         argMax(doc, observed_at) as latest_doc,
         argMax(observation_type, observed_at) as last_type,
         max(observed_at) as last_seen
  from observations
  where kind = 'EndpointSlice'
    and cluster_id = 'CLUSTER_ID'
    and namespace = 'NAMESPACE'
    and observed_at >= toDateTime64('2026-05-20 00:00:00', 3, 'UTC')
    and observed_at < toDateTime64('2026-05-27 00:00:00', 3, 'UTC')
  group by cluster_id, namespace, name, uid
  having last_type != 'DELETED'
)
select cluster_id, namespace, name, uid, last_seen,
       JSONExtractString(latest_doc, 'metadata', 'labels', 'kubernetes.io/service-name') as service_name,
       length(JSONExtractArrayRaw(latest_doc, 'endpoints')) as endpoint_count,
       latest_doc
from latest_slices
limit 1000`,
		},
		{
			Name:        "current_node_capacity_snapshot",
			Description: "Current Node inventory, instance type, and raw capacity/allocatable quantity strings for kube_insight_js. Use this for questions such as how many nodes, which node types, total CPU, or total memory. It collapses observations to the latest non-deleted Node snapshot; parse Kubernetes quantity units and aggregate totals in JavaScript instead of dividing JSONExtractString results inside SQL. Do not sum raw fact rows across a time window for current capacity.",
			SQL: `with latest_nodes as (
  select cluster_id, name, uid,
         argMax(doc, observed_at) as doc,
         argMax(observation_type, observed_at) as last_type,
         max(observed_at) as last_seen
  from observations
  where cluster_id = 'CLUSTER_ID'
    and kind = 'Node'
  group by cluster_id, name, uid
), parsed as (
  select cluster_id, name, uid, last_seen,
         JSONExtractString(doc, 'metadata', 'labels', 'node.kubernetes.io/instance-type') as instance_type,
         JSONExtractString(doc, 'metadata', 'labels', 'cloud.google.com/gke-nodepool') as nodepool,
         JSONExtractString(doc, 'status', 'capacity', 'cpu') as capacity_cpu,
         JSONExtractString(doc, 'status', 'capacity', 'memory') as capacity_memory,
         JSONExtractString(doc, 'status', 'allocatable', 'cpu') as allocatable_cpu,
         JSONExtractString(doc, 'status', 'allocatable', 'memory') as allocatable_memory
  from latest_nodes
  where last_type != 'DELETED'
)
select cluster_id, name, uid,
       if(instance_type = '', 'unknown', instance_type) as instance_type,
       nodepool,
       capacity_cpu,
       capacity_memory,
       allocatable_cpu,
       allocatable_memory,
       last_seen
from parsed
order by cluster_id, instance_type, name
limit 5000`,
		},
		{
			Name:        "recent_node_lifecycle",
			Description: "Recent Node ADDED/DELETED events for a time window. Pair this with current_node_capacity_snapshot and start/end latest non-deleted Node snapshots when the user asks whether nodes changed and also wants current totals. Report churn or replacements separately from net_delta and net capacity changes; balanced added/deleted counts alone do not prove stable node count or capacity. The event row is enriched from nearby same-name observations with instance-type labels because ADDED docs can arrive before labels such as instance type are complete.",
			SQL: `with lifecycle as (
  select cluster_id, observation_type, name, uid,
         min(observed_at) as first_seen,
         max(observed_at) as last_seen
  from observations
  where cluster_id = 'CLUSTER_ID'
    and kind = 'Node'
    and observation_type in ('ADDED', 'DELETED')
    and observed_at >= toDateTime64('2026-05-25 00:00:00', 3, 'UTC')
    and observed_at < toDateTime64('2026-05-26 00:00:00', 3, 'UTC')
  group by cluster_id, observation_type, name, uid
), label_docs as (
  select l.cluster_id, l.observation_type, l.name, l.uid, l.first_seen, l.last_seen,
         argMaxIf(o.doc, o.observed_at, position(o.doc, 'node.kubernetes.io/instance-type') > 0) as doc
  from lifecycle as l
  left join observations as o
    on o.cluster_id = l.cluster_id
   and o.kind = 'Node'
   and o.name = l.name
   and o.observed_at >= l.first_seen - interval 30 minute
   and o.observed_at <= l.first_seen + interval 60 minute
  group by l.cluster_id, l.observation_type, l.name, l.uid, l.first_seen, l.last_seen
)
select observation_type, name, uid,
       JSONExtractString(doc, 'metadata', 'labels', 'node.kubernetes.io/instance-type') as instance_type,
       JSONExtractString(doc, 'metadata', 'labels', 'cloud.google.com/gke-nodepool') as nodepool,
       JSONExtractString(doc, 'metadata', 'creationTimestamp') as creation_timestamp,
       first_seen,
       last_seen
from label_docs
order by first_seen
limit 100`,
		},
		{
			Name:        "pvc_resize_candidates_for_js",
			Description: "PVC resize candidate discovery. Use this before pvc_storage_history_for_js when the target PVC names are unknown. changes has object_id but no uid; get uid from observations if needed. This candidate query is not final proof; fetch ordered observations and collapse storage transitions in JS before answering.",
			SQL: `select cluster_id, object_id, namespace, name,
	       count() as changes,
	       min(ts) as first_change,
	       max(ts) as last_change,
	       arrayStringConcat(arraySlice(groupUniqArray(path), 1, 8), ', ') as paths
	from changes
	where kind = 'PersistentVolumeClaim'
	  and ts >= toDateTime64('2026-05-20 00:00:00', 3, 'UTC')
	  and ts < toDateTime64('2026-05-27 00:00:00', 3, 'UTC')
	  and (path like '%storage%' or path like '%FileSystemResize%' or path like '%Resizing%')
	group by cluster_id, object_id, namespace, name
	order by last_change desc
	limit 100`,
		},
		{
			Name:        "pvc_storage_history_for_js",
			Description: "PVC request/capacity history rows for kube_insight_js. Use this for PVC resize/expansion questions and short follow-ups such as 'from how much to how much'. Query the target PVC set from prior context or from pvc_resize_candidates_for_js, collapse adjacent equal request/capacity values in JS, pair requested and capacity changes into one unique PVC-level resize record, convert bytes/Ki/Gi quantities to GiB, and return before/after deltas as answer-ready JSON. Do not return separate rows per changed field, stream many raw PVC observations back to the model, or re-query after a result has exact before/after rows.",
			SQL: `select cluster_id, namespace, name, uid, observed_at, observation_type,
       JSONExtractString(doc, 'spec', 'resources', 'requests', 'storage') as requested_storage,
       JSONExtractString(doc, 'status', 'capacity', 'storage') as capacity_storage,
       JSONExtractString(doc, 'status', 'phase') as phase
from observations
where kind = 'PersistentVolumeClaim'
  and observed_at >= toDateTime64('2026-05-20 00:00:00', 3, 'UTC')
  and observed_at < toDateTime64('2026-05-27 00:00:00', 3, 'UTC')
  and name in ('PVC_NAME_1', 'PVC_NAME_2')
  and position(doc, '"storage"') > 0
order by cluster_id, namespace, name, uid, observed_at
limit 2000`,
		},
		{
			Name:        "raw_doc_field_profile",
			Description: "Fallback only after facts/changes do not carry a requested configuration field. Keep time and identity predicates tight before scanning doc text.",
			SQL: `select cluster_id, kind, observation_type, count() as rows, max(observed_at) as last_seen
from observations
where observed_at >= toDateTime64('2026-05-25 00:00:00', 3, 'UTC')
  and kind = 'KIND'
  and position(doc, '\"FIELD_NAME\"') > 0
group by cluster_id, kind, observation_type
order by rows desc, last_seen desc
limit 25`,
		},
		{
			Name:        "object_proof_after_candidate",
			Description: "Fetch retained proof only after facts/changes/search identify a specific object.",
			SQL: `select object_id, seq, observed_at, resource_version, doc_hash, materialization, raw_size, stored_size
from versions
where cluster_id = 'CLUSTER_ID'
  and kind = 'KIND'
  and namespace = 'NAMESPACE'
  and name = 'NAME'
  and observed_at >= toDateTime64('2026-05-25 00:00:00', 3, 'UTC')
order by observed_at desc, seq desc
limit 20`,
		},
	}
}

func validateReadOnlyQuery(query string) error {
	if strings.TrimSpace(query) == "" {
		return errors.New("sql query is required")
	}
	sanitized := sanitizeClickHouseSQLForValidation(query)
	trimmed := strings.TrimSpace(sanitized)
	trimmed = strings.TrimSuffix(trimmed, ";")
	if strings.Contains(trimmed, ";") {
		return errors.New("only one SQL statement is allowed")
	}
	tokens := clickHouseSQLTokens(trimmed)
	if len(tokens) == 0 {
		return errors.New("sql query is required")
	}
	allowed := false
	switch tokens[0] {
	case "select", "with", "describe", "desc", "show":
		allowed = true
	case "explain":
		for _, token := range tokens[1:] {
			if token == "select" || token == "with" || token == "describe" || token == "desc" || token == "show" {
				allowed = true
				break
			}
		}
	}
	if !allowed {
		return fmt.Errorf("only read-only ClickHouse SELECT/WITH/EXPLAIN/DESCRIBE/SHOW queries are allowed, got %q", tokens[0])
	}
	for _, token := range tokens {
		if forbiddenClickHouseSQLToken(token) {
			return fmt.Errorf("read-only ClickHouse SQL rejected forbidden token %q", token)
		}
	}
	return nil
}

func sanitizeClickHouseSQLForValidation(query string) string {
	var out strings.Builder
	for i := 0; i < len(query); {
		switch {
		case i+1 < len(query) && query[i] == '-' && query[i+1] == '-':
			for i < len(query) && query[i] != '\n' {
				out.WriteByte(' ')
				i++
			}
		case i+1 < len(query) && query[i] == '/' && query[i+1] == '*':
			out.WriteString("  ")
			i += 2
			for i+1 < len(query) && !(query[i] == '*' && query[i+1] == '/') {
				out.WriteByte(' ')
				i++
			}
			if i+1 < len(query) {
				out.WriteString("  ")
				i += 2
			}
		case query[i] == '\'' || query[i] == '"' || query[i] == '`':
			quote := query[i]
			out.WriteByte(' ')
			i++
			for i < len(query) {
				out.WriteByte(' ')
				if query[i] == quote {
					if i+1 < len(query) && query[i+1] == quote {
						out.WriteByte(' ')
						i += 2
						continue
					}
					i++
					break
				}
				i++
			}
		default:
			out.WriteRune(unicode.ToLower(rune(query[i])))
			i++
		}
	}
	return out.String()
}

func clickHouseSQLTokens(query string) []string {
	return strings.FieldsFunc(query, func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_')
	})
}

func forbiddenClickHouseSQLToken(token string) bool {
	switch token {
	case "insert", "update", "delete", "drop", "alter", "create", "replace",
		"truncate", "attach", "detach", "rename", "exchange", "undrop", "optimize",
		"grant", "revoke", "set", "kill", "backup", "restore", "watch",
		"begin", "commit", "rollback":
		return true
	default:
		return false
	}
}

func clickHouseTableDescription(name string) string {
	switch name {
	case "api_resources":
		return "Kubernetes discovery metadata for list/watch resources."
	case "observations":
		return "Append-only observed Kubernetes object events after filtering."
	case "object_aliases":
		return "Lookup aliases that map names and UIDs to canonical object IDs."
	case "versions":
		return "Retained proof documents and materialization metadata."
	case "facts":
		return "Extracted fact rows for fast investigation candidates."
	case "edges":
		return "Extracted topology and ownership relationships."
	case "changes":
		return "Extracted scalar/status/topology changes."
	case "filter_decisions":
		return "Auditable destructive and sensitive filter decisions."
	case "ingestion_offsets":
		return "Append-only list/watch progress and health state."
	default:
		return ""
	}
}
