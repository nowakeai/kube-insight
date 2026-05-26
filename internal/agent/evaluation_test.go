package agent

import (
	"testing"
	"time"
)

func TestDefaultEvaluationCasesCoverRepresentativeAgentQuestions(t *testing.T) {
	cases := DefaultEvaluationCases()
	if len(cases) < 5 {
		t.Fatalf("default evaluation cases = %d, want at least 5", len(cases))
	}
	wantIDs := map[string]bool{
		"service-health":               false,
		"oom-restart":                  false,
		"oom-aggregate":                false,
		"allocation-followup":          false,
		"node-capacity":                false,
		"scripted-query-node-capacity": false,
		"recent-changes":               false,
		"history-diff":                 false,
		"exact-recent-changes":         false,
		"schema-sql-evidence":          false,
		"topology-mapping":             false,
		"js-transform-aggregation":     false,
	}
	for _, tc := range cases {
		if tc.ID == "" || tc.Question == "" {
			t.Fatalf("case must have id and question: %#v", tc)
		}
		if (len(tc.RequiredTools) == 0 && len(tc.AlternativeRequiredTools) == 0) || len(tc.RequiredAnswerTerms) == 0 {
			t.Fatalf("case must require tools and answer terms: %#v", tc)
		}
		if _, ok := wantIDs[tc.ID]; ok {
			wantIDs[tc.ID] = true
		}
	}
	for id, found := range wantIDs {
		if !found {
			t.Fatalf("missing default evaluation case %q", id)
		}
	}
}

func TestEvaluateRunEventsPassesServiceHealthTranscript(t *testing.T) {
	tc := DefaultEvaluationCases()[0]
	events := evaluationTranscript(
		[]EvaluationToolCall{
			{ID: "tool_health", Name: "kube_insight_health", Status: "completed", DurationMS: 120},
			{ID: "tool_service", Name: "kube_insight_service_investigation", Status: "completed", DurationMS: 310},
		},
		[]string{ArtifactKindK8sResourceList, ArtifactKindK8sTopology},
		1,
		"The default/api Service has healthy EndpointSlice evidence and ready Pods. Evidence: Service default/api, EndpointSlice default/api-abc.",
	)

	report := EvaluateRunEvents(tc, events)
	if !report.Passed {
		t.Fatalf("report should pass: %#v", report)
	}
	if report.TotalToolDurationMS != 430 {
		t.Fatalf("total tool duration = %d", report.TotalToolDurationMS)
	}
}

func TestEvaluateRunEventsPassesScriptedQueryTranscript(t *testing.T) {
	var tc EvaluationCase
	for _, candidate := range DefaultEvaluationCases() {
		if candidate.ID == "scripted-query-node-capacity" {
			tc = candidate
			break
		}
	}
	if tc.ID == "" {
		t.Fatal("scripted-query-node-capacity case missing")
	}
	events := evaluationTranscript(
		[]EvaluationToolCall{
			{ID: "tool_schema", Name: "kube_insight_schema", Status: "completed", DurationMS: 80},
			{ID: "tool_script", Name: scriptedQueryToolName, Status: "completed", DurationMS: 320},
		},
		[]string{ArtifactKindToolCall},
		1,
		"The cluster has 18 Node objects with 144 CPU cores and 576 GiB memory capacity.",
	)

	report := EvaluateRunEvents(tc, events)
	if !report.Passed {
		t.Fatalf("report should pass: %#v", report)
	}
}

func TestEvaluateRunEventsPassesHistoryDiffTranscript(t *testing.T) {
	tc := evaluationCaseByIDForTest(t, "history-diff")
	events := evaluationTranscript(
		[]EvaluationToolCall{
			{ID: "tool_history", Name: "kube_insight_history", Status: "completed", DurationMS: 180},
		},
		[]string{ArtifactKindK8sHistory},
		1,
		"Pod default/api-0 changed when the api container lastState reason became OOMKilled.",
	)

	report := EvaluateRunEvents(tc, events)
	if !report.Passed {
		t.Fatalf("report should pass: %#v", report)
	}
}

func TestEvaluateRunEventsPassesSchemaSQLEvidenceTranscript(t *testing.T) {
	tc := evaluationCaseByIDForTest(t, "schema-sql-evidence")
	events := evaluationTranscript(
		[]EvaluationToolCall{
			{ID: "tool_schema", Name: "kube_insight_schema", Status: "completed", DurationMS: 80},
			{ID: "tool_sql", Name: "kube_insight_sql", Status: "completed", DurationMS: 170},
		},
		[]string{ArtifactKindMarkdown},
		1,
		"Deployment default/api had memory request and limit changes according to the SQL change rows.",
	)

	report := EvaluateRunEvents(tc, events)
	if !report.Passed {
		t.Fatalf("report should pass: %#v", report)
	}
}

func TestEvaluateRunEventsFlagsMissingEvidence(t *testing.T) {
	tc := EvaluationCase{
		ID:                    "missing-evidence",
		Question:              "Why did api restart?",
		RequiredTools:         []string{"kube_insight_search", "kube_insight_history"},
		RequiredArtifactKinds: []string{ArtifactKindK8sHistory},
		RequiredAnswerTerms:   []string{"OOMKilled"},
		MinCitations:          1,
	}
	events := evaluationTranscript(nil, nil, 0, "The api probably restarted because of memory pressure.")

	report := EvaluateRunEvents(tc, events)
	if report.Passed {
		t.Fatalf("report should fail: %#v", report)
	}
	if len(report.Missing.Tools) != 2 || len(report.Missing.ArtifactKinds) != 1 || len(report.Missing.AnswerTerms) != 1 {
		t.Fatalf("missing details = %#v", report.Missing)
	}
	if got := failedCheckNames(report); got != "required tools,required artifacts,citations,answer terms" {
		t.Fatalf("failed checks = %s", got)
	}
}

func TestEvaluateRunEventsFlagsToolFailureAndLatency(t *testing.T) {
	tc := EvaluationCase{
		ID:                    "tool-quality",
		RequiredTools:         []string{"kube_insight_sql"},
		RequiredAnswerTerms:   []string{"schema"},
		MinCitations:          0,
		MaxToolCallDurationMS: 100,
	}
	events := evaluationTranscript(
		[]EvaluationToolCall{{ID: "tool_sql", Name: "kube_insight_sql", Status: "failed", DurationMS: 250, Error: "unknown table objects"}},
		nil,
		0,
		"I retried after reading schema.",
	)

	report := EvaluateRunEvents(tc, events)
	if report.Passed {
		t.Fatalf("report should fail: %#v", report)
	}
	if got := failedCheckNames(report); got != "tool failures,per-tool latency" {
		t.Fatalf("failed checks = %s", got)
	}
}

func evaluationCaseByIDForTest(t *testing.T, id string) EvaluationCase {
	t.Helper()
	for _, tc := range DefaultEvaluationCases() {
		if tc.ID == id {
			return tc
		}
	}
	t.Fatalf("evaluation case %q missing", id)
	return EvaluationCase{}
}

func evaluationTranscript(toolCalls []EvaluationToolCall, artifactKinds []string, citations int, finalAnswer string) []RunEvent {
	now := time.Unix(1_700_000_000, 0).UTC()
	events := []RunEvent{{ID: "event_started", RunID: "run_eval", Sequence: 1, Type: EventRunStarted, CreatedAt: now, Data: jsonRaw(RunStatusEventData{RunID: "run_eval", Status: RunRunning})}}
	sequence := int64(2)
	for _, call := range toolCalls {
		events = append(events, RunEvent{
			ID:        "event_tool_" + call.ID,
			RunID:     "run_eval",
			Sequence:  sequence,
			Type:      EventToolAudit,
			CreatedAt: now,
			Data: jsonRaw(ToolAuditEventData{
				RunID:      "run_eval",
				ToolCallID: call.ID,
				Name:       call.Name,
				Status:     call.Status,
				DurationMS: call.DurationMS,
				Error:      call.Error,
			}),
		})
		sequence++
	}
	for _, kind := range artifactKinds {
		events = append(events, RunEvent{
			ID:        "event_artifact_" + kind,
			RunID:     "run_eval",
			Sequence:  sequence,
			Type:      EventArtifact,
			CreatedAt: now,
			Data:      jsonRaw(ArtifactEventData{Artifact: Artifact{ID: NewArtifactID(), Kind: kind, Title: kind}}),
		})
		sequence++
	}
	for i := 0; i < citations; i++ {
		events = append(events, RunEvent{
			ID:        "event_citation",
			RunID:     "run_eval",
			Sequence:  sequence,
			Type:      EventCitation,
			CreatedAt: now,
			Data:      jsonRaw(CitationEventData{Citation: Citation{ID: NewCitationID(), ArtifactID: "artifact_eval", Text: "proof"}}),
		})
		sequence++
	}
	if finalAnswer != "" {
		events = append(events, RunEvent{
			ID:        "event_answer",
			RunID:     "run_eval",
			Sequence:  sequence,
			Type:      EventFinalAnswer,
			CreatedAt: now,
			Data:      jsonRaw(MessageEventData{Role: RoleAssistant, Content: finalAnswer}),
		})
	}
	return events
}

func failedCheckNames(report EvaluationReport) string {
	out := ""
	for _, check := range report.Checks {
		if check.Passed {
			continue
		}
		if out != "" {
			out += ","
		}
		out += check.Name
	}
	return out
}
