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
}

type Message struct {
	ID        string          `json:"id"`
	Role      MessageRole     `json:"role"`
	Content   string          `json:"content"`
	RunID     string          `json:"runId,omitempty"`
	CreatedAt time.Time       `json:"createdAt"`
	Metadata  json.RawMessage `json:"metadata,omitempty"`
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
	Metadata    json.RawMessage `json:"metadata,omitempty"`
}

type RunEvent struct {
	ID        string          `json:"id"`
	RunID     string          `json:"runId"`
	Sequence  int64           `json:"sequence"`
	Type      string          `json:"type"`
	CreatedAt time.Time       `json:"createdAt"`
	Data      json.RawMessage `json:"data,omitempty"`
}

type CreateSessionInput struct {
	Title    string
	Provider string
	Model    string
}

type CreateRunInput struct {
	Input    string
	Provider string
	Model    string
	Metadata json.RawMessage
}

type AppendEventInput struct {
	Type string
	Data json.RawMessage
}
