package clickhouse

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type TSVResult struct {
	Columns []string
	Rows    [][]string
}

func (c HTTPClient) QueryTSV(ctx context.Context, query string) (TSVResult, error) {
	endpoint := strings.TrimSpace(c.Endpoint)
	if endpoint == "" {
		return TSVResult{}, fmt.Errorf("clickhouse endpoint is required")
	}
	text := strings.TrimSpace(query)
	if text == "" {
		return TSVResult{}, fmt.Errorf("query is required")
	}
	if !strings.Contains(strings.ToUpper(text), "FORMAT ") {
		text += " FORMAT TSVWithNames"
	}
	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewBufferString(text))
	if err != nil {
		return TSVResult{}, err
	}
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")
	resp, err := client.Do(req)
	if err != nil {
		return TSVResult{}, err
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 16*1024*1024))
	closeErr := resp.Body.Close()
	if readErr != nil {
		return TSVResult{}, readErr
	}
	if closeErr != nil {
		return TSVResult{}, closeErr
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := strings.TrimSpace(string(body))
		if message == "" {
			message = resp.Status
		}
		return TSVResult{}, fmt.Errorf("clickhouse query failed: status=%s body=%s", resp.Status, message)
	}
	return parseTSVWithNames(body)
}

func ParseTSVForClient(body []byte) (TSVResult, error) {
	return parseTSVWithNames(body)
}

func parseTSVWithNames(body []byte) (TSVResult, error) {
	text := strings.TrimRight(string(body), "\n")
	if text == "" {
		return TSVResult{Rows: [][]string{}}, nil
	}
	lines := strings.Split(text, "\n")
	columns := splitTSVLine(lines[0])
	for i := range columns {
		columns[i] = unescapeTSV(columns[i])
	}
	rows := make([][]string, 0, len(lines)-1)
	for _, line := range lines[1:] {
		if line == "" {
			continue
		}
		fields := splitTSVLine(line)
		if len(fields) != len(columns) {
			return TSVResult{}, fmt.Errorf("clickhouse TSV row has %d fields, expected %d", len(fields), len(columns))
		}
		for i := range fields {
			fields[i] = unescapeTSV(fields[i])
		}
		rows = append(rows, fields)
	}
	return TSVResult{Columns: columns, Rows: rows}, nil
}

func splitTSVLine(line string) []string {
	return strings.Split(line, "\t")
}

func unescapeTSV(value string) string {
	if !strings.Contains(value, "\\") {
		return value
	}
	var b strings.Builder
	b.Grow(len(value))
	for i := 0; i < len(value); i++ {
		if value[i] != '\\' || i+1 >= len(value) {
			b.WriteByte(value[i])
			continue
		}
		i++
		switch value[i] {
		case 'n':
			b.WriteByte('\n')
		case 't':
			b.WriteByte('\t')
		case 'r':
			b.WriteByte('\r')
		case 'b':
			b.WriteByte('\b')
		case 'f':
			b.WriteByte('\f')
		case '0':
			b.WriteByte(0)
		case 'N':
			// ClickHouse TSV null marker. Keep the empty string because current callers
			// distinguish nullability from context rather than from row values.
		default:
			b.WriteByte(value[i])
		}
	}
	return b.String()
}
