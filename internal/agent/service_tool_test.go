package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"kube-insight/internal/storage"
)

func TestServiceInvestigationToolInfo(t *testing.T) {
	tool := NewServiceInvestigationTool(&fakeServiceInvestigationStore{})
	info, err := tool.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != ServiceInvestigationToolName || !strings.Contains(info.Desc, "Service investigation bundle") || info.ParamsOneOf == nil {
		t.Fatalf("tool info = %#v", info)
	}
}

func TestServiceInvestigationToolInvokableRun(t *testing.T) {
	store := &fakeServiceInvestigationStore{result: storage.ServiceInvestigation{
		Service: storage.EvidenceBundle{Object: storage.ObjectRecord{ClusterID: "c1", Version: "v1", Resource: "services", Kind: "Service", Namespace: "default", Name: "api"}},
		Summary: storage.ServiceInvestigationSummary{Objects: 1, Pods: 1},
	}}
	tool := NewServiceInvestigationTool(store)
	out, err := tool.InvokableRun(context.Background(), `{"clusterId":"c1","namespace":"default","name":"api","from":"2026-05-20","to":"2026-05-21T00:00:00Z","maxEvidenceObjects":999,"maxVersionsPerObject":999,"maxFactsPerObject":999,"maxChangesPerObject":999}`)
	if err != nil {
		t.Fatal(err)
	}
	if store.target.ClusterID != "c1" || store.target.Kind != "Service" || store.target.Namespace != "default" || store.target.Name != "api" {
		t.Fatalf("target = %#v", store.target)
	}
	if store.opts.MaxEvidenceObjects != maxServiceToolEvidenceObjects || store.opts.MaxVersionsPerObject != maxServiceToolVersionsPerObject || store.opts.MaxFactsPerObject != maxServiceToolFactsPerObject || store.opts.MaxChangesPerObject != maxServiceToolChangesPerObject {
		t.Fatalf("opts = %#v", store.opts)
	}
	if store.opts.From.Format("2006-01-02") != "2026-05-20" || store.opts.To.Format(time.RFC3339) != "2026-05-21T00:00:00Z" {
		t.Fatalf("time opts = from %s to %s", store.opts.From, store.opts.To)
	}
	var result storage.ServiceInvestigation
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	if result.Service.Object.Name != "api" || result.Summary.Pods != 1 {
		t.Fatalf("result = %#v", result)
	}
}

func TestServiceInvestigationToolDefaultsAndArgumentErrors(t *testing.T) {
	store := &fakeServiceInvestigationStore{}
	tool := NewServiceInvestigationTool(store)
	if _, err := tool.InvokableRun(context.Background(), `{"namespace":"default","name":"api"}`); err != nil {
		t.Fatal(err)
	}
	if store.opts.MaxEvidenceObjects != defaultServiceToolEvidenceObjects || store.opts.MaxVersionsPerObject != defaultServiceToolVersionsPerObject || store.opts.MaxFactsPerObject != defaultServiceToolFactsPerObject || store.opts.MaxChangesPerObject != defaultServiceToolChangesPerObject {
		t.Fatalf("defaults = %#v", store.opts)
	}
	for _, args := range []string{"", `{`, `{"namespace":"default"}`, `{"namespace":"default","name":"api","from":"2026-05-22","to":"2026-05-20"}`} {
		if _, err := tool.InvokableRun(context.Background(), args); err == nil {
			t.Fatalf("expected error for args %q", args)
		}
	}
}

type fakeServiceInvestigationStore struct {
	target storage.ObjectTarget
	opts   storage.InvestigationOptions
	result storage.ServiceInvestigation
}

func (s *fakeServiceInvestigationStore) InvestigateServiceWithOptions(_ context.Context, target storage.ObjectTarget, opts storage.InvestigationOptions) (storage.ServiceInvestigation, error) {
	s.target = target
	s.opts = opts
	return s.result, nil
}
