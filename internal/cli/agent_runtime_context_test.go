package cli

import (
	"context"
	"strings"
	"testing"

	"kube-insight/internal/agent"
	"kube-insight/internal/storage"
)

func TestBuildAgentRuntimeContextUsesLeanRoutingHints(t *testing.T) {
	store := fakeRuntimeContextStore{
		health: storage.ResourceHealthReport{
			Summary: storage.ResourceHealthSummary{Resources: 3, Healthy: 3, Complete: true},
			Resources: []storage.ResourceHealthRecord{
				{Group: "", Resource: "pods", Status: "listed", LatestObjects: 4},
				{Group: "", Resource: "services", Status: "listed", LatestObjects: 2},
				{Group: "discovery.k8s.io", Resource: "endpointslices", Status: "listed", LatestObjects: 2},
			},
		},
		sqlResults: []storage.SQLQueryResult{
			{Rows: []map[string]any{{"kind": "Pod", "objects": int64(4)}, {"kind": "Service", "objects": int64(2)}}},
			{Rows: []map[string]any{{"namespace": "default", "name": "api", "pods": int64(3)}}},
		},
	}
	runtimeContext := buildAgentRuntimeContext(context.Background(), &store)
	for _, want := range []string{
		"Runtime kube-insight orientation",
		"routing context only",
		"resources=3 healthy=3",
		"pods=listed latest=4",
		"Pod=4",
		"default/api pods=3",
		"Exact Service health",
		"Do not call kube_insight_schema",
	} {
		if !strings.Contains(runtimeContext, want) {
			t.Fatalf("context missing %q:\n%s", want, runtimeContext)
		}
	}
	if len(runtimeContext) > maxAgentRuntimeContextBytes+80 {
		t.Fatalf("context length = %d", len(runtimeContext))
	}
}

func TestRuntimeContextRunnerPrependsSystemMessage(t *testing.T) {
	inner := &captureAgentRunner{}
	runner := agentRuntimeContextRunner{
		inner: inner,
		build: func(context.Context) string {
			return "Runtime kube-insight orientation:\n- Use typed tools first."
		},
	}
	_, err := runner.Run(context.Background(), agent.EinoRunInput{
		Messages: []agent.Message{{Role: agent.RoleUser, Content: "is default/api healthy?"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(inner.input.Messages) != 2 {
		t.Fatalf("messages = %#v", inner.input.Messages)
	}
	if inner.input.Messages[0].Role != agent.RoleSystem || !strings.Contains(inner.input.Messages[0].Content, "Runtime kube-insight orientation") {
		t.Fatalf("system message = %#v", inner.input.Messages[0])
	}
	if inner.input.Messages[1].Role != agent.RoleUser || inner.input.Messages[1].Content != "is default/api healthy?" {
		t.Fatalf("user message = %#v", inner.input.Messages[1])
	}
}

type fakeRuntimeContextStore struct {
	health     storage.ResourceHealthReport
	sqlResults []storage.SQLQueryResult
	queries    int
}

func (s *fakeRuntimeContextStore) ResourceHealth(context.Context, storage.ResourceHealthOptions) (storage.ResourceHealthReport, error) {
	return s.health, nil
}

func (s *fakeRuntimeContextStore) QuerySQL(context.Context, storage.SQLQueryOptions) (storage.SQLQueryResult, error) {
	if s.queries >= len(s.sqlResults) {
		return storage.SQLQueryResult{}, nil
	}
	result := s.sqlResults[s.queries]
	s.queries++
	return result, nil
}

func (s *fakeRuntimeContextStore) QuerySchema(context.Context) (storage.SQLSchema, error) {
	return storage.SQLSchema{}, nil
}

func (s *fakeRuntimeContextStore) Close() error {
	return nil
}

type captureAgentRunner struct {
	input agent.EinoRunInput
}

func (r *captureAgentRunner) Run(_ context.Context, input agent.EinoRunInput) (agent.EinoRunResult, error) {
	r.input = input
	return agent.EinoRunResult{FinalAnswer: "ok", Events: 1}, nil
}
