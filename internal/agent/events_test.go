package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRunEventTypeNamesAreStableAndUnique(t *testing.T) {
	values := []RunEventType{
		EventRunCreated,
		EventRunStarted,
		EventRunStatus,
		EventRunCompleted,
		EventRunFailed,
		EventRunCancelled,
		EventMessageCreated,
		EventMessageDelta,
		EventMessageDone,
		EventFinalAnswer,
		EventUsageDelta,
		EventToolStarted,
		EventToolCompleted,
		EventToolFailed,
		EventToolAudit,
		EventArtifact,
		EventArtifactUpdate,
		EventCitation,
		EventError,
	}
	seen := map[RunEventType]bool{}
	for _, value := range values {
		if value == "" {
			t.Fatal("empty event type")
		}
		if seen[value] {
			t.Fatalf("duplicate event type %q", value)
		}
		seen[value] = true
	}
	if EventRunCreated != "run.created" || EventFinalAnswer != "answer.final" || EventUsageDelta != "usage.delta" || EventToolAudit != "tool.audit" || EventArtifact != "artifact.created" {
		t.Fatalf("unexpected event names: %q %q %q %q %q", EventRunCreated, EventFinalAnswer, EventUsageDelta, EventToolAudit, EventArtifact)
	}
}

func TestRunEventPayloadsMarshalContractFields(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want []string
	}{
		{
			name: "run status",
			in:   RunStatusEventData{RunID: "run_1", SessionID: "sess_1", Status: RunRunning},
			want: []string{`"runId":"run_1"`, `"sessionId":"sess_1"`, `"status":"running"`},
		},
		{
			name: "message delta",
			in:   MessageEventData{MessageID: "msg_1", Role: RoleAssistant, Delta: "checking"},
			want: []string{`"messageId":"msg_1"`, `"role":"assistant"`, `"delta":"checking"`},
		},
		{
			name: "usage delta",
			in:   UsageEventData{Phase: "streaming", Source: "provider", PromptTokens: 10, CompletionTokens: 3, TotalTokens: 13},
			want: []string{`"phase":"streaming"`, `"source":"provider"`, `"promptTokens":10`, `"completionTokens":3`, `"totalTokens":13`},
		},
		{
			name: "tool call",
			in:   ToolCallEventData{ToolCallID: "tool_1", Name: "kube_insight_health", Status: "completed", DurationMS: 12},
			want: []string{`"toolCallId":"tool_1"`, `"name":"kube_insight_health"`, `"status":"completed"`, `"durationMs":12`},
		},
		{
			name: "tool audit",
			in:   ToolAuditEventData{RunID: "run_1", ToolCallID: "tool_1", Name: "kube_insight_health", Status: "completed", DurationMS: 12},
			want: []string{`"runId":"run_1"`, `"toolCallId":"tool_1"`, `"name":"kube_insight_health"`, `"status":"completed"`, `"durationMs":12`},
		},
		{
			name: "artifact",
			in:   ArtifactEventData{Artifact: Artifact{ID: "artifact_1", Kind: "k8s.resource", Title: "Pod default/api-0"}},
			want: []string{`"id":"artifact_1"`, `"kind":"k8s.resource"`, `"title":"Pod default/api-0"`},
		},
		{
			name: "citation",
			in:   CitationEventData{Citation: Citation{ID: "citation_1", ArtifactID: "artifact_1", Text: "version 3"}},
			want: []string{`"id":"citation_1"`, `"artifactId":"artifact_1"`, `"text":"version 3"`},
		},
		{
			name: "error",
			in:   ErrorEventData{Code: "provider_missing_key", Message: "missing API key", Retryable: true},
			want: []string{`"code":"provider_missing_key"`, `"message":"missing API key"`, `"retryable":true`},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(tc.in)
			if err != nil {
				t.Fatal(err)
			}
			text := string(data)
			for _, want := range tc.want {
				if !strings.Contains(text, want) {
					t.Fatalf("payload missing %s: %s", want, text)
				}
			}
		})
	}
}
