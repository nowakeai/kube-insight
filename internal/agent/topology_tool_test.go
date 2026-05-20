package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"kube-insight/internal/storage"
)

func TestTopologyToolInfo(t *testing.T) {
	tool := NewTopologyTool(&fakeTopologyStore{})
	info, err := tool.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != TopologyToolName || !strings.Contains(info.Desc, "topology graph") || info.ParamsOneOf == nil {
		t.Fatalf("tool info = %#v", info)
	}
}

func TestTopologyToolInvokableRun(t *testing.T) {
	store := &fakeTopologyStore{graph: storage.TopologyGraph{
		Root:    storage.ObjectRecord{ClusterID: "c1", Version: "v1", Resource: "services", Kind: "Service", Namespace: "default", Name: "api"},
		Nodes:   []storage.ObjectRecord{{ClusterID: "c1", Version: "v1", Resource: "pods", Kind: "Pod", Namespace: "default", Name: "api-0"}},
		Summary: storage.TopologySummary{Nodes: 1, Edges: 0},
	}}
	tool := NewTopologyTool(store)
	out, err := tool.InvokableRun(context.Background(), `{"clusterId":"c1","kind":"Service","namespace":"default","name":"api","uid":"uid-1"}`)
	if err != nil {
		t.Fatal(err)
	}
	if store.target.ClusterID != "c1" || store.target.Kind != "Service" || store.target.Namespace != "default" || store.target.Name != "api" || store.target.UID != "uid-1" {
		t.Fatalf("target = %#v", store.target)
	}
	var graph storage.TopologyGraph
	if err := json.Unmarshal([]byte(out), &graph); err != nil {
		t.Fatal(err)
	}
	if graph.Root.Name != "api" || graph.Summary.Nodes != 1 {
		t.Fatalf("graph = %#v", graph)
	}
}

func TestTopologyToolArgumentErrors(t *testing.T) {
	tool := NewTopologyTool(&fakeTopologyStore{})
	for _, args := range []string{"", `{`, `{"kind":"Service"}`} {
		if _, err := tool.InvokableRun(context.Background(), args); err == nil {
			t.Fatalf("expected error for args %q", args)
		}
	}
}

type fakeTopologyStore struct {
	target storage.ObjectTarget
	graph  storage.TopologyGraph
}

func (s *fakeTopologyStore) Topology(_ context.Context, target storage.ObjectTarget) (storage.TopologyGraph, error) {
	s.target = target
	return s.graph, nil
}
