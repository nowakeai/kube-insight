//go:build chdb

package chdb

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"

	chdbgo "github.com/chdb-io/chdb-go/chdb"

	"kube-insight/internal/storage"
	"kube-insight/internal/storage/clickhouse"
)

const (
	defaultBatchSize       = 1000
	defaultFlushIntervalMS = 1000
)

var identifierPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

type Store struct {
	*clickhouse.Store
	client *client
}

func NewStore(opts Options) (storage.Store, error) {
	opts.Path = strings.TrimSpace(opts.Path)
	opts.Database = strings.TrimSpace(opts.Database)
	if opts.Path == "" {
		return nil, fmt.Errorf("chdb path is required")
	}
	if opts.Database == "" {
		return nil, fmt.Errorf("chdb database is required")
	}
	client, err := newClient(opts.Path)
	if err != nil {
		return nil, err
	}
	store, err := clickhouse.NewStore(client, clickhouse.Options{
		Database:        opts.Database,
		BatchSize:       defaultBatchSize,
		FlushIntervalMS: defaultFlushIntervalMS,
	})
	if err != nil {
		client.Close()
		return nil, err
	}
	return &Store{Store: store, client: client}, nil
}

func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	err := s.Store.Close()
	if s.client != nil {
		s.client.Close()
	}
	return err
}

type client struct {
	path    string
	session *chdbgo.Session
	mu      sync.Mutex
}

var _ clickhouse.Client = (*client)(nil)

func newClient(path string) (*client, error) {
	session, err := chdbgo.NewSession(path)
	if err != nil {
		return nil, fmt.Errorf("open chdb session: %w", err)
	}
	return &client{path: path, session: session}, nil
}

func (c *client) Close() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.session == nil {
		return
	}
	c.session.Close()
	c.session = nil
}

func (c *client) ApplySchema(ctx context.Context, statements []string) (clickhouse.ApplyResult, error) {
	out := clickhouse.ApplyResult{Endpoint: c.path, Statements: len(statements)}
	for _, statement := range statements {
		text := strings.TrimSpace(statement)
		if text == "" {
			continue
		}
		if !strings.HasSuffix(text, ";") {
			text += ";"
		}
		if _, err := c.exec(ctx, text); err != nil {
			wrapped := fmt.Errorf("chdb schema statement failed: %w", err)
			out.Errors = append(out.Errors, wrapped.Error())
			return out, wrapped
		}
		out.Applied++
	}
	return out, nil
}

func (c *client) QueryJSON(ctx context.Context, query string) (clickhouse.QueryResult, error) {
	text := strings.TrimSpace(query)
	if text == "" {
		return clickhouse.QueryResult{}, fmt.Errorf("query is required")
	}
	result, err := c.execFormat(ctx, text, "JSON")
	if err != nil {
		return clickhouse.QueryResult{}, err
	}
	if result == nil {
		return clickhouse.QueryResult{Data: []map[string]any{}}, nil
	}
	defer result.Free()
	var raw struct {
		Meta       []clickhouse.QueryMeta `json:"meta"`
		Data       []map[string]any       `json:"data"`
		Rows       int                    `json:"rows"`
		RowsBefore int                    `json:"rows_before_limit_at_least"`
		Statistics struct {
			Elapsed   float64 `json:"elapsed"`
			RowsRead  float64 `json:"rows_read"`
			BytesRead float64 `json:"bytes_read"`
		} `json:"statistics"`
	}
	if err := json.Unmarshal(result.Buf(), &raw); err != nil {
		return clickhouse.QueryResult{}, err
	}
	stats := map[string]float64{}
	if raw.Statistics.Elapsed != 0 || raw.Statistics.RowsRead != 0 || raw.Statistics.BytesRead != 0 {
		stats["elapsed"] = raw.Statistics.Elapsed
		stats["rowsRead"] = raw.Statistics.RowsRead
		stats["bytesRead"] = raw.Statistics.BytesRead
	}
	if raw.Data == nil {
		raw.Data = []map[string]any{}
	}
	return clickhouse.QueryResult{
		Meta:       raw.Meta,
		Data:       raw.Data,
		Rows:       raw.Rows,
		RowsBefore: raw.RowsBefore,
		Statistics: stats,
	}, nil
}

func (c *client) QueryTSV(ctx context.Context, query string) (clickhouse.TSVResult, error) {
	text := strings.TrimSpace(query)
	if text == "" {
		return clickhouse.TSVResult{}, fmt.Errorf("query is required")
	}
	result, err := c.execFormat(ctx, text, "JSONCompact")
	if err != nil {
		return clickhouse.TSVResult{}, err
	}
	if result == nil {
		return clickhouse.TSVResult{Rows: [][]string{}}, nil
	}
	defer result.Free()
	var raw struct {
		Meta []clickhouse.QueryMeta `json:"meta"`
		Data [][]any                `json:"data"`
	}
	if err := json.Unmarshal(result.Buf(), &raw); err != nil {
		return clickhouse.TSVResult{}, err
	}
	columns := make([]string, 0, len(raw.Meta))
	for _, meta := range raw.Meta {
		columns = append(columns, meta.Name)
	}
	rows := make([][]string, 0, len(raw.Data))
	for _, rawRow := range raw.Data {
		row := make([]string, len(rawRow))
		for i, value := range rawRow {
			row[i] = compactValueString(value)
		}
		rows = append(rows, row)
	}
	return clickhouse.TSVResult{Columns: columns, Rows: rows}, nil
}

func compactValueString(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	case json.Number:
		return v.String()
	default:
		return fmt.Sprint(v)
	}
}

func (c *client) InsertEvidenceBatch(ctx context.Context, batch clickhouse.EvidenceBatch) (clickhouse.InsertResult, error) {
	out := clickhouse.InsertResult{Endpoint: c.path, Database: batch.Database, Tables: map[string]int{}}
	for _, table := range []struct {
		name string
		rows []map[string]any
	}{
		{name: "observations", rows: batch.Observations},
		{name: "object_aliases", rows: batch.ObjectAliases},
		{name: "versions", rows: batch.Versions},
		{name: "facts", rows: batch.Facts},
		{name: "edges", rows: batch.Edges},
		{name: "changes", rows: batch.Changes},
	} {
		if len(table.rows) == 0 {
			continue
		}
		if err := c.InsertRows(ctx, batch.Database, table.name, table.rows); err != nil {
			return out, err
		}
		out.Tables[table.name] = len(table.rows)
		out.Rows += len(table.rows)
	}
	return out, nil
}

func (c *client) InsertRows(ctx context.Context, database, table string, rows []map[string]any) error {
	if len(rows) == 0 {
		return nil
	}
	if err := (clickhouse.SchemaOptions{Database: database}).Validate(); err != nil {
		return err
	}
	if !identifierPattern.MatchString(table) {
		return fmt.Errorf("table %q is not a valid ClickHouse identifier", table)
	}
	var body bytes.Buffer
	body.WriteString("INSERT INTO ")
	body.WriteString(quoteIdentifier(database))
	body.WriteString(".")
	body.WriteString(quoteIdentifier(table))
	body.WriteString(" FORMAT JSONEachRow\n")
	encoder := json.NewEncoder(&body)
	for _, row := range rows {
		if err := encoder.Encode(row); err != nil {
			return err
		}
	}
	_, err := c.exec(ctx, body.String())
	if err != nil {
		return fmt.Errorf("chdb insert into %s failed: %w", table, err)
	}
	return nil
}

func (c *client) exec(ctx context.Context, query string) (chdbResult, error) {
	return c.execFormat(ctx, query, "CSV")
}

func (c *client) execFormat(ctx context.Context, query, format string) (chdbResult, error) {
	if c == nil {
		return nil, fmt.Errorf("chdb session is closed")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	if c.session == nil {
		c.mu.Unlock()
		return nil, fmt.Errorf("chdb session is closed")
	}
	result, err := c.session.Query(query, format)
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		if result != nil {
			result.Free()
		}
		return nil, err
	}
	return result, nil
}

type chdbResult interface {
	Buf() []byte
	Free()
}

func quoteIdentifier(identifier string) string {
	return "`" + strings.ReplaceAll(identifier, "`", "``") + "`"
}
