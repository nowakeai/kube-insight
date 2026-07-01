package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"kube-insight/internal/agent"
)

func TestA2AAgentCardEndpoint(t *testing.T) {
	handler, err := NewServer(ServerOptions{
		OpenStore: func(context.Context) (ReadStore, error) {
			closed := false
			return fakeReadStore{closed: &closed}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	body := getBody(t, server.URL+"/.well-known/agent-card.json", http.StatusOK)
	for _, want := range []string{
		`"name": "kube-insight"`,
		`"protocolVersion": "1.0"`,
		`"preferredTransport": "HTTP+JSON"`,
		`"url": "` + server.URL + `"`,
		`"kubernetes-retained-evidence-investigation"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("agent card missing %q: %s", want, body)
		}
	}
}

func TestA2ASendMessageCreatesCompletedTask(t *testing.T) {
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

	var response a2aSendMessageResponse
	postJSON(t, server.URL+"/message:send", `{"message":{"messageId":"msg_1","role":"user","parts":[{"kind":"text","text":"why did api restart?"}]},"configuration":{"historyLength":2}}`, http.StatusOK, &response)
	if response.Task == nil {
		t.Fatal("A2A response did not include task")
	}
	if response.Task.ID == "" || response.Task.ContextID == "" || response.Task.Status.State != "completed" {
		t.Fatalf("task = %#v", response.Task)
	}
	if len(response.Task.Artifacts) != 1 || !strings.Contains(response.Task.Artifacts[0].Parts[0].Text, "checked cluster health") {
		t.Fatalf("task artifacts = %#v", response.Task.Artifacts)
	}

	select {
	case input := <-runner.inputs:
		if input.RunID != response.Task.ID || len(input.Messages) == 0 || input.Messages[len(input.Messages)-1].Content != "why did api restart?" {
			t.Fatalf("runner input = %#v", input)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runner was not started")
	}

	body := getBody(t, server.URL+"/tasks/"+response.Task.ID, http.StatusOK)
	for _, want := range []string{
		`"state": "completed"`,
		`"contextId": "` + response.Task.ContextID + `"`,
		`"artifactId": "final-answer"`,
		`"text": "checked cluster health"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("task body missing %q: %s", want, body)
		}
	}
}

func TestA2ATaskIncludesEvidenceArtifactsAndCitations(t *testing.T) {
	handler, err := NewServer(ServerOptions{
		OpenStore: func(context.Context) (ReadStore, error) {
			closed := false
			return fakeReadStore{closed: &closed}, nil
		},
		AgentRunner: &artifactAgentRunner{},
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	var response a2aSendMessageResponse
	postJSON(t, server.URL+"/message:send", `{"message":{"parts":[{"kind":"text","text":"show proof"}]}}`, http.StatusOK, &response)
	if response.Task == nil {
		t.Fatal("A2A response did not include task")
	}
	body := getBody(t, server.URL+"/tasks/"+response.Task.ID, http.StatusOK)
	for _, want := range []string{
		`"artifactId": "final-answer"`,
		`"artifactId": "artifact_health"`,
		`"text": "### Health evidence\\n\\nhealthy=3 stale=0"`,
		`"kubeInsightArtifactKind": "markdown"`,
		`"artifactId": "artifact_health"`,
		`"id": "citation_health"`,
		`"source": "kube_insight_health"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("A2A task missing %q: %s", want, body)
		}
	}
}

func TestA2AStreamMessageEmitsFinalTaskSnapshot(t *testing.T) {
	handler, err := NewServer(ServerOptions{
		OpenStore: func(context.Context) (ReadStore, error) {
			closed := false
			return fakeReadStore{closed: &closed}, nil
		},
		AgentRunner: &artifactAgentRunner{},
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	resp, err := http.Post(server.URL+"/message:stream", "application/json", strings.NewReader(`{"message":{"parts":[{"kind":"text","text":"stream proof"}]},"configuration":{"historyLength":1}}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	body := string(bodyBytes)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stream status = %d: %s", resp.StatusCode, body)
	}
	for _, want := range []string{
		"event: task",
		`"kind":"task"`,
		`"state":"completed"`,
		`"final":true`,
		`"artifactId":"artifact_health"`,
		`"text":"health proof is attached"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("stream missing %q: %s", want, body)
		}
	}
}

func TestA2ASendMessageWithoutRunnerFailsTask(t *testing.T) {
	handler, err := NewServer(ServerOptions{
		OpenStore: func(context.Context) (ReadStore, error) {
			closed := false
			return fakeReadStore{closed: &closed}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	var response a2aSendMessageResponse
	postJSON(t, server.URL+"/message:send", `{"message":{"parts":[{"kind":"text","text":"investigate"}]}}`, http.StatusOK, &response)
	if response.Task == nil || response.Task.Status.State != "failed" {
		t.Fatalf("task = %#v", response.Task)
	}
	if response.Task.Status.Message == nil || !strings.Contains(response.Task.Status.Message.Parts[0].Text, "runner is not configured") {
		t.Fatalf("status = %#v", response.Task.Status)
	}
}

func TestA2AEndpointsValidateInput(t *testing.T) {
	handler, err := NewServer(ServerOptions{
		OpenStore: func(context.Context) (ReadStore, error) {
			closed := false
			return fakeReadStore{closed: &closed}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	assertPOSTStatus(t, server.URL+"/message:send", `{"message":{"parts":[]}}`, http.StatusBadRequest)
	getBody(t, server.URL+"/tasks/missing", http.StatusNotFound)
	postA2ACancel(t, server.URL+"/tasks/missing:cancel", http.StatusNotFound)
}

func TestA2AMountHTTPExposesRootPathsBesideAPI(t *testing.T) {
	apiServer, err := NewServer(ServerOptions{
		OpenStore: func(context.Context) (ReadStore, error) {
			closed := false
			return fakeReadStore{closed: &closed}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	apiServer.MountHTTP(mux)
	apiServer.MountA2AHTTP(mux)
	server := httptest.NewServer(mux)
	defer server.Close()

	assertGETContains(t, server.URL+"/api/v1/server/info", `"storage"`)
	assertGETContains(t, server.URL+"/.well-known/agent-card.json", `"name": "kube-insight"`)
}

func postA2ACancel(t *testing.T, url string, wantStatus int) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != wantStatus {
		var parsed errorResponse
		_ = json.NewDecoder(resp.Body).Decode(&parsed)
		t.Fatalf("POST %s status = %d, want %d: %#v", url, resp.StatusCode, wantStatus, parsed)
	}
}
