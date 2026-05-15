package sqlite

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"kube-insight/internal/resourceprofile"

	_ "modernc.org/sqlite"
)

type SQLQueryOptions struct {
	SQL     string `json:"sql"`
	MaxRows int    `json:"maxRows"`
}

type SQLQueryResult struct {
	SQL       string           `json:"sql"`
	Columns   []string         `json:"columns"`
	Rows      []map[string]any `json:"rows"`
	RowCount  int              `json:"rowCount"`
	MaxRows   int              `json:"maxRows"`
	Truncated bool             `json:"truncated"`
	ElapsedMS float64          `json:"elapsedMs"`
}

type SQLSchema struct {
	Tables        []SQLSchemaTable        `json:"tables"`
	Relationships []SQLSchemaRelationship `json:"relationships,omitempty"`
	Recipes       []SQLSchemaRecipe       `json:"recipes,omitempty"`
	Notes         []string                `json:"notes,omitempty"`
}

type SQLSchemaTable struct {
	Name        string            `json:"name"`
	Type        string            `json:"type,omitempty"`
	Description string            `json:"description,omitempty"`
	Columns     []SQLSchemaColumn `json:"columns"`
	Indexes     []SQLSchemaIndex  `json:"indexes,omitempty"`
}

type SQLSchemaColumn struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	NotNull bool   `json:"notNull"`
	Primary bool   `json:"primary"`
}

type SQLSchemaIndex struct {
	Name    string   `json:"name"`
	Unique  bool     `json:"unique"`
	Columns []string `json:"columns"`
}

type SQLSchemaRelationship struct {
	Name        string `json:"name"`
	From        string `json:"from"`
	To          string `json:"to"`
	SQL         string `json:"sql"`
	Description string `json:"description,omitempty"`
}

type SQLSchemaRecipe struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	SQL         string `json:"sql"`
}

func OpenReadOnly(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("sqlite path is required")
	}
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	values := url.Values{}
	values.Set("mode", "ro")
	values.Add("_pragma", "busy_timeout=1000")
	values.Add("_pragma", "query_only=1")
	db, err := sql.Open("sqlite", sqliteFileURI(path, values))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db, path: path, profileRules: resourceprofile.DefaultRules()}, nil
}

func sqliteFileURI(path string, values url.Values) string {
	if strings.HasPrefix(path, "file:") {
		u, err := url.Parse(path)
		if err == nil {
			query := u.Query()
			for key, entries := range values {
				for _, value := range entries {
					query.Add(key, value)
				}
			}
			u.RawQuery = query.Encode()
			return u.String()
		}
	}
	u := url.URL{Scheme: "file", RawQuery: values.Encode()}
	if filepath.IsAbs(path) {
		u.Path = path
	} else {
		u.Opaque = path
	}
	return u.String()
}

func (s *Store) QuerySQL(ctx context.Context, opts SQLQueryOptions) (SQLQueryResult, error) {
	query := strings.TrimSpace(opts.SQL)
	if err := validateReadOnlySQL(query); err != nil {
		return SQLQueryResult{}, err
	}
	maxRows := opts.MaxRows
	if maxRows <= 0 {
		maxRows = 1000
	}
	start := time.Now()
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return SQLQueryResult{}, err
	}
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		return SQLQueryResult{}, err
	}
	out := SQLQueryResult{
		SQL:     query,
		Columns: columns,
		MaxRows: maxRows,
		Rows:    []map[string]any{},
	}
	for rows.Next() {
		if out.RowCount >= maxRows {
			out.Truncated = true
			break
		}
		values := make([]any, len(columns))
		scan := make([]any, len(columns))
		for i := range values {
			scan[i] = &values[i]
		}
		if err := rows.Scan(scan...); err != nil {
			return SQLQueryResult{}, err
		}
		row := map[string]any{}
		for i, column := range columns {
			row[column] = sqlValue(values[i])
		}
		out.Rows = append(out.Rows, row)
		out.RowCount++
	}
	if err := rows.Err(); err != nil {
		return SQLQueryResult{}, err
	}
	out.ElapsedMS = float64(time.Since(start).Microseconds()) / 1000
	return out, nil
}

func (s *Store) QuerySchema(ctx context.Context) (SQLSchema, error) {
	rows, err := s.db.QueryContext(ctx, `
select name, type
from sqlite_master
where type in ('table', 'view')
  and name not like 'sqlite_%'
order by type, name`)
	if err != nil {
		return SQLSchema{}, err
	}
	defer rows.Close()

	type schemaObject struct {
		name string
		typ  string
	}
	var schemaObjects []schemaObject
	for rows.Next() {
		var object schemaObject
		if err := rows.Scan(&object.name, &object.typ); err != nil {
			return SQLSchema{}, err
		}
		schemaObjects = append(schemaObjects, object)
	}
	if err := rows.Err(); err != nil {
		return SQLSchema{}, err
	}
	if err := rows.Close(); err != nil {
		return SQLSchema{}, err
	}

	var schema SQLSchema
	for _, object := range schemaObjects {
		table := SQLSchemaTable{Name: object.name, Type: object.typ, Description: schemaTableDescription(object.name)}
		columns, err := s.schemaColumns(ctx, object.name)
		if err != nil {
			return SQLSchema{}, err
		}
		table.Columns = columns
		if object.typ == "table" {
			indexes, err := s.schemaIndexes(ctx, object.name)
			if err != nil {
				return SQLSchema{}, err
			}
			table.Indexes = indexes
		}
		schema.Tables = append(schema.Tables, table)
	}
	schema.Relationships = schemaRelationships()
	schema.Recipes = schemaRecipes()
	schema.Notes = []string{
		"All timestamps are Unix milliseconds.",
		"Join latest_raw_index.kind_id, latest_index.kind_id, or objects.kind_id to object_kinds.id, then object_kinds.api_resource_id to api_resources.id.",
		"object_observations records every observed object event; versions records retained content documents.",
		"Use object_facts and object_changes for fast investigation candidates; use versions/blob_ref/blobs.data for proof when exact retained JSON is needed.",
		"object_edges.src_id and object_edges.dst_id reference objects.id.",
		"Prefer object_facts/object_edges/object_changes before scanning blobs.data; blob scans are proof fallback and can be slower.",
		"Use latest_raw_documents for the latest observed sanitized cluster snapshot; use latest_documents for the latest retained/normalized proof document.",
		"SQL access is read-only; use SELECT/WITH/EXPLAIN only.",
	}
	return schema, nil
}

func schemaTableDescription(name string) string {
	descriptions := map[string]string{
		"clusters":                     "Stored Kubernetes cluster identities. Join clusters.id to cluster_id columns.",
		"api_resources":                "Discovered Kubernetes API resources: group/version/resource/kind/scope/verbs and discovery lifecycle.",
		"object_kinds":                 "Normalized kind records linked to api_resources. Join objects.kind_id or latest_index.kind_id here to recover api_group/api_version/kind.",
		"resource_processing_profiles": "Per-resource processing policy selected during discovery: retention policy, filters, extractor set, priority, and enabled flag.",
		"objects":                      "Stable logical Kubernetes objects keyed by cluster, kind, namespace/name, and uid. This is the hub for history, facts, edges, and latest rows.",
		"blobs":                        "Deduplicated retained JSON payloads. During the SQLite PoC codec is usually identity; cast(data as text) to inspect JSON proof.",
		"versions":                     "Retained content-changing object versions. Join versions.object_id to objects.id and versions.blob_ref to blobs.digest.",
		"object_observations":          "Every list/watch observation, including unchanged sightings. version_id is null or points to the retained content version.",
		"latest_index":                 "Fast latest retained/normalized history index. Join latest_version_id -> versions.id -> blobs.digest for latest retained JSON proof.",
		"latest_documents":             "View that exposes latest_index plus latest retained/normalized JSON text as doc for identity-coded blobs.",
		"latest_raw_index":             "Fast latest observed sanitized cluster snapshot. This row updates even when filters prevent a new retained version.",
		"latest_raw_documents":         "View that exposes latest_raw_index plus latest observed sanitized JSON text as doc.",
		"object_edges":                 "Extracted topology/reference edges between objects, such as Event->object, Service->Pod, RBAC binding->role, webhook->Service.",
		"object_facts":                 "Extracted searchable facts with time, severity, key/value, and optional object/node/workload/service anchors.",
		"object_changes":               "Scalar change summaries and important path changes for retained versions.",
		"filter_decisions":             "Auditable ingestion filter decisions, including destructive redaction/removal metadata.",
		"filter_decision_rollups":      "Hourly aggregate counts for high-volume low-risk filter decisions such as resourceVersion and managedFields normalization.",
		"ingestion_offsets":            "Per-resource collector health, last list/watch/bookmark times, resourceVersion, queued/retrying/error status.",
		"maintenance_runs":             "Storage maintenance audit records for compaction/checkpoint/retention tasks.",
	}
	return descriptions[name]
}

func schemaRelationships() []SQLSchemaRelationship {
	return []SQLSchemaRelationship{
		{
			Name:        "latest_object_kind",
			From:        "latest_index.kind_id",
			To:          "object_kinds.id",
			SQL:         "latest_index li join object_kinds ok on ok.id = li.kind_id",
			Description: "Resolve current rows to api_group/api_version/kind.",
		},
		{
			Name:        "object_kind",
			From:        "objects.kind_id",
			To:          "object_kinds.id",
			SQL:         "objects o join object_kinds ok on ok.id = o.kind_id",
			Description: "Resolve logical objects to api_group/api_version/kind.",
		},
		{
			Name:        "kind_resource",
			From:        "object_kinds.api_resource_id",
			To:          "api_resources.id",
			SQL:         "object_kinds ok join api_resources ar on ar.id = ok.api_resource_id",
			Description: "Get Kubernetes resource plural, scope, and verbs for a kind.",
		},
		{
			Name:        "latest_current_json",
			From:        "latest_index.latest_version_id",
			To:          "versions.id -> blobs.digest",
			SQL:         "latest_index li join versions v on v.id = li.latest_version_id join blobs b on b.digest = v.blob_ref",
			Description: "Fetch latest retained JSON proof for latest objects.",
		},
		{
			Name:        "latest_raw_json",
			From:        "latest_raw_index.object_id",
			To:          "objects.id",
			SQL:         "latest_raw_documents lrd join objects o on o.id = lrd.object_id",
			Description: "Fetch the latest observed sanitized cluster snapshot, including resourceVersion/generation churn.",
		},
		{
			Name:        "object_history",
			From:        "objects.id",
			To:          "versions.object_id",
			SQL:         "objects o join versions v on v.object_id = o.id join blobs b on b.digest = v.blob_ref",
			Description: "Fetch retained content versions and historical JSON proof.",
		},
		{
			Name:        "object_observation_trail",
			From:        "objects.id",
			To:          "object_observations.object_id",
			SQL:         "objects o join object_observations oo on oo.object_id = o.id",
			Description: "Fetch every observed sighting, including unchanged observations.",
		},
		{
			Name:        "object_facts",
			From:        "objects.id",
			To:          "object_facts.object_id",
			SQL:         "objects o join object_facts f on f.object_id = o.id",
			Description: "Find indexed evidence for an object without scanning JSON blobs.",
		},
		{
			Name:        "object_changes",
			From:        "objects.id",
			To:          "object_changes.object_id",
			SQL:         "objects o join object_changes ch on ch.object_id = o.id",
			Description: "Find important path/scalar changes for retained object versions.",
		},
		{
			Name:        "edge_source_target",
			From:        "object_edges.src_id/object_edges.dst_id",
			To:          "objects.id",
			SQL:         "object_edges e join objects src on src.id = e.src_id join objects dst on dst.id = e.dst_id",
			Description: "Traverse extracted topology/reference edges.",
		},
		{
			Name:        "collector_health",
			From:        "ingestion_offsets.api_resource_id",
			To:          "api_resources.id",
			SQL:         "api_resources ar left join ingestion_offsets io on io.api_resource_id = ar.id",
			Description: "Check whether a resource was listed, queued for a watch slot, watched, retrying, or errored before making claims.",
		},
	}
}

func schemaRecipes() []SQLSchemaRecipe {
	return []SQLSchemaRecipe{
		{
			Name:        "coverage_first",
			Description: "Check collector coverage and resources that may make evidence incomplete.",
			SQL: `select ar.api_group, ar.api_version, ar.resource, ar.kind,
       coalesce(io.status, 'not_started') as status, io.error
from api_resources ar
left join ingestion_offsets io on io.api_resource_id = ar.id
where coalesce(io.status, 'not_started') in ('not_started','retrying','list_error','watch_error')
order by status, ar.api_group, ar.resource
limit 50`,
		},
		{
			Name:        "latest_by_kind",
			Description: "List latest observed objects for one kind.",
			SQL: `select ok.api_group, ok.api_version, ok.kind, li.namespace, li.name, li.uid
from latest_raw_index li
join object_kinds ok on ok.id = li.kind_id
where ok.kind = 'Pod'
order by li.observed_at desc
limit 50`,
		},
		{
			Name:        "event_to_involved_object",
			Description: "Follow Event edges to the object the Event references.",
			SQL: `select ev.namespace as event_namespace, ev.name as event_name,
       dst_kind.kind as involved_kind, dst.namespace as involved_namespace, dst.name as involved_name,
       reason.fact_value as reason
from object_edges e
join objects ev on ev.id = e.src_id
join objects dst on dst.id = e.dst_id
join object_kinds dst_kind on dst_kind.id = dst.kind_id
left join object_facts reason on reason.object_id = ev.id and reason.fact_key = 'k8s_event.reason'
where e.edge_type in ('event_regarding_object','event_involves_object','event_related_object')
order by e.valid_from desc
limit 50`,
		},
		{
			Name:        "service_to_endpointslice_to_pod",
			Description: "Trace a Service to EndpointSlices and target Pods.",
			SQL: `select svc.namespace as service_namespace, svc.name as service_name,
       slice.name as endpointslice_name, pod.namespace as pod_namespace, pod.name as pod_name
from object_edges svc_edge
join objects slice on slice.id = svc_edge.src_id
join objects svc on svc.id = svc_edge.dst_id
join object_edges pod_edge on pod_edge.src_id = slice.id and pod_edge.edge_type = 'endpointslice_targets_pod'
join objects pod on pod.id = pod_edge.dst_id
where svc_edge.edge_type = 'endpointslice_for_service'
order by svc.namespace, svc.name, slice.name, pod.name
limit 100`,
		},
		{
			Name:        "rbac_binding_chain",
			Description: "Show RoleBinding/ClusterRoleBinding subjects and granted roles.",
			SQL: `select binding_kind.kind as binding_kind, binding.namespace, binding.name as binding_name,
       e.edge_type, target_kind.kind as target_kind, target.namespace as target_namespace, target.name as target_name
from object_edges e
join objects binding on binding.id = e.src_id
join object_kinds binding_kind on binding_kind.id = binding.kind_id
join objects target on target.id = e.dst_id
join object_kinds target_kind on target_kind.id = target.kind_id
where e.edge_type in ('rbac_binding_binds_subject','rbac_binding_grants_role')
order by binding.name, e.edge_type
limit 100`,
		},
		{
			Name:        "webhook_to_service",
			Description: "Find admission, CRD conversion, and APIService webhooks and their backing Services.",
			SQL: `select src_kind.kind as source_kind, src.name as source_name, e.edge_type,
       dst.namespace as service_namespace, dst.name as service_name
from object_edges e
join objects src on src.id = e.src_id
join object_kinds src_kind on src_kind.id = src.kind_id
join objects dst on dst.id = e.dst_id
where e.edge_type in ('webhook_uses_service','crd_conversion_webhook_uses_service','apiservice_uses_service')
order by e.edge_type, src.name
limit 100`,
		},
		{
			Name:        "object_version_timeline",
			Description: "Fetch retained versions and JSON proof for one object.",
			SQL: `select ok.kind, o.namespace, o.name, v.seq, v.resource_version,
       datetime(v.observed_at / 1000, 'unixepoch') as observed_at,
       cast(b.data as text) as doc
from objects o
join object_kinds ok on ok.id = o.kind_id
join versions v on v.object_id = o.id
join blobs b on b.digest = v.blob_ref
where ok.kind = 'Pod' and o.namespace = 'default' and o.name = 'example'
order by v.seq desc
limit 20`,
		},
	}
}

func (s *Store) schemaColumns(ctx context.Context, table string) ([]SQLSchemaColumn, error) {
	rows, err := s.db.QueryContext(ctx, "pragma table_info("+quoteIdentifier(table)+")")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var columns []SQLSchemaColumn
	for rows.Next() {
		var cid int
		var column SQLSchemaColumn
		var defaultValue any
		var notNull int
		var primary int
		if err := rows.Scan(&cid, &column.Name, &column.Type, &notNull, &defaultValue, &primary); err != nil {
			return nil, err
		}
		column.NotNull = notNull != 0
		column.Primary = primary != 0
		columns = append(columns, column)
	}
	return columns, rows.Err()
}

func (s *Store) schemaIndexes(ctx context.Context, table string) ([]SQLSchemaIndex, error) {
	rows, err := s.db.QueryContext(ctx, "pragma index_list("+quoteIdentifier(table)+")")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var indexes []SQLSchemaIndex
	for rows.Next() {
		var seq int
		var index SQLSchemaIndex
		var unique int
		var origin string
		var partial int
		if err := rows.Scan(&seq, &index.Name, &unique, &origin, &partial); err != nil {
			return nil, err
		}
		index.Unique = unique != 0
		indexes = append(indexes, index)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for i := range indexes {
		columns, err := s.schemaIndexColumns(ctx, indexes[i].Name)
		if err != nil {
			return nil, err
		}
		indexes[i].Columns = columns
	}
	return indexes, nil
}

func (s *Store) schemaIndexColumns(ctx context.Context, index string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, "pragma index_info("+quoteIdentifier(index)+")")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var columns []string
	for rows.Next() {
		var seq int
		var cid int
		var name string
		if err := rows.Scan(&seq, &cid, &name); err != nil {
			return nil, err
		}
		columns = append(columns, name)
	}
	return columns, rows.Err()
}

func validateReadOnlySQL(query string) error {
	if query == "" {
		return errors.New("sql query is required")
	}
	sanitized := sanitizeSQLForValidation(query)
	trimmed := strings.TrimSpace(sanitized)
	trimmed = strings.TrimSuffix(trimmed, ";")
	if strings.Contains(trimmed, ";") {
		return errors.New("only one SQL statement is allowed")
	}
	tokens := sqlTokens(trimmed)
	if len(tokens) == 0 {
		return errors.New("sql query is required")
	}
	switch tokens[0] {
	case "select", "with":
	case "explain":
		if len(tokens) < 2 || (tokens[1] != "select" && tokens[1] != "query") {
			return errors.New("only EXPLAIN SELECT or EXPLAIN QUERY PLAN SELECT is allowed")
		}
	default:
		return fmt.Errorf("only read-only SELECT/WITH/EXPLAIN SQL is allowed, got %q", tokens[0])
	}
	for _, token := range tokens {
		if forbiddenSQLToken(token) {
			return fmt.Errorf("read-only SQL rejected forbidden token %q", token)
		}
	}
	return nil
}

func sanitizeSQLForValidation(query string) string {
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
		case query[i] == '\'' || query[i] == '"':
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

func sqlTokens(query string) []string {
	return strings.FieldsFunc(query, func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_')
	})
}

func forbiddenSQLToken(token string) bool {
	switch token {
	case "insert", "update", "delete", "drop", "alter", "create", "replace",
		"truncate", "attach", "detach", "vacuum", "reindex", "analyze", "pragma",
		"begin", "commit", "rollback", "savepoint", "release", "load_extension":
		return true
	default:
		return false
	}
}

func sqlValue(value any) any {
	switch v := value.(type) {
	case []byte:
		if utf8.Valid(v) {
			return string(v)
		}
		return map[string]string{
			"base64": base64.StdEncoding.EncodeToString(v),
		}
	default:
		return v
	}
}

func quoteIdentifier(identifier string) string {
	return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`
}
