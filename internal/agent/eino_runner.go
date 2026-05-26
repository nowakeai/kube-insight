package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
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
	Name               string
	Description        string
	Instruction        string
	Model              model.BaseChatModel
	Tools              []tool.BaseTool
	ToolReturnDirectly map[string]bool
	EmitInternalEvents bool
	Handlers           []adk.ChatModelAgentMiddleware
	Middlewares        []adk.AgentMiddleware
	ModelRetryConfig   *adk.ModelRetryConfig
	CheckPointStore    adk.CheckPointStore
	EnableStreaming    bool
	RunOptions         []adk.AgentRunOption
	MaxIterations      int
}

type EinoRunner struct {
	runner      *adk.Runner
	runOptions  []adk.AgentRunOption
	toolTimings *toolTimingStore
}

type EinoRunInput struct {
	Messages     []Message
	Store        Store
	RunID        string
	CheckPointID string
	RunOptions   []adk.AgentRunOption
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
	toolTimings := newToolTimingStore()
	handlers := []adk.ChatModelAgentMiddleware{
		&toolTimingMiddleware{timings: toolTimings},
		&toolBudgetMiddleware{maxIterations: cfg.MaxIterations},
	}
	handlers = append(handlers, cfg.Handlers...)
	agentConfig := &adk.ChatModelAgentConfig{
		Name:             name,
		Description:      cfg.Description,
		Instruction:      instruction,
		Model:            cfg.Model,
		Handlers:         handlers,
		Middlewares:      cfg.Middlewares,
		ModelRetryConfig: cfg.ModelRetryConfig,
		MaxIterations:    cfg.MaxIterations,
	}
	if len(cfg.Tools) > 0 || len(cfg.ToolReturnDirectly) > 0 || cfg.EmitInternalEvents {
		agentConfig.ToolsConfig = adk.ToolsConfig{
			ToolsNodeConfig:    compose.ToolsNodeConfig{Tools: cfg.Tools},
			ReturnDirectly:     cfg.ToolReturnDirectly,
			EmitInternalEvents: cfg.EmitInternalEvents,
		}
	}
	agent, err := adk.NewChatModelAgent(ctx, agentConfig)
	if err != nil {
		return nil, err
	}
	runOptions := append([]adk.AgentRunOption(nil), cfg.RunOptions...)
	return &EinoRunner{
		runner: adk.NewRunner(ctx, adk.RunnerConfig{
			Agent:           agent,
			EnableStreaming: cfg.EnableStreaming,
			CheckPointStore: cfg.CheckPointStore,
		}),
		runOptions:  runOptions,
		toolTimings: toolTimings,
	}, nil
}

func (r *EinoRunner) Run(ctx context.Context, input EinoRunInput) (EinoRunResult, error) {
	if r == nil || r.runner == nil {
		return EinoRunResult{}, errors.New("eino runner is not initialized")
	}
	recorder := newEinoRunRecorder(input.Store, input.RunID, r.toolTimings)
	if err := recorder.Start(ctx); err != nil {
		return EinoRunResult{}, err
	}
	messages := make([]adk.Message, 0, len(input.Messages))
	for _, message := range input.Messages {
		messages = append(messages, einoMessage(message))
	}
	runOptions := append([]adk.AgentRunOption(nil), r.runOptions...)
	runOptions = append(runOptions, input.RunOptions...)
	if input.CheckPointID != "" {
		runOptions = append(runOptions, adk.WithCheckPointID(input.CheckPointID))
	}
	iter := r.runner.Run(ctx, messages, runOptions...)
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
		recorded, err := recorder.Record(ctx, event)
		if err != nil {
			return result, err
		}
		if recorded.Role == RoleAssistant && recorded.Content != "" {
			result.FinalAnswer = recorded.Content
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
	store       Store
	runID       string
	toolCalls   map[string]toolAuditDraft
	toolTimings *toolTimingStore
}

type toolAuditDraft struct {
	Name       string
	Input      json.RawMessage
	StartedAt  time.Time
	DurationMS int64
}

type toolTiming struct {
	Name       string
	Input      json.RawMessage
	StartedAt  time.Time
	DurationMS int64
}

type toolTimingStore struct {
	mu     sync.Mutex
	values map[string]toolTiming
}

func newToolTimingStore() *toolTimingStore {
	return &toolTimingStore{values: map[string]toolTiming{}}
}

func (s *toolTimingStore) put(callID string, timing toolTiming) {
	if s == nil || callID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[callID] = timing
}

func (s *toolTimingStore) take(callID string) (toolTiming, bool) {
	if s == nil || callID == "" {
		return toolTiming{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	timing, ok := s.values[callID]
	if ok {
		delete(s.values, callID)
	}
	return timing, ok
}

type toolTimingMiddleware struct {
	adk.BaseChatModelAgentMiddleware
	timings *toolTimingStore
}

func (m toolTimingMiddleware) WrapInvokableToolCall(ctx context.Context, endpoint adk.InvokableToolCallEndpoint, tCtx *adk.ToolContext) (adk.InvokableToolCallEndpoint, error) {
	return func(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
		start := time.Now()
		result, err := endpoint(ctx, argumentsInJSON, opts...)
		if tCtx != nil {
			m.timings.put(tCtx.CallID, toolTiming{Name: tCtx.Name, Input: normalizedToolInput(argumentsInJSON), StartedAt: start, DurationMS: toolAuditDurationMS(start)})
		}
		return result, err
	}, nil
}

type einoRecordedMessage struct {
	Role    MessageRole
	Content string
}

func newEinoRunRecorder(store Store, runID string, timings ...*toolTimingStore) einoRunRecorder {
	if store == nil || runID == "" {
		return einoRunRecorder{}
	}
	var timingStore *toolTimingStore
	if len(timings) > 0 {
		timingStore = timings[0]
	}
	return einoRunRecorder{store: store, runID: runID, toolCalls: map[string]toolAuditDraft{}, toolTimings: timingStore}
}

func (r einoRunRecorder) enabled() bool {
	return r.store != nil && r.runID != ""
}

func (r einoRunRecorder) takeToolTiming(callID string) (toolTiming, bool) {
	if r.toolTimings == nil {
		return toolTiming{}, false
	}
	return r.toolTimings.take(callID)
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
		citations, err := r.verifiedAnswerCitations(ctx, finalAnswer)
		if err != nil {
			return err
		}
		annotatedAnswer := annotateAnswerWithEvidenceReferences(finalAnswer, citations)
		if err := r.append(ctx, EventFinalAnswer, MessageEventData{MessageID: NewMessageID(), Role: RoleAssistant, Content: annotatedAnswer}); err != nil {
			return err
		}
		for _, citation := range citations {
			if err := r.append(ctx, EventCitation, CitationEventData{Citation: citation.Citation}); err != nil {
				return err
			}
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

func normalizedToolInput(arguments string) json.RawMessage {
	input := json.RawMessage(arguments)
	if json.Valid(input) {
		return input
	}
	return jsonRaw(map[string]string{"arguments": arguments})
}

func (r einoRunRecorder) Record(ctx context.Context, event *adk.AgentEvent) (einoRecordedMessage, error) {
	if event == nil || event.Output == nil || event.Output.MessageOutput == nil {
		return einoRecordedMessage{}, nil
	}
	output := event.Output.MessageOutput
	if output.IsStreaming && output.MessageStream != nil {
		return r.recordStream(ctx, output)
	}
	msg, err := output.GetMessage()
	if err != nil {
		return einoRecordedMessage{}, err
	}
	if msg == nil {
		return einoRecordedMessage{}, nil
	}
	role := messageRoleFromEino(output.Role, msg.Role)
	recorded := einoRecordedMessage{Role: role, Content: msg.Content}
	if !r.enabled() {
		return recorded, nil
	}
	if msg.Content != "" && role != RoleTool {
		if err := r.append(ctx, EventMessageCreated, MessageEventData{MessageID: NewMessageID(), Role: role, Content: msg.Content}); err != nil {
			return recorded, err
		}
	}
	for _, call := range msg.ToolCalls {
		input := normalizedToolInput(call.Function.Arguments)
		r.toolCalls[call.ID] = toolAuditDraft{Name: call.Function.Name, Input: input, StartedAt: time.Now()}
		if err := r.append(ctx, EventToolStarted, ToolCallEventData{ToolCallID: call.ID, Name: call.Function.Name, Status: "started", Input: input}); err != nil {
			return recorded, err
		}
	}
	if role == RoleTool {
		outputData := toolOutputData(msg.Content)
		draft := r.toolCalls[msg.ToolCallID]
		if timing, ok := r.takeToolTiming(msg.ToolCallID); ok {
			if draft.Name == "" {
				draft.Name = timing.Name
			}
			if len(draft.Input) == 0 {
				draft.Input = timing.Input
			}
			if draft.StartedAt.IsZero() {
				draft.StartedAt = timing.StartedAt
			}
			if timing.DurationMS > 0 {
				draft.DurationMS = timing.DurationMS
			}
		}
		name := firstNonEmptyString(output.ToolName, msg.ToolName, draft.Name)
		outputSummary := summarizeToolOutput(outputData)
		status := "completed"
		eventType := EventToolCompleted
		errorMessage := toolOutputErrorMessage(outputData)
		if errorMessage != "" {
			status = "failed"
			eventType = EventToolFailed
			outputSummary = "error: " + compactText(errorMessage, 180)
		}
		durationMS := toolAuditDurationMS(draft.StartedAt)
		if draft.DurationMS > 0 {
			durationMS = draft.DurationMS
		}
		artifactID := ""
		if len(outputData) > 0 {
			artifactID = NewArtifactID()
			artifactData := jsonRaw(map[string]any{
				"toolCallId":    msg.ToolCallID,
				"name":          name,
				"input":         rawMessageValue(draft.Input),
				"output":        rawMessageValue(outputData),
				"outputSummary": outputSummary,
				"status":        status,
				"durationMs":    durationMS,
			})
			if errorMessage != "" {
				artifactData = jsonRaw(map[string]any{
					"toolCallId":    msg.ToolCallID,
					"name":          name,
					"input":         rawMessageValue(draft.Input),
					"output":        rawMessageValue(outputData),
					"outputSummary": outputSummary,
					"status":        status,
					"durationMs":    durationMS,
					"error":         errorMessage,
				})
			}
			if err := r.append(ctx, EventArtifact, ArtifactEventData{Artifact: Artifact{ID: artifactID, Kind: ArtifactKindToolCall, Title: "Tool output: " + name, Data: artifactData}}); err != nil {
				return recorded, err
			}
		}
		if err := r.append(ctx, eventType, ToolCallEventData{ToolCallID: msg.ToolCallID, Name: name, Status: status, OutputSummary: outputSummary, OutputArtifactID: artifactID, DurationMS: durationMS, Error: errorMessage}); err != nil {
			return recorded, err
		}
		if err := r.append(ctx, EventToolAudit, ToolAuditEventData{RunID: r.runID, ToolCallID: msg.ToolCallID, Name: name, Status: status, Input: draft.Input, OutputSummary: outputSummary, OutputArtifactID: artifactID, DurationMS: durationMS, Error: errorMessage}); err != nil {
			return recorded, err
		}
		ObserveToolCallDuration(name, status, durationMS)
		if status == "completed" {
			if err := r.appendEvidenceArtifacts(ctx, name, outputData); err != nil {
				return recorded, err
			}
		}
		delete(r.toolCalls, msg.ToolCallID)
	}
	return recorded, nil
}

func (r einoRunRecorder) recordStream(ctx context.Context, output *adk.MessageVariant) (einoRecordedMessage, error) {
	messageID := NewMessageID()
	var builder strings.Builder
	role := messageRoleFromEino(output.Role, "")
	for {
		frame, err := output.MessageStream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return einoRecordedMessage{}, err
		}
		if frame == nil {
			continue
		}
		frameRole := messageRoleFromEino(output.Role, frame.Role)
		if role == "" {
			role = frameRole
		}
		if frame.Content == "" {
			continue
		}
		builder.WriteString(frame.Content)
		if r.enabled() {
			if err := r.append(ctx, EventMessageDelta, MessageEventData{MessageID: messageID, Role: frameRole, Delta: frame.Content}); err != nil {
				return einoRecordedMessage{}, err
			}
		}
	}
	content := builder.String()
	if r.enabled() && content != "" {
		if err := r.append(ctx, EventMessageDone, MessageEventData{MessageID: messageID, Role: role, Content: content}); err != nil {
			return einoRecordedMessage{}, err
		}
	}
	return einoRecordedMessage{Role: role, Content: content}, nil
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
	if duration == 0 {
		return 1
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

func toolOutputErrorMessage(output json.RawMessage) string {
	if len(output) == 0 {
		return ""
	}
	var value any
	if err := json.Unmarshal(output, &value); err != nil {
		return ""
	}
	return toolOutputErrorMessageValue(value)
}

func toolOutputErrorMessageValue(value any) string {
	switch typed := value.(type) {
	case map[string]any:
		if isError, _ := typed["isError"].(bool); isError {
			if msg := firstStringField(typed, "error", "message", "exception"); msg != "" {
				return msg
			}
		}
		if inner, ok := mcpTextContentValue(typed); ok {
			return toolOutputErrorMessageValue(inner)
		}
		if msg := firstStringField(typed, "error", "exception"); msg != "" {
			return msg
		}
	}
	return ""
}

func firstStringField(value map[string]any, keys ...string) string {
	for _, key := range keys {
		if text, ok := value[key].(string); ok && text != "" {
			return text
		}
	}
	return ""
}

func summarizeToolOutput(output json.RawMessage) string {
	if len(output) == 0 {
		return "no output"
	}
	var value any
	if err := json.Unmarshal(output, &value); err != nil {
		return compactText(string(output), 180)
	}
	return summarizeToolOutputValue(value, string(output))
}

func summarizeToolOutputValue(value any, fallback string) string {
	switch typed := value.(type) {
	case map[string]any:
		if inner, ok := mcpTextContentValue(typed); ok {
			return summarizeToolOutputValue(inner, compactJSONValue(inner, 180))
		}
		parts := make([]string, 0, 4)
		if summary, ok := typed["summary"]; ok {
			parts = append(parts, "summary="+compactJSONValue(summary, 96))
		}
		for _, key := range []string{"rowCount", "matches", "nodes", "edges", "truncated"} {
			if v, ok := typed[key]; ok {
				parts = append(parts, fmt.Sprintf("%s=%v", key, v))
			}
		}
		if rows, ok := typed["rows"].([]any); ok {
			parts = append(parts, fmt.Sprintf("rows=%d", len(rows)))
		}
		if matches, ok := typed["matches"].([]any); ok {
			parts = append(parts, fmt.Sprintf("matches=%d", len(matches)))
		}
		if len(parts) > 0 {
			return strings.Join(parts, " ")
		}
	case []any:
		return fmt.Sprintf("items=%d", len(typed))
	}
	return compactText(fallback, 180)
}

func mcpTextContentValue(value map[string]any) (any, bool) {
	items, ok := value["content"].([]any)
	if !ok || len(items) == 0 {
		return nil, false
	}
	first, ok := items[0].(map[string]any)
	if !ok || first["type"] != "text" {
		return nil, false
	}
	text, ok := first["text"].(string)
	if !ok || text == "" {
		return nil, false
	}
	var parsed any
	if err := json.Unmarshal([]byte(text), &parsed); err == nil {
		return parsed, true
	}
	return text, true
}

func compactJSONValue(value any, maxRunes int) string {
	if text, ok := value.(string); ok {
		return compactText(text, maxRunes)
	}
	data, err := json.Marshal(value)
	if err != nil {
		return compactText(fmt.Sprint(value), maxRunes)
	}
	return compactText(string(data), maxRunes)
}

func rawMessageValue(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return string(raw)
	}
	return value
}

func compactText(value string, maxRunes int) string {
	line := strings.Join(strings.Fields(value), " ")
	if maxRunes <= 0 {
		return line
	}
	runes := []rune(line)
	if len(runes) <= maxRunes {
		return line
	}
	if maxRunes <= 1 {
		return string(runes[:maxRunes])
	}
	return string(runes[:maxRunes-1]) + "..."
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
