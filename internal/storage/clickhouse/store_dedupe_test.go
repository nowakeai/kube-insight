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

func TestStorePutObservationSkipsUnchangedVersionRows(t *testing.T) {
	obs := core.Observation{
		Type:            core.ObservationModified,
		ObservedAt:      time.Unix(20, 0),
		ResourceVersion: "20",
		Ref:             core.ResourceRef{ClusterID: "c1", Version: "v1", Resource: "pods", Kind: "Pod", Namespace: "default", Name: "api-0", UID: "pod-uid"},
		Object:          map[string]any{"kind": "Pod", "status": map[string]any{"phase": "Running"}},
	}
	baseline, err := BuildEvidenceBatch("ki", []core.Observation{obs}, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	hash := baseline.Observations[0]["doc_hash"].(string)

	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		query := string(data)
		requests = append(requests, query)
		if strings.Contains(query, "argMax(doc_hash, observed_at)") {
			writeTSV(w, "object_id\tseq\tdoc_hash", "c1/pod-uid\t7\t"+hash)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	store := &Store{Client: HTTPClient{Endpoint: server.URL, HTTPClient: server.Client()}, Database: "ki"}
	err = store.PutObservation(context.Background(), obs, extractor.Evidence{Facts: []core.Fact{{
		Time:     obs.ObservedAt,
		ObjectID: "c1/pod-uid",
		Key:      "pod_status.phase",
		Value:    "Running",
	}}})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(requests, "\n")
	for _, want := range []string{"SELECT object_id, max(seq)", "INSERT INTO `ki`.`observations`", "INSERT INTO `ki`.`object_aliases`"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("requests missing %s:\n%s", want, joined)
		}
	}
	for _, notWant := range []string{"INSERT INTO `ki`.`versions`", "INSERT INTO `ki`.`facts`", "INSERT INTO `ki`.`edges`", "INSERT INTO `ki`.`changes`"} {
		if strings.Contains(joined, notWant) {
			t.Fatalf("unchanged observation wrote %s:\n%s", notWant, joined)
		}
	}
}
