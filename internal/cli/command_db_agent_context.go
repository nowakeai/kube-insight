package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"kube-insight/internal/agent"

	"github.com/spf13/cobra"
)

type agentContextAuditOutput struct {
	Run                 agentContextAuditRun       `json:"run"`
	Requests            []agentContextAuditRequest `json:"requests"`
	LegacyMessages      []agentContextAuditMessage `json:"legacyMessages,omitempty"`
	CompatibilityReplay []agentContextAuditMessage `json:"compatibilityReplay,omitempty"`
	EventTypes          map[string]int             `json:"eventTypes,omitempty"`
	Warning             string                     `json:"warning,omitempty"`
}

type agentContextAuditRun struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionId"`
	Status    string `json:"status"`
	Input     string `json:"input"`
	Provider  string `json:"provider,omitempty"`
	Model     string `json:"model,omitempty"`
	CreatedAt string `json:"createdAt"`
}

type agentContextAuditRequest struct {
	EventID      string                     `json:"eventId"`
	Sequence     int64                      `json:"sequence"`
	CreatedAt    string                     `json:"createdAt"`
	Mode         string                     `json:"mode,omitempty"`
	Provider     string                     `json:"provider,omitempty"`
	Model        string                     `json:"model,omitempty"`
	MessageCount int                        `json:"messageCount"`
	ToolCount    int                        `json:"toolCount"`
	Options      map[string]any             `json:"options,omitempty"`
	Messages     []agentContextAuditMessage `json:"messages"`
}

type agentContextAuditMessage struct {
	Index          int      `json:"index"`
	Role           string   `json:"role"`
	Content        string   `json:"content,omitempty"`
	ContentChars   int      `json:"contentChars"`
	ToolCallID     string   `json:"toolCallId,omitempty"`
	ToolName       string   `json:"toolName,omitempty"`
	ToolCalls      []string `json:"toolCalls,omitempty"`
	ContentPreview string   `json:"contentPreview,omitempty"`
}

func dbAgentContextCommand(ctx context.Context, stdout io.Writer, state *cliState) *cobra.Command {
	var all bool
	var fullContent bool
	var output string
	cmd := &cobra.Command{
		Use:   "agent-context RUN_ID",
		Short: "Inspect the recorded provider-facing completion request for an agent run.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateOutputFormat(output); err != nil {
				return err
			}
			rt, err := loadRuntimeConfig(cmd, state, "", false)
			if err != nil {
				return err
			}
			store, err := openCLIAgentStore(ctx, cmd, state, rt, "agent-context")
			if err != nil {
				return err
			}
			defer store.Close()
			report, err := buildAgentContextAudit(ctx, store, args[0], all)
			if err != nil {
				return err
			}
			if output == outputJSON {
				return writeJSON(stdout, report)
			}
			return writeAgentContextAuditTable(stdout, report, fullContent)
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "Show all completion.request events; default shows only the latest provider call")
	cmd.Flags().BoolVar(&fullContent, "full-content", false, "Do not truncate message content in table output")
	addOutputFlag(cmd, &output, outputTable)
	return cmd
}

type cliAgentStore interface {
	agent.Store
	Close() error
}

func openCLIAgentStore(ctx context.Context, cmd *cobra.Command, state *cliState, rt runtimeSettings, command string) (cliAgentStore, error) {
	store, err := openCLIReadStore(ctx, cmd, state, rt, command)
	if err != nil {
		return nil, err
	}
	agentStore, ok := store.(cliAgentStore)
	if !ok {
		_ = store.Close()
		return nil, fmt.Errorf("%s store does not support agent sessions", storageDriver(rt.Config))
	}
	return agentStore, nil
}

func buildAgentContextAudit(ctx context.Context, store agent.Store, runID string, all bool) (agentContextAuditOutput, error) {
	run, err := store.GetRun(ctx, runID)
	if err != nil {
		return agentContextAuditOutput{}, err
	}
	events, err := store.ListRunEvents(ctx, runID)
	if err != nil {
		return agentContextAuditOutput{}, err
	}
	requests := make([]agentContextAuditRequest, 0)
	eventTypes := map[string]int{}
	for _, event := range events {
		eventTypes[string(event.Type)]++
		if event.Type != agent.EventCompletionRequest {
			continue
		}
		request, ok := agentContextAuditRequestFromEvent(event)
		if ok {
			requests = append(requests, request)
		}
	}
	var legacyMessages []agentContextAuditMessage
	var compatibilityReplay []agentContextAuditMessage
	warning := ""
	if len(requests) == 0 {
		warning = "no completion.request events recorded; exact provider request is unavailable for this run"
		legacyMessages = legacyAgentContextAuditMessages(run, events)
		compatibilityReplay = compatibilityAgentContextReplay(ctx, store, run)
	}
	if !all && len(requests) > 0 {
		requests = requests[len(requests)-1:]
	}
	return agentContextAuditOutput{
		Run: agentContextAuditRun{
			ID:        run.ID,
			SessionID: run.SessionID,
			Status:    string(run.Status),
			Input:     run.Input,
			Provider:  run.Provider,
			Model:     run.Model,
			CreatedAt: humanTime(run.CreatedAt),
		},
		Requests:            requests,
		LegacyMessages:      legacyMessages,
		CompatibilityReplay: compatibilityReplay,
		EventTypes:          eventTypes,
		Warning:             warning,
	}, nil
}

func agentContextAuditRequestFromEvent(event agent.RunEvent) (agentContextAuditRequest, bool) {
	if len(event.Data) == 0 || !json.Valid(event.Data) {
		return agentContextAuditRequest{}, false
	}
	var data map[string]any
	if err := json.Unmarshal(event.Data, &data); err != nil {
		return agentContextAuditRequest{}, false
	}
	messages, _ := data["messages"].([]any)
	tools, _ := data["tools"].([]any)
	request := agentContextAuditRequest{
		EventID:      event.ID,
		Sequence:     event.Sequence,
		CreatedAt:    humanTime(event.CreatedAt),
		Mode:         auditStringField(data, "mode"),
		Provider:     auditStringField(data, "provider"),
		Model:        auditStringField(data, "model"),
		MessageCount: len(messages),
		ToolCount:    len(tools),
		Options:      auditMapField(data, "options"),
		Messages:     make([]agentContextAuditMessage, 0, len(messages)),
	}
	for index, item := range messages {
		message, ok := agentContextAuditMessageFromValue(index, item)
		if ok {
			request.Messages = append(request.Messages, message)
		}
	}
	return request, true
}

func agentContextAuditMessageFromValue(index int, value any) (agentContextAuditMessage, bool) {
	data, ok := value.(map[string]any)
	if !ok {
		return agentContextAuditMessage{}, false
	}
	content := auditStringField(data, "content")
	message := agentContextAuditMessage{
		Index:          index,
		Role:           auditStringField(data, "role"),
		Content:        content,
		ContentChars:   len([]rune(content)),
		ToolCallID:     firstAuditStringField(data, "tool_call_id", "toolCallId"),
		ToolName:       firstAuditStringField(data, "name", "toolName"),
		ToolCalls:      toolCallNames(data["tool_calls"]),
		ContentPreview: shortText(content, 180),
	}
	return message, message.Role != ""
}

func toolCallNames(value any) []string {
	items, _ := value.([]any)
	names := make([]string, 0, len(items))
	for _, item := range items {
		call, _ := item.(map[string]any)
		function, _ := call["function"].(map[string]any)
		name := auditStringField(function, "name")
		if name == "" {
			name = auditStringField(call, "id")
		}
		if name != "" {
			names = append(names, name)
		}
	}
	return names
}

func writeAgentContextAuditTable(stdout io.Writer, report agentContextAuditOutput, fullContent bool) error {
	if _, err := fmt.Fprintf(stdout, "Run: %s  status=%s  session=%s  provider=%s  model=%s\n", report.Run.ID, report.Run.Status, report.Run.SessionID, report.Run.Provider, report.Run.Model); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(stdout, "Input: %s\n", report.Run.Input); err != nil {
		return err
	}
	if report.Warning != "" {
		if _, err := fmt.Fprintf(stdout, "Warning: %s\n", report.Warning); err != nil {
			return err
		}
		if len(report.EventTypes) > 0 {
			rows := make([][]string, 0, len(report.EventTypes))
			for _, eventType := range sortedAuditEventTypes(report.EventTypes) {
				rows = append(rows, []string{eventType, fmt.Sprintf("%d", report.EventTypes[eventType])})
			}
			if err := writeSection(stdout, "\nRecorded event types", []string{"type", "count"}, rows); err != nil {
				return err
			}
		}
		if len(report.LegacyMessages) > 0 {
			if err := writeAgentContextMessagesTable(stdout, "\nLegacy visible messages", report.LegacyMessages, fullContent); err != nil {
				return err
			}
		}
		if len(report.CompatibilityReplay) > 0 {
			if err := writeAgentContextMessagesTable(stdout, "\nCompatibility replay candidate", report.CompatibilityReplay, fullContent); err != nil {
				return err
			}
		}
	}
	for _, request := range report.Requests {
		if _, err := fmt.Fprintf(stdout, "\nRequest seq=%d event=%s mode=%s provider=%s model=%s messages=%d tools=%d created=%s\n", request.Sequence, request.EventID, request.Mode, request.Provider, request.Model, request.MessageCount, request.ToolCount, request.CreatedAt); err != nil {
			return err
		}
		if err := writeAgentContextMessagesTable(stdout, "", request.Messages, fullContent); err != nil {
			return err
		}
	}
	return nil
}

func legacyAgentContextAuditMessages(run agent.Run, events []agent.RunEvent) []agentContextAuditMessage {
	messages := []agentContextAuditMessage{}
	if run.Input != "" {
		messages = append(messages, agentContextAuditMessage{
			Index:          len(messages),
			Role:           string(agent.RoleUser),
			Content:        run.Input,
			ContentChars:   len([]rune(run.Input)),
			ContentPreview: shortText(run.Input, 180),
		})
	}
	finalAnswer := ""
	for _, event := range events {
		if event.Type != agent.EventFinalAnswer && event.Type != agent.EventMessageDone {
			continue
		}
		var data agent.MessageEventData
		if json.Unmarshal(event.Data, &data) == nil && data.Role == agent.RoleAssistant && strings.TrimSpace(data.Content) != "" {
			finalAnswer = data.Content
		}
	}
	if finalAnswer != "" {
		messages = append(messages, agentContextAuditMessage{
			Index:          len(messages),
			Role:           string(agent.RoleAssistant),
			Content:        finalAnswer,
			ContentChars:   len([]rune(finalAnswer)),
			ContentPreview: shortText(finalAnswer, 180),
		})
	}
	return messages
}

func compatibilityAgentContextReplay(ctx context.Context, store agent.Store, run agent.Run) []agentContextAuditMessage {
	if run.SessionID == "" {
		return legacyAgentContextAuditMessages(run, nil)
	}
	session, err := store.GetSession(ctx, run.SessionID)
	if err != nil {
		return legacyAgentContextAuditMessages(run, nil)
	}
	prior := compatibleVisiblePriorRuns(session.Runs, run)
	messages := []agentContextAuditMessage{}
	for _, candidate := range prior {
		events, err := store.ListRunEvents(ctx, candidate.ID)
		if err != nil {
			continue
		}
		for _, message := range legacyAgentContextAuditMessages(candidate, events) {
			message.Index = len(messages)
			messages = append(messages, message)
		}
	}
	if run.Input != "" {
		messages = append(messages, agentContextAuditMessage{
			Index:          len(messages),
			Role:           string(agent.RoleUser),
			Content:        run.Input,
			ContentChars:   len([]rune(run.Input)),
			ContentPreview: shortText(run.Input, 180),
		})
	}
	return messages
}

func compatibleVisiblePriorRuns(runs []agent.Run, target agent.Run) []agent.Run {
	sorted := append([]agent.Run(nil), runs...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].CreatedAt.Equal(sorted[j].CreatedAt) {
			return sorted[i].ID < sorted[j].ID
		}
		return sorted[i].CreatedAt.Before(sorted[j].CreatedAt)
	})
	visible := []agent.Run{}
	for _, candidate := range sorted {
		if candidate.ID == target.ID || !candidate.CreatedAt.Before(target.CreatedAt) || !auditRunTerminal(candidate.Status) || auditMetadataString(candidate.Metadata, "parentRunId") != "" {
			continue
		}
		retryOf := auditMetadataString(candidate.Metadata, "retryOfRunId")
		if retryOf == "" {
			visible = append(visible, candidate)
			continue
		}
		replaceIndex := -1
		retryRoot := auditMetadataString(candidate.Metadata, "retryRootRunId")
		for index, existing := range visible {
			if existing.ID == retryOf || (retryRoot != "" && (existing.ID == retryRoot || auditMetadataString(existing.Metadata, "retryRootRunId") == retryRoot)) {
				replaceIndex = index
				break
			}
		}
		if replaceIndex >= 0 {
			visible = append(visible[:replaceIndex], candidate)
		} else {
			visible = append(visible, candidate)
		}
	}
	return visible
}

func auditRunTerminal(status agent.RunStatus) bool {
	switch status {
	case agent.RunCompleted, agent.RunFailed, agent.RunCancelled:
		return true
	default:
		return false
	}
}

func auditMetadataString(raw json.RawMessage, key string) string {
	if len(raw) == 0 || !json.Valid(raw) {
		return ""
	}
	var record map[string]any
	if json.Unmarshal(raw, &record) != nil {
		return ""
	}
	return auditStringField(record, key)
}

func writeAgentContextMessagesTable(stdout io.Writer, title string, messages []agentContextAuditMessage, fullContent bool) error {
	rows := make([][]string, 0, len(messages))
	for _, message := range messages {
		content := message.ContentPreview
		if fullContent {
			content = message.Content
		}
		rows = append(rows, []string{
			fmt.Sprintf("%d", message.Index),
			message.Role,
			message.ToolName,
			strings.Join(message.ToolCalls, ","),
			fmt.Sprintf("%d", message.ContentChars),
			content,
		})
	}
	if title != "" {
		return writeSection(stdout, title, []string{"#", "role", "tool", "tool_calls", "chars", "content"}, rows)
	}
	return writeTable(stdout, []string{"#", "role", "tool", "tool_calls", "chars", "content"}, rows)
}

func sortedAuditEventTypes(counts map[string]int) []string {
	values := make([]string, 0, len(counts))
	for eventType := range counts {
		values = append(values, eventType)
	}
	sort.Strings(values)
	return values
}

func auditStringField(record map[string]any, key string) string {
	value, _ := record[key].(string)
	return value
}

func firstAuditStringField(record map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := auditStringField(record, key); value != "" {
			return value
		}
	}
	return ""
}

func auditMapField(record map[string]any, key string) map[string]any {
	value, _ := record[key].(map[string]any)
	return value
}
