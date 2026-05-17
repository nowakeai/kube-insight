package api

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kube-insight/internal/ingest"
	"kube-insight/internal/storage"
	"kube-insight/internal/storage/clickhouse"
	"kube-insight/internal/storage/sqlite"
)

func TestServerReadOnlyAgentEndpoints(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "kube-insight.db")
	store, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	pipeline := ingest.DefaultPipeline(store)
	pipeline.ClusterID = "c1"
	fixture, err := os.ReadFile(filepath.Join("..", "..", "testdata", "fixtures", "kube", "core.json"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = pipeline.IngestJSON(context.Background(), fixture)
	closeErr := store.Close()
	if err != nil {
		t.Fatal(err)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}

	handler, err := NewServer(ServerOptions{DBPath: dbPath})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	assertGETContains(t, server.URL+"/healthz", `"ok": true`)
	assertGETContains(t, server.URL+"/api/v1/schema", `"name": "latest_index"`)
	assertGETContains(t, server.URL+"/api/v1/schema", `"relationships":`)
	assertGETContains(t, server.URL+"/api/v1/schema", `"recipes":`)
	assertPOSTContains(t, server.URL+"/api/v1/sql", `{"sql":"select name from latest_index where name = 'api-0'","maxRows":5}`, `"name": "api-0"`)
	assertPOSTContains(t, server.URL+"/api/v1/sql", `{"sql":"delete from latest_index"}`, `"error":`)
	assertGETContains(t, server.URL+"/api/v1/health?limit=5", `"summary":`)
	assertGETContains(t, server.URL+"/api/v1/history?kind=Pod&namespace=default&name=api-0&maxVersions=2&maxObservations=5", `"observations": 1`)
	assertGETContains(t, server.URL+"/api/v1/history?kind=Pod&namespace=default&name=api-0&maxVersions=2&maxObservations=5", `"versions": 1`)
	assertGETContains(t, server.URL+"/api/v1/search?q=BackOff&kind=Event&namespace=default&includeHealth=false", `"matches": 1`)
	assertGETContains(t, server.URL+"/api/v1/services/default/api/investigation?maxEvidenceObjects=5&maxVersionsPerObject=2", `"endpointSlices": 1`)
	assertGETContains(t, server.URL+"/api/v1/services/default/api/investigation?maxEvidenceObjects=5&maxVersionsPerObject=2", `"pods": 1`)
	assertGETContains(t, server.URL+"/api/v1/topology?kind=Pod&namespace=default&name=api-0", `"pod_on_node"`)
}

func assertGETContains(t *testing.T, url string, want string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body bytes.Buffer
	if _, err := body.ReadFrom(resp.Body); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body.String(), want) {
		t.Fatalf("GET %s missing %q: %s", url, want, body.String())
	}
}

func assertPOSTContains(t *testing.T, url, body, want string) {
	t.Helper()
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out bytes.Buffer
	if _, err := out.ReadFrom(resp.Body); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), want) {
		t.Fatalf("POST %s missing %q: %s", url, want, out.String())
	}
}

type fakeReadStore struct {
	closed *bool
}

func (f fakeReadStore) Close() error {
	*f.closed = true
	return nil
}

func (f fakeReadStore) QuerySchema(context.Context) (storage.SQLSchema, error) {
	return storage.SQLSchema{Tables: []storage.SQLSchemaTable{{Name: "injected_table"}}}, nil
}

func (f fakeReadStore) QuerySQL(context.Context, storage.SQLQueryOptions) (storage.SQLQueryResult, error) {
	return storage.SQLQueryResult{Columns: []string{"name"}, Rows: []map[string]any{{"name": "from-injected-store"}}, RowCount: 1}, nil
}

func TestServerUsesInjectedReadStore(t *testing.T) {
	closed := false
	handler, err := NewServer(ServerOptions{OpenStore: func(context.Context) (ReadStore, error) {
		return fakeReadStore{closed: &closed}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	assertGETContains(t, server.URL+"/api/v1/schema", "injected_table")
	assertPOSTContains(t, server.URL+"/api/v1/sql", `{"sql":"select 1"}`, "from-injected-store")
	if !closed {
		t.Fatalf("injected store was not closed")
	}
}

func TestServerClickHouseReadEndpoints(t *testing.T) {
	clickhouseHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		query := string(data)
		switch {
		case strings.Contains(query, "FROM `ki`.versions") && strings.Contains(query, "GROUP BY object_id") && strings.Contains(query, "kind = 'Service'"):
			writeClickHouseJSON(w, `{"data":[{"object_id":"c1/svc-uid","cluster_id":"c1","api_group":"","api_version":"v1","resource":"services","kind":"Service","namespace":"default","name":"api","uid":"svc-uid","latest_observed_at":"1970-01-01 00:00:20.000"}],"rows":1}`)
		case strings.Contains(query, "FROM `ki`.versions") && strings.Contains(query, "GROUP BY object_id") && strings.Contains(query, "kind = 'Pod'"):
			writeClickHouseJSON(w, `{"data":[{"object_id":"c1/pod-uid","cluster_id":"c1","api_group":"","api_version":"v1","resource":"pods","kind":"Pod","namespace":"default","name":"api-0","uid":"pod-uid","latest_observed_at":"1970-01-01 00:00:20.000"}],"rows":1}`)
		case strings.Contains(query, "SELECT observed_at, observation_type"):
			writeClickHouseJSON(w, `{"data":[{"observed_at":"1970-01-01 00:00:20.000","observation_type":"MODIFIED","resource_version":"20","doc_hash":"h2"}],"rows":1}`)
		case strings.Contains(query, "SELECT seq, observed_at"):
			writeClickHouseJSON(w, `{"data":[{"seq":1,"observed_at":"1970-01-01 00:00:20.000","resource_version":"20","doc_hash":"h1","materialization":"full","raw_size":14,"stored_size":14,"doc":"{\"kind\":\"Pod\"}"}],"rows":1}`)
		case strings.Contains(query, "FROM `ki`.`facts`") && strings.Contains(query, "positionCaseInsensitive"):
			writeClickHouseJSON(w, `{"data":[{"object_id":"c1/pod-uid","fact_key":"pod_status.phase","severity":10}],"rows":1}`)
		case strings.Contains(query, "FROM `ki`.`changes`") && strings.Contains(query, "positionCaseInsensitive"):
			writeClickHouseJSON(w, `{"data":[],"rows":0}`)
		case strings.Contains(query, "FROM `ki`.object_aliases"):
			writeClickHouseJSON(w, `{"data":[],"rows":0}`)
		case strings.Contains(query, "FROM `ki`.edges") && strings.Contains(query, "c1/svc-uid"):
			writeClickHouseJSON(w, `{"data":[{"edge_type":"endpointslice_for_service","src_id":"c1/eps-uid","dst_id":"c1/svc-uid","src_kind":"EndpointSlice","dst_kind":"Service","valid_from":"1970-01-01 00:00:20.000","valid_to":"2100-01-01 00:00:00.000"}],"rows":1}`)
		case strings.Contains(query, "FROM `ki`.edges") && strings.Contains(query, "c1/eps-uid"):
			writeClickHouseJSON(w, `{"data":[{"edge_type":"endpointslice_for_service","src_id":"c1/eps-uid","dst_id":"c1/svc-uid","src_kind":"EndpointSlice","dst_kind":"Service","valid_from":"1970-01-01 00:00:20.000","valid_to":"2100-01-01 00:00:00.000"},{"edge_type":"endpointslice_targets_pod","src_id":"c1/eps-uid","dst_id":"c1/pod-uid","src_kind":"EndpointSlice","dst_kind":"Pod","valid_from":"1970-01-01 00:00:20.000","valid_to":"2100-01-01 00:00:00.000"}],"rows":2}`)
		case strings.Contains(query, "FROM `ki`.edges") && strings.Contains(query, "c1/pod-uid"):
			writeClickHouseJSON(w, `{"data":[{"edge_type":"endpointslice_targets_pod","src_id":"c1/eps-uid","dst_id":"c1/pod-uid","src_kind":"EndpointSlice","dst_kind":"Pod","valid_from":"1970-01-01 00:00:20.000","valid_to":"2100-01-01 00:00:00.000"},{"edge_type":"pod_on_node","src_id":"c1/pod-uid","dst_id":"c1/node-uid","src_kind":"Pod","dst_kind":"Node","valid_from":"1970-01-01 00:00:20.000","valid_to":"2100-01-01 00:00:00.000"}],"rows":2}`)
		case strings.Contains(query, "FROM `ki`.edges") && strings.Contains(query, "c1/node-uid"):
			writeClickHouseJSON(w, `{"data":[],"rows":0}`)
		case strings.Contains(query, "FROM `ki`.versions") && strings.Contains(query, "object_id IN"):
			writeClickHouseJSON(w, `{"data":[{"object_id":"c1/svc-uid","cluster_id":"c1","api_group":"","api_version":"v1","resource":"services","kind":"Service","namespace":"default","name":"api","uid":"svc-uid","latest_observed_at":"1970-01-01 00:00:20.000"},{"object_id":"c1/eps-uid","cluster_id":"c1","api_group":"discovery.k8s.io","api_version":"v1","resource":"endpointslices","kind":"EndpointSlice","namespace":"default","name":"api-abc","uid":"eps-uid","latest_observed_at":"1970-01-01 00:00:20.000"},{"object_id":"c1/pod-uid","cluster_id":"c1","api_group":"","api_version":"v1","resource":"pods","kind":"Pod","namespace":"default","name":"api-0","uid":"pod-uid","latest_observed_at":"1970-01-01 00:00:20.000"},{"object_id":"c1/node-uid","cluster_id":"c1","api_group":"","api_version":"v1","resource":"nodes","kind":"Node","namespace":"","name":"node-a","uid":"node-uid","latest_observed_at":"1970-01-01 00:00:20.000"}],"rows":4}`)
		case strings.Contains(query, "SELECT doc FROM `ki`.versions"):
			writeClickHouseJSON(w, `{"data":[{"doc":"{\"kind\":\"Pod\"}"}],"rows":1}`)
		case strings.Contains(query, "SELECT ts, object_id, fact_key"):
			writeClickHouseJSON(w, `{"data":[{"ts":"1970-01-01 00:00:20.000","object_id":"c1/pod-uid","fact_key":"pod_status.phase","fact_value":"Running","numeric_value":null,"severity":10,"detail":"{}"}],"rows":1}`)
		case strings.Contains(query, "SELECT ts, object_id, change_family"):
			writeClickHouseJSON(w, `{"data":[],"rows":0}`)
		default:
			t.Fatalf("unexpected ClickHouse query: %s", query)
		}
	}))
	defer clickhouseHTTP.Close()

	handler, err := NewServer(ServerOptions{OpenStore: func(context.Context) (ReadStore, error) {
		return &clickhouse.Store{Client: clickhouse.HTTPClient{Endpoint: clickhouseHTTP.URL, HTTPClient: clickhouseHTTP.Client()}, Database: "ki"}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	assertGETContains(t, server.URL+"/api/v1/history?cluster=c1&kind=Pod&namespace=default&name=api-0&maxVersions=1&maxObservations=1", `"versions": 1`)
	assertGETContains(t, server.URL+"/api/v1/search?cluster=c1&q=Running&kind=Pod&namespace=default&includeHealth=false", `"matches": 1`)
	assertGETContains(t, server.URL+"/api/v1/topology?cluster=c1&kind=Pod&namespace=default&name=api-0", `"pod_on_node"`)
	assertGETContains(t, server.URL+"/api/v1/services/default/api/investigation?cluster=c1&maxVersionsPerObject=1", `"endpointSlices": 1`)
	assertGETContains(t, server.URL+"/api/v1/services/default/api/investigation?cluster=c1&maxVersionsPerObject=1", `"pods": 1`)
}

func TestParseServiceInvestigationRequestAcceptsLimitAlias(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/services/default/api/investigation?limit=7&maxVersionsPerObject=2", nil)
	req.SetPathValue("namespace", "default")
	req.SetPathValue("name", "api")
	target, opts, err := parseServiceInvestigationRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	if target.Kind != "Service" || target.Namespace != "default" || target.Name != "api" {
		t.Fatalf("target = %#v", target)
	}
	if opts.MaxEvidenceObjects != 7 || opts.MaxVersionsPerObject != 2 {
		t.Fatalf("opts = %#v", opts)
	}
}

func writeClickHouseJSON(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(body))
}
