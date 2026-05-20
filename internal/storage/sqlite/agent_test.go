package sqlite

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"kube-insight/internal/agent"
)

func TestAgentStorePersistsSessionsRunsAndEvents(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "kube-insight.db")
	store, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	session, err := store.CreateSession(ctx, agent.CreateSessionInput{Title: "API restart", Provider: "openai-compatible", Model: "mimo-v2.5-pro"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.CreateRun(ctx, session.ID, agent.CreateRunInput{Input: "why did api restart?", Provider: session.Provider, Model: session.Model, Metadata: json.RawMessage(`{"source":"test"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateRunStatus(ctx, run.ID, agent.RunRunning, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendRunEvent(ctx, run.ID, agent.AppendEventInput{Type: agent.EventRunCreated, Data: json.RawMessage(`{"runId":"` + run.ID + `"}`)}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendRunEvent(ctx, run.ID, agent.AppendEventInput{Type: agent.EventMessageDelta, Data: json.RawMessage(`{"delta":"checking"}`)}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	gotSession, err := reopened.GetSession(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotSession.Title != "API restart" || len(gotSession.Runs) != 1 {
		t.Fatalf("session after reopen = %#v", gotSession)
	}
	gotRun := gotSession.Runs[0]
	if gotRun.ID != run.ID || gotRun.Status != agent.RunRunning || gotRun.StartedAt == nil || string(gotRun.Metadata) != `{"source":"test"}` {
		t.Fatalf("run after reopen = %#v metadata=%s", gotRun, string(gotRun.Metadata))
	}
	events, err := reopened.ListRunEvents(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Sequence != 1 || events[1].Sequence != 2 || events[1].Type != agent.EventMessageDelta {
		t.Fatalf("events after reopen = %#v", events)
	}
}

func TestAgentStoreMissingRecordsUseAgentErrors(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "kube-insight.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if _, err := store.GetSession(ctx, "missing"); err != agent.ErrSessionNotFound {
		t.Fatalf("GetSession err = %v", err)
	}
	if _, err := store.CreateRun(ctx, "missing", agent.CreateRunInput{Input: "test"}); err != agent.ErrSessionNotFound {
		t.Fatalf("CreateRun err = %v", err)
	}
	if _, err := store.GetRun(ctx, "missing"); err != agent.ErrRunNotFound {
		t.Fatalf("GetRun err = %v", err)
	}
	if _, err := store.ListRunEvents(ctx, "missing"); err != agent.ErrRunNotFound {
		t.Fatalf("ListRunEvents err = %v", err)
	}
}
