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
	for _, want := range []string{"kube-insight Kubernetes investigation agent", "Evidence first", "kube_insight_health", "kube_insight_schema", "ClickHouse-compatible", "kube_insight_search", "includeDocs false", "maxRows bounded", "imaginary tables such as objects", "failed tool call is diagnostic evidence", "Parallel tool calls", "parallel_investigation", "multi-branch prompts", "Visible progress notes", "user-visible progress notes", "private chain-of-thought", "client time zone", "IANA time zone", "cluster display map", "cluster id in parentheses", "isError true", "cite the exact proof", "human-readable evidence label", "{{evidence: OOM facts}}", "query OOMKilled with kind Pod", "exactly one Pod OOMKilled/restart search", "do not parallelize OOM synonyms", "follow-up questions that only narrow the previous symptom", "one bounded Pod OOMKilled/restart search is terminal", "Do not call history before the aggregate SQL", "do not run repeated includeBundles searches", "health plus schema", "do not add a cluster_id predicate", "natural-language cluster name", "do not normalize it into a guessed cluster_id", "stable cluster_id", "not RFC3339 strings with T or Z", "cluster or UID is known", "one changes SQL query", "exact recent-change", "answer only the observed changes", "rollup-style changes SQL", "group by change_family/path/severity", "do not run spec-only", "Do not use search for exact recent-change", "Do not use topology for a pure recent-change", "Do not run wildcard", "kube_insight_scripted_query", "dependent profile -> proof queries", "sqlAll", "serial repeated kube_insight_sql plus artifact_transform_js", "artifact_transform_js", "literal JSON rows", "JavaScript array", "one rollup changes query", "terminal unless it returns zero rows", "Use evidence_condenser only", "source artifact IDs", "do not pass only your own prose summary", "answer immediately", "relative time words from the client context", "facts.ts, changes.ts, observations.observed_at", "fact_value = 'OOMKilled'", "Avoid ILIKE, LIKE, positionCaseInsensitive", "avoid observations.doc ILIKE scans", "configuration/allocation questions", "resource requests or limits", "container_resource_allocation_rollup", "Node capacity", "node_capacity.cpu", "node_allocatable.memory", "latest numeric_value per cluster_id/name/fact_key", "status.capacity/status.allocatable", "not spec", "do not infer capacity from node names", "at most one aggregate SQL plus one optional proof/sample SQL", "do not run multiple near-duplicate rollups", "scoped observations/doc profile", "unknown semantic fields", "profile query", "After two related zero-result probes", "partial answer", "tool budget is under pressure", "version IDs", "Evidence section"} {
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
