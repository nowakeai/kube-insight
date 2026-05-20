package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"kube-insight/internal/storage"
)

func TestHistoryToolInfo(t *testing.T) {
	tool := NewHistoryTool(&fakeHistoryStore{})
	info, err := tool.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != HistoryToolName || !strings.Contains(info.Desc, "retained versions") || info.ParamsOneOf == nil {
		t.Fatalf("tool info = %#v", info)
	}
}

func TestHistoryToolInvokableRun(t *testing.T) {
	store := &fakeHistoryStore{history: storage.ObjectHistory{
		Object:   storage.ObjectRecord{ClusterID: "c1", Version: "v1", Resource: "pods", Kind: "Pod", Namespace: "default", Name: "api-0"},
		Versions: []storage.HistoryVersion{{ID: 1, Sequence: 1, ObservedAt: time.Unix(100, 0).UTC(), DocumentHash: "sha256:test"}},
		Summary:  storage.ObjectHistorySummary{Versions: 1},
	}}
	tool := NewHistoryTool(store)
	out, err := tool.InvokableRun(context.Background(), `{"clusterId":"c1","kind":"Pod","namespace":"default","name":"api-0","uid":"uid-1","from":"2026-05-20","to":"2026-05-21T00:00:00Z","maxVersions":999,"maxObservations":999,"includeDocs":true,"includeDiffs":true}`)
	if err != nil {
		t.Fatal(err)
	}
	if store.target.ClusterID != "c1" || store.target.Kind != "Pod" || store.target.Namespace != "default" || store.target.Name != "api-0" || store.target.UID != "uid-1" {
		t.Fatalf("target = %#v", store.target)
	}
	if store.opts.MaxVersions != maxHistoryToolVersions || store.opts.MaxObservations != maxHistoryToolObservations || !store.opts.IncludeDocs || !store.opts.IncludeDiffs {
		t.Fatalf("opts = %#v", store.opts)
	}
	if store.opts.From.Format("2006-01-02") != "2026-05-20" || store.opts.To.Format(time.RFC3339) != "2026-05-21T00:00:00Z" {
		t.Fatalf("time opts = from %s to %s", store.opts.From, store.opts.To)
	}
	var history storage.ObjectHistory
	if err := json.Unmarshal([]byte(out), &history); err != nil {
		t.Fatal(err)
	}
	if history.Object.Name != "api-0" || len(history.Versions) != 1 {
		t.Fatalf("history = %#v", history)
	}
}

func TestHistoryToolDefaultsAndArgumentErrors(t *testing.T) {
	store := &fakeHistoryStore{}
	tool := NewHistoryTool(store)
	if _, err := tool.InvokableRun(context.Background(), `{"kind":"Pod","name":"api-0"}`); err != nil {
		t.Fatal(err)
	}
	if store.opts.MaxVersions != defaultHistoryToolVersions || store.opts.MaxObservations != defaultHistoryToolObservations {
		t.Fatalf("defaults = %#v", store.opts)
	}
	for _, args := range []string{"", `{`, `{"kind":"Pod"}`, `{"kind":"Pod","name":"api-0","from":"2026-05-22","to":"2026-05-20"}`} {
		if _, err := tool.InvokableRun(context.Background(), args); err == nil {
			t.Fatalf("expected error for args %q", args)
		}
	}
}

type fakeHistoryStore struct {
	target  storage.ObjectTarget
	opts    storage.ObjectHistoryOptions
	history storage.ObjectHistory
}

func (s *fakeHistoryStore) ObjectHistory(_ context.Context, target storage.ObjectTarget, opts storage.ObjectHistoryOptions) (storage.ObjectHistory, error) {
	s.target = target
	s.opts = opts
	return s.history, nil
}
