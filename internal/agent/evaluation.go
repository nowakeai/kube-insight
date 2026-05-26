package agent

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// EvaluationCase describes one stable, transcript-level expectation for an
// agent run. It intentionally scores emitted run events instead of model text
// alone so tests can verify evidence collection, citations, and latency.
type EvaluationCase struct {
	ID                       string
	Question                 string
	RequiredTools            []string
	RequiredArtifactKinds    []string
	AlternativeRequiredTools [][]string
	AlternativeArtifactKinds [][]string
	RequiredAnswerTerms      []string
	MinCitations             int
	MaxToolCalls             int
	MaxToolCallDurationMS    int64
	MaxTotalToolDurationMS   int64
	AllowFailedTools         bool
}

type EvaluationReport struct {
	CaseID              string                   `json:"caseId"`
	Question            string                   `json:"question,omitempty"`
	Passed              bool                     `json:"passed"`
	Checks              []EvaluationCheck        `json:"checks"`
	ToolCalls           []EvaluationToolCall     `json:"toolCalls,omitempty"`
	ArtifactKinds       []string                 `json:"artifactKinds,omitempty"`
	Citations           int                      `json:"citations"`
	FinalAnswer         string                   `json:"finalAnswer,omitempty"`
	ToolCallCount       int                      `json:"toolCallCount,omitempty"`
	TotalToolDurationMS int64                    `json:"totalToolDurationMs,omitempty"`
	Missing             EvaluationMissingDetails `json:"missing,omitempty"`
}

type EvaluationCheck struct {
	Name    string `json:"name"`
	Passed  bool   `json:"passed"`
	Message string `json:"message,omitempty"`
}

type EvaluationToolCall struct {
	ID         string `json:"id,omitempty"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	DurationMS int64  `json:"durationMs,omitempty"`
	Error      string `json:"error,omitempty"`
}

type EvaluationMissingDetails struct {
	Tools         []string `json:"tools,omitempty"`
	ArtifactKinds []string `json:"artifactKinds,omitempty"`
	AnswerTerms   []string `json:"answerTerms,omitempty"`
}

func DefaultEvaluationCases() []EvaluationCase {
	return []EvaluationCase{
		{
			ID:                    "service-health",
			Question:              "Is the default/api Service healthy right now?",
			RequiredTools:         []string{"kube_insight_health", "kube_insight_service_investigation"},
			RequiredArtifactKinds: []string{ArtifactKindK8sResourceList, ArtifactKindK8sTopology},
			RequiredAnswerTerms:   []string{"service", "endpoint"},
			MinCitations:          1,
			MaxToolCalls:          5,
			MaxToolCallDurationMS: 2000,
		},
		{
			ID:                    "oom-restart",
			Question:              "Find Pods with OOMKilled or restart evidence in default.",
			RequiredTools:         []string{"kube_insight_search"},
			RequiredArtifactKinds: []string{ArtifactKindK8sResourceList},
			RequiredAnswerTerms:   []string{"oomkilled", "pod"},
			MinCitations:          1,
			MaxToolCalls:          4,
			MaxToolCallDurationMS: 2000,
		},
		{
			ID:                    "oom-aggregate",
			Question:              "Which Pods had OOMKilled facts in the last 24 hours? Rank by fact count.",
			RequiredTools:         []string{"kube_insight_health", "kube_insight_schema", "kube_insight_sql"},
			RequiredArtifactKinds: []string{ArtifactKindMarkdown},
			RequiredAnswerTerms:   []string{"oomkilled", "pod"},
			MinCitations:          1,
			MaxToolCalls:          4,
			MaxToolCallDurationMS: 2000,
		},
		{
			ID:                    "allocation-followup",
			Question:              "I meant allocated resources, not actual usage. Show the requests and limits distribution.",
			RequiredTools:         []string{"kube_insight_health", "kube_insight_schema", "kube_insight_sql"},
			RequiredArtifactKinds: []string{ArtifactKindMarkdown},
			RequiredAnswerTerms:   []string{"request", "limit", "allocation"},
			MinCitations:          1,
			MaxToolCalls:          6,
			MaxToolCallDurationMS: 2000,
		},
		{
			ID:                       "node-capacity",
			Question:                 "How many Nodes are there, and what are the total capacity CPU and memory?",
			AlternativeRequiredTools: [][]string{{"kube_insight_health", "kube_insight_schema", "kube_insight_sql"}, {"kube_insight_health", "kube_insight_schema", scriptedQueryToolName}},
			AlternativeArtifactKinds: [][]string{{ArtifactKindMarkdown}, {ArtifactKindToolCall}},
			RequiredAnswerTerms:      []string{"node", "cpu", "memory"},
			MinCitations:             1,
			MaxToolCalls:             5,
			MaxToolCallDurationMS:    2000,
		},
		{
			ID:                    "scripted-query-node-capacity",
			Question:              "Use one bounded scripted SQL plan to get Node count plus total capacity CPU and memory, then report the result.",
			RequiredTools:         []string{"kube_insight_schema", scriptedQueryToolName},
			RequiredArtifactKinds: []string{ArtifactKindToolCall},
			RequiredAnswerTerms:   []string{"node", "cpu", "memory"},
			MinCitations:          1,
			MaxToolCalls:          4,
			MaxToolCallDurationMS: 2000,
		},
		{
			ID:                       "recent-changes",
			Question:                 "Show recent changes for default/api.",
			AlternativeRequiredTools: [][]string{{"kube_insight_search", "kube_insight_history"}, {"kube_insight_schema", "kube_insight_sql"}},
			AlternativeArtifactKinds: [][]string{{ArtifactKindK8sHistory}, {ArtifactKindMarkdown}},
			RequiredAnswerTerms:      []string{"change", "default/api"},
			MinCitations:             1,
			MaxToolCalls:             4,
			MaxToolCallDurationMS:    2000,
		},
		{
			ID:                    "exact-recent-changes",
			Question:              "What changed recently for Deployment default/api?",
			RequiredTools:         []string{"kube_insight_health", "kube_insight_schema", "kube_insight_sql"},
			RequiredArtifactKinds: []string{ArtifactKindMarkdown},
			RequiredAnswerTerms:   []string{"change", "deployment", "default/api"},
			MinCitations:          1,
			MaxToolCalls:          3,
			MaxToolCallDurationMS: 2000,
		},
		{
			ID:                    "topology-mapping",
			Question:              "Map topology for namespace default.",
			RequiredTools:         []string{"kube_insight_search", "kube_insight_topology"},
			RequiredArtifactKinds: []string{ArtifactKindK8sTopology},
			RequiredAnswerTerms:   []string{"topology", "default"},
			MinCitations:          1,
			MaxToolCalls:          5,
			MaxToolCallDurationMS: 2000,
		},
		{
			ID:                    "js-transform-aggregation",
			Question:              "Run a bounded SQL query for recent OOMKilled Pod fact rows, then use artifact_transform_js to group the returned rows by namespace and report counts.",
			RequiredTools:         []string{"kube_insight_schema", "kube_insight_sql", jsTransformToolName},
			RequiredArtifactKinds: []string{ArtifactKindToolCall},
			RequiredAnswerTerms:   []string{"oomkilled", "namespace", "count"},
			MinCitations:          1,
			MaxToolCalls:          6,
			MaxToolCallDurationMS: 2000,
		},
	}
}

func EvaluateRunEvents(tc EvaluationCase, events []RunEvent) EvaluationReport {
	report := EvaluationReport{CaseID: tc.ID, Question: tc.Question}
	report.ToolCalls = evaluationToolCalls(events)
	report.ToolCallCount = len(report.ToolCalls)
	report.ArtifactKinds = evaluationArtifactKinds(events)
	report.Citations = countEvents(events, EventCitation)
	report.FinalAnswer = finalAnswerFromEvents(events)
	for _, call := range report.ToolCalls {
		report.TotalToolDurationMS += call.DurationMS
	}

	seenTools := map[string]bool{}
	failedTools := []EvaluationToolCall{}
	for _, call := range report.ToolCalls {
		if call.Name != "" {
			seenTools[call.Name] = true
		}
		if call.Status == "failed" {
			failedTools = append(failedTools, call)
		}
	}

	seenArtifacts := map[string]bool{}
	for _, kind := range report.ArtifactKinds {
		seenArtifacts[kind] = true
	}

	report.Missing.Tools = missingRequiredSet(tc.RequiredTools, tc.AlternativeRequiredTools, seenTools)
	report.Missing.ArtifactKinds = missingRequiredSet(tc.RequiredArtifactKinds, tc.AlternativeArtifactKinds, seenArtifacts)
	report.Missing.AnswerTerms = missingAnswerTerms(tc.RequiredAnswerTerms, report.FinalAnswer)

	report.addCheck("required tools", len(report.Missing.Tools) == 0, missingMessage("missing tools", report.Missing.Tools))
	report.addCheck("required artifacts", len(report.Missing.ArtifactKinds) == 0, missingMessage("missing artifact kinds", report.Missing.ArtifactKinds))
	report.addCheck("citations", report.Citations >= tc.MinCitations, fmt.Sprintf("got %d citations, want at least %d", report.Citations, tc.MinCitations))
	report.addCheck("answer terms", len(report.Missing.AnswerTerms) == 0, missingMessage("missing answer terms", report.Missing.AnswerTerms))

	if !tc.AllowFailedTools {
		report.addCheck("tool failures", len(failedTools) == 0, fmt.Sprintf("got %d failed tool calls", len(failedTools)))
	}
	if tc.MaxToolCalls > 0 {
		report.addCheck("tool call count", report.ToolCallCount <= tc.MaxToolCalls, fmt.Sprintf("got %d tool calls, want at most %d", report.ToolCallCount, tc.MaxToolCalls))
	}
	if tc.MaxToolCallDurationMS > 0 {
		report.addCheck("per-tool latency", toolCallsWithinBudget(report.ToolCalls, tc.MaxToolCallDurationMS), fmt.Sprintf("tool call exceeded %dms", tc.MaxToolCallDurationMS))
	}
	if tc.MaxTotalToolDurationMS > 0 {
		report.addCheck("total tool latency", report.TotalToolDurationMS <= tc.MaxTotalToolDurationMS, fmt.Sprintf("total tool duration %dms exceeds %dms", report.TotalToolDurationMS, tc.MaxTotalToolDurationMS))
	}

	report.Passed = true
	for _, check := range report.Checks {
		if !check.Passed {
			report.Passed = false
			break
		}
	}
	return report
}

func (r *EvaluationReport) addCheck(name string, passed bool, message string) {
	check := EvaluationCheck{Name: name, Passed: passed}
	if !passed {
		check.Message = message
	}
	r.Checks = append(r.Checks, check)
}

func evaluationToolCalls(events []RunEvent) []EvaluationToolCall {
	callsByID := map[string]EvaluationToolCall{}
	order := []string{}
	for _, event := range events {
		if event.Type != EventToolAudit && event.Type != EventToolCompleted && event.Type != EventToolFailed {
			continue
		}
		var data ToolCallEventData
		if event.Type == EventToolAudit {
			var audit ToolAuditEventData
			if err := json.Unmarshal(event.Data, &audit); err != nil {
				continue
			}
			data = ToolCallEventData{
				ToolCallID: audit.ToolCallID,
				Name:       audit.Name,
				Status:     audit.Status,
				DurationMS: audit.DurationMS,
				Error:      audit.Error,
			}
		} else if err := json.Unmarshal(event.Data, &data); err != nil {
			continue
		}
		key := data.ToolCallID
		if key == "" {
			key = fmt.Sprintf("%s-%d", data.Name, len(order)+1)
		}
		if _, ok := callsByID[key]; !ok {
			order = append(order, key)
		}
		callsByID[key] = EvaluationToolCall{ID: data.ToolCallID, Name: data.Name, Status: data.Status, DurationMS: data.DurationMS, Error: data.Error}
	}
	calls := make([]EvaluationToolCall, 0, len(order))
	for _, key := range order {
		calls = append(calls, callsByID[key])
	}
	return calls
}

func evaluationArtifactKinds(events []RunEvent) []string {
	seen := map[string]bool{}
	kinds := []string{}
	for _, event := range events {
		if event.Type != EventArtifact && event.Type != EventArtifactUpdate {
			continue
		}
		var data ArtifactEventData
		if err := json.Unmarshal(event.Data, &data); err != nil || data.Artifact.Kind == "" {
			continue
		}
		if !seen[data.Artifact.Kind] {
			seen[data.Artifact.Kind] = true
			kinds = append(kinds, data.Artifact.Kind)
		}
	}
	slices.Sort(kinds)
	return kinds
}

func countEvents(events []RunEvent, eventType RunEventType) int {
	count := 0
	for _, event := range events {
		if event.Type == eventType {
			count++
		}
	}
	return count
}

func finalAnswerFromEvents(events []RunEvent) string {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type != EventFinalAnswer && events[i].Type != EventMessageDone {
			continue
		}
		var data MessageEventData
		if err := json.Unmarshal(events[i].Data, &data); err == nil && data.Content != "" {
			return data.Content
		}
	}
	return ""
}

func missingRequiredSet(required []string, alternatives [][]string, seen map[string]bool) []string {
	if len(alternatives) == 0 {
		return missingStrings(required, seen)
	}
	best := []string(nil)
	for _, candidate := range alternatives {
		missing := missingStrings(candidate, seen)
		if len(missing) == 0 {
			return nil
		}
		if best == nil || len(missing) < len(best) {
			best = missing
		}
	}
	return best
}

func missingStrings(required []string, seen map[string]bool) []string {
	missing := []string{}
	for _, value := range required {
		if !seen[value] {
			missing = append(missing, value)
		}
	}
	return missing
}

func missingAnswerTerms(required []string, answer string) []string {
	answer = strings.ToLower(answer)
	missing := []string{}
	for _, term := range required {
		if !strings.Contains(answer, strings.ToLower(term)) {
			missing = append(missing, term)
		}
	}
	return missing
}

func missingMessage(prefix string, missing []string) string {
	if len(missing) == 0 {
		return ""
	}
	return prefix + ": " + strings.Join(missing, ", ")
}

func toolCallsWithinBudget(calls []EvaluationToolCall, maxDurationMS int64) bool {
	for _, call := range calls {
		if call.DurationMS > maxDurationMS {
			return false
		}
	}
	return true
}
