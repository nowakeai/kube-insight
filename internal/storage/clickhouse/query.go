package clickhouse

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type QueryResult struct {
	Meta       []QueryMeta        `json:"meta,omitempty"`
	Data       []map[string]any   `json:"data"`
	Rows       int                `json:"rows"`
	RowsBefore int                `json:"rowsBeforeLimitAtLeast,omitempty"`
	Statistics map[string]float64 `json:"statistics,omitempty"`
}

type QueryMeta struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type rawQueryResult struct {
	Meta       []QueryMeta      `json:"meta"`
	Data       []map[string]any `json:"data"`
	Rows       int              `json:"rows"`
	RowsBefore int              `json:"rows_before_limit_at_least"`
	Statistics struct {
		Elapsed   float64 `json:"elapsed"`
		RowsRead  float64 `json:"rows_read"`
		BytesRead float64 `json:"bytes_read"`
	} `json:"statistics"`
}

func (c HTTPClient) QueryJSON(ctx context.Context, query string) (QueryResult, error) {
	endpoint := strings.TrimSpace(c.Endpoint)
	if endpoint == "" {
		return QueryResult{}, fmt.Errorf("clickhouse endpoint is required")
	}
	text := strings.TrimSpace(query)
	if text == "" {
		return QueryResult{}, fmt.Errorf("query is required")
	}
	if !strings.Contains(strings.ToUpper(text), "FORMAT JSON") {
		text += " FORMAT JSON"
	}
	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewBufferString(text))
	if err != nil {
		return QueryResult{}, err
	}
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")
	resp, err := client.Do(req)
	if err != nil {
		return QueryResult{}, err
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 16*1024*1024))
	closeErr := resp.Body.Close()
	if readErr != nil {
		return QueryResult{}, readErr
	}
	if closeErr != nil {
		return QueryResult{}, closeErr
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := strings.TrimSpace(string(body))
		if message == "" {
			message = resp.Status
		}
		return QueryResult{}, fmt.Errorf("clickhouse query failed: status=%s body=%s", resp.Status, message)
	}
	var raw rawQueryResult
	if err := json.Unmarshal(body, &raw); err != nil {
		return QueryResult{}, err
	}
	stats := map[string]float64{}
	if raw.Statistics.Elapsed != 0 || raw.Statistics.RowsRead != 0 || raw.Statistics.BytesRead != 0 {
		stats["elapsed"] = raw.Statistics.Elapsed
		stats["rowsRead"] = raw.Statistics.RowsRead
		stats["bytesRead"] = raw.Statistics.BytesRead
	}
	return QueryResult{
		Meta:       raw.Meta,
		Data:       raw.Data,
		Rows:       raw.Rows,
		RowsBefore: raw.RowsBefore,
		Statistics: stats,
	}, nil
}

type ServiceEvidenceOptions struct {
	Database  string
	ClusterID string
	Namespace string
	Name      string
	Limit     int
}

type ServiceEvidenceResult struct {
	Input   ServiceEvidenceOptions `json:"input"`
	Service []map[string]any       `json:"service"`
	Objects []map[string]any       `json:"objects"`
	Facts   []map[string]any       `json:"facts"`
	Edges   []map[string]any       `json:"edges"`
	Changes []map[string]any       `json:"changes"`
	Summary ServiceEvidenceSummary `json:"summary"`
}

type ServiceEvidenceSummary struct {
	Services int `json:"services"`
	Objects  int `json:"objects"`
	Facts    int `json:"facts"`
	Edges    int `json:"edges"`
	Changes  int `json:"changes"`
}

func (c HTTPClient) QueryServiceEvidence(ctx context.Context, opts ServiceEvidenceOptions) (ServiceEvidenceResult, error) {
	return QueryServiceEvidence(ctx, c, opts)
}

func QueryServiceEvidence(ctx context.Context, runner QueryRunner, opts ServiceEvidenceOptions) (ServiceEvidenceResult, error) {
	if runner == nil {
		return ServiceEvidenceResult{}, fmt.Errorf("clickhouse query runner is required")
	}
	if opts.Database == "" {
		opts.Database = defaultDatabase
	}
	opts.Limit = boundedLimit(opts.Limit, 100, 10000)
	if err := (SchemaOptions{Database: opts.Database}).Validate(); err != nil {
		return ServiceEvidenceResult{}, err
	}
	if opts.Namespace == "" || opts.Name == "" {
		return ServiceEvidenceResult{}, fmt.Errorf("service evidence requires namespace and name")
	}
	serviceWhere := fmt.Sprintf("kind = 'Service' AND namespace = %s AND name = %s", quoteString(opts.Namespace), quoteString(opts.Name))
	if opts.ClusterID != "" {
		serviceWhere += " AND cluster_id = " + quoteString(opts.ClusterID)
	}
	serviceQuery := fmt.Sprintf("SELECT cluster_id, object_id, kind, namespace, name, uid, max(observed_at) AS last_observed_at FROM %s.versions WHERE %s GROUP BY cluster_id, object_id, kind, namespace, name, uid ORDER BY last_observed_at DESC LIMIT 10", q(opts.Database), serviceWhere)
	service, err := runner.QueryJSON(ctx, serviceQuery)
	if err != nil {
		return ServiceEvidenceResult{}, err
	}
	objectIDs := valuesForColumn(service.Data, "object_id")
	if len(objectIDs) == 0 {
		out := ServiceEvidenceResult{Input: opts, Service: service.Data}
		out.Summary.Services = len(out.Service)
		return out, nil
	}
	edgeRows := []map[string]any{}
	edgeSeen := map[string]bool{}
	relatedIDs := append([]string(nil), objectIDs...)
	for range 3 {
		expanded, err := expandObjectIDs(ctx, runner, opts.Database, relatedIDs)
		if err != nil {
			return ServiceEvidenceResult{}, err
		}
		edgeQuery := fmt.Sprintf("SELECT edge_type, src_id, dst_id, src_kind, dst_kind, valid_from, valid_to FROM %s.edges WHERE src_id IN (%s) OR dst_id IN (%s) ORDER BY valid_from DESC LIMIT %d", q(opts.Database), sqlStringList(expanded.Search), sqlStringList(expanded.Search), opts.Limit)
		edges, err := runner.QueryJSON(ctx, edgeQuery)
		if err != nil {
			return ServiceEvidenceResult{}, err
		}
		appendUniqueEdgeRows(&edgeRows, edgeSeen, edges.Data)
		rawRelated := relatedObjectIDs(expanded.Canonical, edgeRows)
		nextExpanded, err := expandObjectIDs(ctx, runner, opts.Database, rawRelated)
		if err != nil {
			return ServiceEvidenceResult{}, err
		}
		nextIDs := nextExpanded.Canonical
		if stringSetEqual(nextIDs, relatedIDs) {
			break
		}
		relatedIDs = nextIDs
	}
	finalExpanded, err := expandObjectIDs(ctx, runner, opts.Database, relatedObjectIDs(relatedIDs, edgeRows))
	if err != nil {
		return ServiceEvidenceResult{}, err
	}
	edgeRows = canonicalizeEdgeRows(edgeRows, finalExpanded)
	relatedIDs = finalExpanded.Canonical
	objects := QueryResult{}
	facts := QueryResult{}
	changes := QueryResult{}
	if len(relatedIDs) > 0 {
		objectQuery := fmt.Sprintf("SELECT cluster_id, object_id, kind, namespace, name, uid, max(observed_at) AS last_observed_at FROM %s.versions WHERE object_id IN (%s) GROUP BY cluster_id, object_id, kind, namespace, name, uid ORDER BY last_observed_at DESC LIMIT %d", q(opts.Database), sqlStringList(relatedIDs), opts.Limit)
		objects, err = runner.QueryJSON(ctx, objectQuery)
		if err != nil {
			return ServiceEvidenceResult{}, err
		}
		factQuery := fmt.Sprintf("SELECT ts, object_id, kind, namespace, name, fact_key, fact_value, severity FROM %s.facts WHERE object_id IN (%s) ORDER BY ts DESC LIMIT %d", q(opts.Database), sqlStringList(relatedIDs), opts.Limit)
		facts, err = runner.QueryJSON(ctx, factQuery)
		if err != nil {
			return ServiceEvidenceResult{}, err
		}
		changeQuery := fmt.Sprintf("SELECT ts, object_id, kind, namespace, name, change_family, path, op, old_scalar, new_scalar, severity FROM %s.changes WHERE object_id IN (%s) ORDER BY ts DESC LIMIT %d", q(opts.Database), sqlStringList(relatedIDs), opts.Limit)
		changes, err = runner.QueryJSON(ctx, changeQuery)
		if err != nil {
			return ServiceEvidenceResult{}, err
		}
	}
	out := ServiceEvidenceResult{
		Input:   opts,
		Service: service.Data,
		Objects: objects.Data,
		Facts:   facts.Data,
		Edges:   edgeRows,
		Changes: changes.Data,
	}
	out.Summary = ServiceEvidenceSummary{
		Services: len(out.Service),
		Objects:  len(out.Objects),
		Facts:    len(out.Facts),
		Edges:    len(out.Edges),
		Changes:  len(out.Changes),
	}
	return out, nil
}

type objectIDExpansion struct {
	Canonical     []string
	Search        []string
	AliasToObject map[string]string
	KindByObject  map[string]string
}

func expandObjectIDs(ctx context.Context, runner QueryRunner, database string, ids []string) (objectIDExpansion, error) {
	canonical := map[string]bool{}
	search := map[string]bool{}
	aliasToObject := map[string]string{}
	kindByObject := map[string]string{}
	for _, id := range ids {
		if id == "" {
			continue
		}
		canonical[id] = true
		search[id] = true
		aliasToObject[id] = id
	}
	if len(search) == 0 {
		return objectIDExpansion{AliasToObject: aliasToObject, KindByObject: kindByObject}, nil
	}
	query := fmt.Sprintf("SELECT object_id, alias_id, kind FROM %s.object_aliases WHERE object_id IN (%s) OR alias_id IN (%s) LIMIT 10000", q(database), sqlStringList(mapKeys(search)), sqlStringList(mapKeys(search)))
	rows, err := runner.QueryJSON(ctx, query)
	if err != nil {
		return objectIDExpansion{}, err
	}
	for _, row := range rows.Data {
		objectID, _ := row["object_id"].(string)
		aliasID, _ := row["alias_id"].(string)
		kind, _ := row["kind"].(string)
		if objectID != "" {
			canonical[objectID] = true
			search[objectID] = true
			aliasToObject[objectID] = objectID
			if kind != "" {
				kindByObject[objectID] = kind
			}
		}
		if aliasID != "" {
			search[aliasID] = true
			if objectID != "" {
				aliasToObject[aliasID] = objectID
			}
		}
	}
	return objectIDExpansion{Canonical: mapKeys(canonical), Search: mapKeys(search), AliasToObject: aliasToObject, KindByObject: kindByObject}, nil
}

func canonicalizeEdgeRows(rows []map[string]any, expanded objectIDExpansion) []map[string]any {
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		next := map[string]any{}
		for key, value := range row {
			next[key] = value
		}
		canonicalizeEdgeEndpoint(next, "src_id", "src_kind", expanded)
		canonicalizeEdgeEndpoint(next, "dst_id", "dst_kind", expanded)
		out = append(out, next)
	}
	return out
}

func canonicalizeEdgeEndpoint(row map[string]any, idColumn, kindColumn string, expanded objectIDExpansion) {
	value, _ := row[idColumn].(string)
	if value == "" {
		return
	}
	canonical := expanded.AliasToObject[value]
	if canonical == "" {
		canonical = value
	}
	row[idColumn] = canonical
	if kind := expanded.KindByObject[canonical]; kind != "" {
		row[kindColumn] = kind
	}
}

func valuesForColumn(rows []map[string]any, column string) []string {
	seen := map[string]bool{}
	var out []string
	for _, row := range rows {
		value, _ := row[column].(string)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func appendUniqueEdgeRows(out *[]map[string]any, seen map[string]bool, rows []map[string]any) {
	for _, row := range rows {
		key := edgeRowKey(row)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		*out = append(*out, row)
	}
}

func edgeRowKey(row map[string]any) string {
	parts := make([]string, 0, 5)
	for _, column := range []string{"edge_type", "src_id", "dst_id", "valid_from", "valid_to"} {
		value, _ := row[column].(string)
		parts = append(parts, value)
	}
	key := strings.Join(parts, "\x00")
	if strings.Trim(key, "\x00") == "" {
		return ""
	}
	return key
}

func relatedObjectIDs(seed []string, edges []map[string]any) []string {
	seen := map[string]bool{}
	for _, value := range seed {
		if value != "" {
			seen[value] = true
		}
	}
	for _, edge := range edges {
		for _, column := range []string{"src_id", "dst_id"} {
			value, _ := edge[column].(string)
			if value != "" {
				seen[value] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for value := range seen {
		out = append(out, value)
	}
	return out
}

func mapKeys(values map[string]bool) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	return out
}

func stringSetEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]bool{}
	for _, value := range a {
		seen[value] = true
	}
	for _, value := range b {
		if !seen[value] {
			return false
		}
	}
	return true
}

func sqlStringList(values []string) string {
	if len(values) == 0 {
		return "''"
	}
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, quoteString(value))
	}
	return strings.Join(parts, ",")
}
