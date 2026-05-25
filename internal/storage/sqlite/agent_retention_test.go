package sqlite

import (
	"context"
	"encoding/json"
	"testing"

	"kube-insight/internal/agent"
)

func TestAgentRetentionPrunesSupersededRunsAndUnreferencedArtifacts(t *testing.T) {
	ctx := context.Background()
	store, err := Open(t.TempDir() + "/ki.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	session, err := store.CreateSession(ctx, agent.CreateSessionInput{Title: "retry"})
	if err != nil {
		t.Fatal(err)
	}
	run1 := createCompletedAgentRun(t, ctx, store, session.ID, "first", nil)
	run2 := createCompletedAgentRun(t, ctx, store, session.ID, "later", nil)
	run3 := createCompletedAgentRun(t, ctx, store, session.ID, "retry", json.RawMessage(`{"retryOfRunId":"`+run1.ID+`"}`))
	keepArtifactID := "artifact_keep"
	keepArtifact := appendArtifactEvent(t, ctx, store, run3.ID, keepArtifactID)
	dropArtifact := appendArtifactEvent(t, ctx, store, run3.ID, "artifact_drop")
	appendCitationEvent(t, ctx, store, run3.ID, keepArtifactID)

	report, err := store.ApplyAgentRetention(ctx, agent.DefaultRetentionOptions())
	if err != nil {
		t.Fatal(err)
	}
	if report.SupersededRunsDeleted != 2 || report.SupersededRunEventsDeleted == 0 || report.UnreferencedArtifactEventsDeleted != 1 {
		t.Fatalf("report = %#v", report)
	}
	if _, err := store.GetRun(ctx, run1.ID); err != agent.ErrRunNotFound {
		t.Fatalf("run1 err = %v", err)
	}
	if _, err := store.GetRun(ctx, run2.ID); err != agent.ErrRunNotFound {
		t.Fatalf("run2 err = %v", err)
	}
	if _, err := store.GetRun(ctx, run3.ID); err != nil {
		t.Fatalf("run3 should remain: %v", err)
	}
	events, err := store.ListRunEvents(ctx, run3.ID)
	if err != nil {
		t.Fatal(err)
	}
	if hasEventID(events, dropArtifact) || !hasEventID(events, keepArtifact) {
		t.Fatalf("events after retention = %#v keep=%s drop=%s", events, keepArtifact, dropArtifact)
	}
}

func createCompletedAgentRun(t *testing.T, ctx context.Context, store *Store, sessionID, input string, metadata json.RawMessage) agent.Run {
	t.Helper()
	run, err := store.CreateRun(ctx, sessionID, agent.CreateRunInput{Input: input, Metadata: metadata})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendRunEvent(ctx, run.ID, agent.AppendEventInput{Type: agent.EventFinalAnswer, Data: json.RawMessage(`{"content":"ok"}`)}); err != nil {
		t.Fatal(err)
	}
	run, err = store.UpdateRunStatus(ctx, run.ID, agent.RunCompleted, "")
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func appendArtifactEvent(t *testing.T, ctx context.Context, store *Store, runID, artifactID string) string {
	t.Helper()
	event, err := store.AppendRunEvent(ctx, runID, agent.AppendEventInput{Type: agent.EventArtifact, Data: mustJSON(t, agent.ArtifactEventData{Artifact: agent.Artifact{ID: artifactID, Kind: agent.ArtifactKindMarkdown}})})
	if err != nil {
		t.Fatal(err)
	}
	return event.ID
}

func appendCitationEvent(t *testing.T, ctx context.Context, store *Store, runID, artifactID string) {
	t.Helper()
	_, err := store.AppendRunEvent(ctx, runID, agent.AppendEventInput{Type: agent.EventCitation, Data: mustJSON(t, agent.CitationEventData{Citation: agent.Citation{ID: "citation_1", ArtifactID: artifactID}})})
	if err != nil {
		t.Fatal(err)
	}
}

func hasEventID(events []agent.RunEvent, id string) bool {
	for _, event := range events {
		if event.ID == id {
			return true
		}
	}
	return false
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
