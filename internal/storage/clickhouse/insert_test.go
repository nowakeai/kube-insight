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
)

func TestBuildEvidenceBatch(t *testing.T) {
	obs := core.Observation{
		Type:            core.ObservationModified,
		ObservedAt:      time.Unix(10, 123000000),
		ResourceVersion: "10",
		Ref: core.ResourceRef{
			ClusterID: "c1",
			Version:   "v1",
			Resource:  "pods",
			Kind:      "Pod",
			Namespace: "default",
			Name:      "api-1",
			UID:       "pod-uid",
		},
		Object: map[string]any{"kind": "Pod"},
	}
	node := core.Observation{
		Type:            core.ObservationModified,
		ObservedAt:      obs.ObservedAt,
		ResourceVersion: "11",
		Ref: core.ResourceRef{
			ClusterID: "c1",
			Version:   "v1",
			Resource:  "nodes",
			Kind:      "Node",
			Name:      "node-a",
			UID:       "node-uid",
		},
		Object: map[string]any{"kind": "Node"},
	}
	batch, err := BuildEvidenceBatch("ki", []core.Observation{obs, node}, []core.Fact{{
		Time:     obs.ObservedAt,
		ObjectID: "c1/pod-uid",
		Key:      "pod.phase",
		Value:    "Running",
	}}, []core.Edge{{
		Type:      "pod_on_node",
		SourceID:  "c1/pod-uid",
		TargetID:  "c1/nodes/node-a",
		ValidFrom: obs.ObservedAt,
	}}, []core.Change{{
		Time:     obs.ObservedAt,
		ObjectID: "c1/pod-uid",
		Family:   "status",
		Path:     "status.phase",
		Op:       "replace",
		New:      "Running",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Observations) != 2 || len(batch.ObjectAliases) != 4 || len(batch.Versions) != 2 || len(batch.Facts) != 1 || len(batch.Edges) != 1 || len(batch.Changes) != 1 {
		t.Fatalf("batch = %#v", batch)
	}
	if batch.Versions[0]["object_id"] != "c1/pod-uid" || batch.Versions[0]["seq"] != uint64(1) {
		t.Fatalf("version row = %#v", batch.Versions[0])
	}
	if batch.Observations[0]["doc_hash"] == "" || !strings.Contains(batch.Observations[0]["doc"].(string), "Pod") {
		t.Fatalf("observation row = %#v", batch.Observations[0])
	}
	if batch.Edges[0]["src_id"] != "c1/pod-uid" || batch.Edges[0]["dst_id"] != "c1/node-uid" {
		t.Fatalf("edge row = %#v", batch.Edges[0])
	}
}

func TestBuildEvidenceBatchForPendingSkipsUnchangedEvidence(t *testing.T) {
	obs := core.Observation{
		Type:            core.ObservationModified,
		ObservedAt:      time.Unix(10, 0),
		ResourceVersion: "10",
		Ref: core.ResourceRef{
			ClusterID: "c1",
			Version:   "v1",
			Resource:  "configmaps",
			Kind:      "ConfigMap",
			Namespace: "default",
			Name:      "settings",
			UID:       "cm-uid",
		},
		Object: map[string]any{"kind": "ConfigMap", "data": map[string]any{"mode": "prod"}},
	}
	baseline, err := BuildEvidenceBatch("ki", []core.Observation{obs}, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	hash := baseline.Observations[0]["doc_hash"].(string)
	pending := []pendingObservation{{
		Observation: obs,
		Evidence: extractor.Evidence{Facts: []core.Fact{{
			Time:     obs.ObservedAt,
			ObjectID: "c1/cm-uid",
			Key:      "config.mode",
			Value:    "prod",
		}}},
	}}

	unchanged, err := buildEvidenceBatchForPending("ki", pending, map[string]objectVersionState{
		"c1/cm-uid": {Seq: 7, DocHash: hash, Exists: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(unchanged.Observations) != 1 || len(unchanged.ObjectAliases) != 2 {
		t.Fatalf("unchanged proof rows = observations %d aliases %d", len(unchanged.Observations), len(unchanged.ObjectAliases))
	}
	if len(unchanged.Versions) != 0 || len(unchanged.Facts) != 0 || len(unchanged.Edges) != 0 || len(unchanged.Changes) != 0 {
		t.Fatalf("unchanged batch retained evidence = %#v", unchanged)
	}

	changedObs := obs
	changedObs.ResourceVersion = "11"
	changedObs.Object = map[string]any{"kind": "ConfigMap", "data": map[string]any{"mode": "dev"}}
	changedPending := []pendingObservation{{Observation: changedObs, Evidence: pending[0].Evidence}}
	changed, err := buildEvidenceBatchForPending("ki", changedPending, map[string]objectVersionState{
		"c1/cm-uid": {Seq: 7, DocHash: hash, Exists: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(changed.Versions) != 1 || changed.Versions[0]["seq"] != uint64(8) || len(changed.Facts) != 1 {
		t.Fatalf("changed batch = %#v", changed)
	}
}

func TestHTTPClientInsertEvidenceBatch(t *testing.T) {
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

	batch := EvidenceBatch{
		Database:      "ki",
		Observations:  []map[string]any{{"cluster_id": "c1"}},
		ObjectAliases: []map[string]any{{"cluster_id": "c1", "object_id": "c1/pod-uid", "alias_id": "c1/pods/default/api-0"}},
		Facts:         []map[string]any{{"cluster_id": "c1", "fact_key": "pod.phase"}},
	}
	result, err := (HTTPClient{Endpoint: server.URL, HTTPClient: server.Client()}).InsertEvidenceBatch(context.Background(), batch)
	if err != nil {
		t.Fatal(err)
	}
	if result.Rows != 3 || result.Tables["observations"] != 1 || result.Tables["object_aliases"] != 1 || result.Tables["facts"] != 1 {
		t.Fatalf("result = %#v", result)
	}
	if len(requests) != 3 || !strings.Contains(requests[0], "INSERT INTO `ki`.`observations` FORMAT JSONEachRow") || !strings.Contains(requests[1], "INSERT INTO `ki`.`object_aliases` FORMAT JSONEachRow") {
		t.Fatalf("requests = %#v", requests)
	}
}

func TestHTTPClientInsertRowsAppliesAsyncInsertSettings(t *testing.T) {
	var rawQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawQuery = r.URL.RawQuery
		if got := r.URL.Query().Get("async_insert"); got != "1" {
			t.Fatalf("async_insert = %q", got)
		}
		if got := r.URL.Query().Get("wait_for_async_insert"); got != "1" {
			t.Fatalf("wait_for_async_insert = %q", got)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	err := (HTTPClient{Endpoint: server.URL + "?user=ki", HTTPClient: server.Client(), AsyncInsert: true}).InsertRows(
		context.Background(),
		"ki",
		"facts",
		[]map[string]any{{"cluster_id": "c1"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rawQuery, "user=ki") {
		t.Fatalf("existing endpoint query was not preserved: %s", rawQuery)
	}
}
