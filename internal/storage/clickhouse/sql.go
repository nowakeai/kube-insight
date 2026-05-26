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
	start := time.Now()
	runner, err := s.sqlQueryRunner()
	if err != nil {
		return storage.SQLQueryResult{}, err
	}
	result, err := runner.QueryJSON(ctx, query)
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
			"When the requested semantic field is unknown, profile real data first: distinct fact_key/fact_value pairs, observation_type values, recent kind counts, cluster ids, and min/max timestamps with tight LIMITs.",
			"Use kube_insight_health cluster display map to render cluster_id as a human-readable cluster/context name in final answers.",
			"For recent/today/last-N queries, include UTC time bounds on facts.ts, changes.ts, observations.observed_at, versions.observed_at, edges.valid_from, or ingestion_offsets.updated_at.",
			"Prefer sorted columns before text search: facts(cluster_id,fact_key,fact_value,ts), changes(cluster_id,change_family,path,ts), observations(cluster_id,kind,namespace,name,observed_at), edges(cluster_id,edge_type,src_id,valid_from_ms).",
			"Avoid doc/detail text scans until candidates are narrowed by cluster, kind, namespace, name, exact fact/change/edge keys, and time.",
			"For configuration fields such as container requests/limits, profile facts first; if facts do not carry those fields, use one scoped observations.doc profile over recent rows to find the relevant kind/observation_type before fetching proof.",
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
       max(updated_at) as updated_at
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
			Description: "Generic fast path for recent changes. Use exact change_family/path/kind filters when known; avoid old_scalar/new_scalar text scans until narrowed.",
			SQL: `select kind, namespace, name, change_family, path,
       count() as changes,
       min(ts) as first_seen,
       max(ts) as last_seen
from changes
where cluster_id = 'CLUSTER_ID'
  and ts >= toDateTime64('2026-05-25 00:00:00', 3, 'UTC')
  and change_family in ('status','spec','topology')
group by kind, namespace, name, change_family, path
order by changes desc, last_seen desc
limit 50`,
		},
		{
			Name:        "container_resource_allocation_rollup",
			Description: "Generic Kubernetes container requests/limits rollup for allocation/configuration questions. Use this after schema/health when the user asks for allocated resources rather than live usage; answer from returned rows instead of probing JSON syntax repeatedly.",
			SQL: `with latest_pods as (
  select namespace, name, uid, doc,
         row_number() over (partition by cluster_id, kind, namespace, name, uid order by observed_at desc) as rn
  from observations
  where cluster_id = 'CLUSTER_ID'
    and kind = 'Pod'
    and observed_at >= toDateTime64('2026-05-25 00:00:00', 3, 'UTC')
    and position(doc, '\"resources\"') > 0
), container_resources as (
  select namespace, name,
         arrayJoin(JSONExtractArrayRaw(doc, 'spec', 'containers')) as container_raw,
         JSONExtractString(container_raw, 'resources', 'requests', 'cpu') as cpu_request,
         JSONExtractString(container_raw, 'resources', 'requests', 'memory') as memory_request
  from latest_pods
  where rn = 1
)
select namespace,
       countDistinct(name) as pods,
       count() as containers,
       countIf(JSONExtractRaw(container_raw, 'resources', 'requests') != '') as containers_with_requests,
       countIf(JSONExtractRaw(container_raw, 'resources', 'limits') != '') as containers_with_limits,
       arrayStringConcat(arraySlice(groupUniqArrayIf(cpu_request, cpu_request != ''), 1, 5), ', ') as cpu_request_samples,
       arrayStringConcat(arraySlice(groupUniqArrayIf(memory_request, memory_request != ''), 1, 5), ', ') as memory_request_samples
from container_resources
group by namespace
order by containers_with_requests desc, containers desc
limit 25`,
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
