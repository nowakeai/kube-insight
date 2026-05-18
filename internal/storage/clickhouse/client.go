package clickhouse

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type HTTPClient struct {
	Endpoint    string
	HTTPClient  *http.Client
	AsyncInsert bool
}

type QueryRunner interface {
	QueryJSON(ctx context.Context, query string) (QueryResult, error)
}

type RowQueryRunner interface {
	QueryTSV(ctx context.Context, query string) (TSVResult, error)
}

type EvidenceWriter interface {
	InsertEvidenceBatch(ctx context.Context, batch EvidenceBatch) (InsertResult, error)
	InsertRows(ctx context.Context, database, table string, rows []map[string]any) error
}

type SchemaApplier interface {
	ApplySchema(ctx context.Context, statements []string) (ApplyResult, error)
}

type Client interface {
	QueryRunner
	RowQueryRunner
	EvidenceWriter
	SchemaApplier
}

var _ Client = HTTPClient{}

type ApplyResult struct {
	Endpoint   string   `json:"endpoint"`
	Statements int      `json:"statements"`
	Applied    int      `json:"applied"`
	DryRun     bool     `json:"dryRun,omitempty"`
	Errors     []string `json:"errors,omitempty"`
}

func (c HTTPClient) ApplySchema(ctx context.Context, statements []string) (ApplyResult, error) {
	endpoint := strings.TrimSpace(c.Endpoint)
	if endpoint == "" {
		return ApplyResult{}, fmt.Errorf("clickhouse endpoint is required")
	}
	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	out := ApplyResult{Endpoint: endpoint, Statements: len(statements)}
	for _, statement := range statements {
		text := strings.TrimSpace(statement)
		if text == "" {
			continue
		}
		if !strings.HasSuffix(text, ";") {
			text += ";"
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewBufferString(text))
		if err != nil {
			return out, err
		}
		req.Header.Set("Content-Type", "text/plain; charset=utf-8")
		resp, err := client.Do(req)
		if err != nil {
			return out, err
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		closeErr := resp.Body.Close()
		if readErr != nil {
			return out, readErr
		}
		if closeErr != nil {
			return out, closeErr
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			message := strings.TrimSpace(string(body))
			if message == "" {
				message = resp.Status
			}
			err := fmt.Errorf("clickhouse schema statement failed: status=%s body=%s", resp.Status, message)
			out.Errors = append(out.Errors, err.Error())
			return out, err
		}
		out.Applied++
	}
	return out, nil
}
