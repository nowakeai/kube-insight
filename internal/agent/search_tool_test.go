package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"kube-insight/internal/storage"
)

func TestSearchToolInfo(t *testing.T) {
	tool := NewSearchTool(&fakeSearchStore{}, SearchToolOptions{DefaultLimit: 5, MaxLimit: 12})
	info, err := tool.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != SearchToolName || !strings.Contains(info.Desc, "Search kube-insight evidence") || info.ParamsOneOf == nil {
		t.Fatalf("tool info = %#v", info)
	}
	schema, err := info.ParamsOneOf.ToJSONSchema()
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(schema)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "query") || !strings.Contains(string(data), "includeHealth") {
		t.Fatalf("schema = %s", string(data))
	}
}

func TestSearchToolInvokableRun(t *testing.T) {
	store := &fakeSearchStore{result: storage.EvidenceSearchResult{
		Summary: storage.EvidenceSearchSummary{Matches: 1},
		Matches: []storage.EvidenceSearchMatch{{
			Object:  storage.ObjectRecord{ClusterID: "c1", Version: "v1", Resource: "pods", Kind: "Pod", Namespace: "default", Name: "api-0"},
			Score:   3,
			Reasons: []string{"fact:status=CrashLoopBackOff"},
		}},
	}}
	tool := NewSearchTool(store, SearchToolOptions{DefaultLimit: 5, MaxLimit: 12})
	out, err := tool.InvokableRun(context.Background(), `{"query":"CrashLoopBackOff api","clusterId":"c1","kind":"Pod","namespace":"default","from":"2026-05-20","to":"2026-05-21T00:00:00Z","limit":99,"maxVersionsPerObject":99,"includeBundles":true,"includeHealth":false,"healthStaleAfter":"10m"}`)
	if err != nil {
		t.Fatal(err)
	}
	if store.opts.Query != "CrashLoopBackOff api" || store.opts.ClusterID != "c1" || store.opts.Kind != "Pod" || store.opts.Namespace != "default" {
		t.Fatalf("opts = %#v", store.opts)
	}
	if store.opts.Limit != 12 || store.opts.MaxVersionsPerObject != maxSearchToolVersionsPerObject || !store.opts.IncludeBundles || store.opts.IncludeHealth || store.opts.HealthStaleAfter != 10*time.Minute {
		t.Fatalf("opts = %#v", store.opts)
	}
	if store.opts.From.Format("2006-01-02") != "2026-05-20" || store.opts.To.Format(time.RFC3339) != "2026-05-21T00:00:00Z" {
		t.Fatalf("time opts = from %s to %s", store.opts.From, store.opts.To)
	}
	var result storage.EvidenceSearchResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	if result.Summary.Matches != 1 || result.Matches[0].Object.Name != "api-0" {
		t.Fatalf("result = %#v", result)
	}
}

func TestSearchToolDefaultsAndArgumentErrors(t *testing.T) {
	store := &fakeSearchStore{}
	tool := NewSearchTool(store, SearchToolOptions{DefaultLimit: 7, MaxLimit: 12})
	if _, err := tool.InvokableRun(context.Background(), `{"query":"api"}`); err != nil {
		t.Fatal(err)
	}
	if store.opts.Limit != 7 || !store.opts.IncludeHealth {
		t.Fatalf("default opts = %#v", store.opts)
	}
	for _, args := range []string{"", `{`, `{"query":""}`, `{"query":"api","from":"2026-05-22","to":"2026-05-20"}`, `{"query":"api","healthStaleAfter":"soon"}`} {
		if _, err := tool.InvokableRun(context.Background(), args); err == nil {
			t.Fatalf("expected error for args %q", args)
		}
	}
}

type fakeSearchStore struct {
	opts   storage.EvidenceSearchOptions
	result storage.EvidenceSearchResult
}

func (s *fakeSearchStore) SearchEvidence(_ context.Context, opts storage.EvidenceSearchOptions) (storage.EvidenceSearchResult, error) {
	s.opts = opts
	return s.result, nil
}
