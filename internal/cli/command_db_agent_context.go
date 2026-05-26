package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"kube-insight/internal/agent"

	"github.com/spf13/cobra"
)

type agentContextAuditOutput struct {
	Run      agentContextAuditRun       `json:"run"`
	Requests []agentContextAuditRequest `json:"requests"`
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
	for _, event := range events {
		if event.Type != agent.EventCompletionRequest {
			continue
		}
		request, ok := agentContextAuditRequestFromEvent(event)
		if ok {
			requests = append(requests, request)
		}
	}
	if len(requests) == 0 {
		return agentContextAuditOutput{}, fmt.Errorf("run %s has no completion.request events", runID)
	}
	if !all {
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
		Requests: requests,
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
		Mode:         stringField(data, "mode"),
		Provider:     stringField(data, "provider"),
		Model:        stringField(data, "model"),
		MessageCount: len(messages),
		ToolCount:    len(tools),
		Options:      mapField(data, "options"),
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
	content := stringField(data, "content")
	message := agentContextAuditMessage{
		Index:          index,
		Role:           stringField(data, "role"),
		Content:        content,
		ContentChars:   len([]rune(content)),
		ToolCallID:     firstStringField(data, "tool_call_id", "toolCallId"),
		ToolName:       firstStringField(data, "name", "toolName"),
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
		name := stringField(function, "name")
		if name == "" {
			name = stringField(call, "id")
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
	for _, request := range report.Requests {
		if _, err := fmt.Fprintf(stdout, "\nRequest seq=%d event=%s mode=%s provider=%s model=%s messages=%d tools=%d created=%s\n", request.Sequence, request.EventID, request.Mode, request.Provider, request.Model, request.MessageCount, request.ToolCount, request.CreatedAt); err != nil {
			return err
		}
		rows := make([][]string, 0, len(request.Messages))
		for _, message := range request.Messages {
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
		if err := writeTable(stdout, []string{"#", "role", "tool", "tool_calls", "chars", "content"}, rows); err != nil {
			return err
		}
	}
	return nil
}

func stringField(record map[string]any, key string) string {
	value, _ := record[key].(string)
	return value
}

func firstStringField(record map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := stringField(record, key); value != "" {
			return value
		}
	}
	return ""
}

func mapField(record map[string]any, key string) map[string]any {
	value, _ := record[key].(map[string]any)
	return value
}
