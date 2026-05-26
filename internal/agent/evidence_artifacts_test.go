package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

func TestEvidenceArtifactsIncludeHealthAndSQLProofPanels(t *testing.T) {
	health := evidenceArtifactsFromToolOutput("kube_insight_health", jsonRaw(map[string]any{
		"content": []any{map[string]any{"type": "text", "text": "kube_insight_health v1\nsummary: healthy=3 stale=0"}},
	}))
	if len(health) != 1 || health[0].Kind != ArtifactKindMarkdown || health[0].CitationText != "Health evidence" {
		t.Fatalf("health artifacts = %#v", health)
	}
	if !strings.Contains(string(health[0].Data), "kube_insight_health") {
		t.Fatalf("health artifact data = %s", string(health[0].Data))
	}

	sql := evidenceArtifactsFromToolOutput("kube_insight_sql", jsonRaw(map[string]any{
		"content": []any{map[string]any{"type": "text", "text": `{"columns":["kind","name"],"rows":[["Pod","api-0"]],"rowCount":1}`}},
	}))
	if len(sql) != 1 || sql[0].Kind != ArtifactKindMarkdown || sql[0].CitationText != "SQL evidence (1 rows)" {
		t.Fatalf("sql artifacts = %#v", sql)
	}
	if !strings.Contains(string(sql[0].Target), CitationTargetSQLRow) {
		t.Fatalf("sql target = %s", string(sql[0].Target))
	}
}

func TestSQLEvidenceArtifactsUseSemanticFactTitleAndTable(t *testing.T) {
	artifacts := evidenceArtifactsFromToolOutput("kube_insight_sql", jsonRaw(map[string]any{
		"columns":  []any{"kind", "namespace", "name", "fact_key", "fact_value", "rows", "first_seen", "last_seen"},
		"rowCount": 2,
		"rows": []any{
			map[string]any{"kind": "Pod", "namespace": "vm", "name": "vmagent-0", "fact_key": "pod_status.last_reason", "fact_value": "OOMKilled", "rows": 12, "first_seen": "2026-05-24T00:00:00Z", "last_seen": "2026-05-25T00:00:00Z"},
			map[string]any{"kind": "Pod", "namespace": "default", "name": "api-0", "fact_key": "pod_status.reason", "fact_value": "OOMKilled", "rows": 1, "first_seen": "2026-05-24T01:00:00Z", "last_seen": "2026-05-24T01:00:00Z"},
		},
	}))
	if len(artifacts) != 1 {
		t.Fatalf("artifacts = %#v", artifacts)
	}
	if artifacts[0].Title != "OOMKilled facts by Pod (2 rows)" || artifacts[0].CitationText != artifacts[0].Title {
		t.Fatalf("artifact title/citation = %#v", artifacts[0])
	}
	data := string(artifacts[0].Data)
	for _, want := range []string{"| kind | namespace | name | fact_key | fact_value | rows | first_seen | last_seen |", "vmagent-0", "OOMKilled"} {
		if !strings.Contains(data, want) {
			t.Fatalf("artifact data missing %q: %s", want, data)
		}
	}
}

func TestSQLEvidenceArtifactsInferOOMTitleFromAggregateColumn(t *testing.T) {
	artifacts := evidenceArtifactsFromToolOutput("kube_insight_sql", jsonRaw(map[string]any{
		"columns":  []any{"kind", "namespace", "name", "oom_count"},
		"rowCount": 1,
		"rows":     []any{map[string]any{"kind": "Pod", "namespace": "vm", "name": "vmagent-0", "oom_count": 244}},
	}))
	if len(artifacts) != 1 {
		t.Fatalf("artifacts = %#v", artifacts)
	}
	if artifacts[0].Title != "OOMKilled facts by Pod (1 rows)" {
		t.Fatalf("artifact title = %q", artifacts[0].Title)
	}
}

func TestTopologyEvidenceArtifactsAcceptSrcDstEdges(t *testing.T) {
	artifact := evidenceArtifactsFromToolOutput("kube_insight_topology", jsonRaw(map[string]any{
		"root": liveTestObject("Service", "default", "api"),
		"edges": []any{map[string]any{
			"type": "service_selects_endpointslice",
			"src":  liveTestObject("Service", "default", "api"),
			"dst":  liveTestObject("EndpointSlice", "default", "api-abc"),
		}},
	}))
	if len(artifact) != 1 || artifact[0].Kind != ArtifactKindK8sTopology {
		t.Fatalf("topology artifacts = %#v", artifact)
	}
	data := string(artifact[0].Data)
	if !strings.Contains(data, "service_selects_endpointslice") || !strings.Contains(data, "EndpointSlice") {
		t.Fatalf("topology artifact data = %s", data)
	}
}

func TestHistoryEvidenceArtifactsIncludeChangesAndDiffs(t *testing.T) {
	artifacts := evidenceArtifactsFromToolOutput("kube_insight_history", jsonRaw(map[string]any{
		"object": liveTestObject("Pod", "default", "api-0"),
		"versions": []any{
			map[string]any{"versionId": "v1", "changeSummary": "ready pod"},
			map[string]any{"versionId": "v2", "changeSummary": "reason OOMKilled"},
		},
		"changes": []any{
			map[string]any{"path": "status.containerStatuses.api.lastState.reason", "after": "OOMKilled"},
		},
		"diffs": []any{
			map[string]any{"path": "status.containerStatuses.api.restartCount", "before": 0, "after": 1},
		},
	}))
	if len(artifacts) != 1 || artifacts[0].Kind != ArtifactKindK8sHistory {
		t.Fatalf("history artifacts = %#v", artifacts)
	}
	data := string(artifacts[0].Data)
	for _, want := range []string{"api-0", "OOMKilled", "changes", "diffs", "restartCount"} {
		if !strings.Contains(data, want) {
			t.Fatalf("history artifact data missing %q: %s", want, data)
		}
	}
}

func TestSearchEvidenceArtifactsIncludeNestedMatchReasons(t *testing.T) {
	artifacts := evidenceArtifactsFromToolOutput("kube_insight_search", jsonRaw(map[string]any{
		"matches": []any{map[string]any{
			"object": map[string]any{"clusterId": "c1", "kind": "Pod", "namespace": "default", "name": "api-0"},
			"score":  0.92,
			"reasons": []any{
				"fact:pod_status.last_reason=OOMKilled",
				"fact:pod_status.restart_count=3",
			},
		}},
	}))
	if len(artifacts) != 1 || artifacts[0].Kind != ArtifactKindK8sResourceList {
		t.Fatalf("artifacts = %#v", artifacts)
	}
	if artifacts[0].Title != "OOMKilled Pod candidates (1 resources)" {
		t.Fatalf("artifact title = %q", artifacts[0].Title)
	}
	data := string(artifacts[0].Data)
	for _, want := range []string{"api-0", "OOMKilled", "pod_status.last_reason"} {
		if !strings.Contains(data, want) {
			t.Fatalf("artifact data missing %q: %s", want, data)
		}
	}
}

func liveTestObject(kind, namespace, name string) map[string]any {
	return map[string]any{
		"clusterId": "test-cluster",
		"kind":      kind,
		"namespace": namespace,
		"name":      name,
	}
}

func TestEinoRunRecorderCreatesVerifiedCitationForUsedHealthOutput(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	session, err := store.CreateSession(ctx, CreateSessionInput{})
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.CreateRun(ctx, session.ID, CreateRunInput{Input: "is api healthy"})
	if err != nil {
		t.Fatal(err)
	}
	recorder := newEinoRunRecorder(store, run.ID)
	if err := recorder.Start(ctx); err != nil {
		t.Fatal(err)
	}
	assistant := schema.AssistantMessage("checking health", []schema.ToolCall{{ID: "tool_health", Function: schema.FunctionCall{Name: "kube_insight_health", Arguments: `{}`}}})
	if _, err := recorder.Record(ctx, adk.EventFromMessage(assistant, nil, schema.Assistant, "")); err != nil {
		t.Fatal(err)
	}
	toolMessage := schema.ToolMessage(`{"content":[{"type":"text","text":"kube_insight_health v1\nsummary: healthy=3 stale=0"}]}`, "tool_health", schema.WithToolName("kube_insight_health"))
	if _, err := recorder.Record(ctx, adk.EventFromMessage(toolMessage, nil, schema.Tool, "kube_insight_health")); err != nil {
		t.Fatal(err)
	}
	events, err := store.ListRunEvents(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	var markdownArtifacts int
	var citations int
	for _, event := range events {
		switch event.Type {
		case EventArtifact:
			var data ArtifactEventData
			if err := json.Unmarshal(event.Data, &data); err != nil {
				t.Fatal(err)
			}
			if data.Artifact.Kind == ArtifactKindMarkdown && strings.Contains(data.Artifact.Title, "Health") {
				markdownArtifacts++
			}
		case EventCitation:
			citations++
		}
	}
	if markdownArtifacts != 1 || citations != 0 {
		t.Fatalf("markdownArtifacts=%d citations=%d events=%#v", markdownArtifacts, citations, events)
	}
	if err := recorder.Complete(ctx, "Collector health evidence shows healthy=3 and stale=0."); err != nil {
		t.Fatal(err)
	}
	events, err = store.ListRunEvents(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	citations = 0
	for _, event := range events {
		if event.Type == EventCitation {
			citations++
		}
	}
	if citations != 1 {
		t.Fatalf("citations=%d events=%#v", citations, events)
	}
}
