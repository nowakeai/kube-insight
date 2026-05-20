package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"kube-insight/internal/storage"
)

func TestHealthToolInfo(t *testing.T) {
	tool := NewHealthTool(&fakeHealthStore{}, HealthToolOptions{DefaultLimit: 10, MaxLimit: 25})
	info, err := tool.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != HealthToolName || !strings.Contains(info.Desc, "collector coverage") || info.ParamsOneOf == nil {
		t.Fatalf("tool info = %#v", info)
	}
	schema, err := info.ParamsOneOf.ToJSONSchema()
	if err != nil {
		t.Fatal(err)
	}
	schemaData, err := json.Marshal(schema)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(schemaData), "clusterId") {
		t.Fatalf("schema missing clusterId: %s", string(schemaData))
	}
}

func TestHealthToolInvokableRun(t *testing.T) {
	store := &fakeHealthStore{report: storage.ResourceHealthReport{
		CheckedAt: time.Unix(100, 0).UTC(),
		Summary:   storage.ResourceHealthSummary{Resources: 1, Healthy: 1, Complete: true},
		ByStatus:  map[string]int{"watching": 1},
		Resources: []storage.ResourceHealthRecord{{ClusterID: "c1", Version: "v1", Resource: "pods", Kind: "Pod", Status: "watching", LatestObjects: 3}},
	}}
	tool := NewHealthTool(store, HealthToolOptions{DefaultLimit: 10, MaxLimit: 25})
	out, err := tool.InvokableRun(context.Background(), `{"clusterId":"c1","errorsOnly":true,"staleAfter":"5m","limit":99,"excludeResources":["events"],"includeSkipped":true}`)
	if err != nil {
		t.Fatal(err)
	}
	if store.opts.ClusterID != "c1" || !store.opts.ErrorsOnly || store.opts.StaleAfter != 5*time.Minute || store.opts.Limit != 25 || !store.opts.IncludeExcluded {
		t.Fatalf("opts = %#v", store.opts)
	}
	if len(store.opts.ExcludeResources) != 1 || store.opts.ExcludeResources[0] != "events" {
		t.Fatalf("exclude resources = %#v", store.opts.ExcludeResources)
	}
	var report storage.ResourceHealthReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatal(err)
	}
	if report.Summary.Resources != 1 || report.Resources[0].Resource != "pods" {
		t.Fatalf("report = %#v", report)
	}
}

func TestHealthToolDefaultsAndArgumentErrors(t *testing.T) {
	store := &fakeHealthStore{}
	tool := NewHealthTool(store, HealthToolOptions{DefaultLimit: 7, MaxLimit: 25})
	if _, err := tool.InvokableRun(context.Background(), ``); err != nil {
		t.Fatal(err)
	}
	if store.opts.Limit != 7 {
		t.Fatalf("default limit = %d", store.opts.Limit)
	}
	if _, err := tool.InvokableRun(context.Background(), `{`); err == nil || !strings.Contains(err.Error(), "invalid kube_insight_health arguments") {
		t.Fatalf("invalid json err = %v", err)
	}
	if _, err := tool.InvokableRun(context.Background(), `{"staleAfter":"soon"}`); err == nil || !strings.Contains(err.Error(), "staleAfter") {
		t.Fatalf("invalid staleAfter err = %v", err)
	}
}

type fakeHealthStore struct {
	opts   storage.ResourceHealthOptions
	report storage.ResourceHealthReport
}

func (s *fakeHealthStore) ResourceHealth(_ context.Context, opts storage.ResourceHealthOptions) (storage.ResourceHealthReport, error) {
	s.opts = opts
	return s.report, nil
}
