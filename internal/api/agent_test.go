package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	body := getBody(t, server.URL+"/api/v1/agent/sessions?limit=5", http.StatusOK)
	for _, want := range []string{`"sessions": [`, `"title": "API investigation"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("session list missing %q: %s", want, body)
		}
	}

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

	body = getBody(t, server.URL+"/api/v1/agent/runs?limit=5", http.StatusOK)
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

	assertDELETEStatus(t, server.URL+"/api/v1/agent/sessions/"+session.ID, http.StatusNoContent)
	getBody(t, server.URL+"/api/v1/agent/sessions/"+session.ID, http.StatusNotFound)
	getBody(t, server.URL+"/api/v1/agent/runs/"+run.ID+"/events", http.StatusNotFound)
	body = getBody(t, server.URL+"/api/v1/agent/sessions?limit=5", http.StatusOK)
	if strings.Contains(body, session.ID) {
		t.Fatalf("deleted session still listed: %s", body)
	}
}

func TestWriteServerSentEventsUsesSequenceAndIDCursor(t *testing.T) {
	events := []agent.RunEvent{
		{ID: "event_a", RunID: "run_1", Sequence: 1, Type: agent.EventRunStarted},
		{ID: "event_b", RunID: "run_1", Sequence: 1, Type: agent.EventMessageDelta},
		{ID: "event_c", RunID: "run_1", Sequence: 2, Type: agent.EventRunCompleted},
	}
	var first bytes.Buffer
	cursor, ok := writeServerSentEvents(&first, events[:1], agentRunEventCursor{})
	if !ok {
		t.Fatal("first write failed")
	}
	var second bytes.Buffer
	cursor, ok = writeServerSentEvents(&second, events, cursor)
	if !ok {
		t.Fatal("second write failed")
	}
	body := second.String()
	if strings.Contains(body, "event_a") || !strings.Contains(body, "event_b") || !strings.Contains(body, "event_c") {
		t.Fatalf("unexpected replay body after cursor %#v: %s", cursor, body)
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
	postJSON(t, server.URL+"/api/v1/agent/sessions/"+session.ID+"/runs", `{"input":"is the api healthy?","metadata":{"clientContext":{"sentAt":"2026-05-24T15:30:00.000Z","localTime":"2026-05-24T23:30:00+08:00","timeZone":"Asia/Shanghai","timezoneOffsetMinutes":480,"locale":"zh-CN","languages":["zh-CN","en-US"],"pageURL":"http://127.0.0.1:5173/"}}}`, http.StatusCreated, &run)
	if run.ID == "" || run.Status != "queued" {
		t.Fatalf("run = %#v", run)
	}

	select {
	case input := <-runner.inputs:
		if input.RunID != run.ID || len(input.Messages) != 2 || input.Messages[0].Role != agent.RoleSystem || input.Messages[1].Content != "is the api healthy?" {
			t.Fatalf("runner input = %#v", input)
		}
		for _, want := range []string{"Client context for this run", "UTC for tool arguments", "render final-answer timestamps in the client time zone", "IANA time zone", "2026-05-24T23:30:00+08:00", "Asia/Shanghai", "zh-CN"} {
			if !strings.Contains(input.Messages[0].Content, want) {
				t.Fatalf("client context missing %q: %s", want, input.Messages[0].Content)
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runner was not started")
	}

	body := getBody(t, server.URL+"/api/v1/agent/runs/"+run.ID+"/events?follow=true", http.StatusOK)
	for _, want := range []string{"event: run.created", "event: completion.message", "event: run.started", "event: answer.final", "event: run.completed", `"content":"checked cluster health"`, "is the api healthy?"} {
		if !strings.Contains(body, want) {
			t.Fatalf("events missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, `"transcript"`) {
		t.Fatalf("run.created should not contain cumulative transcript: %s", body)
	}
}

func TestCreateAgentRunReplaysPriorConversationMessages(t *testing.T) {
	runner := &fakeAgentRunner{inputs: make(chan agent.EinoRunInput, 2)}
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

	var first struct {
		ID string `json:"id"`
	}
	postJSON(t, server.URL+"/api/v1/agent/sessions/"+session.ID+"/runs", `{"input":"最近有没有 OOM 现象？"}`, http.StatusCreated, &first)
	select {
	case input := <-runner.inputs:
		if input.RunID != first.ID || len(input.Messages) != 1 || input.Messages[0].Content != "最近有没有 OOM 现象？" {
			t.Fatalf("first runner input = %#v", input)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("first runner input missing")
	}
	body := getBody(t, server.URL+"/api/v1/agent/runs/"+first.ID+"/events?follow=true", http.StatusOK)
	for _, want := range []string{"event: completion.message", "最近有没有 OOM 现象？"} {
		if !strings.Contains(body, want) {
			t.Fatalf("first run events missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, `"transcript"`) {
		t.Fatalf("first run should not contain cumulative transcript: %s", body)
	}

	var second struct {
		ID string `json:"id"`
	}
	postJSON(t, server.URL+"/api/v1/agent/sessions/"+session.ID+"/runs", `{"input":"最近1小时内呢"}`, http.StatusCreated, &second)
	select {
	case input := <-runner.inputs:
		if input.RunID != second.ID || len(input.Messages) != 3 {
			t.Fatalf("second runner input = %#v", input)
		}
		wantMessages := []agent.Message{
			{Role: agent.RoleUser, Content: "最近有没有 OOM 现象？"},
			{Role: agent.RoleAssistant, Content: "checked cluster health"},
			{Role: agent.RoleUser, Content: "最近1小时内呢"},
		}
		for i, want := range wantMessages {
			if input.Messages[i].Role != want.Role || input.Messages[i].Content != want.Content {
				t.Fatalf("message %d = %#v, want %#v", i, input.Messages[i], want)
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second runner input missing")
	}
}

func TestCreateAgentRunStreamsArtifactAndCitationEvents(t *testing.T) {
	runner := &artifactAgentRunner{}
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
		ID string `json:"id"`
	}
	postJSON(t, server.URL+"/api/v1/agent/sessions/"+session.ID+"/runs", `{"input":"show health proof"}`, http.StatusCreated, &run)

	body := getBody(t, server.URL+"/api/v1/agent/runs/"+run.ID+"/events?follow=true", http.StatusOK)
	for _, want := range []string{
		"event: artifact.created",
		"event: citation.created",
		`"kind":"markdown"`,
		`"title":"Health evidence"`,
		`"artifactId":"artifact_health"`,
		`"source":"kube_insight_health"`,
		"event: answer.final",
		"event: run.completed",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("events missing %q: %s", want, body)
		}
	}
}

func TestAgentRunEventsFollowFlushesDelayedTerminalEvent(t *testing.T) {
	runner := &delayedTerminalEventRunner{statusCompleted: make(chan struct{}), delay: 150 * time.Millisecond}
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
		ID string `json:"id"`
	}
	postJSON(t, server.URL+"/api/v1/agent/sessions/"+session.ID+"/runs", `{"input":"delayed terminal event"}`, http.StatusCreated, &run)
	select {
	case <-runner.statusCompleted:
	case <-time.After(2 * time.Second):
		t.Fatal("runner did not reach terminal status")
	}

	body := getBody(t, server.URL+"/api/v1/agent/runs/"+run.ID+"/events?follow=true", http.StatusOK)
	for _, want := range []string{"event: answer.final", "event: run.completed", "delayed terminal answer"} {
		if !strings.Contains(body, want) {
			t.Fatalf("events missing %q: %s", want, body)
		}
	}
}

func TestRetryAgentRunCreatesNewRunFromTerminalRun(t *testing.T) {
	runner := &retryingAgentRunner{inputs: make(chan agent.EinoRunInput, 3)}
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
		ID string `json:"id"`
	}
	postJSON(t, server.URL+"/api/v1/agent/sessions/"+session.ID+"/runs", `{"input":"retry this","provider":"openai-compatible","model":"mimo-v2.5-pro","metadata":{"source":"test"}}`, http.StatusCreated, &run)
	select {
	case input := <-runner.inputs:
		if len(input.Messages) != 1 || input.Messages[0].Content != "retry this" {
			t.Fatalf("initial retry runner input = %#v", input.Messages)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("initial retry runner input missing")
	}
	body := getBody(t, server.URL+"/api/v1/agent/runs/"+run.ID+"/events?follow=true", http.StatusOK)
	if !strings.Contains(body, "event: run.failed") || !strings.Contains(body, "provider temporarily unavailable") {
		t.Fatalf("failed run events = %s", body)
	}

	var retried struct {
		ID        string          `json:"id"`
		SessionID string          `json:"sessionId"`
		Status    string          `json:"status"`
		Input     string          `json:"input"`
		Provider  string          `json:"provider"`
		Model     string          `json:"model"`
		Metadata  json.RawMessage `json:"metadata"`
	}
	postJSON(t, server.URL+"/api/v1/agent/runs/"+run.ID+"/retry", `{"metadata":{"clientContext":{"sentAt":"2026-05-25T02:10:00.000Z","timeZone":"Asia/Shanghai"}}}`, http.StatusCreated, &retried)
	if retried.ID == "" || retried.ID == run.ID || retried.SessionID != session.ID || retried.Status != "queued" || retried.Input != "retry this" || retried.Provider != "openai-compatible" || retried.Model != "mimo-v2.5-pro" {
		t.Fatalf("retried run = %#v", retried)
	}
	var metadata map[string]any
	if err := json.Unmarshal(retried.Metadata, &metadata); err != nil {
		t.Fatal(err)
	}
	clientContext, _ := metadata["clientContext"].(map[string]any)
	if metadata["retryOfRunId"] != run.ID || metadata["retryRootRunId"] != run.ID || metadata["source"] != "test" || clientContext["sentAt"] != "2026-05-25T02:10:00.000Z" {
		t.Fatalf("retry metadata = %#v", metadata)
	}
	select {
	case input := <-runner.inputs:
		if len(input.Messages) != 2 || input.Messages[0].Role != agent.RoleSystem || input.Messages[1].Content != "retry this" {
			t.Fatalf("retried runner input = %#v", input.Messages)
		}
		for _, message := range input.Messages {
			if message.Role == agent.RoleAssistant || strings.Contains(message.Content, "provider temporarily unavailable") {
				t.Fatalf("retry should rewind before retried run, got messages %#v", input.Messages)
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("retried runner input missing")
	}

	body = getBody(t, server.URL+"/api/v1/agent/runs/"+retried.ID+"/events?follow=true", http.StatusOK)
	for _, want := range []string{"event: run.created", "event: run.started", "event: answer.final", "event: run.completed", "retried successfully"} {
		if !strings.Contains(body, want) {
			t.Fatalf("retried events missing %q: %s", want, body)
		}
	}

	var retriedCompleted struct {
		ID        string          `json:"id"`
		SessionID string          `json:"sessionId"`
		Status    string          `json:"status"`
		Input     string          `json:"input"`
		Metadata  json.RawMessage `json:"metadata"`
	}
	postJSON(t, server.URL+"/api/v1/agent/runs/"+retried.ID+"/retry", `{}`, http.StatusCreated, &retriedCompleted)
	if retriedCompleted.ID == "" || retriedCompleted.ID == retried.ID || retriedCompleted.SessionID != session.ID || retriedCompleted.Status != "queued" || retriedCompleted.Input != "retry this" {
		t.Fatalf("retried completed run = %#v", retriedCompleted)
	}
	metadata = map[string]any{}
	if err := json.Unmarshal(retriedCompleted.Metadata, &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata["retryOfRunId"] != retried.ID || metadata["retryRootRunId"] != run.ID || metadata["source"] != "test" {
		t.Fatalf("retry completed metadata = %#v", metadata)
	}
}

func TestCompactAgentRetentionEndpointPrunesRetryBranchAndArtifacts(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	store := agent.NewMemoryStore()
	handler, err := NewServer(ServerOptions{
		OpenStore: func(context.Context) (ReadStore, error) {
			closed := false
			return fakeReadStore{closed: &closed}, nil
		},
		AgentStore: store,
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	ctx := context.Background()
	session, err := store.CreateSession(ctx, agent.CreateSessionInput{})
	if err != nil {
		t.Fatal(err)
	}
	run1, err := store.CreateRun(ctx, session.ID, agent.CreateRunInput{Input: "first"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateRunStatus(ctx, run1.ID, agent.RunCompleted, ""); err != nil {
		t.Fatal(err)
	}
	run2, err := store.CreateRun(ctx, session.ID, agent.CreateRunInput{Input: "retry", Metadata: json.RawMessage(`{"retryOfRunId":"` + run1.ID + `"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendRunEvent(ctx, run2.ID, agent.AppendEventInput{Type: agent.EventArtifact, Data: mustJSON(agent.ArtifactEventData{Artifact: agent.Artifact{ID: "artifact_drop", Kind: agent.ArtifactKindMarkdown}})}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateRunStatus(ctx, run2.ID, agent.RunCompleted, ""); err != nil {
		t.Fatal(err)
	}

	var report agent.RetentionReport
	postJSON(t, server.URL+"/api/v1/agent/retention/compact", `{}`, http.StatusOK, &report)
	if report.SupersededRunsDeleted != 1 || report.UnreferencedArtifactEventsDeleted != 1 {
		t.Fatalf("report = %#v", report)
	}
	if _, err := store.GetRun(ctx, run1.ID); err != agent.ErrRunNotFound {
		t.Fatalf("run1 err = %v", err)
	}
	events, err := store.ListRunEvents(ctx, run2.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("run2 events after compaction = %#v", events)
	}
}

func TestAgentRetentionRunsPeriodically(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	store := agent.NewMemoryStore()
	handler, err := NewServer(ServerOptions{
		OpenStore: func(context.Context) (ReadStore, error) {
			closed := false
			return fakeReadStore{closed: &closed}, nil
		},
		AgentStore:               store,
		AgentRetentionInterval:   10 * time.Millisecond,
		AgentRetentionRunOnStart: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer handler.Close()

	ctx := context.Background()
	session, err := store.CreateSession(ctx, agent.CreateSessionInput{})
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.CreateRun(ctx, session.ID, agent.CreateRunInput{Input: "periodic retention"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendRunEvent(ctx, run.ID, agent.AppendEventInput{Type: agent.EventArtifact, Data: mustJSON(agent.ArtifactEventData{Artifact: agent.Artifact{ID: "artifact_drop", Kind: agent.ArtifactKindMarkdown}})}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateRunStatus(ctx, run.ID, agent.RunCompleted, ""); err != nil {
		t.Fatal(err)
	}

	deadline := time.After(2 * time.Second)
	for {
		events, err := store.ListRunEvents(ctx, run.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(events) == 0 {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("retention did not prune unreferenced artifact events: %#v", events)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestCancelAgentRunCancelsRunnerContext(t *testing.T) {
	runner := newBlockingAgentRunner()
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
	postJSON(t, server.URL+"/api/v1/agent/sessions/"+session.ID+"/runs", `{"input":"long running check"}`, http.StatusCreated, &run)
	select {
	case <-runner.started:
	case <-time.After(2 * time.Second):
		t.Fatal("runner did not start")
	}

	postJSON(t, server.URL+"/api/v1/agent/runs/"+run.ID+"/cancel", `{}`, http.StatusOK, &run)
	if run.Status != "cancelled" {
		t.Fatalf("cancelled run = %#v", run)
	}
	select {
	case <-runner.cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("runner context was not cancelled")
	}

	body := getBody(t, server.URL+"/api/v1/agent/runs/"+run.ID+"/events", http.StatusOK)
	for _, want := range []string{"event: run.started", "event: run.cancelled"} {
		if !strings.Contains(body, want) {
			t.Fatalf("events missing %q: %s", want, body)
		}
	}
}

func TestServerCloseWaitsForInFlightAgentRunBeforeCloseFunc(t *testing.T) {
	runner := newCloseWaitAgentRunner()
	closeCalled := make(chan struct{})
	handler, err := NewServer(ServerOptions{
		OpenStore: func(context.Context) (ReadStore, error) {
			closed := false
			return fakeReadStore{closed: &closed}, nil
		},
		AgentRunner: runner,
		Close: func() error {
			close(closeCalled)
			return nil
		},
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
	postJSON(t, server.URL+"/api/v1/agent/sessions/"+session.ID+"/runs", `{"input":"long running check"}`, http.StatusCreated, &run)
	select {
	case <-runner.started:
	case <-time.After(2 * time.Second):
		t.Fatal("runner did not start")
	}

	closeDone := make(chan error, 1)
	go func() { closeDone <- handler.Close() }()
	select {
	case <-runner.cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("runner context was not cancelled")
	}
	select {
	case <-closeCalled:
		t.Fatal("server close func ran before agent runner returned")
	case <-time.After(50 * time.Millisecond):
	}
	close(runner.release)
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server close did not finish")
	}
	select {
	case <-closeCalled:
	default:
		t.Fatal("server close func was not called")
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
	assertDELETEStatus(t, server.URL+"/api/v1/agent/sessions/missing", http.StatusNotFound)
}

type delayedTerminalEventRunner struct {
	statusCompleted chan struct{}
	delay           time.Duration
}

func (r *delayedTerminalEventRunner) Run(ctx context.Context, input agent.EinoRunInput) (agent.EinoRunResult, error) {
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
		Data: mustJSON(agent.MessageEventData{Role: agent.RoleAssistant, Content: "delayed terminal answer"}),
	}); err != nil {
		return agent.EinoRunResult{}, err
	}
	run, err = input.Store.UpdateRunStatus(ctx, input.RunID, agent.RunCompleted, "")
	if err != nil {
		return agent.EinoRunResult{}, err
	}
	close(r.statusCompleted)
	timer := time.NewTimer(r.delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return agent.EinoRunResult{}, ctx.Err()
	case <-timer.C:
	}
	if _, err := input.Store.AppendRunEvent(ctx, input.RunID, agent.AppendEventInput{
		Type: agent.EventRunCompleted,
		Data: mustJSON(agent.RunStatusEventData{RunID: input.RunID, SessionID: run.SessionID, Status: run.Status}),
	}); err != nil {
		return agent.EinoRunResult{}, err
	}
	return agent.EinoRunResult{FinalAnswer: "delayed terminal answer", Events: 1}, nil
}
