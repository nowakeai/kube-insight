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
	if store == nil || run.SessionID == "" {
		return nil
	}
	session, err := store.GetSession(ctx, run.SessionID)
	if err != nil {
		return nil
	}
	prior := make([]agent.Run, 0, len(session.Runs))
	for _, candidate := range session.Runs {
		if candidate.ID == run.ID || !candidate.CreatedAt.Before(run.CreatedAt) || !agentRunStatusTerminal(candidate.Status) {
			continue
		}
		prior = append(prior, candidate)
	}
	if len(prior) == 0 {
		return nil
	}
	latest := prior[len(prior)-1]
	if transcript := visibleConversationFromRunCreatedTranscript(ctx, store, latest.ID); len(transcript) > 0 {
		return appendFinalAnswerIfMissing(ctx, store, latest.ID, transcript)
	}
	messages := make([]agent.Message, 0, len(prior)*2)
	for _, candidate := range prior {
		messages = append(messages, agent.Message{Role: agent.RoleUser, Content: candidate.Input, RunID: candidate.ID, CreatedAt: candidate.CreatedAt})
		if answer := finalAnswerForRunContext(ctx, store, candidate.ID); answer != "" {
			messages = append(messages, agent.Message{Role: agent.RoleAssistant, Content: answer, RunID: candidate.ID})
		}
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

func agentTranscriptMessagesValue(messages []agent.Message) []map[string]any {
	values := make([]map[string]any, 0, len(messages))
	for _, message := range messages {
		value := map[string]any{
			"role":    message.Role,
			"content": message.Content,
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
		values = append(values, value)
	}
	return values
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
