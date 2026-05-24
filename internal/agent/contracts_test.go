package agent

import (
	"strings"
	"testing"
)

func TestArtifactAndCitationContractNames(t *testing.T) {
	artifactKinds := []string{
		ArtifactKindMarkdown,
		ArtifactKindK8sResource,
		ArtifactKindK8sResourceList,
		ArtifactKindK8sTopology,
		ArtifactKindK8sHistory,
		ArtifactKindK8sDiff,
		ArtifactKindToolCall,
		ArtifactKindCitation,
	}
	assertUniqueNonEmpty(t, "artifact kind", artifactKinds)
	if ArtifactKindK8sResource != "k8s.resource" || ArtifactKindK8sTopology != "k8s.topology" || ArtifactKindCitation != "citation" {
		t.Fatalf("unexpected artifact contract names: %#v", artifactKinds)
	}

	citationTargets := []string{
		CitationTargetObject,
		CitationTargetVersion,
		CitationTargetFact,
		CitationTargetChange,
		CitationTargetEdge,
		CitationTargetSQLRow,
		CitationTargetArtifact,
	}
	assertUniqueNonEmpty(t, "citation target", citationTargets)
	if CitationTargetObject != "object" || CitationTargetSQLRow != "sql_row" || CitationTargetArtifact != "artifact" {
		t.Fatalf("unexpected citation target names: %#v", citationTargets)
	}
}

func TestDefaultAgentInstructionRequiresEvidenceCitations(t *testing.T) {
	instruction := DefaultAgentInstruction()
	for _, want := range []string{"kube-insight Kubernetes investigation agent", "Evidence first", "kube_insight_health", "kube_insight_schema", "ClickHouse-compatible", "kube_insight_search", "includeDocs false", "maxRows bounded", "imaginary tables such as objects", "failed tool call is diagnostic evidence", "Parallel tool calls", "Visible progress notes", "user-visible progress notes", "private chain-of-thought", "isError true", "cite the exact proof", "version IDs", "Evidence section"} {
		if !strings.Contains(instruction, want) {
			t.Fatalf("default instruction missing %q: %s", want, instruction)
		}
	}
}

func assertUniqueNonEmpty(t *testing.T, label string, values []string) {
	t.Helper()
	seen := map[string]struct{}{}
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			t.Fatalf("empty %s in %#v", label, values)
		}
		if _, ok := seen[value]; ok {
			t.Fatalf("duplicate %s %q in %#v", label, value, values)
		}
		seen[value] = struct{}{}
	}
}
