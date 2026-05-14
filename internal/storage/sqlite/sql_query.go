package sqlite

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

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
	Tables []SQLSchemaTable `json:"tables"`
	Notes  []string         `json:"notes,omitempty"`
}

type SQLSchemaTable struct {
	Name    string            `json:"name"`
	Columns []SQLSchemaColumn `json:"columns"`
	Indexes []SQLSchemaIndex  `json:"indexes,omitempty"`
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

func OpenReadOnly(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("sqlite path is required")
	}
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	for _, stmt := range []string{
		"pragma query_only = on",
		"pragma foreign_keys = on",
		"pragma busy_timeout = 5000",
	} {
		if _, err := db.ExecContext(context.Background(), stmt); err != nil {
			_ = db.Close()
			return nil, err
		}
	}
	return &Store{db: db}, nil
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
select name
from sqlite_master
where type = 'table'
  and name not like 'sqlite_%'
order by name`)
	if err != nil {
		return SQLSchema{}, err
	}
	defer rows.Close()

	var tableNames []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return SQLSchema{}, err
		}
		tableNames = append(tableNames, name)
	}
	if err := rows.Err(); err != nil {
		return SQLSchema{}, err
	}
	if err := rows.Close(); err != nil {
		return SQLSchema{}, err
	}

	var schema SQLSchema
	for _, name := range tableNames {
		table := SQLSchemaTable{Name: name}
		columns, err := s.schemaColumns(ctx, name)
		if err != nil {
			return SQLSchema{}, err
		}
		table.Columns = columns
		indexes, err := s.schemaIndexes(ctx, name)
		if err != nil {
			return SQLSchema{}, err
		}
		table.Indexes = indexes
		schema.Tables = append(schema.Tables, table)
	}
	schema.Notes = []string{
		"All timestamps are Unix milliseconds.",
		"Join latest_index.kind_id or objects.kind_id to object_kinds.id, then object_kinds.api_resource_id to api_resources.id.",
		"Use object_facts and object_changes for fast investigation candidates; use versions/blob_ref/blobs.data for proof when exact retained JSON is needed.",
		"object_edges.src_id and object_edges.dst_id reference objects.id.",
		"SQL access is read-only; use SELECT/WITH/EXPLAIN only.",
	}
	return schema, nil
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
