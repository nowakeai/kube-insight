package agent

import (
	"encoding/json"
	"testing"
	"time"
)

func TestPlanRetentionPrunesCompletedRetryBranchAndUnreferencedArtifacts(t *testing.T) {
	base := time.Unix(100, 0).UTC()
	runs := []Run{
		{ID: "run_1", SessionID: "sess_1", Status: RunCompleted, CreatedAt: base},
		{ID: "run_2", SessionID: "sess_1", Status: RunCompleted, CreatedAt: base.Add(time.Second)},
		{ID: "run_3", SessionID: "sess_1", Status: RunCompleted, CreatedAt: base.Add(2 * time.Second), Metadata: json.RawMessage(`{"retryOfRunId":"run_1"}`)},
	}
	events := map[string][]RunEvent{
		"run_1": {{ID: "evt_1", RunID: "run_1", Type: EventFinalAnswer}},
		"run_2": {{ID: "evt_2", RunID: "run_2", Type: EventFinalAnswer}},
		"run_3": {
			artifactEvent("evt_keep", "artifact_keep"),
			artifactEvent("evt_drop", "artifact_drop"),
			citationEvent("evt_cite", "artifact_keep"),
		},
	}

	plan := PlanRetention(runs, events, DefaultRetentionOptions())
	if got, want := joinIDs(plan.SupersededRunIDs), "run_1,run_2"; got != want {
		t.Fatalf("superseded runs = %q, want %q", got, want)
	}
	if got, want := joinIDs(plan.UnreferencedArtifactEventIDs), "evt_drop"; got != want {
		t.Fatalf("artifact event deletes = %q, want %q", got, want)
	}
	if plan.SupersededRunEvents != 2 || plan.EventsScanned != 5 {
		t.Fatalf("plan counts = %#v", plan)
	}
}

func TestPlanRetentionPreservesOldBranchWhenRetryFailed(t *testing.T) {
	base := time.Unix(100, 0).UTC()
	runs := []Run{
		{ID: "run_1", SessionID: "sess_1", Status: RunCompleted, CreatedAt: base},
		{ID: "run_2", SessionID: "sess_1", Status: RunFailed, CreatedAt: base.Add(time.Second), Metadata: json.RawMessage(`{"retryOfRunId":"run_1"}`)},
	}
	plan := PlanRetention(runs, nil, DefaultRetentionOptions())
	if len(plan.SupersededRunIDs) != 0 {
		t.Fatalf("failed retry should not prune old branch: %#v", plan.SupersededRunIDs)
	}
}

func TestPlanRetentionDoesNotPruneArtifactsFromRunningRuns(t *testing.T) {
	runs := []Run{{ID: "run_running", SessionID: "sess_1", Status: RunRunning, CreatedAt: time.Unix(100, 0).UTC()}}
	events := map[string][]RunEvent{
		"run_running": {artifactEvent("evt_pending", "artifact_pending")},
	}

	plan := PlanRetention(runs, events, DefaultRetentionOptions())
	if len(plan.UnreferencedArtifactEventIDs) != 0 {
		t.Fatalf("running run artifacts should not be pruned: %#v", plan.UnreferencedArtifactEventIDs)
	}
}

func artifactEvent(eventID, artifactID string) RunEvent {
	return RunEvent{ID: eventID, Type: EventArtifact, Data: jsonRaw(ArtifactEventData{Artifact: Artifact{ID: artifactID, Kind: ArtifactKindMarkdown}})}
}

func citationEvent(eventID, artifactID string) RunEvent {
	return RunEvent{ID: eventID, Type: EventCitation, Data: jsonRaw(CitationEventData{Citation: Citation{ID: "citation_1", ArtifactID: artifactID}})}
}

func joinIDs(values []string) string {
	out := ""
	for i, value := range values {
		if i > 0 {
			out += ","
		}
		out += value
	}
	return out
}
