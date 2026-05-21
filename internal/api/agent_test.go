package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kube-insight/internal/agent"
)

func TestAgentSessionRunLifecycleEndpoints(t *testing.T) {
	handler, err := NewServer(ServerOptions{OpenStore: func(context.Context) (ReadStore, error) {
		closed := false
		return fakeReadStore{closed: &closed}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	var session struct {
		ID       string `json:"id"`
		Title    string `json:"title"`
		Provider string `json:"provider"`
		Model    string `json:"model"`
	}
	postJSON(t, server.URL+"/api/v1/agent/sessions", `{"title":"API investigation","provider":"openai-compatible","model":"mimo-v2.5-pro"}`, http.StatusCreated, &session)
	if session.ID == "" || session.Provider != "openai-compatible" || session.Model != "mimo-v2.5-pro" {
		t.Fatalf("session = %#v", session)
	}

	assertGETContains(t, server.URL+"/api/v1/agent/sessions/"+session.ID, `"title": "API investigation"`)

	var run struct {
		ID        string `json:"id"`
		SessionID string `json:"sessionId"`
		Status    string `json:"status"`
		Input     string `json:"input"`
	}
	postJSON(t, server.URL+"/api/v1/agent/sessions/"+session.ID+"/runs", `{"input":"why did api restart?","provider":"openai-compatible","model":"mimo-v2.5-pro"}`, http.StatusCreated, &run)
	if run.ID == "" || run.SessionID != session.ID || run.Status != "queued" || run.Input == "" {
		t.Fatalf("run = %#v", run)
	}

	body := getBody(t, server.URL+"/api/v1/agent/runs?limit=5", http.StatusOK)
	for _, want := range []string{`"queued": 1`, `"total": 1`, `"runs": [`} {
		if !strings.Contains(body, want) {
			t.Fatalf("run list missing %q: %s", want, body)
		}
	}

	body = getBody(t, server.URL+"/api/v1/agent/runs/"+run.ID+"/events", http.StatusOK)
	for _, want := range []string{"event: run.created", `"sequence":1`, `"type":"run.created"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("events missing %q: %s", want, body)
		}
	}

	postJSON(t, server.URL+"/api/v1/agent/runs/"+run.ID+"/cancel", `{}`, http.StatusOK, &run)
	if run.Status != "cancelled" {
		t.Fatalf("cancelled run = %#v", run)
	}
	body = getBody(t, server.URL+"/api/v1/agent/runs/"+run.ID+"/events", http.StatusOK)
	for _, want := range []string{"event: run.cancelled", `"sequence":2`, `"type":"run.cancelled"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("events missing %q: %s", want, body)
		}
	}
}

func TestCreateAgentRunStartsRunnerAndFollowEvents(t *testing.T) {
	runner := &fakeAgentRunner{inputs: make(chan agent.EinoRunInput, 1)}
	handler, err := NewServer(ServerOptions{
		OpenStore: func(context.Context) (ReadStore, error) {
			closed := false
			return fakeReadStore{closed: &closed}, nil
		},
		AgentRunner: runner,
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	var session struct {
		ID string `json:"id"`
	}
	postJSON(t, server.URL+"/api/v1/agent/sessions", `{}`, http.StatusCreated, &session)

	var run struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	postJSON(t, server.URL+"/api/v1/agent/sessions/"+session.ID+"/runs", `{"input":"is the api healthy?"}`, http.StatusCreated, &run)
	if run.ID == "" || run.Status != "queued" {
		t.Fatalf("run = %#v", run)
	}

	select {
	case input := <-runner.inputs:
		if input.RunID != run.ID || len(input.Messages) != 1 || input.Messages[0].Content != "is the api healthy?" {
			t.Fatalf("runner input = %#v", input)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runner was not started")
	}

	body := getBody(t, server.URL+"/api/v1/agent/runs/"+run.ID+"/events?follow=true", http.StatusOK)
	for _, want := range []string{"event: run.created", "event: run.started", "event: answer.final", "event: run.completed", `"content":"checked cluster health"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("events missing %q: %s", want, body)
		}
	}
}

func TestCreateAgentRunRecordsRunnerFailure(t *testing.T) {
	handler, err := NewServer(ServerOptions{
		OpenStore: func(context.Context) (ReadStore, error) {
			closed := false
			return fakeReadStore{closed: &closed}, nil
		},
		AgentRunner: fakeFailingAgentRunner{err: errors.New("chat API key env OPENAI_API_KEY is not set")},
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	var session struct {
		ID string `json:"id"`
	}
	postJSON(t, server.URL+"/api/v1/agent/sessions", `{}`, http.StatusCreated, &session)

	var run struct {
		ID string `json:"id"`
	}
	postJSON(t, server.URL+"/api/v1/agent/sessions/"+session.ID+"/runs", `{"input":"test missing key"}`, http.StatusCreated, &run)

	body := getBody(t, server.URL+"/api/v1/agent/runs/"+run.ID+"/events?follow=true", http.StatusOK)
	for _, want := range []string{"event: error", "event: run.failed", "OPENAI_API_KEY"} {
		if !strings.Contains(body, want) {
			t.Fatalf("events missing %q: %s", want, body)
		}
	}
}

func TestAgentSessionEndpointsPersistWithDBPath(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "kube-insight.db")
	handler, err := NewServer(ServerOptions{DBPath: dbPath})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)

	var session struct {
		ID string `json:"id"`
	}
	postJSON(t, server.URL+"/api/v1/agent/sessions", `{"title":"persist me"}`, http.StatusCreated, &session)
	if session.ID == "" {
		t.Fatalf("session = %#v", session)
	}
	server.Close()
	if err := handler.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := NewServer(ServerOptions{DBPath: dbPath})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	reopenedServer := httptest.NewServer(reopened)
	defer reopenedServer.Close()
	assertGETContains(t, reopenedServer.URL+"/api/v1/agent/sessions/"+session.ID, `"title": "persist me"`)
}

func TestAgentEndpointsValidateInputAndMissingIDs(t *testing.T) {
	handler, err := NewServer(ServerOptions{OpenStore: func(context.Context) (ReadStore, error) {
		closed := false
		return fakeReadStore{closed: &closed}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	getBody(t, server.URL+"/api/v1/agent/runs?status=bogus", http.StatusBadRequest)
	getBody(t, server.URL+"/api/v1/agent/runs?limit=-1", http.StatusBadRequest)
	assertPOSTStatus(t, server.URL+"/api/v1/agent/sessions/missing/runs", `{"input":"test"}`, http.StatusNotFound)
	assertPOSTStatus(t, server.URL+"/api/v1/agent/sessions", `{`, http.StatusBadRequest)

	var session struct {
		ID string `json:"id"`
	}
	postJSON(t, server.URL+"/api/v1/agent/sessions", `{}`, http.StatusCreated, &session)
	assertPOSTStatus(t, server.URL+"/api/v1/agent/sessions/"+session.ID+"/runs", `{}`, http.StatusBadRequest)
	getBody(t, server.URL+"/api/v1/agent/runs/missing/events", http.StatusNotFound)
	assertPOSTStatus(t, server.URL+"/api/v1/agent/runs/missing/cancel", `{}`, http.StatusNotFound)
}

func postJSON(t *testing.T, url, body string, wantStatus int, out any) {
	t.Helper()
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != wantStatus {
		t.Fatalf("POST %s status = %d, want %d", url, resp.StatusCode, wantStatus)
	}
	if out == nil {
		return
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		t.Fatal(err)
	}
}

func assertPOSTStatus(t *testing.T, url, body string, wantStatus int) {
	t.Helper()
	postJSON(t, url, body, wantStatus, nil)
}

func getBody(t *testing.T, url string, wantStatus int) string {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != wantStatus {
		t.Fatalf("GET %s status = %d, want %d: %s", url, resp.StatusCode, wantStatus, string(body))
	}
	return string(body)
}

type fakeAgentRunner struct {
	inputs chan agent.EinoRunInput
}

func (r *fakeAgentRunner) Run(ctx context.Context, input agent.EinoRunInput) (agent.EinoRunResult, error) {
	r.inputs <- input
	run, err := input.Store.UpdateRunStatus(ctx, input.RunID, agent.RunRunning, "")
	if err != nil {
		return agent.EinoRunResult{}, err
	}
	if _, err := input.Store.AppendRunEvent(ctx, input.RunID, agent.AppendEventInput{
		Type: agent.EventRunStarted,
		Data: mustJSON(agent.RunStatusEventData{RunID: input.RunID, SessionID: run.SessionID, Status: run.Status}),
	}); err != nil {
		return agent.EinoRunResult{}, err
	}
	if _, err := input.Store.AppendRunEvent(ctx, input.RunID, agent.AppendEventInput{
		Type: agent.EventFinalAnswer,
		Data: mustJSON(agent.MessageEventData{Role: agent.RoleAssistant, Content: "checked cluster health"}),
	}); err != nil {
		return agent.EinoRunResult{}, err
	}
	run, err = input.Store.UpdateRunStatus(ctx, input.RunID, agent.RunCompleted, "")
	if err != nil {
		return agent.EinoRunResult{}, err
	}
	if _, err := input.Store.AppendRunEvent(ctx, input.RunID, agent.AppendEventInput{
		Type: agent.EventRunCompleted,
		Data: mustJSON(agent.RunStatusEventData{RunID: input.RunID, SessionID: run.SessionID, Status: run.Status}),
	}); err != nil {
		return agent.EinoRunResult{}, err
	}
	return agent.EinoRunResult{FinalAnswer: "checked cluster health", Events: 1}, nil
}

type fakeFailingAgentRunner struct {
	err error
}

func (r fakeFailingAgentRunner) Run(context.Context, agent.EinoRunInput) (agent.EinoRunResult, error) {
	return agent.EinoRunResult{}, r.err
}
