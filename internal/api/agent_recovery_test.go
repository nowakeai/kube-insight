package api

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"kube-insight/internal/agent"
	"kube-insight/internal/storage/sqlite"
)

func TestAgentServerRecoversInterruptedRunsOnStart(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "kube-insight.db")
	store, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	session, err := store.CreateSession(context.Background(), agent.CreateSessionInput{Title: "interrupted"})
	if err != nil {
		t.Fatal(err)
	}
	runningRun, err := store.CreateRun(context.Background(), session.ID, agent.CreateRunInput{Input: "still running"})
	if err != nil {
		t.Fatal(err)
	}
	runningRun, err = store.UpdateRunStatus(context.Background(), runningRun.ID, agent.RunRunning, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendRunEvent(context.Background(), runningRun.ID, agent.AppendEventInput{
		Type: agent.EventRunStarted,
		Data: mustJSON(agent.RunStatusEventData{RunID: runningRun.ID, SessionID: session.ID, Status: agent.RunRunning}),
	}); err != nil {
		t.Fatal(err)
	}
	queuedRun, err := store.CreateRun(context.Background(), session.ID, agent.CreateRunInput{Input: "still queued"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	handler, err := NewServer(ServerOptions{DBPath: dbPath, AgentRunner: fakeFailingAgentRunner{err: errors.New("unused")}})
	if err != nil {
		t.Fatal(err)
	}
	defer handler.Close()

	for _, runID := range []string{runningRun.ID, queuedRun.ID} {
		run, err := handler.agentStore.GetRun(context.Background(), runID)
		if err != nil {
			t.Fatal(err)
		}
		if run.Status != agent.RunFailed {
			t.Fatalf("run %s status = %s, want failed", run.ID, run.Status)
		}
		if !strings.Contains(run.Error, "interrupted before this API server started") {
			t.Fatalf("run %s error = %q", run.ID, run.Error)
		}
		events, err := handler.agentStore.ListRunEvents(context.Background(), runID)
		if err != nil {
			t.Fatal(err)
		}
		if !runEventsContainType(events, agent.EventError) || !runEventsContainType(events, agent.EventRunFailed) {
			t.Fatalf("run %s events = %#v, want error and run.failed", run.ID, events)
		}
	}
}

func runEventsContainType(events []agent.RunEvent, eventType agent.RunEventType) bool {
	for _, event := range events {
		if event.Type == eventType {
			return true
		}
	}
	return false
}
