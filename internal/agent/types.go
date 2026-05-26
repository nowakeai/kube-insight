package agent

import (
	"encoding/json"
	"time"
)

type RunStatus string

const (
	RunQueued    RunStatus = "queued"
	RunRunning   RunStatus = "running"
	RunCompleted RunStatus = "completed"
	RunFailed    RunStatus = "failed"
	RunCancelled RunStatus = "cancelled"
)

type MessageRole string

const (
	RoleUser      MessageRole = "user"
	RoleAssistant MessageRole = "assistant"
	RoleSystem    MessageRole = "system"
	RoleTool      MessageRole = "tool"
)

type Session struct {
	ID        string    `json:"id"`
	Title     string    `json:"title,omitempty"`
	Provider  string    `json:"provider,omitempty"`
	Model     string    `json:"model,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	Messages  []Message `json:"messages,omitempty"`
	Runs      []Run     `json:"runs,omitempty"`
	RunCount  int       `json:"runCount,omitempty"`
	LatestRun *Run      `json:"latestRun,omitempty"`
}

type Message struct {
	ID         string          `json:"id"`
	Role       MessageRole     `json:"role"`
	Content    string          `json:"content"`
	ToolCalls  []ToolCall      `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
	ToolName   string          `json:"name,omitempty"`
	RunID      string          `json:"runId,omitempty"`
	CreatedAt  time.Time       `json:"createdAt"`
	Metadata   json.RawMessage `json:"metadata,omitempty"`
}

type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type,omitempty"`
	Function FunctionCall `json:"function"`
}

type FunctionCall struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

type Run struct {
	ID          string          `json:"id"`
	SessionID   string          `json:"sessionId"`
	Status      RunStatus       `json:"status"`
	Input       string          `json:"input"`
	Provider    string          `json:"provider,omitempty"`
	Model       string          `json:"model,omitempty"`
	CreatedAt   time.Time       `json:"createdAt"`
	StartedAt   *time.Time      `json:"startedAt,omitempty"`
	CompletedAt *time.Time      `json:"completedAt,omitempty"`
	Error       string          `json:"error,omitempty"`
	FinalAnswer string          `json:"finalAnswer,omitempty"`
	Metadata    json.RawMessage `json:"metadata,omitempty"`
}

type RunEvent struct {
	ID        string          `json:"id"`
	RunID     string          `json:"runId"`
	Sequence  int64           `json:"sequence"`
	Type      RunEventType    `json:"type"`
	CreatedAt time.Time       `json:"createdAt"`
	Data      json.RawMessage `json:"data,omitempty"`
}

type CreateSessionInput struct {
	Title    string
	Provider string
	Model    string
}

type ListSessionsOptions struct {
	Limit int
}

type SessionList struct {
	Sessions []Session `json:"sessions"`
}

type CreateRunInput struct {
	Input    string
	Provider string
	Model    string
	Metadata json.RawMessage
}

type AppendEventInput struct {
	Type RunEventType
	Data json.RawMessage
}

type ListRunsOptions struct {
	Status RunStatus
	Limit  int
}

type RunList struct {
	Summary RunSummary `json:"summary"`
	Runs    []Run      `json:"runs"`
}

type RunSummary struct {
	Queued    int `json:"queued"`
	Running   int `json:"running"`
	Completed int `json:"completed"`
	Failed    int `json:"failed"`
	Cancelled int `json:"cancelled"`
	Total     int `json:"total"`
}
