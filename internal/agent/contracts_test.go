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

func TestDefaultAgentInstructionRequiresEvidenceCitationsAndFollowUps(t *testing.T) {
	instruction := DefaultAgentInstruction()
	for _, want := range []string{"kube-insight Kubernetes investigation agent", "Evidence first", "kube_insight_health", "kube_insight_schema", "ClickHouse-compatible", "kube_insight_search", "includeDocs false", "maxRows bounded", "imaginary tables such as objects", "failed tool call is diagnostic evidence", "Parallel tool calls", "parallel_investigation", "multi-branch prompts", "Visible progress notes", "user-visible progress notes", "private chain-of-thought", "client time zone", "IANA time zone", "cluster display map", "cluster id in parentheses", "isError true", "cite the exact proof", "human-readable evidence label", "{{evidence: OOM facts}}", "query OOMKilled with kind Pod", "exactly one Pod OOMKilled/restart search", "do not parallelize OOM synonyms", "follow-up questions that only narrow the previous symptom", "one bounded Pod OOMKilled/restart search is terminal", "current user prompt as the task boundary", "complete new question", "do not reuse the previous turn's target", "Do not call history before the aggregate SQL", "do not run repeated includeBundles searches", "health plus schema", "do not add a cluster_id predicate", "cluster name, context name, display name, or fragment", "gcp2", "actively identify available clusters", "Do not pass the user fragment as the health cluster argument", "Never assume the user fragment is the stored cluster_id", "dataful cluster", "stable cluster_id", "not RFC3339 strings with T or Z", "cluster or UID is known", "one changes SQL query", "exact recent-change", "answer only the observed changes", "rollup-style changes SQL", "group by change_family/path/severity", "do not run spec-only", "Do not use search for exact recent-change", "Do not use topology for a pure recent-change", "Do not run wildcard", "kube_insight_js", "dependent profile/proof queries", "sqlAll", "serial kube_insight_sql chains", "answer-ready summarization", "SQL integer columns", "ki.sumBy", "Multiple JS calls are acceptable", "do not treat \"one JS call\" as a correctness requirement", "profile/coverage/min-max", "separate focused SQL/JS steps", "relevant clusters", "requested ranking dimensions", "row_count, coverage, and truncated", "lifecycle peak", "choose a useful cap instead of probing repeatedly", "PVC resize", "Do not rely on SQL window functions", "compare adjacent values in JavaScript", "variable defined in the script", "aggregate aliases", "not available in WHERE", "results.name or rows(result)", "never results.rows", "never write a comma-expression", "Never call kube_insight_js just to reformat", "{{evidence: Node capacity facts}}", "one rollup changes query", "terminal unless it returns zero rows", "Use evidence_condenser only", "source artifact IDs", "do not pass only your own prose summary", "relative time words from the client context", "Client sent at/local time/time zone", "server time", "before the first data tool call", "half day", "translated equivalents", "last 12 hours", "within the last hour?", "from how much to how much?", "facts.ts, changes.ts, observations.observed_at", "fact_value = 'OOMKilled'", "Avoid ILIKE, LIKE, positionCaseInsensitive", "avoid observations.doc ILIKE scans", "configuration/allocation questions", "resource requests or limits", "container_resource_allocation_rollup", "Node inventory", "same Node's following observations", "added_count", "deleted_count", "net_delta", "latest Node snapshot", "never count or sum all fact rows", "status.capacity/status.allocatable", "not spec", "do not infer capacity from node names", "Exact Node inventory/capacity/lifecycle prompts are recipe-shaped aggregation tasks", "do not call search, topology, history, evidence_condenser, or parallel_investigation", "avoid multiple near-duplicate rollups", "scoped observations/doc profile", "unknown semantic fields", "profile query", "After two related zero-result probes", "answer-ready SQL/JS/tool result", "do not call kube_insight_health again", "partial answer", "tool budget is under pressure", "version IDs", "Evidence section", "language of the user's current prompt", "examples in another language are context only", "not a translation instruction", "{{followup: Check resource requests and limits for the affected Pods}}", "avoid generic random prompts"} {
		if !strings.Contains(instruction, want) {
			t.Fatalf("default instruction missing %q: %s", want, instruction)
		}
	}
	for _, want := range []string{"PVC resize", "from how much to how much", "pvc_resize_candidates_for_js", "pvc_storage_history_for_js", "never select or group by changes.uid", "Collapse adjacent equal requested/capacity values", "unique PVC-level resize record", "serial kube_insight_sql", "never write position(latest_doc, ...) in WHERE", "state the actual query window and the display timezone explicitly", "do not answer with UTC only", "Do not use bare dates", "absolute deltas", "percent deltas", "new namespace classification", "interpret \"resource\" as Pod requests/limits allocation", "Query actual Pod coverage/min/max timestamps", "If start/end snapshots are sparse or stale, answer with that coverage caveat", "pod_count_peak_intervals_for_js", "interval_start, interval_end, and from_baseline", "Do not split that recipe", "negative", "sweep state events once", "latest non-deleted Pod snapshot", "ADDED and MODIFIED mean the object exists", "Pods can have only MODIFIED rows inside the window", "never use kube_insight_sql countDistinct(uid)", "raw Pod observation export returns exactly maxRows", "Never run one SQL query per bucket", "never build totalCountHistory then filter", "never implement bucketCounts by scanning every lifecycle row for every bucket", "maxQueries is hard-capped at 10", "EndpointSlice", "empty endpoint arrays", "zero arrayJoin rows is not proof", "ExternalName Services", "start/end latest non-deleted Node snapshots", "churn or replacement separately from net node-count", "added_count equals deleted_count proves only zero event-count delta", "max(observed_at) AS latest_observed_at", "do not request 50000 or 100000 rows"} {
		if !strings.Contains(instruction, want) {
			t.Fatalf("default instruction missing PVC resize rule %q: %s", want, instruction)
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
