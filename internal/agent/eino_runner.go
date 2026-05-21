package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

const defaultEinoAgentName = "kube-insight-agent"

var ErrEinoModelRequired = errors.New("eino chat model is required")

type EinoRunnerConfig struct {
	Name          string
	Description   string
	Instruction   string
	Model         model.BaseChatModel
	Tools         []tool.BaseTool
	MaxIterations int
}

type EinoRunner struct {
	runner *adk.Runner
}

type EinoRunInput struct {
	Messages []Message
	Store    Store
	RunID    string
}

type EinoRunResult struct {
	FinalAnswer string
	Events      int
}

func NewEinoRunner(ctx context.Context, cfg EinoRunnerConfig) (*EinoRunner, error) {
	if cfg.Model == nil {
		return nil, ErrEinoModelRequired
	}
	name := cfg.Name
	if name == "" {
		name = defaultEinoAgentName
	}
	instruction := cfg.Instruction
	if instruction == "" {
		instruction = DefaultAgentInstruction()
	}
	agentConfig := &adk.ChatModelAgentConfig{
		Name:          name,
		Description:   cfg.Description,
		Instruction:   instruction,
		Model:         cfg.Model,
		MaxIterations: cfg.MaxIterations,
	}
	if len(cfg.Tools) > 0 {
		agentConfig.ToolsConfig = adk.ToolsConfig{ToolsNodeConfig: compose.ToolsNodeConfig{Tools: cfg.Tools}}
	}
	agent, err := adk.NewChatModelAgent(ctx, agentConfig)
	if err != nil {
		return nil, err
	}
	return &EinoRunner{runner: adk.NewRunner(ctx, adk.RunnerConfig{Agent: agent})}, nil
}

func (r *EinoRunner) Run(ctx context.Context, input EinoRunInput) (EinoRunResult, error) {
	if r == nil || r.runner == nil {
		return EinoRunResult{}, errors.New("eino runner is not initialized")
	}
	recorder := newEinoRunRecorder(input.Store, input.RunID)
	if err := recorder.Start(ctx); err != nil {
		return EinoRunResult{}, err
	}
	messages := make([]adk.Message, 0, len(input.Messages))
	for _, message := range input.Messages {
		messages = append(messages, einoMessage(message))
	}
	iter := r.runner.Run(ctx, messages)
	var result EinoRunResult
	for {
		event, ok := iter.Next()
		if !ok {
			if err := recorder.Complete(ctx, result.FinalAnswer); err != nil {
				return result, err
			}
			return result, nil
		}
		result.Events++
		if event == nil {
			continue
		}
		if event.Err != nil {
			if err := recorder.Fail(ctx, event.Err); err != nil {
				return result, err
			}
			return result, event.Err
		}
		if err := recorder.Record(ctx, event); err != nil {
			return result, err
		}
		if event.Output == nil || event.Output.MessageOutput == nil || event.Output.MessageOutput.Message == nil {
			continue
		}
		if content := event.Output.MessageOutput.Message.Content; content != "" {
			result.FinalAnswer = content
		}
	}
}

func einoMessage(message Message) adk.Message {
	switch message.Role {
	case RoleSystem:
		return schema.SystemMessage(message.Content)
	case RoleAssistant:
		return schema.AssistantMessage(message.Content, nil)
	case RoleTool:
		if message.ID != "" {
			return schema.ToolMessage(message.Content, message.ID)
		}
		return &schema.Message{Role: schema.Tool, Content: message.Content}
	case RoleUser:
		return schema.UserMessage(message.Content)
	default:
		return schema.UserMessage(fmt.Sprintf("%s", message.Content))
	}
}

type einoRunRecorder struct {
	store     Store
	runID     string
	toolCalls map[string]toolAuditDraft
}

type toolAuditDraft struct {
	Name      string
	Input     json.RawMessage
	StartedAt time.Time
}

func newEinoRunRecorder(store Store, runID string) einoRunRecorder {
	if store == nil || runID == "" {
		return einoRunRecorder{}
	}
	return einoRunRecorder{store: store, runID: runID, toolCalls: map[string]toolAuditDraft{}}
}

func (r einoRunRecorder) enabled() bool {
	return r.store != nil && r.runID != ""
}

func (r einoRunRecorder) Start(ctx context.Context) error {
	if !r.enabled() {
		return nil
	}
	run, err := r.store.UpdateRunStatus(ctx, r.runID, RunRunning, "")
	if err != nil {
		return err
	}
	return r.append(ctx, EventRunStarted, RunStatusEventData{RunID: r.runID, SessionID: run.SessionID, Status: run.Status})
}

func (r einoRunRecorder) Complete(ctx context.Context, finalAnswer string) error {
	if !r.enabled() {
		return nil
	}
	if finalAnswer != "" {
		if err := r.append(ctx, EventFinalAnswer, MessageEventData{MessageID: NewMessageID(), Role: RoleAssistant, Content: finalAnswer}); err != nil {
			return err
		}
	}
	run, err := r.store.UpdateRunStatus(ctx, r.runID, RunCompleted, "")
	if err != nil {
		return err
	}
	return r.append(ctx, EventRunCompleted, RunStatusEventData{RunID: r.runID, SessionID: run.SessionID, Status: run.Status})
}

func (r einoRunRecorder) Fail(ctx context.Context, runErr error) error {
	if !r.enabled() {
		return nil
	}
	message := ""
	if runErr != nil {
		message = runErr.Error()
	}
	if err := r.append(ctx, EventError, ErrorEventData{Message: message}); err != nil {
		return err
	}
	run, err := r.store.UpdateRunStatus(ctx, r.runID, RunFailed, message)
	if err != nil {
		return err
	}
	return r.append(ctx, EventRunFailed, RunStatusEventData{RunID: r.runID, SessionID: run.SessionID, Status: run.Status, Error: message})
}

func (r einoRunRecorder) Record(ctx context.Context, event *adk.AgentEvent) error {
	if !r.enabled() || event == nil || event.Output == nil || event.Output.MessageOutput == nil || event.Output.MessageOutput.Message == nil {
		return nil
	}
	output := event.Output.MessageOutput
	msg := output.Message
	role := messageRoleFromEino(output.Role, msg.Role)
	if msg.Content != "" {
		if err := r.append(ctx, EventMessageCreated, MessageEventData{MessageID: NewMessageID(), Role: role, Content: msg.Content}); err != nil {
			return err
		}
	}
	for _, call := range msg.ToolCalls {
		input := json.RawMessage(call.Function.Arguments)
		if !json.Valid(input) {
			input = jsonRaw(map[string]string{"arguments": call.Function.Arguments})
		}
		r.toolCalls[call.ID] = toolAuditDraft{Name: call.Function.Name, Input: input, StartedAt: time.Now()}
		if err := r.append(ctx, EventToolStarted, ToolCallEventData{ToolCallID: call.ID, Name: call.Function.Name, Status: "started", Input: input}); err != nil {
			return err
		}
	}
	if role == RoleTool {
		outputData := toolOutputData(msg.Content)
		draft := r.toolCalls[msg.ToolCallID]
		name := firstNonEmptyString(output.ToolName, msg.ToolName, draft.Name)
		if err := r.append(ctx, EventToolCompleted, ToolCallEventData{ToolCallID: msg.ToolCallID, Name: name, Status: "completed", Output: outputData}); err != nil {
			return err
		}
		if err := r.append(ctx, EventToolAudit, ToolAuditEventData{RunID: r.runID, ToolCallID: msg.ToolCallID, Name: name, Status: "completed", Input: draft.Input, Output: outputData, DurationMS: toolAuditDurationMS(draft.StartedAt)}); err != nil {
			return err
		}
		delete(r.toolCalls, msg.ToolCallID)
	}
	return nil
}

func (r einoRunRecorder) append(ctx context.Context, eventType RunEventType, data any) error {
	encoded, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = r.store.AppendRunEvent(ctx, r.runID, AppendEventInput{Type: eventType, Data: encoded})
	return err
}

func messageRoleFromEino(outputRole schema.RoleType, messageRole schema.RoleType) MessageRole {
	role := outputRole
	if role == "" {
		role = messageRole
	}
	switch role {
	case schema.Assistant:
		return RoleAssistant
	case schema.System:
		return RoleSystem
	case schema.Tool:
		return RoleTool
	case schema.User:
		return RoleUser
	default:
		return RoleAssistant
	}
}

func toolAuditDurationMS(start time.Time) int64 {
	if start.IsZero() {
		return 0
	}
	duration := time.Since(start).Milliseconds()
	if duration < 0 {
		return 0
	}
	return duration
}

func toolOutputData(content string) json.RawMessage {
	if content == "" {
		return nil
	}
	data := json.RawMessage(content)
	if json.Valid(data) {
		return data
	}
	return jsonRaw(map[string]string{"content": content})
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func jsonRaw(value any) json.RawMessage {
	data, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	return data
}
