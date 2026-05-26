package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"kube-insight/internal/agent"
)

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

type artifactAgentRunner struct{}

func (r *artifactAgentRunner) Run(ctx context.Context, input agent.EinoRunInput) (agent.EinoRunResult, error) {
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
		Type: agent.EventArtifact,
		Data: mustJSON(agent.ArtifactEventData{Artifact: agent.Artifact{
			ID:    "artifact_health",
			Kind:  "markdown",
			Title: "Health evidence",
			Data:  json.RawMessage(`{"markdown":"### Health evidence\\n\\nhealthy=3 stale=0"}`),
		}}),
	}); err != nil {
		return agent.EinoRunResult{}, err
	}
	if _, err := input.Store.AppendRunEvent(ctx, input.RunID, agent.AppendEventInput{
		Type: agent.EventCitation,
		Data: mustJSON(agent.CitationEventData{Citation: agent.Citation{
			ID:         "citation_health",
			ArtifactID: "artifact_health",
			Text:       "Health evidence",
			Target:     json.RawMessage(`{"type":"artifact","source":"kube_insight_health"}`),
		}}),
	}); err != nil {
		return agent.EinoRunResult{}, err
	}
	if _, err := input.Store.AppendRunEvent(ctx, input.RunID, agent.AppendEventInput{
		Type: agent.EventFinalAnswer,
		Data: mustJSON(agent.MessageEventData{Role: agent.RoleAssistant, Content: "health proof is attached"}),
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
	return agent.EinoRunResult{FinalAnswer: "health proof is attached", Events: 4}, nil
}

type fakeFailingAgentRunner struct {
	err error
}

func (r fakeFailingAgentRunner) Run(context.Context, agent.EinoRunInput) (agent.EinoRunResult, error) {
	return agent.EinoRunResult{}, r.err
}

type blockingAgentRunner struct {
	started   chan struct{}
	cancelled chan struct{}
	once      sync.Once
}

func newBlockingAgentRunner() *blockingAgentRunner {
	return &blockingAgentRunner{started: make(chan struct{}), cancelled: make(chan struct{})}
}

func (r *blockingAgentRunner) Run(ctx context.Context, input agent.EinoRunInput) (agent.EinoRunResult, error) {
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
	r.once.Do(func() { close(r.started) })
	<-ctx.Done()
	close(r.cancelled)
	return agent.EinoRunResult{}, ctx.Err()
}

type retryingAgentRunner struct {
	mu    sync.Mutex
	calls int
}

func (r *retryingAgentRunner) Run(ctx context.Context, input agent.EinoRunInput) (agent.EinoRunResult, error) {
	r.mu.Lock()
	r.calls++
	call := r.calls
	r.mu.Unlock()
	if call == 1 {
		return agent.EinoRunResult{}, errors.New("provider temporarily unavailable")
	}
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
		Data: mustJSON(agent.MessageEventData{Role: agent.RoleAssistant, Content: "retried successfully"}),
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
	return agent.EinoRunResult{FinalAnswer: "retried successfully", Events: 1}, nil
}

type closeWaitAgentRunner struct {
	started   chan struct{}
	cancelled chan struct{}
	release   chan struct{}
	once      sync.Once
}

func newCloseWaitAgentRunner() *closeWaitAgentRunner {
	return &closeWaitAgentRunner{started: make(chan struct{}), cancelled: make(chan struct{}), release: make(chan struct{})}
}

func (r *closeWaitAgentRunner) Run(ctx context.Context, input agent.EinoRunInput) (agent.EinoRunResult, error) {
	r.once.Do(func() { close(r.started) })
	<-ctx.Done()
	close(r.cancelled)
	<-r.release
	return agent.EinoRunResult{}, ctx.Err()
}
