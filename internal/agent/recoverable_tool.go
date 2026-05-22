package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// WrapRecoverableToolErrors converts tool execution errors into successful tool
// messages with an explicit error payload. This keeps autonomous agent loops
// alive so the model can inspect the failure, revise its plan, and retry with a
// better tool call instead of failing the whole run on the first bad SQL/query.
func WrapRecoverableToolErrors(tools []tool.BaseTool) []tool.BaseTool {
	if len(tools) == 0 {
		return tools
	}
	wrapped := make([]tool.BaseTool, 0, len(tools))
	for _, baseTool := range tools {
		invokable, ok := baseTool.(tool.InvokableTool)
		if !ok {
			wrapped = append(wrapped, baseTool)
			continue
		}
		wrapped = append(wrapped, recoverableTool{inner: invokable})
	}
	return wrapped
}

type recoverableTool struct {
	inner tool.InvokableTool
}

func (t recoverableTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return t.inner.Info(ctx)
}

func (t recoverableTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	result, err := t.inner.InvokableRun(ctx, argumentsInJSON, opts...)
	if err == nil {
		return result, nil
	}
	info, infoErr := t.inner.Info(ctx)
	name := "unknown"
	if infoErr == nil && info != nil && info.Name != "" {
		name = info.Name
	}
	payload := map[string]any{
		"isError": true,
		"tool":    name,
		"error":   compactText(err.Error(), 2000),
		"message": "Tool call failed. Use the error details to revise the next tool call or choose another kube-insight tool; do not stop only because this tool failed.",
	}
	if strings.TrimSpace(argumentsInJSON) != "" {
		var args any
		if json.Unmarshal([]byte(argumentsInJSON), &args) == nil {
			payload["input"] = args
		} else {
			payload["input"] = argumentsInJSON
		}
	}
	data, marshalErr := json.Marshal(payload)
	if marshalErr != nil {
		return fmt.Sprintf(`{"isError":true,"tool":%q,"error":%q}`, name, compactText(err.Error(), 2000)), nil
	}
	return string(data), nil
}
