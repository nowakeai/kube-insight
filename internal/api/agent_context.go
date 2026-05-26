package api

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"kube-insight/internal/agent"
)

func (s *Server) agentMessagesForRun(ctx context.Context, run agent.Run) []agent.Message {
	messages := agentConversationMessagesForRun(ctx, s.agentStore, run)
	if context := agentRunClientContextMessage(run.Metadata); context != "" {
		messages = append(messages, agent.Message{Role: agent.RoleSystem, Content: context})
	}
	messages = append(messages, agent.Message{Role: agent.RoleUser, Content: run.Input})
	return messages
}

func agentConversationMessagesForRun(ctx context.Context, store agent.Store, run agent.Run) []agent.Message {
	return newAgentContextReplayBuilder(ctx, store).Build(run)
}

type agentContextReplayBuilder struct {
	ctx   context.Context
	store agent.Store
}

func newAgentContextReplayBuilder(ctx context.Context, store agent.Store) agentContextReplayBuilder {
	return agentContextReplayBuilder{ctx: ctx, store: store}
}

func (b agentContextReplayBuilder) Build(run agent.Run) []agent.Message {
	prior := b.priorRuns(run)
	if len(prior) == 0 {
		return nil
	}
	if transcript := conversationFromLatestCompletionRequests(b.ctx, b.store, prior); len(transcript) > 0 {
		return transcript
	}
	if transcript := conversationFromCompletionEvents(b.ctx, b.store, prior); len(transcript) > 0 {
		return transcript
	}
	latest := prior[len(prior)-1]
	if transcript := visibleConversationFromRunCreatedTranscript(b.ctx, b.store, latest.ID); len(transcript) > 0 {
		return appendFinalAnswerIfMissing(b.ctx, b.store, latest.ID, transcript)
	}
	messages := make([]agent.Message, 0, len(prior)*2)
	for _, candidate := range prior {
		messages = append(messages, agent.Message{Role: agent.RoleUser, Content: candidate.Input, RunID: candidate.ID, CreatedAt: candidate.CreatedAt})
		if answer := finalAnswerForRunContext(b.ctx, b.store, candidate.ID); answer != "" {
			messages = append(messages, agent.Message{Role: agent.RoleAssistant, Content: answer, RunID: candidate.ID})
		}
	}
	return messages
}

func (b agentContextReplayBuilder) priorRuns(run agent.Run) []agent.Run {
	if b.store == nil || run.SessionID == "" {
		return nil
	}
	session, err := b.store.GetSession(b.ctx, run.SessionID)
	if err != nil {
		return nil
	}
	contextRoot := run
	if retryOfRunID := retryOfRunIDFromMetadata(run.Metadata); retryOfRunID != "" {
		if retried, err := b.store.GetRun(b.ctx, retryOfRunID); err == nil {
			contextRoot = retried
		}
	}
	prior := make([]agent.Run, 0, len(session.Runs))
	for _, candidate := range session.Runs {
		if candidate.ID == run.ID || candidate.ID == contextRoot.ID || agentRunParentID(candidate.Metadata) != "" || !candidate.CreatedAt.Before(contextRoot.CreatedAt) || !agentRunStatusTerminal(candidate.Status) {
			continue
		}
		prior = append(prior, candidate)
	}
	return prior
}

func agentRunParentID(metadata json.RawMessage) string {
	if len(metadata) == 0 || !json.Valid(metadata) {
		return ""
	}
	var record map[string]any
	if json.Unmarshal(metadata, &record) != nil {
		return ""
	}
	value, _ := record["parentRunId"].(string)
	return value
}

func retryOfRunIDFromMetadata(metadata json.RawMessage) string {
	if len(metadata) == 0 || !json.Valid(metadata) {
		return ""
	}
	var record map[string]any
	if json.Unmarshal(metadata, &record) != nil {
		return ""
	}
	value, _ := record["retryOfRunId"].(string)
	return value
}

func (s *Server) recordCompletionInput(ctx context.Context, run agent.Run) error {
	userMessage := agent.Message{Role: agent.RoleUser, Content: run.Input, RunID: run.ID, CreatedAt: run.CreatedAt}
	_, err := s.agentStore.AppendRunEvent(ctx, run.ID, agent.AppendEventInput{
		Type: agent.EventCompletionMessage,
		Data: mustJSON(completionMessageEventValue(userMessage)),
	})
	return err
}

func conversationFromLatestCompletionRequests(ctx context.Context, store agent.Store, runs []agent.Run) []agent.Message {
	messages := []agent.Message{}
	sawRequest := false
	for _, run := range runs {
		transcript := visibleConversationFromLatestCompletionRequest(ctx, store, run.ID)
		if len(transcript) > 0 {
			sawRequest = true
			messages = appendFinalAnswerIfMissing(ctx, store, run.ID, transcript)
			continue
		}
		if !sawRequest {
			continue
		}
		if run.Input != "" {
			messages = append(messages, agent.Message{Role: agent.RoleUser, Content: run.Input, RunID: run.ID, CreatedAt: run.CreatedAt})
		}
		if answer := finalAnswerForRunContext(ctx, store, run.ID); answer != "" {
			messages = append(messages, agent.Message{Role: agent.RoleAssistant, Content: answer, RunID: run.ID})
		}
	}
	if !sawRequest {
		return nil
	}
	return messages
}

func visibleConversationFromLatestCompletionRequest(ctx context.Context, store agent.Store, runID string) []agent.Message {
	events, err := store.ListRunEvents(ctx, runID)
	if err != nil {
		return nil
	}
	var latest []agent.Message
	for _, event := range events {
		if event.Type != agent.EventCompletionRequest {
			continue
		}
		messages := visibleConversationFromCompletionRequestData(event.Data)
		if len(messages) > 0 {
			latest = messages
		}
	}
	return latest
}

func visibleConversationFromCompletionRequestData(raw json.RawMessage) []agent.Message {
	if len(raw) == 0 || !json.Valid(raw) {
		return nil
	}
	var data map[string]any
	if json.Unmarshal(raw, &data) != nil {
		return nil
	}
	items, _ := data["messages"].([]any)
	messages := make([]agent.Message, 0, len(items))
	for _, item := range items {
		encoded, err := json.Marshal(item)
		if err != nil {
			continue
		}
		message, ok := completionMessageFromEventData(encoded)
		if !ok {
			continue
		}
		switch message.Role {
		case agent.RoleUser, agent.RoleTool:
			messages = append(messages, message)
		case agent.RoleAssistant:
			if strings.TrimSpace(message.Content) != "" || len(message.ToolCalls) > 0 {
				messages = append(messages, message)
			}
		}
	}
	return messages
}

func conversationFromCompletionEvents(ctx context.Context, store agent.Store, runs []agent.Run) []agent.Message {
	messages := []agent.Message{}
	sawCompletionEvents := false
	for _, run := range runs {
		events, err := store.ListRunEvents(ctx, run.ID)
		if err != nil {
			continue
		}
		runSawCompletionEvents := false
		runHadAssistant := false
		for _, event := range events {
			switch event.Type {
			case agent.EventCompletionMessage:
				message, ok := completionMessageFromEventData(event.Data)
				if !ok {
					continue
				}
				sawCompletionEvents = true
				runSawCompletionEvents = true
				if message.Role != agent.RoleUser && message.Role != agent.RoleAssistant && message.Role != agent.RoleTool {
					continue
				}
				if message.Role == agent.RoleAssistant && strings.TrimSpace(message.Content) == "" && len(message.ToolCalls) == 0 {
					continue
				}
				if message.RunID == "" {
					message.RunID = run.ID
				}
				messages = append(messages, message)
				if message.Role == agent.RoleAssistant {
					runHadAssistant = true
				}
			case agent.EventCompletionToolResult:
				sawCompletionEvents = true
				runSawCompletionEvents = true
				message, ok := completionToolResultFromEventData(event.Data)
				if !ok {
					continue
				}
				if message.RunID == "" {
					message.RunID = run.ID
				}
				messages = append(messages, message)
			}
		}
		if runSawCompletionEvents && !runHadAssistant {
			if answer := finalAnswerForRunContext(ctx, store, run.ID); answer != "" {
				messages = append(messages, agent.Message{Role: agent.RoleAssistant, Content: answer, RunID: run.ID})
			}
			continue
		}
		if runSawCompletionEvents {
			continue
		}
		if transcript := visibleConversationFromRunCreatedTranscript(ctx, store, run.ID); len(transcript) > 0 {
			messages = append(messages, appendFinalAnswerIfMissing(ctx, store, run.ID, transcript)...)
			continue
		}
		if run.Input != "" {
			messages = append(messages, agent.Message{Role: agent.RoleUser, Content: run.Input, RunID: run.ID, CreatedAt: run.CreatedAt})
		}
		if answer := finalAnswerForRunContext(ctx, store, run.ID); answer != "" {
			messages = append(messages, agent.Message{Role: agent.RoleAssistant, Content: answer, RunID: run.ID})
		}
	}
	if !sawCompletionEvents && len(messages) == 0 {
		return nil
	}
	return messages
}

func visibleConversationFromRunCreatedTranscript(ctx context.Context, store agent.Store, runID string) []agent.Message {
	events, err := store.ListRunEvents(ctx, runID)
	if err != nil {
		return nil
	}
	for _, event := range events {
		if event.Type != agent.EventRunCreated {
			continue
		}
		messages := agentTranscriptMessagesFromRunCreatedData(event.Data)
		visible := make([]agent.Message, 0, len(messages))
		for _, message := range messages {
			if message.Role == agent.RoleUser || message.Role == agent.RoleAssistant {
				visible = append(visible, message)
			}
		}
		return visible
	}
	return nil
}

func appendFinalAnswerIfMissing(ctx context.Context, store agent.Store, runID string, messages []agent.Message) []agent.Message {
	answer := finalAnswerForRunContext(ctx, store, runID)
	if answer == "" {
		return messages
	}
	if len(messages) > 0 {
		last := messages[len(messages)-1]
		if last.Role == agent.RoleAssistant && last.Content == answer {
			return messages
		}
	}
	return append(messages, agent.Message{Role: agent.RoleAssistant, Content: answer, RunID: runID})
}

func completionMessageEventValue(message agent.Message) map[string]any {
	value := completionRequestMessageValue(message)
	value["format"] = "kube-insight.agent.message.v1"
	return value
}

func completionRequestMessageValue(message agent.Message) map[string]any {
	value := map[string]any{
		"role":    message.Role,
		"content": message.Content,
	}
	if len(message.ToolCalls) > 0 {
		toolCalls := make([]map[string]any, 0, len(message.ToolCalls))
		for _, call := range message.ToolCalls {
			callType := call.Type
			if callType == "" {
				callType = "function"
			}
			toolCalls = append(toolCalls, map[string]any{
				"id":   call.ID,
				"type": callType,
				"function": map[string]any{
					"name":      call.Function.Name,
					"arguments": call.Function.Arguments,
				},
			})
		}
		value["tool_calls"] = toolCalls
	}
	if message.ToolCallID != "" {
		value["tool_call_id"] = message.ToolCallID
		value["toolCallId"] = message.ToolCallID
	}
	if message.ToolName != "" {
		value["name"] = message.ToolName
	}
	if message.ID != "" {
		value["id"] = message.ID
	}
	if message.RunID != "" {
		value["runId"] = message.RunID
	}
	if !message.CreatedAt.IsZero() {
		value["createdAt"] = message.CreatedAt.Format(time.RFC3339Nano)
	}
	if len(message.Metadata) > 0 && json.Valid(message.Metadata) {
		var metadata any
		if json.Unmarshal(message.Metadata, &metadata) == nil {
			value["metadata"] = metadata
		}
	}
	return value
}

func completionMessageFromEventData(raw json.RawMessage) (agent.Message, bool) {
	if len(raw) == 0 || !json.Valid(raw) {
		return agent.Message{}, false
	}
	var object map[string]any
	if json.Unmarshal(raw, &object) != nil {
		return agent.Message{}, false
	}
	role, _ := object["role"].(string)
	content, _ := object["content"].(string)
	if role == "" {
		return agent.Message{}, false
	}
	message := agent.Message{Role: agent.MessageRole(role), Content: content}
	message.ToolCalls = toolCallsFromEventObject(object)
	message.ToolCallID = firstNonEmptyStringFromMap(object, "tool_call_id", "toolCallId")
	message.ToolName = firstNonEmptyStringFromMap(object, "name", "toolName")
	message.ID, _ = object["id"].(string)
	message.RunID, _ = object["runId"].(string)
	if createdAt, _ := object["createdAt"].(string); createdAt != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, createdAt); err == nil {
			message.CreatedAt = parsed
		}
	}
	if metadata, ok := object["metadata"]; ok {
		if encoded, err := json.Marshal(metadata); err == nil {
			message.Metadata = encoded
		}
	}
	return message, true
}

func completionToolResultFromEventData(raw json.RawMessage) (agent.Message, bool) {
	message, ok := completionMessageFromEventData(raw)
	if !ok {
		return agent.Message{}, false
	}
	if message.Role != agent.RoleTool {
		return agent.Message{}, false
	}
	if strings.TrimSpace(message.Content) == "" {
		var object map[string]any
		if json.Unmarshal(raw, &object) == nil {
			message.Content, _ = object["outputSummary"].(string)
		}
	}
	if message.ToolCallID == "" {
		return agent.Message{}, false
	}
	return message, true
}

func toolCallsFromEventObject(object map[string]any) []agent.ToolCall {
	items, _ := object["tool_calls"].([]any)
	if len(items) == 0 {
		return nil
	}
	calls := make([]agent.ToolCall, 0, len(items))
	for _, item := range items {
		callObject, _ := item.(map[string]any)
		if callObject == nil {
			continue
		}
		functionObject, _ := callObject["function"].(map[string]any)
		id, _ := callObject["id"].(string)
		callType, _ := callObject["type"].(string)
		name, _ := functionObject["name"].(string)
		arguments, _ := functionObject["arguments"].(string)
		if id == "" && name == "" {
			continue
		}
		calls = append(calls, agent.ToolCall{
			ID:   id,
			Type: callType,
			Function: agent.FunctionCall{
				Name:      name,
				Arguments: arguments,
			},
		})
	}
	return calls
}

func firstNonEmptyStringFromMap(object map[string]any, keys ...string) string {
	for _, key := range keys {
		value, _ := object[key].(string)
		if value != "" {
			return value
		}
	}
	return ""
}

func agentTranscriptMessagesFromRunCreatedData(raw json.RawMessage) []agent.Message {
	if len(raw) == 0 || !json.Valid(raw) {
		return nil
	}
	var data map[string]any
	if json.Unmarshal(raw, &data) != nil {
		return nil
	}
	transcript, _ := data["transcript"].(map[string]any)
	items, _ := transcript["messages"].([]any)
	if len(items) == 0 {
		return nil
	}
	messages := make([]agent.Message, 0, len(items))
	for _, item := range items {
		object, _ := item.(map[string]any)
		role, _ := object["role"].(string)
		content, _ := object["content"].(string)
		if role == "" || content == "" {
			continue
		}
		message := agent.Message{Role: agent.MessageRole(role), Content: content}
		message.ID, _ = object["id"].(string)
		message.RunID, _ = object["runId"].(string)
		if createdAt, _ := object["createdAt"].(string); createdAt != "" {
			if parsed, err := time.Parse(time.RFC3339Nano, createdAt); err == nil {
				message.CreatedAt = parsed
			}
		}
		if metadata, ok := object["metadata"]; ok {
			if encoded, err := json.Marshal(metadata); err == nil {
				message.Metadata = encoded
			}
		}
		messages = append(messages, message)
	}
	return messages
}

func finalAnswerForRunContext(ctx context.Context, store agent.Store, runID string) string {
	events, err := store.ListRunEvents(ctx, runID)
	if err != nil {
		return ""
	}
	answer := ""
	for _, event := range events {
		if event.Type != agent.EventFinalAnswer && event.Type != agent.EventMessageDone {
			continue
		}
		var data agent.MessageEventData
		if json.Unmarshal(event.Data, &data) != nil || data.Role != agent.RoleAssistant || strings.TrimSpace(data.Content) == "" {
			continue
		}
		answer = data.Content
	}
	return answer
}

func agentRunClientContextMessage(metadata json.RawMessage) string {
	if len(metadata) == 0 || !json.Valid(metadata) {
		return ""
	}
	var record map[string]any
	if err := json.Unmarshal(metadata, &record); err != nil || record == nil {
		return ""
	}
	contextRecord := mapValue(record["clientContext"])
	if contextRecord == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("Client context for this run:\n")
	b.WriteString("- Use this to interpret relative user dates/times such as today, yesterday, last 10 minutes, or this week. It is not proof about Kubernetes state. Use UTC for tool arguments and SQL predicates, but render final-answer timestamps in the client time zone when provided, preferably with the IANA time zone name or numeric offset instead of ambiguous abbreviations; include UTC only as a secondary reference when useful.\n")
	writeClientContextLine(&b, "Client sent at", contextString(contextRecord, "sentAt"))
	writeClientContextLine(&b, "Client local time", contextString(contextRecord, "localTime"))
	writeClientContextLine(&b, "Client time zone", contextString(contextRecord, "timeZone"))
	writeClientContextLine(&b, "Client UTC offset minutes", contextNumberOrString(contextRecord, "timezoneOffsetMinutes"))
	writeClientContextLine(&b, "Client locale", contextString(contextRecord, "locale"))
	writeClientContextLine(&b, "Client languages", contextStringList(contextRecord, "languages"))
	writeClientContextLine(&b, "Page URL", contextString(contextRecord, "pageURL"))
	value := strings.TrimSpace(b.String())
	if value == "Client context for this run:" {
		return ""
	}
	return value
}

func writeClientContextLine(b *strings.Builder, key, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	if len(value) > 160 {
		value = value[:157] + "..."
	}
	b.WriteString("- ")
	b.WriteString(key)
	b.WriteString(": ")
	b.WriteString(value)
	b.WriteString(".\n")
}

func mapValue(value any) map[string]any {
	record, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	return record
}

func contextString(record map[string]any, key string) string {
	value, ok := record[key]
	if !ok {
		return ""
	}
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return text
}

func contextNumberOrString(record map[string]any, key string) string {
	value, ok := record[key]
	if !ok {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case int:
		return strconv.Itoa(typed)
	default:
		return ""
	}
}

func contextStringList(record map[string]any, key string) string {
	value, ok := record[key]
	if !ok {
		return ""
	}
	items, ok := value.([]any)
	if !ok {
		return contextString(record, key)
	}
	parts := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, ", ")
}
