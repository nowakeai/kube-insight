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
