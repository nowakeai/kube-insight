package clickhouse

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"kube-insight/internal/core"
	"kube-insight/internal/extractor"
	"kube-insight/internal/filter"
	"kube-insight/internal/kubeapi"
	"kube-insight/internal/storage"
)

func writeTSV(w http.ResponseWriter, header string, rows ...string) {
	_, _ = w.Write([]byte(header + "\n"))
	for _, row := range rows {
		_, _ = w.Write([]byte(row + "\n"))
	}
}

func writeEmptyTSV(w http.ResponseWriter, header string) {
	writeTSV(w, header)
}

func TestNewStoreDefaultsAndValidatesOptions(t *testing.T) {
	store, err := NewStore(HTTPClient{Endpoint: "http://clickhouse.example"}, Options{FlushIntervalMS: 250})
	if err != nil {
		t.Fatal(err)
	}
	if store.Database != defaultDatabase {
		t.Fatalf("database = %q", store.Database)
	}
	if store.FlushInterval != 250*time.Millisecond {
		t.Fatalf("flush interval = %s", store.FlushInterval)
	}
}

func TestNewHTTPStoreRequiresEndpoint(t *testing.T) {
	if _, err := NewHTTPStore("  ", Options{}); err == nil {
		t.Fatal("expected empty endpoint error")
	}
}

func TestStorePutObservationWritesEvidenceBatch(t *testing.T) {
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		requests = append(requests, string(data))
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	obs := core.Observation{
		Type:       core.ObservationModified,
		ObservedAt: time.Unix(10, 0),
		Ref:        core.ResourceRef{ClusterID: "c1", Version: "v1", Resource: "pods", Kind: "Pod", Namespace: "default", Name: "api-0", UID: "pod-uid"},
		Object:     map[string]any{"kind": "Pod"},
	}
	store := &Store{Client: HTTPClient{Endpoint: server.URL, HTTPClient: server.Client()}, Database: "ki"}
	err := store.PutObservation(context.Background(), obs, extractor.Evidence{
		Facts: []core.Fact{{Time: obs.ObservedAt, ObjectID: "c1/pod-uid", Key: "pod.phase", Value: "Running"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(requests, "\n")
	for _, want := range []string{"`ki`.`observations`", "`ki`.`object_aliases`", "`ki`.`versions`", "`ki`.`facts"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("requests missing %s:\n%s", want, joined)
		}
	}
}

func TestStoreBuffersUntilBatchSizeAndFlushes(t *testing.T) {
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		requests = append(requests, string(data))
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	store := &Store{Client: HTTPClient{Endpoint: server.URL, HTTPClient: server.Client()}, Database: "ki", BatchSize: 2}
	first := core.Observation{
		Type:       core.ObservationModified,
		ObservedAt: time.Unix(10, 0),
		Ref:        core.ResourceRef{ClusterID: "c1", Version: "v1", Resource: "pods", Kind: "Pod", Namespace: "default", Name: "api-0", UID: "pod-0"},
		Object:     map[string]any{"kind": "Pod"},
	}
	second := first
	second.Ref.Name = "api-1"
	second.Ref.UID = "pod-1"

	if err := store.PutObservation(context.Background(), first, extractor.Evidence{}); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 0 {
		t.Fatalf("requests before batch threshold = %d", len(requests))
	}
	if err := store.PutObservation(context.Background(), second, extractor.Evidence{}); err != nil {
		t.Fatal(err)
	}
	if len(requests) == 0 {
		t.Fatalf("expected automatic flush at batch threshold")
	}

	requests = nil
	if err := store.PutObservation(context.Background(), first, extractor.Evidence{}); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 0 {
		t.Fatalf("requests before explicit flush = %d", len(requests))
	}
	if err := store.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(requests) == 0 {
		t.Fatalf("expected explicit flush to write pending observation")
	}
}

func TestStoreOptionalControlPlaneInterfacesAreNotAdvertised(t *testing.T) {
	store := &Store{}
	if _, ok := any(store).(storage.RawLatestStore); ok {
		t.Fatal("clickhouse store should not advertise raw latest support")
	}
	if _, ok := any(store).(storage.ClusterStore); ok {
		t.Fatal("clickhouse store should not advertise cluster metadata upsert support")
	}
}

func TestStoreGetFactsAndEdgesQueryClickHouse(t *testing.T) {
	var queries []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		query := string(data)
		queries = append(queries, query)
		if strings.Contains(query, "FORMAT TSVWithNames") {
			switch {
			case strings.Contains(query, "SELECT object_id, doc"):
				writeTSV(w, "object_id	doc", `c1/pod-uid	{"kind":"Pod"}`)
			case strings.Contains(query, "SELECT ts, object_id, fact_key"):
				writeTSV(w, "ts	object_id	fact_key	fact_value	numeric_value	severity	detail", `1970-01-01 00:00:20.000	c1/pod-uid	pod_status.phase	Running		10	{}`)
			case strings.Contains(query, "SELECT ts, object_id, change_family"):
				writeEmptyTSV(w, "ts	object_id	change_family	path	op	old_scalar	new_scalar	severity")
			case strings.Contains(query, "SELECT object_id, alias_id"):
				writeEmptyTSV(w, "object_id	alias_id")
			case strings.Contains(query, "SELECT edge_type, src_id, dst_id, valid_from"):
				writeEmptyTSV(w, "edge_type	src_id	dst_id	valid_from	valid_to	detail")
			case strings.Contains(query, "SELECT object_id, seq"):
				writeTSV(w, "object_id	seq	observed_at	resource_version	doc_hash	materialization	raw_size	stored_size", `c1/pod-uid	1	1970-01-01 00:00:20.000	20	h1	full	14	14`)
			default:
				t.Fatalf("unexpected TSV query: %s", query)
			}
			return
		}
		switch {
		case strings.HasPrefix(strings.TrimSpace(query), "SELECT alias_id, argMax"):
			_, _ = w.Write([]byte(`{"data":[{"alias_id":"c1/pods/default/api-0","object_id":"c1/pod-uid"}],"rows":1}`))
		case strings.HasPrefix(strings.TrimSpace(query), "SELECT alias_id"), strings.HasPrefix(strings.TrimSpace(query), "SELECT object_id, alias_id"):
			_, _ = w.Write([]byte(`{"data":[{"alias_id":"c1/pods/default/api-0"}],"rows":1}`))
		case strings.Contains(query, "FROM `ki`.facts"):
			if !strings.Contains(query, "FROM `ki`.object_aliases") {
				t.Fatalf("facts query does not resolve aliases: %s", query)
			}
			_, _ = w.Write([]byte(`{"data":[{"ts":"1970-01-01 00:00:20.000","object_id":"c1/pod-uid","fact_key":"pod.phase","fact_value":"Running","numeric_value":null,"severity":10,"detail":"{\"source\":\"test\"}"}],"rows":1}`))
		case strings.Contains(query, "FROM `ki`.edges"):
			if !strings.Contains(query, "src_id IN") || !strings.Contains(query, "c1/pod-uid") {
				t.Fatalf("edges query does not resolve aliases: %s", query)
			}
			_, _ = w.Write([]byte(`{"data":[{"edge_type":"pod_on_node","src_id":"c1/pod-uid","dst_id":"c1/node-uid","valid_from":"1970-01-01 00:00:20.000","valid_to":"","detail":"{\"node\":\"node-a\"}"}],"rows":1}`))
		default:
			t.Fatalf("unexpected query: %s", query)
		}
	}))
	defer server.Close()

	store := &Store{Client: HTTPClient{Endpoint: server.URL, HTTPClient: server.Client()}, Database: "ki"}
	facts, err := store.GetFacts(context.Background(), "c1/pods/default/api-0")
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 1 || facts[0].ObjectID != "c1/pod-uid" || facts[0].Key != "pod.phase" || facts[0].Detail["source"] != "test" {
		t.Fatalf("facts = %#v", facts)
	}
	edges, err := store.GetEdges(context.Background(), "c1/pods/default/api-0")
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 1 || edges[0].SourceID != "c1/pod-uid" || edges[0].TargetID != "c1/node-uid" || edges[0].ValidTo != nil || edges[0].Detail["node"] != "node-a" {
		t.Fatalf("edges = %#v", edges)
	}
	if len(queries) != 4 {
		t.Fatalf("queries = %#v", queries)
	}
}

func TestStorePutFilterDecisionsPersistsAuditableRows(t *testing.T) {
	var request string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		request = string(data)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	store := &Store{Client: HTTPClient{Endpoint: server.URL, HTTPClient: server.Client()}, Database: "ki"}
	obs := core.Observation{
		Type:            core.ObservationModified,
		ObservedAt:      time.Unix(20, 0),
		ResourceVersion: "42",
		Ref:             core.ResourceRef{ClusterID: "c1", Version: "v1", Resource: "secrets", Kind: "Secret", Namespace: "default", Name: "api-secret", UID: "secret-uid"},
	}
	err := store.PutFilterDecisions(context.Background(), obs, []filter.Decision{
		{Outcome: filter.Keep, Reason: "noop", Meta: map[string]any{"filter": "noop_filter"}},
		{Outcome: filter.KeepModified, Reason: "secret_payload_removed", Meta: map[string]any{"filter": "secret_metadata_only", "secretPayloadRemoved": true}},
		{Outcome: filter.DiscardResource, Reason: "lease_skipped", Meta: map[string]any{"filter": "lease_skip"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"INSERT INTO `ki`.`filter_decisions`", `"filter_name":"secret_metadata_only"`, `"filter_name":"lease_skip"`, `"destructive":true`} {
		if !strings.Contains(request, want) {
			t.Fatalf("request missing %s:\n%s", want, request)
		}
	}
	if strings.Contains(request, "noop_filter") {
		t.Fatalf("non-auditable keep decision should not be persisted:\n%s", request)
	}
}

func TestStoreUpsertIngestionOffsetWritesStateRow(t *testing.T) {
	var request string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		request = string(data)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	store := &Store{Client: HTTPClient{Endpoint: server.URL, HTTPClient: server.Client()}, Database: "ki"}
	err := store.UpsertIngestionOffset(context.Background(), storage.IngestionOffset{
		ClusterID:       "c1",
		Resource:        kubeapi.ResourceInfo{Group: "apps", Version: "v1", Resource: "deployments", Kind: "Deployment", Namespaced: true},
		Namespace:       "default",
		ResourceVersion: "42",
		Event:           storage.OffsetEventWatch,
		Status:          "watching",
		At:              time.Unix(20, 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"INSERT INTO `ki`.`ingestion_offsets`", `"resource":"deployments"`, `"last_watch_at":"1970-01-01 00:00:20.000"`, `"status":"watching"`} {
		if !strings.Contains(request, want) {
			t.Fatalf("request missing %s:\n%s", want, request)
		}
	}
}

func TestStoreBuffersIngestionOffsetsUntilBatchSize(t *testing.T) {
	var request string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		request = string(data)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	store := &Store{Client: HTTPClient{Endpoint: server.URL, HTTPClient: server.Client()}, Database: "ki", BatchSize: 2}
	offset := storage.IngestionOffset{
		ClusterID:       "c1",
		Resource:        kubeapi.ResourceInfo{Version: "v1", Resource: "pods", Kind: "Pod", Namespaced: true},
		Namespace:       "default",
		ResourceVersion: "41",
		Event:           storage.OffsetEventWatch,
		Status:          "watching",
		At:              time.Unix(20, 0),
	}
	if err := store.UpsertIngestionOffset(context.Background(), offset); err != nil {
		t.Fatal(err)
	}
	if request != "" {
		t.Fatalf("request before batch threshold = %q", request)
	}
	offset.Resource.Resource = "services"
	offset.Resource.Kind = "Service"
	offset.ResourceVersion = "42"
	offset.At = time.Unix(21, 0)
	if err := store.UpsertIngestionOffset(context.Background(), offset); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(request, "INSERT INTO `ki`.`ingestion_offsets`") {
		t.Fatalf("expected ingestion offset insert, got:\n%s", request)
	}
	if got := strings.Count(request, "\n"); got != 3 {
		t.Fatalf("expected query line and two JSONEachRow rows, got newline count %d in:\n%s", got, request)
	}
}

func TestStoreCoalescesPendingIngestionOffsetsByResourceAndEvent(t *testing.T) {
	var request string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		request = string(data)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	store := &Store{Client: HTTPClient{Endpoint: server.URL, HTTPClient: server.Client()}, Database: "ki", BatchSize: 1000}
	offset := storage.IngestionOffset{
		ClusterID:       "c1",
		Resource:        kubeapi.ResourceInfo{Version: "v1", Resource: "pods", Kind: "Pod", Namespaced: true},
		ResourceVersion: "41",
		Event:           storage.OffsetEventBookmark,
		Status:          "bookmark",
		At:              time.Unix(20, 0),
	}
	if err := store.UpsertIngestionOffset(context.Background(), offset); err != nil {
		t.Fatal(err)
	}
	offset.ResourceVersion = "42"
	offset.At = time.Unix(21, 0)
	if err := store.UpsertIngestionOffset(context.Background(), offset); err != nil {
		t.Fatal(err)
	}
	if err := store.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(request, `"resource_version":"41"`) || !strings.Contains(request, `"resource_version":"42"`) {
		t.Fatalf("expected pending offset to keep only latest resource version, got:\n%s", request)
	}
	if got := strings.Count(request, "\n"); got != 2 {
		t.Fatalf("expected query line and one JSONEachRow row, got newline count %d in:\n%s", got, request)
	}
}

func TestStoreFlushWritesPendingIngestionOffsets(t *testing.T) {
	var request string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		request = string(data)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	store := &Store{Client: HTTPClient{Endpoint: server.URL, HTTPClient: server.Client()}, Database: "ki", BatchSize: 1000}
	if err := store.UpsertIngestionOffset(context.Background(), storage.IngestionOffset{
		ClusterID:       "c1",
		Resource:        kubeapi.ResourceInfo{Version: "v1", Resource: "pods", Kind: "Pod", Namespaced: true},
		ResourceVersion: "42",
		Event:           storage.OffsetEventBookmark,
		Status:          "watching",
		At:              time.Unix(22, 0),
	}); err != nil {
		t.Fatal(err)
	}
	if request != "" {
		t.Fatalf("request before explicit flush = %q", request)
	}
	if err := store.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"INSERT INTO `ki`.`ingestion_offsets`", `"resource":"pods"`, `"last_bookmark_at":"1970-01-01 00:00:22.000"`} {
		if !strings.Contains(request, want) {
			t.Fatalf("request missing %s:\n%s", want, request)
		}
	}
}

func TestStoreLatestResourceRefsFlushesPendingAndQueriesLiveRefs(t *testing.T) {
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		request := string(data)
		requests = append(requests, request)
		if strings.Contains(request, "FROM `ki`.observations") {
			if !strings.Contains(request, "HAVING latest_type != 'DELETED'") {
				t.Fatalf("latest refs query does not filter deletes: %s", request)
			}
			_, _ = w.Write([]byte(`{"data":[{"api_group":"","api_version":"v1","resource":"pods","kind":"Pod","namespace":"default","name":"api-0","uid":"pod-uid"}],"rows":1}`))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	store := &Store{Client: HTTPClient{Endpoint: server.URL, HTTPClient: server.Client()}, Database: "ki", BatchSize: 1000}
	obs := core.Observation{
		Type:       core.ObservationModified,
		ObservedAt: time.Unix(10, 0),
		Ref:        core.ResourceRef{ClusterID: "c1", Version: "v1", Resource: "pods", Kind: "Pod", Namespace: "default", Name: "api-0", UID: "pod-uid"},
		Object:     map[string]any{"kind": "Pod"},
	}
	if err := store.PutObservation(context.Background(), obs, extractor.Evidence{}); err != nil {
		t.Fatal(err)
	}
	refs, err := store.LatestResourceRefs(context.Background(), "c1", kubeapi.ResourceInfo{Version: "v1", Resource: "pods"}, "default")
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || refs[0].Name != "api-0" || refs[0].UID != "pod-uid" {
		t.Fatalf("refs = %#v", refs)
	}
	joined := strings.Join(requests, "\n")
	if !strings.Contains(joined, "INSERT INTO `ki`.`observations`") || !strings.Contains(joined, "FROM `ki`.observations") {
		t.Fatalf("expected flush insert and latest query, got:\n%s", joined)
	}
}

func TestStoreResourceHealthFlushesPendingIngestionOffsets(t *testing.T) {
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		query := string(data)
		requests = append(requests, query)
		switch {
		case strings.Contains(query, "INSERT INTO `ki`.`ingestion_offsets`"):
			w.WriteHeader(http.StatusOK)
		case strings.Contains(query, "FROM `ki`.api_resources"):
			_, _ = w.Write([]byte(`{"data":[],"rows":0}`))
		case strings.Contains(query, "FROM `ki`.observations"):
			_, _ = w.Write([]byte(`{"data":[],"rows":0}`))
		case strings.Contains(query, "FROM `ki`.ingestion_offsets"):
			_, _ = w.Write([]byte(`{"data":[],"rows":0}`))
		default:
			t.Fatalf("unexpected query: %s", query)
		}
	}))
	defer server.Close()

	store := &Store{Client: HTTPClient{Endpoint: server.URL, HTTPClient: server.Client()}, Database: "ki", BatchSize: 1000}
	if err := store.UpsertIngestionOffset(context.Background(), storage.IngestionOffset{
		ClusterID:       "c1",
		Resource:        kubeapi.ResourceInfo{Version: "v1", Resource: "pods", Kind: "Pod", Namespaced: true},
		ResourceVersion: "42",
		Event:           storage.OffsetEventWatch,
		Status:          "watching",
		At:              time.Unix(30, 0),
	}); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 0 {
		t.Fatalf("request before health read = %#v", requests)
	}
	if _, err := store.ResourceHealth(context.Background(), storage.ResourceHealthOptions{ClusterID: "c1"}); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 4 || !strings.Contains(requests[0], "INSERT INTO `ki`.`ingestion_offsets`") {
		t.Fatalf("expected health read to flush pending offset first, requests = %#v", requests)
	}
}

func TestStoreResourceHealthAggregatesOffsetsAndLatestCounts(t *testing.T) {
	var queries []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		query := string(data)
		queries = append(queries, query)
		switch {
		case strings.Contains(query, "FROM `ki`.api_resources"):
			_, _ = w.Write([]byte(`{"data":[{"api_group":"","api_version":"v1","resource":"pods","kind":"Pod","namespaced":true,"verbs":"[\"list\",\"watch\"]"}],"rows":1}`))
		case strings.Contains(query, "FROM `ki`.observations"):
			_, _ = w.Write([]byte(`{"data":[{"cluster_id":"c1","api_group":"","api_version":"v1","resource":"pods","namespace":"default","latest_objects":2}],"rows":1}`))
		case strings.Contains(query, "FROM `ki`.ingestion_offsets"):
			_, _ = w.Write([]byte(`{"data":[{"cluster_id":"c1","api_group":"","api_version":"v1","resource":"pods","kind":"Pod","namespaced":true,"namespace":"default","status":"watch_error","error":"boom","resource_version":"42","last_list_ms":20000,"last_watch_ms":30000,"last_bookmark_ms":0,"updated_ms":30000}],"rows":1}`))
		default:
			t.Fatalf("unexpected query: %s", query)
		}
	}))
	defer server.Close()

	store := &Store{Client: HTTPClient{Endpoint: server.URL, HTTPClient: server.Client()}, Database: "ki"}
	report, err := store.ResourceHealth(context.Background(), storage.ResourceHealthOptions{ClusterID: "c1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(queries) != 3 || !strings.Contains(queries[0], "FROM `ki`.api_resources") || !strings.Contains(queries[1], "FROM `ki`.observations") || !strings.Contains(queries[2], "FROM `ki`.ingestion_offsets") {
		t.Fatalf("queries = %#v", queries)
	}
	if report.Summary.Resources != 1 || report.Summary.Errors != 1 || report.Summary.Complete {
		t.Fatalf("summary = %#v", report.Summary)
	}
	if len(report.Resources) != 1 {
		t.Fatalf("resources = %#v", report.Resources)
	}
	record := report.Resources[0]
	if record.Status != "watch_error" || record.Error != "boom" || record.LatestObjects != 2 || record.LastWatchAt == nil || record.UpdatedAt == nil {
		t.Fatalf("record = %#v", record)
	}
}

func TestStoreAPIResourcesRoundTripQueriesDiscoveryTable(t *testing.T) {
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		request := string(data)
		requests = append(requests, request)
		if strings.Contains(request, "FROM `ki`.api_resources") {
			_, _ = w.Write([]byte(`{"data":[{"api_group":"apps","api_version":"v1","resource":"deployments","kind":"Deployment","namespaced":true,"verbs":"[\"list\",\"watch\"]"}],"rows":1}`))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	store := &Store{Client: HTTPClient{Endpoint: server.URL, HTTPClient: server.Client()}, Database: "ki"}
	if err := store.UpsertAPIResources(context.Background(), []kubeapi.ResourceInfo{{Group: "apps", Version: "v1", Resource: "deployments", Kind: "Deployment", Namespaced: true, Verbs: []string{"list", "watch"}}}, time.Unix(40, 0)); err != nil {
		t.Fatal(err)
	}
	resources, err := store.APIResources(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(resources) != 1 || resources[0].Resource != "deployments" || len(resources[0].Verbs) != 2 {
		t.Fatalf("resources = %#v", resources)
	}
	joined := strings.Join(requests, "\n")
	for _, want := range []string{"INSERT INTO `ki`.`api_resources`", `"resource":"deployments"`, "FROM `ki`.api_resources"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("requests missing %s:\n%s", want, joined)
		}
	}
}

func TestStoreResourceHealthReportsDiscoveredNotStarted(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		query := string(data)
		if strings.Contains(query, "FORMAT TSVWithNames") {
			switch {
			case strings.Contains(query, "SELECT edge_type, src_id, dst_id"):
				writeTSV(w, "edge_type	src_id	dst_id	src_kind	dst_kind	edge_valid_from	edge_valid_to", `pod_on_node	c1/pod-uid	c1/node-uid	Pod	Node	1970-01-01 00:00:20.000	2100-01-01 00:00:00.000`)
			default:
				t.Fatalf("unexpected TSV query: %s", query)
			}
			return
		}
		switch {
		case strings.Contains(query, "FROM `ki`.api_resources"):
			_, _ = w.Write([]byte(`{"data":[{"api_group":"apps","api_version":"v1","resource":"deployments","kind":"Deployment","namespaced":true,"verbs":"[\"list\",\"watch\"]"}],"rows":1}`))
		case strings.Contains(query, "FROM `ki`.observations"):
			_, _ = w.Write([]byte(`{"data":[],"rows":0}`))
		case strings.Contains(query, "FROM `ki`.ingestion_offsets"):
			_, _ = w.Write([]byte(`{"data":[],"rows":0}`))
		default:
			t.Fatalf("unexpected query: %s", query)
		}
	}))
	defer server.Close()

	store := &Store{Client: HTTPClient{Endpoint: server.URL, HTTPClient: server.Client()}, Database: "ki"}
	report, err := store.ResourceHealth(context.Background(), storage.ResourceHealthOptions{ClusterID: "c1"})
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.NotStarted != 1 || report.Summary.Complete {
		t.Fatalf("summary = %#v", report.Summary)
	}
	if len(report.Resources) != 1 || report.Resources[0].Status != "not_started" || report.Resources[0].Resource != "deployments" {
		t.Fatalf("resources = %#v", report.Resources)
	}
}

func TestStoreObjectHistoryQueriesVersionsAndObservations(t *testing.T) {
	var queries []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		query := string(data)
		queries = append(queries, query)
		switch {
		case strings.Contains(query, "FROM `ki`.versions") && strings.Contains(query, "GROUP BY object_id"):
			_, _ = w.Write([]byte(`{"data":[{"object_id":"c1/pod-uid","cluster_id":"c1","api_group":"","api_version":"v1","resource":"pods","kind":"Pod","namespace":"default","name":"api-0","uid":"pod-uid","latest_observed_at":"1970-01-01 00:00:20.000"}],"rows":1}`))
		case strings.Contains(query, "FROM `ki`.versions") && strings.Contains(query, "SELECT seq"):
			_, _ = w.Write([]byte(`{"data":[{"seq":2,"observed_at":"1970-01-01 00:00:20.000","resource_version":"20","doc_hash":"h2","materialization":"full","raw_size":14,"stored_size":14,"doc":"{\"kind\":\"Pod\"}"}],"rows":1}`))
		case strings.Contains(query, "FROM `ki`.observations"):
			_, _ = w.Write([]byte(`{"data":[{"observed_at":"1970-01-01 00:00:20.000","observation_type":"MODIFIED","resource_version":"20","doc_hash":"h2"}],"rows":1}`))
		default:
			t.Fatalf("unexpected query: %s", query)
		}
	}))
	defer server.Close()

	store := &Store{Client: HTTPClient{Endpoint: server.URL, HTTPClient: server.Client()}, Database: "ki"}
	history, err := store.ObjectHistory(context.Background(), storage.ObjectTarget{ClusterID: "c1", Kind: "Pod", Namespace: "default", Name: "api-0"}, storage.ObjectHistoryOptions{IncludeDocs: true, IncludeDiffs: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(queries) != 3 {
		t.Fatalf("queries = %#v", queries)
	}
	if history.Object.LogicalID != "c1/pod-uid" || history.Summary.Versions != 1 || history.Summary.ReturnedObservations != 1 {
		t.Fatalf("history = %#v", history)
	}
	if history.Versions[0].Document["kind"] != "Pod" || history.Observations[0].ResourceVersion != "20" {
		t.Fatalf("history detail = %#v", history)
	}
}

func TestStoreSearchEvidenceFindsFactsAndBundles(t *testing.T) {
	var queries []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		query := string(data)
		queries = append(queries, query)
		if strings.Contains(query, "FORMAT TSVWithNames") {
			switch {
			case strings.Contains(query, "SELECT object_id, doc"):
				writeTSV(w, "object_id	doc", `c1/pod-uid	{"kind":"Pod"}`)
			case strings.Contains(query, "SELECT ts, object_id, fact_key"):
				writeTSV(w, "ts	object_id	fact_key	fact_value	numeric_value	severity	detail", `1970-01-01 00:00:20.000	c1/pod-uid	pod_status.phase	Running		10	{}`)
			case strings.Contains(query, "SELECT ts, object_id, change_family"):
				writeEmptyTSV(w, "ts	object_id	change_family	path	op	old_scalar	new_scalar	severity")
			case strings.Contains(query, "SELECT object_id, alias_id"):
				writeEmptyTSV(w, "object_id	alias_id")
			case strings.Contains(query, "SELECT edge_type, src_id, dst_id, valid_from"):
				writeEmptyTSV(w, "edge_type	src_id	dst_id	valid_from	valid_to	detail")
			case strings.Contains(query, "SELECT object_id, seq"):
				writeTSV(w, "object_id	seq	observed_at	resource_version	doc_hash	materialization	raw_size	stored_size", `c1/pod-uid	1	1970-01-01 00:00:20.000	20	h1	full	14	14`)
			default:
				t.Fatalf("unexpected TSV query: %s", query)
			}
			return
		}
		switch {
		case strings.HasPrefix(strings.TrimSpace(query), "SELECT alias_id"), strings.HasPrefix(strings.TrimSpace(query), "SELECT object_id, alias_id"):
			_, _ = w.Write([]byte(`{"data":[],"rows":0}`))
		case strings.Contains(query, "FROM `ki`.`facts`") && strings.Contains(query, "positionCaseInsensitive"):
			_, _ = w.Write([]byte(`{"data":[{"object_id":"c1/pod-uid","fact_key":"pod_status.phase","severity":10}],"rows":1}`))
		case strings.Contains(query, "FROM `ki`.`changes`") && strings.Contains(query, "positionCaseInsensitive"):
			_, _ = w.Write([]byte(`{"data":[],"rows":0}`))
		case strings.Contains(query, "FROM `ki`.edges") && strings.Contains(query, "c1/node-uid"):
			_, _ = w.Write([]byte(`{"data":[],"rows":0}`))
		case strings.Contains(query, "FROM `ki`.versions") && strings.Contains(query, "object_id IN"):
			_, _ = w.Write([]byte(`{"data":[{"object_id":"c1/pod-uid","cluster_id":"c1","api_group":"","api_version":"v1","resource":"pods","kind":"Pod","namespace":"default","name":"api-0","uid":"pod-uid","latest_observed_at":"1970-01-01 00:00:20.000"}],"rows":1}`))
		case strings.Contains(query, "SELECT doc FROM `ki`.versions"):
			_, _ = w.Write([]byte(`{"data":[{"doc":"{\"kind\":\"Pod\"}"}],"rows":1}`))
		case strings.Contains(query, "SELECT ts, object_id, fact_key"):
			_, _ = w.Write([]byte(`{"data":[{"ts":"1970-01-01 00:00:20.000","object_id":"c1/pod-uid","fact_key":"pod_status.phase","fact_value":"Running","numeric_value":null,"severity":10,"detail":"{}"}],"rows":1}`))
		case strings.Contains(query, "SELECT ts, object_id, change_family"):
			_, _ = w.Write([]byte(`{"data":[],"rows":0}`))
		case strings.Contains(query, "FROM `ki`.edges"):
			_, _ = w.Write([]byte(`{"data":[],"rows":0}`))
		case strings.Contains(query, "SELECT seq, observed_at"):
			_, _ = w.Write([]byte(`{"data":[{"seq":1,"observed_at":"1970-01-01 00:00:20.000","resource_version":"20","doc_hash":"h1","materialization":"full","raw_size":14,"stored_size":14,"doc":"{}"}],"rows":1}`))
		default:
			t.Fatalf("unexpected query: %s", query)
		}
	}))
	defer server.Close()

	store := &Store{Client: HTTPClient{Endpoint: server.URL, HTTPClient: server.Client()}, Database: "ki"}
	result, err := store.SearchEvidence(context.Background(), storage.EvidenceSearchOptions{Query: "Running", ClusterID: "c1", IncludeBundles: true, MaxVersionsPerObject: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Matches) != 1 || result.Matches[0].Object.Name != "api-0" || result.Summary.Bundles != 1 {
		t.Fatalf("result = %#v", result)
	}
	if len(result.Bundles) != 1 || len(result.Bundles[0].Facts) != 1 || len(result.Bundles[0].Versions) != 1 {
		t.Fatalf("bundles = %#v", result.Bundles)
	}
}

func TestStoreTopologyBuildsGraphFromEdges(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		query := string(data)
		if strings.Contains(query, "FORMAT TSVWithNames") {
			switch {
			case strings.Contains(query, "SELECT edge_type, src_id, dst_id"):
				writeTSV(w, "edge_type	src_id	dst_id	src_kind	dst_kind	edge_valid_from	edge_valid_to", `pod_on_node	c1/pod-uid	c1/node-uid	Pod	Node	1970-01-01 00:00:20.000	2100-01-01 00:00:00.000`)
			default:
				t.Fatalf("unexpected TSV query: %s", query)
			}
			return
		}
		switch {
		case strings.HasPrefix(strings.TrimSpace(query), "SELECT alias_id"), strings.HasPrefix(strings.TrimSpace(query), "SELECT object_id, alias_id"):
			_, _ = w.Write([]byte(`{"data":[],"rows":0}`))
		case strings.Contains(query, "FROM `ki`.versions") && strings.Contains(query, "GROUP BY object_id") && strings.Contains(query, "LIMIT 2"):
			_, _ = w.Write([]byte(`{"data":[{"object_id":"c1/pod-uid","cluster_id":"c1","api_group":"","api_version":"v1","resource":"pods","kind":"Pod","namespace":"default","name":"api-0","uid":"pod-uid","latest_observed_at":"1970-01-01 00:00:20.000"}],"rows":1}`))
		case strings.Contains(query, "FROM `ki`.edges"):
			_, _ = w.Write([]byte(`{"data":[{"edge_type":"pod_on_node","src_id":"c1/pod-uid","dst_id":"c1/node-uid","src_kind":"Pod","dst_kind":"Node","valid_from":"1970-01-01 00:00:20.000","valid_to":"2100-01-01 00:00:00.000"}],"rows":1}`))
		case strings.Contains(query, "FROM `ki`.edges") && strings.Contains(query, "c1/node-uid"):
			_, _ = w.Write([]byte(`{"data":[],"rows":0}`))
		case strings.Contains(query, "FROM `ki`.versions") && strings.Contains(query, "object_id IN"):
			_, _ = w.Write([]byte(`{"data":[{"object_id":"c1/pod-uid","cluster_id":"c1","api_group":"","api_version":"v1","resource":"pods","kind":"Pod","namespace":"default","name":"api-0","uid":"pod-uid","latest_observed_at":"1970-01-01 00:00:20.000"},{"object_id":"c1/node-uid","cluster_id":"c1","api_group":"","api_version":"v1","resource":"nodes","kind":"Node","namespace":"","name":"node-a","uid":"node-uid","latest_observed_at":"1970-01-01 00:00:20.000"}],"rows":2}`))
		default:
			t.Fatalf("unexpected query: %s", query)
		}
	}))
	defer server.Close()

	store := &Store{Client: HTTPClient{Endpoint: server.URL, HTTPClient: server.Client()}, Database: "ki"}
	graph, err := store.Topology(context.Background(), storage.ObjectTarget{ClusterID: "c1", Kind: "Pod", Namespace: "default", Name: "api-0"})
	if err != nil {
		t.Fatal(err)
	}
	if graph.Summary.Nodes != 2 || graph.Summary.Edges != 1 || graph.Edges[0].Target.Kind != "Node" || graph.Edges[0].Direction != "out" {
		t.Fatalf("graph = %#v", graph)
	}
}
