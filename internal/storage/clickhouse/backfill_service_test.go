package clickhouse

import (
	"context"
	"strings"
	"testing"
)

type fakeServiceBackfillClient struct {
	queries []string
	batches []QueryResult
	inserts map[string][]map[string]any
}

func (f *fakeServiceBackfillClient) QueryJSON(_ context.Context, query string) (QueryResult, error) {
	f.queries = append(f.queries, query)
	if len(f.batches) == 0 {
		return QueryResult{}, nil
	}
	result := f.batches[0]
	f.batches = f.batches[1:]
	return result, nil
}

func (f *fakeServiceBackfillClient) InsertEvidenceBatch(context.Context, EvidenceBatch) (InsertResult, error) {
	return InsertResult{}, nil
}

func (f *fakeServiceBackfillClient) InsertRows(_ context.Context, _ string, table string, rows []map[string]any) error {
	if f.inserts == nil {
		f.inserts = map[string][]map[string]any{}
	}
	f.inserts[table] = append(f.inserts[table], rows...)
	return nil
}

func TestBackfillServiceFactsDryRunPlansMissingServiceFacts(t *testing.T) {
	client := &fakeServiceBackfillClient{batches: []QueryResult{{Data: []map[string]any{serviceBackfillRow()}}}}
	report, err := BackfillServiceFacts(context.Background(), client, client, ServiceFactsBackfillOptions{Database: "ki", DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if report.VersionsScanned != 1 || report.FactsInserted != 5 || report.ChangesInserted != 2 {
		t.Fatalf("report = %#v", report)
	}
	if len(client.inserts) != 0 {
		t.Fatalf("dry-run inserted rows: %#v", client.inserts)
	}
	if len(client.queries) == 0 || !strings.Contains(client.queries[0], "startsWith(fact_key, 'service.')") {
		t.Fatalf("query missing service fact anti-join: %s", client.queries[0])
	}
}

func TestBackfillServiceFactsAppliesFactsAndChanges(t *testing.T) {
	client := &fakeServiceBackfillClient{batches: []QueryResult{{Data: []map[string]any{serviceBackfillRow()}}}}
	report, err := BackfillServiceFacts(context.Background(), client, client, ServiceFactsBackfillOptions{Database: "ki"})
	if err != nil {
		t.Fatal(err)
	}
	if report.FactsInserted != int64(len(client.inserts["facts"])) {
		t.Fatalf("facts report/insert mismatch: %#v inserts=%#v", report, client.inserts)
	}
	if report.ChangesInserted != int64(len(client.inserts["changes"])) {
		t.Fatalf("changes report/insert mismatch: %#v inserts=%#v", report, client.inserts)
	}
	if !insertHasFact(client.inserts["facts"], "service.load_balancer.ingress_ip", "34.1.2.3") {
		t.Fatalf("missing ingress fact: %#v", client.inserts["facts"])
	}
}

func serviceBackfillRow() map[string]any {
	return map[string]any{
		"cluster_id":       "c1",
		"object_id":        "c1/svc-uid",
		"seq":              float64(1),
		"observed_at":      "2026-05-18 13:32:17.092",
		"observation_type": "MODIFIED",
		"api_group":        "",
		"api_version":      "v1",
		"resource":         "services",
		"kind":             "Service",
		"namespace":        "default",
		"name":             "api",
		"uid":              "svc-uid",
		"resource_version": "123",
		"doc":              `{"spec":{"type":"LoadBalancer","clusterIP":"10.0.0.10"},"status":{"loadBalancer":{"ingress":[{"ip":"34.1.2.3","ipMode":"VIP"}]}}}`,
	}
}

func insertHasFact(rows []map[string]any, key, value string) bool {
	for _, row := range rows {
		if row["fact_key"] == key && row["fact_value"] == value {
			return true
		}
	}
	return false
}
