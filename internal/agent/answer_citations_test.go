package agent

import (
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
