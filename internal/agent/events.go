package agent

import "encoding/json"

type RunEventType string

const (
	EventRunCreated           RunEventType = "run.created"
	EventRunStarted           RunEventType = "run.started"
	EventRunStatus            RunEventType = "run.status"
	EventRunCompleted         RunEventType = "run.completed"
	EventRunFailed            RunEventType = "run.failed"
	EventRunCancelled         RunEventType = "run.cancelled"
	EventMessageCreated       RunEventType = "message.created"
	EventMessageDelta         RunEventType = "message.delta"
	EventMessageDone          RunEventType = "message.completed"
	EventFinalAnswer          RunEventType = "answer.final"
	EventCompletionRequest    RunEventType = "completion.request"
	EventCompletionMessage    RunEventType = "completion.message"
	EventCompletionToolResult RunEventType = "completion.tool_result"
	EventUsageDelta           RunEventType = "usage.delta"
	EventToolStarted          RunEventType = "tool.started"
	EventToolCompleted        RunEventType = "tool.completed"
	EventToolFailed           RunEventType = "tool.failed"
	EventToolAudit            RunEventType = "tool.audit"
	EventArtifact             RunEventType = "artifact.created"
	EventArtifactUpdate       RunEventType = "artifact.updated"
	EventCitation             RunEventType = "citation.created"
	EventFollowUpSuggestions  RunEventType = "followup.suggestions"
	EventError                RunEventType = "error"
)

type RunStatusEventData struct {
	RunID     string    `json:"runId"`
	SessionID string    `json:"sessionId,omitempty"`
	Status    RunStatus `json:"status"`
	Error     string    `json:"error,omitempty"`
}

type MessageEventData struct {
	MessageID string      `json:"messageId,omitempty"`
	Role      MessageRole `json:"role"`
	Delta     string      `json:"delta,omitempty"`
	Content   string      `json:"content,omitempty"`
}

type UsageEventData struct {
	Phase            string `json:"phase,omitempty"`
	Source           string `json:"source,omitempty"`
	Approximate      bool   `json:"approximate,omitempty"`
	PromptTokens     int64  `json:"promptTokens,omitempty"`
	CompletionTokens int64  `json:"completionTokens,omitempty"`
	TotalTokens      int64  `json:"totalTokens,omitempty"`
	SentTokens       int64  `json:"sentTokens,omitempty"`
	ReceivedTokens   int64  `json:"receivedTokens,omitempty"`
}

type ToolCallEventData struct {
	ToolCallID       string          `json:"toolCallId"`
	Name             string          `json:"name"`
	Status           string          `json:"status"`
	Input            json.RawMessage `json:"input,omitempty"`
	Output           json.RawMessage `json:"output,omitempty"`
	OutputSummary    string          `json:"outputSummary,omitempty"`
	OutputArtifactID string          `json:"outputArtifactId,omitempty"`
	DurationMS       int64           `json:"durationMs,omitempty"`
	Error            string          `json:"error,omitempty"`
}

type ToolAuditEventData struct {
	RunID            string          `json:"runId"`
	ToolCallID       string          `json:"toolCallId"`
	Name             string          `json:"name"`
	Status           string          `json:"status"`
	Input            json.RawMessage `json:"input,omitempty"`
	Output           json.RawMessage `json:"output,omitempty"`
	OutputSummary    string          `json:"outputSummary,omitempty"`
	OutputArtifactID string          `json:"outputArtifactId,omitempty"`
	DurationMS       int64           `json:"durationMs,omitempty"`
	Error            string          `json:"error,omitempty"`
}

func FinalAnswerFromRunEvent(event RunEvent) string {
	if event.Type != EventFinalAnswer && event.Type != EventRunCompleted {
		return ""
	}
	var data struct {
		Content     string `json:"content"`
		FinalAnswer string `json:"finalAnswer"`
	}
	if err := json.Unmarshal(event.Data, &data); err != nil {
		return ""
	}
	if data.Content != "" {
		return data.Content
	}
	return data.FinalAnswer
}

type Artifact struct {
	ID    string          `json:"id"`
	Kind  string          `json:"kind"`
	Title string          `json:"title,omitempty"`
	Data  json.RawMessage `json:"data,omitempty"`
}

type ArtifactEventData struct {
	Artifact Artifact `json:"artifact"`
}

type Citation struct {
	ID         string          `json:"id"`
	ArtifactID string          `json:"artifactId,omitempty"`
	Text       string          `json:"text,omitempty"`
	Target     json.RawMessage `json:"target,omitempty"`
}

type CitationEventData struct {
	Citation Citation `json:"citation"`
}

type FollowUpSuggestionsEventData struct {
	Suggestions []string `json:"suggestions"`
}

type ErrorEventData struct {
	Code      string `json:"code,omitempty"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable,omitempty"`
}
