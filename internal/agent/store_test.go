package agent

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestMemoryStoreSessionRunAndEvents(t *testing.T) {
	store := NewMemoryStore()
	current := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return current }

	session, err := store.CreateSession(context.Background(), CreateSessionInput{
		Title:    "Investigate API pods",
		Provider: "openai",
		Model:    "gpt-5.2",
	})
	if err != nil {
		t.Fatal(err)
	}
	if session.ID == "" || session.Provider != "openai" || session.Model != "gpt-5.2" {
		t.Fatalf("session = %#v", session)
	}

	run, err := store.CreateRun(context.Background(), session.ID, CreateRunInput{
		Input:    "why did api restart?",
		Provider: "openai",
		Model:    "gpt-5.2",
		Metadata: json.RawMessage(`{"source":"test"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != RunQueued || run.SessionID != session.ID || run.Input == "" {
		t.Fatalf("run = %#v", run)
	}

	current = current.Add(time.Second)
	run, err = store.UpdateRunStatus(context.Background(), run.ID, RunRunning, "")
	if err != nil {
		t.Fatal(err)
	}
	if run.StartedAt == nil || run.Status != RunRunning {
		t.Fatalf("running run = %#v", run)
	}

	first, err := store.AppendRunEvent(context.Background(), run.ID, AppendEventInput{Type: "message.delta", Data: json.RawMessage(`{"text":"checking"}`)})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.AppendRunEvent(context.Background(), run.ID, AppendEventInput{Type: "tool.completed"})
	if err != nil {
		t.Fatal(err)
	}
	if first.Sequence != 1 || second.Sequence != 2 {
		t.Fatalf("event sequences = %d %d", first.Sequence, second.Sequence)
	}

	events, err := store.ListRunEvents(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Type != "message.delta" || events[1].Type != "tool.completed" {
		t.Fatalf("events = %#v", events)
	}

	current = current.Add(time.Second)
	run, err = store.UpdateRunStatus(context.Background(), run.ID, RunCompleted, "")
	if err != nil {
		t.Fatal(err)
	}
	if run.CompletedAt == nil || run.Status != RunCompleted {
		t.Fatalf("completed run = %#v", run)
	}

	loaded, err := store.GetSession(context.Background(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Runs) != 1 || loaded.Runs[0].ID != run.ID {
		t.Fatalf("loaded session = %#v", loaded)
	}
}

func TestMemoryStoreListRunsSummaryAndFilters(t *testing.T) {
	store := NewMemoryStore()
	current := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return current }
	session, err := store.CreateSession(context.Background(), CreateSessionInput{Title: "runs"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.CreateRun(context.Background(), session.ID, CreateRunInput{Input: "first"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateRunStatus(context.Background(), first.ID, RunCompleted, ""); err != nil {
		t.Fatal(err)
	}
	current = current.Add(time.Second)
	second, err := store.CreateRun(context.Background(), session.ID, CreateRunInput{Input: "second"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateRunStatus(context.Background(), second.ID, RunRunning, ""); err != nil {
		t.Fatal(err)
	}
	current = current.Add(time.Second)
	third, err := store.CreateRun(context.Background(), session.ID, CreateRunInput{Input: "third"})
	if err != nil {
		t.Fatal(err)
	}

	listed, err := store.ListRuns(context.Background(), ListRunsOptions{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if listed.Summary.Total != 3 || listed.Summary.Completed != 1 || listed.Summary.Running != 1 || listed.Summary.Queued != 1 {
		t.Fatalf("summary = %#v", listed.Summary)
	}
	if len(listed.Runs) != 2 || listed.Runs[0].ID != third.ID || listed.Runs[1].ID != second.ID {
		t.Fatalf("runs = %#v", listed.Runs)
	}
	running, err := store.ListRuns(context.Background(), ListRunsOptions{Status: RunRunning})
	if err != nil {
		t.Fatal(err)
	}
	if len(running.Runs) != 1 || running.Runs[0].ID != second.ID || running.Summary.Total != 3 {
		t.Fatalf("running list = %#v", running)
	}
}

func TestMemoryStoreNotFound(t *testing.T) {
	store := NewMemoryStore()
	_, err := store.GetSession(context.Background(), "missing")
	if !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("session err = %v", err)
	}
	_, err = store.GetRun(context.Background(), "missing")
	if !errors.Is(err, ErrRunNotFound) {
		t.Fatalf("run err = %v", err)
	}
	_, err = store.CreateRun(context.Background(), "missing", CreateRunInput{Input: "test"})
	if !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("create run err = %v", err)
	}
	_, err = store.AppendRunEvent(context.Background(), "missing", AppendEventInput{Type: "test"})
	if !errors.Is(err, ErrRunNotFound) {
		t.Fatalf("append event err = %v", err)
	}
}
