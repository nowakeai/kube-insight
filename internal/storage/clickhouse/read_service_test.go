package clickhouse

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"kube-insight/internal/storage"
)

func TestStoreInvestigateServiceExpandsTopologyAndEvidence(t *testing.T) {
	var factQuery string
	var changeQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		query := string(data)
		if strings.Contains(query, "FORMAT TSVWithNames") {
			switch {
			case strings.Contains(query, "SELECT edge_type, src_id, dst_id") && strings.Contains(query, "c1/svc-uid"):
				writeTSV(w, "edge_type	src_id	dst_id	src_kind	dst_kind	edge_valid_from	edge_valid_to", `endpointslice_for_service	c1/eps-uid	c1/svc-uid	EndpointSlice	Service	1970-01-01 00:00:20.000	2100-01-01 00:00:00.000`)
			case strings.Contains(query, "SELECT edge_type, src_id, dst_id") && strings.Contains(query, "c1/eps-uid"):
				writeTSV(w, "edge_type	src_id	dst_id	src_kind	dst_kind	edge_valid_from	edge_valid_to", `endpointslice_for_service	c1/eps-uid	c1/svc-uid	EndpointSlice	Service	1970-01-01 00:00:20.000	2100-01-01 00:00:00.000`, `endpointslice_targets_pod	c1/eps-uid	c1/pod-uid	EndpointSlice	Pod	1970-01-01 00:00:20.000	2100-01-01 00:00:00.000`)
			case strings.Contains(query, "SELECT edge_type, src_id, dst_id") && strings.Contains(query, "c1/pod-uid"):
				writeTSV(w, "edge_type	src_id	dst_id	src_kind	dst_kind	edge_valid_from	edge_valid_to", `endpointslice_targets_pod	c1/eps-uid	c1/pod-uid	EndpointSlice	Pod	1970-01-01 00:00:20.000	2100-01-01 00:00:00.000`, `pod_on_node	c1/pod-uid	c1/node-uid	Pod	Node	1970-01-01 00:00:20.000	2100-01-01 00:00:00.000`)
			case strings.Contains(query, "SELECT edge_type, src_id, dst_id"):
				writeEmptyTSV(w, "edge_type	src_id	dst_id	src_kind	dst_kind	edge_valid_from	edge_valid_to")
			case strings.Contains(query, "SELECT object_id, doc"):
				writeTSV(w, "object_id	doc", `c1/svc-uid	{"kind":"Service"}`, `c1/eps-uid	{"kind":"EndpointSlice"}`, `c1/pod-uid	{"kind":"Pod"}`, `c1/node-uid	{"kind":"Node"}`)
			case strings.Contains(query, "SELECT ts, object_id, fact_key"):
				factQuery = query
				writeTSV(w, "ts	object_id	fact_key	fact_value	numeric_value	severity	detail", `1970-01-01 00:00:20.000	c1/pod-uid	pod_status.phase	Running		10	{}`)
			case strings.Contains(query, "SELECT ts, object_id, change_family"):
				changeQuery = query
				writeEmptyTSV(w, "ts	object_id	change_family	path	op	old_scalar	new_scalar	severity")
			case strings.Contains(query, "SELECT object_id, alias_id"):
				writeEmptyTSV(w, "object_id	alias_id")
			case strings.Contains(query, "SELECT edge_type, src_id, dst_id, valid_from"):
				writeEmptyTSV(w, "edge_type	src_id	dst_id	valid_from	valid_to	detail")
			case strings.Contains(query, "SELECT object_id, seq"):
				writeTSV(w, "object_id	seq	observed_at	resource_version	doc_hash	materialization	raw_size	stored_size", `c1/svc-uid	1	1970-01-01 00:00:20.000	20	h1	full	14	14`, `c1/eps-uid	1	1970-01-01 00:00:20.000	20	h1	full	14	14`, `c1/pod-uid	1	1970-01-01 00:00:20.000	20	h1	full	14	14`, `c1/node-uid	1	1970-01-01 00:00:20.000	20	h1	full	14	14`)
			default:
				t.Fatalf("unexpected TSV query: %s", query)
			}
			return
		}
		switch {
		case strings.HasPrefix(strings.TrimSpace(query), "SELECT alias_id"), strings.HasPrefix(strings.TrimSpace(query), "SELECT object_id, alias_id"):
			_, _ = w.Write([]byte(`{"data":[],"rows":0}`))
		case strings.Contains(query, "FROM `ki`.versions") && strings.Contains(query, "GROUP BY object_id") && strings.Contains(query, "LIMIT 2"):
			_, _ = w.Write([]byte(`{"data":[{"object_id":"c1/svc-uid","cluster_id":"c1","api_group":"","api_version":"v1","resource":"services","kind":"Service","namespace":"default","name":"api","uid":"svc-uid","latest_observed_at":"1970-01-01 00:00:20.000"}],"rows":1}`))
		case strings.Contains(query, "SELECT doc FROM `ki`.versions"):
			_, _ = w.Write([]byte(`{"data":[{"doc":"{\"kind\":\"Service\"}"}],"rows":1}`))
		case strings.Contains(query, "SELECT ts, object_id, fact_key"):
			_, _ = w.Write([]byte(`{"data":[{"ts":"1970-01-01 00:00:20.000","object_id":"c1/pod-uid","fact_key":"pod_status.phase","fact_value":"Running","numeric_value":null,"severity":10,"detail":"{}"}],"rows":1}`))
		case strings.Contains(query, "SELECT ts, object_id, change_family"):
			_, _ = w.Write([]byte(`{"data":[],"rows":0}`))
		case strings.Contains(query, "SELECT seq, observed_at"):
			_, _ = w.Write([]byte(`{"data":[{"seq":1,"observed_at":"1970-01-01 00:00:20.000","resource_version":"20","doc_hash":"h1","materialization":"full","raw_size":14,"stored_size":14,"doc":"{}"}],"rows":1}`))
		case strings.Contains(query, "FROM `ki`.edges") && strings.Contains(query, "c1/svc-uid"):
			_, _ = w.Write([]byte(`{"data":[{"edge_type":"endpointslice_for_service","src_id":"c1/eps-uid","dst_id":"c1/svc-uid","src_kind":"EndpointSlice","dst_kind":"Service","valid_from":"1970-01-01 00:00:20.000","valid_to":"2100-01-01 00:00:00.000"}],"rows":1}`))
		case strings.Contains(query, "FROM `ki`.edges") && strings.Contains(query, "c1/eps-uid"):
			_, _ = w.Write([]byte(`{"data":[{"edge_type":"endpointslice_for_service","src_id":"c1/eps-uid","dst_id":"c1/svc-uid","src_kind":"EndpointSlice","dst_kind":"Service","valid_from":"1970-01-01 00:00:20.000","valid_to":"2100-01-01 00:00:00.000"},{"edge_type":"endpointslice_targets_pod","src_id":"c1/eps-uid","dst_id":"c1/pod-uid","src_kind":"EndpointSlice","dst_kind":"Pod","valid_from":"1970-01-01 00:00:20.000","valid_to":"2100-01-01 00:00:00.000"}],"rows":2}`))
		case strings.Contains(query, "FROM `ki`.edges") && strings.Contains(query, "c1/pod-uid"):
			_, _ = w.Write([]byte(`{"data":[{"edge_type":"endpointslice_targets_pod","src_id":"c1/eps-uid","dst_id":"c1/pod-uid","src_kind":"EndpointSlice","dst_kind":"Pod","valid_from":"1970-01-01 00:00:20.000","valid_to":"2100-01-01 00:00:00.000"},{"edge_type":"pod_on_node","src_id":"c1/pod-uid","dst_id":"c1/node-uid","src_kind":"Pod","dst_kind":"Node","valid_from":"1970-01-01 00:00:20.000","valid_to":"2100-01-01 00:00:00.000"}],"rows":2}`))
		case strings.Contains(query, "FROM `ki`.edges") && strings.Contains(query, "c1/node-uid"):
			_, _ = w.Write([]byte(`{"data":[],"rows":0}`))
		case strings.Contains(query, "FROM `ki`.versions") && strings.Contains(query, "object_id IN"):
			_, _ = w.Write([]byte(`{"data":[{"object_id":"c1/svc-uid","cluster_id":"c1","api_group":"","api_version":"v1","resource":"services","kind":"Service","namespace":"default","name":"api","uid":"svc-uid","latest_observed_at":"1970-01-01 00:00:20.000"},{"object_id":"c1/eps-uid","cluster_id":"c1","api_group":"discovery.k8s.io","api_version":"v1","resource":"endpointslices","kind":"EndpointSlice","namespace":"default","name":"api-abc","uid":"eps-uid","latest_observed_at":"1970-01-01 00:00:20.000"},{"object_id":"c1/pod-uid","cluster_id":"c1","api_group":"","api_version":"v1","resource":"pods","kind":"Pod","namespace":"default","name":"api-0","uid":"pod-uid","latest_observed_at":"1970-01-01 00:00:20.000"},{"object_id":"c1/node-uid","cluster_id":"c1","api_group":"","api_version":"v1","resource":"nodes","kind":"Node","namespace":"","name":"node-a","uid":"node-uid","latest_observed_at":"1970-01-01 00:00:20.000"}],"rows":4}`))
		default:
			t.Fatalf("unexpected query: %s", query)
		}
	}))
	defer server.Close()

	store := &Store{Client: HTTPClient{Endpoint: server.URL, HTTPClient: server.Client()}, Database: "ki"}
	result, err := store.InvestigateServiceWithOptions(context.Background(), storage.ObjectTarget{ClusterID: "c1", Namespace: "default", Name: "api"}, storage.InvestigationOptions{MaxVersionsPerObject: 1})
	if err != nil {
		t.Fatal(err)
	}
	if result.Summary.EndpointSlices != 1 || result.Summary.Pods != 1 || result.Summary.Nodes != 1 || result.Summary.Edges != 3 {
		t.Fatalf("summary = %#v result = %#v", result.Summary, result)
	}
	if len(result.Objects) != 3 || result.Objects[0].Summary.Rank == 0 {
		t.Fatalf("objects = %#v", result.Objects)
	}
	if !strings.Contains(factQuery, "LIMIT 100 BY object_id") || !strings.Contains(changeQuery, "LIMIT 100 BY object_id") {
		t.Fatalf("service evidence limits not applied; fact query=%s change query=%s", factQuery, changeQuery)
	}
}
