package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestAnnotateAnswerWithLLMEvidenceLabels(t *testing.T) {
	citations := []verifiedAnswerCitation{{
		Citation: Citation{ID: "citation_1", Text: "SQL evidence"},
		Tokens:   []string{"default/api-0"},
	}}
	answer := "Pod `default/api-0` restarted recently. {{evidence: restart facts}}"
	annotated := annotateAnswerWithEvidenceReferences(answer, citations)
	if strings.Contains(annotated, "{{evidence:") {
		t.Fatalf("temporary label leaked: %q", annotated)
	}
	if !strings.Contains(annotated, "[restart facts](#citation:citation_1)") {
		t.Fatalf("annotated = %q", annotated)
	}
	if citations[0].Citation.Text != "restart facts" {
		t.Fatalf("citation text = %q", citations[0].Citation.Text)
	}
}

func TestAnnotateAnswerStripsUnverifiedEvidenceLabels(t *testing.T) {
	answer := "This claim has no verified artifact. {{evidence: unsupported source}}"
	annotated := annotateAnswerWithEvidenceReferences(answer, nil)
	if strings.Contains(annotated, "{{evidence:") || strings.Contains(annotated, "unsupported source") {
		t.Fatalf("annotated = %q", annotated)
	}
}

func TestEvidenceLabelCandidatePrefersSemanticSource(t *testing.T) {
	candidates := []answerCitationCandidate{
		{Artifact: Artifact{ID: "artifact_search", Title: "Search evidence"}, Source: "kube_insight_search", Text: "search evidence oomkilled pod candidate"},
		{Artifact: Artifact{ID: "artifact_history", Title: "History evidence"}, Source: "kube_insight_history", Text: "history pod versions"},
		{Artifact: Artifact{ID: "artifact_sql", Title: "OOMKilled facts by Pod (7 rows)"}, Source: "kube_insight_sql", Text: "oomkilled facts fact_key fact_value rows"},
		{Artifact: Artifact{ID: "artifact_health", Title: "Health evidence"}, Source: "kube_insight_health", Text: "collector health healthy stale"},
	}
	seen := map[string]bool{}
	candidate, ok := bestCandidateForEvidenceLabel("OOM facts", candidates, seen, "there are OOMKilled facts")
	if !ok || candidate.Artifact.ID != "artifact_sql" {
		t.Fatalf("OOM label candidate = %#v ok=%v", candidate, ok)
	}
	candidate, ok = bestCandidateForEvidenceLabel("collector health", candidates, seen, "collector health is healthy")
	if !ok || candidate.Artifact.ID != "artifact_health" {
		t.Fatalf("health label candidate = %#v ok=%v", candidate, ok)
	}
}

func TestAnnotateAnswerPlacesCitationOnMatchingEvidenceLabelBlock(t *testing.T) {
	citations := []verifiedAnswerCitation{{
		Citation: Citation{ID: "citation_1", Text: "OOM facts"},
		Tokens:   []string{"oomkilled"},
	}}
	answer := "是的，存在 OOMKilled。\n\nEvidence:\n- SQL 聚合显示 7 个 Pod 有记录。 {{evidence: OOM facts}}"
	annotated := annotateAnswerWithEvidenceReferences(answer, citations)
	if !strings.Contains(annotated, "SQL 聚合显示 7 个 Pod 有记录。 [OOM facts](#citation:citation_1)") {
		t.Fatalf("annotated = %q", annotated)
	}
}

func TestVerifiedAnswerCitationsCanUseParallelInvestigationArtifact(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	session, err := store.CreateSession(ctx, CreateSessionInput{})
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.CreateRun(ctx, session.ID, CreateRunInput{Input: "investigate reliability"})
	if err != nil {
		t.Fatal(err)
	}
	recorder := newEinoRunRecorder(store, run.ID)
	if err := recorder.append(ctx, EventArtifact, ArtifactEventData{Artifact: Artifact{
		ID:    "artifact_parallel",
		Kind:  ArtifactKindToolCall,
		Title: "Tool output: parallel_investigation",
		Data: jsonRaw(map[string]any{
			"toolCallId": "call_parallel",
			"name":       parallelInvestigationToolName,
			"output": map[string]any{
				"tool":    parallelInvestigationToolName,
				"summary": "parallel investigation completed 3 branch(es), failed 0 branch(es)",
				"branches": []map[string]any{{
					"name":       "oom_restarts",
					"childRunId": "run_child",
					"answer":     "OOMKilled facts show Pod default/api-0 restart_count=1. Recent changes and topology edges identify EndpointSlice api-abc.",
				}},
			},
		}),
	}}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Complete(ctx, "Pod `api-0` was OOMKilled. {{evidence: OOM facts}}"); err != nil {
		t.Fatal(err)
	}
	events, err := store.ListRunEvents(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	var citation CitationEventData
	var final MessageEventData
	for _, event := range events {
		switch event.Type {
		case EventCitation:
			if err := json.Unmarshal(event.Data, &citation); err != nil {
				t.Fatal(err)
			}
		case EventFinalAnswer:
			if err := json.Unmarshal(event.Data, &final); err != nil {
				t.Fatal(err)
			}
		}
	}
	if citation.Citation.ArtifactID != "artifact_parallel" || citation.Citation.Text != "OOM facts" {
		t.Fatalf("citation = %#v", citation)
	}
	if !strings.Contains(final.Content, "[OOM facts](#citation:"+citation.Citation.ID+")") {
		t.Fatalf("final = %q citation=%#v", final.Content, citation)
	}
}
