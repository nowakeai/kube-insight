package clickhouse

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPClientApplySchema(t *testing.T) {
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s", r.Method)
		}
		data := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(data)
		requests = append(requests, string(data))
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	result, err := (HTTPClient{Endpoint: server.URL, HTTPClient: server.Client()}).ApplySchema(context.Background(), []string{
		"CREATE DATABASE IF NOT EXISTS `ki`",
		"CREATE TABLE IF NOT EXISTS `ki`.facts (cluster_id String) ENGINE = MergeTree ORDER BY cluster_id",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Applied != 2 || result.Statements != 2 {
		t.Fatalf("result = %#v", result)
	}
	if len(requests) != 2 || !strings.HasSuffix(strings.TrimSpace(requests[0]), ";") {
		t.Fatalf("requests = %#v", requests)
	}
}

func TestHTTPClientApplySchemaReturnsServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "syntax error", http.StatusBadRequest)
	}))
	defer server.Close()

	result, err := (HTTPClient{Endpoint: server.URL, HTTPClient: server.Client()}).ApplySchema(context.Background(), []string{"bad"})
	if err == nil || !strings.Contains(err.Error(), "syntax error") {
		t.Fatalf("err = %v", err)
	}
	if len(result.Errors) != 1 || result.Applied != 0 {
		t.Fatalf("result = %#v", result)
	}
}
