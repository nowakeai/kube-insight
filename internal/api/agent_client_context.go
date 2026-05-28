package api

import (
	"encoding/json"
	"strconv"
	"strings"
)

func agentRunClientContextMessage(metadata json.RawMessage, input string) string {
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
	b.WriteString("- Treat Client sent at/local time/time zone as the current time base for this run. Use this, not server time, model training time, or tool checked_at timestamps, to interpret relative user dates/times such as today, yesterday, last 10 minutes, or this week. It is not proof about Kubernetes state. Compute absolute from/to bounds before the first data tool call. Use UTC for tool arguments and SQL predicates, but render final-answer timestamps in the client time zone when provided, preferably with the IANA time zone name or numeric offset instead of ambiguous abbreviations; include UTC only as a secondary reference when useful.\n")
	b.WriteString("- Final answers that include any query window, observation timestamp, change timestamp, or lookback window must include the client-local time zone label when provided. If a tool returns only UTC, convert or restate the same window with the client time zone next to it before answering.\n")
	b.WriteString("- Client locale and languages are UI formatting context only. Do not use them to choose the response language, even when they are values such as zh-CN; answer in the language of the user's current prompt unless the user explicitly asks for another language.\n")
	if language := detectedPromptLanguage(input); language != "" {
		b.WriteString("- Detected current prompt language: ")
		b.WriteString(language)
		b.WriteString(". Response language for this run: ")
		b.WriteString(language)
		b.WriteString(". Do not answer in a different language because of browser locale or client languages.\n")
	}
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

func detectedPromptLanguage(input string) string {
	han := 0
	latin := 0
	for _, r := range input {
		switch {
		case r >= '\u4e00' && r <= '\u9fff':
			han++
		case (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z'):
			latin++
		}
	}
	if han > 0 {
		return "Chinese"
	}
	if latin > 0 {
		return "English"
	}
	return ""
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
