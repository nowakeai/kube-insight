package agent

import "strings"

const (
	ArtifactKindMarkdown        = "markdown"
	ArtifactKindK8sResource     = "k8s.resource"
	ArtifactKindK8sResourceList = "k8s.resource_list"
	ArtifactKindK8sTopology     = "k8s.topology"
	ArtifactKindK8sHistory      = "k8s.history"
	ArtifactKindK8sDiff         = "k8s.diff"
	ArtifactKindToolCall        = "tool_call"
	ArtifactKindCitation        = "citation"
)

const (
	CitationTargetObject   = "object"
	CitationTargetVersion  = "version"
	CitationTargetFact     = "fact"
	CitationTargetChange   = "change"
	CitationTargetEdge     = "edge"
	CitationTargetSQLRow   = "sql_row"
	CitationTargetArtifact = "artifact"
)

const citationToolGuidance = " Cite concrete proof from the returned data: object identity, version IDs, facts, changes, edges, SQL row numbers, or artifact IDs when available."

func DefaultAgentInstruction() string {
	return strings.TrimSpace(`
You are the kube-insight Kubernetes investigation agent.

Use kube-insight tools to collect evidence before making Kubernetes state, history, topology, or root-cause claims. Check collector health before current-state answers. Do not present unsupported guesses as facts.

When answering, cite the exact proof that supports each important claim. Prefer object identity citations with cluster, kind, namespace, and name. When available, cite version IDs, observation timestamps, fact IDs, change IDs, topology edge IDs, SQL row numbers, or artifact IDs.

Return concise Markdown. Include an Evidence section for non-trivial answers and keep citations close to the claims they support.
`)
}

func toolCitationGuidance() string {
	return citationToolGuidance
}
