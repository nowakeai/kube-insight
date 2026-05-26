package agent

import (
	"context"
	"encoding/json"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type modelContextRecorderKey struct{}

type modelContextRecorderConfig struct {
	Store    Store
	RunID    string
	Provider string
	Model    string
}

func withModelContextRecorder(ctx context.Context, cfg modelContextRecorderConfig) context.Context {
	if cfg.Store == nil || cfg.RunID == "" {
		return ctx
	}
	return context.WithValue(ctx, modelContextRecorderKey{}, cfg)
}

func modelContextRecorderFromContext(ctx context.Context) (modelContextRecorderConfig, bool) {
	cfg, ok := ctx.Value(modelContextRecorderKey{}).(modelContextRecorderConfig)
	return cfg, ok && cfg.Store != nil && cfg.RunID != ""
}

type modelContextRecorderMiddleware struct {
	adk.BaseChatModelAgentMiddleware
}

func (m modelContextRecorderMiddleware) WrapModel(ctx context.Context, inner model.BaseChatModel, mc *adk.ModelContext) (model.BaseChatModel, error) {
	cfg, ok := modelContextRecorderFromContext(ctx)
	if !ok {
		return inner, nil
	}
	return modelContextRecordingModel{inner: inner, cfg: cfg, mc: mc}, nil
}

type modelContextRecordingModel struct {
	inner model.BaseChatModel
	cfg   modelContextRecorderConfig
	mc    *adk.ModelContext
}

func (m modelContextRecordingModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	if err := m.record(ctx, "generate", input, opts...); err != nil {
		return nil, err
	}
	return m.inner.Generate(ctx, input, opts...)
}

func (m modelContextRecordingModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	if err := m.record(ctx, "stream", input, opts...); err != nil {
		return nil, err
	}
	return m.inner.Stream(ctx, input, opts...)
}

func (m modelContextRecordingModel) record(ctx context.Context, mode string, input []*schema.Message, opts ...model.Option) error {
	base := &model.Options{}
	if m.mc != nil && len(m.mc.Tools) > 0 {
		base.Tools = m.mc.Tools
	}
	common := model.GetCommonOptions(base, opts...)
	data := map[string]any{
		"format":   "kube-insight.agent.completion.v1",
		"mode":     mode,
		"provider": m.cfg.Provider,
		"model":    m.cfg.Model,
		"messages": schemaMessagesValue(input),
	}
	if options := modelOptionsValue(common); len(options) > 0 {
		data["options"] = options
	}
	if tools := toolInfosValue(common.Tools); len(tools) > 0 {
		data["tools"] = tools
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = m.cfg.Store.AppendRunEvent(ctx, m.cfg.RunID, AppendEventInput{Type: EventCompletionRequest, Data: encoded})
	return err
}

func schemaMessagesValue(messages []*schema.Message) []map[string]any {
	values := make([]map[string]any, 0, len(messages))
	for _, message := range messages {
		values = append(values, schemaMessageValue(message))
	}
	return values
}

func schemaMessageValue(message *schema.Message) map[string]any {
	if message == nil {
		return map[string]any{}
	}
	value := map[string]any{
		"role":    string(message.Role),
		"content": message.Content,
	}
	if len(message.ToolCalls) > 0 {
		value["tool_calls"] = schemaToolCallsValue(message.ToolCalls)
	}
	if message.ToolCallID != "" {
		value["tool_call_id"] = message.ToolCallID
	}
	if message.ToolName != "" {
		value["name"] = message.ToolName
	}
	if message.Name != "" {
		value["message_name"] = message.Name
	}
	if message.ReasoningContent != "" {
		value["reasoning_content"] = message.ReasoningContent
	}
	return value
}

func schemaToolCallsValue(calls []schema.ToolCall) []map[string]any {
	values := make([]map[string]any, 0, len(calls))
	for _, call := range calls {
		callType := call.Type
		if callType == "" {
			callType = "function"
		}
		values = append(values, map[string]any{
			"id":   call.ID,
			"type": callType,
			"function": map[string]any{
				"name":      call.Function.Name,
				"arguments": call.Function.Arguments,
			},
		})
	}
	return values
}

func toolInfosValue(tools []*schema.ToolInfo) []map[string]any {
	values := make([]map[string]any, 0, len(tools))
	for _, info := range tools {
		if info == nil {
			continue
		}
		value := map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        info.Name,
				"description": info.Desc,
			},
		}
		if info.ParamsOneOf != nil {
			if params, err := info.ParamsOneOf.ToJSONSchema(); err == nil && params != nil {
				value["function"].(map[string]any)["parameters"] = params
			}
		}
		values = append(values, value)
	}
	return values
}

func modelOptionsValue(opts *model.Options) map[string]any {
	if opts == nil {
		return nil
	}
	value := map[string]any{}
	if opts.Temperature != nil {
		value["temperature"] = *opts.Temperature
	}
	if opts.MaxTokens != nil {
		value["maxTokens"] = *opts.MaxTokens
	}
	if opts.Model != nil {
		value["model"] = *opts.Model
	}
	if opts.TopP != nil {
		value["topP"] = *opts.TopP
	}
	if len(opts.Stop) > 0 {
		value["stop"] = opts.Stop
	}
	if opts.ToolChoice != nil {
		value["toolChoice"] = string(*opts.ToolChoice)
	}
	if len(opts.AllowedToolNames) > 0 {
		value["allowedToolNames"] = opts.AllowedToolNames
	}
	return value
}
