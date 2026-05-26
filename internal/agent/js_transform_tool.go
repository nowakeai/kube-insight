package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/dop251/goja"
)

const (
	jsTransformToolName          = "artifact_transform_js"
	defaultJSTransformTimeout    = 500 * time.Millisecond
	maxJSTransformTimeout        = 2 * time.Second
	defaultJSTransformOutputSize = 64 * 1024
	maxJSTransformOutputSize     = 256 * 1024
	maxJSTransformInputSize      = 512 * 1024
	maxJSTransformScriptSize     = 16 * 1024
)

type JSTransformTool struct{}

type jsTransformArguments struct {
	Script         string          `json:"script"`
	Input          json.RawMessage `json:"input,omitempty"`
	TimeoutMillis  int             `json:"timeoutMillis,omitempty"`
	MaxOutputBytes int             `json:"maxOutputBytes,omitempty"`
}

type jsTransformResult struct {
	Result json.RawMessage `json:"result,omitempty"`
	Logs   []string        `json:"logs,omitempty"`
}

func NewJSTransformTool() tool.BaseTool {
	return JSTransformTool{}
}

func (JSTransformTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: jsTransformToolName,
		Desc: "Run bounded JavaScript over JSON data already returned in this investigation. Use it for grouping, sorting, filtering, counting, summarizing rows, and extracting fields from tool outputs. It has no filesystem, network, process, or environment access; pass the data as input and return the final value from the script. One successful transform over the relevant rows is terminal: answer from its JSON result instead of running repeated SQL or a second transform.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"script": {
				Type:     schema.String,
				Required: true,
				Desc:     "JavaScript expression or statements. The variable input contains the provided JSON. Use return to provide the final JSON-serializable value.",
			},
			"input": {
				Type: schema.Object,
				Desc: "JSON object/array/scalar from prior tool output, such as SQL rows or search matches. Keep it bounded and relevant.",
			},
			"timeoutMillis": {
				Type: schema.Integer,
				Desc: "Optional timeout in milliseconds. Defaults to 500 and is capped at 2000.",
			},
			"maxOutputBytes": {
				Type: schema.Integer,
				Desc: "Optional maximum JSON output size. Defaults to 65536 and is capped at 262144.",
			},
		}),
	}, nil
}

func (JSTransformTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	var args jsTransformArguments
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return "", fmt.Errorf("parse %s arguments: %w", jsTransformToolName, err)
	}
	script := strings.TrimSpace(args.Script)
	if script == "" {
		return "", fmt.Errorf("%s requires a script", jsTransformToolName)
	}
	if len(script) > maxJSTransformScriptSize {
		return "", fmt.Errorf("%s script too large: %d bytes > %d", jsTransformToolName, len(script), maxJSTransformScriptSize)
	}
	if len(args.Input) > maxJSTransformInputSize {
		return "", fmt.Errorf("%s input too large: %d bytes > %d", jsTransformToolName, len(args.Input), maxJSTransformInputSize)
	}
	var input any
	if len(args.Input) > 0 {
		if err := json.Unmarshal(args.Input, &input); err != nil {
			return "", fmt.Errorf("%s input must be valid JSON: %w", jsTransformToolName, err)
		}
	}
	timeout := boundedJSTransformTimeout(args.TimeoutMillis)
	outputLimit := boundedJSTransformOutputLimit(args.MaxOutputBytes)

	vm := goja.New()
	logs := []string{}
	_ = vm.Set("input", input)
	_ = vm.Set("rows", jsTransformRowsAlias(input))
	_ = vm.Set("console", map[string]any{
		"log": func(call goja.FunctionCall) goja.Value {
			logs = append(logs, compactText(jsLogArgs(call.Arguments), 500))
			return goja.Undefined()
		},
	})
	_ = vm.Set("print", func(call goja.FunctionCall) goja.Value {
		logs = append(logs, compactText(jsLogArgs(call.Arguments), 500))
		return goja.Undefined()
	})
	_ = vm.Set("json", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return vm.ToValue("null")
		}
		data, err := json.Marshal(call.Arguments[0].Export())
		if err != nil {
			panic(vm.ToValue(err.Error()))
		}
		return vm.ToValue(string(data))
	})
	timer := time.AfterFunc(timeout, func() {
		vm.Interrupt("timeout")
	})
	defer timer.Stop()

	result, err := vm.RunString(wrapJSTransformScript(script))
	if err != nil {
		return "", fmt.Errorf("%s failed: %w", jsTransformToolName, err)
	}
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	default:
	}
	outputData, err := json.Marshal(result.Export())
	if err != nil {
		return "", fmt.Errorf("%s result is not JSON serializable: %w", jsTransformToolName, err)
	}
	if len(outputData) > outputLimit {
		return "", fmt.Errorf("%s output too large: %d bytes > %d", jsTransformToolName, len(outputData), outputLimit)
	}
	out := jsTransformResult{Result: outputData, Logs: logs}
	encoded, err := json.Marshal(out)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func wrapJSTransformScript(script string) string {
	return "\"use strict\";\n(() => {\n" + script + "\n})()"
}

func jsTransformRowsAlias(input any) any {
	if rows, ok := input.([]any); ok {
		return rows
	}
	if object, ok := input.(map[string]any); ok {
		if rows, ok := object["rows"]; ok {
			return rows
		}
	}
	return nil
}

func boundedJSTransformTimeout(ms int) time.Duration {
	if ms <= 0 {
		return defaultJSTransformTimeout
	}
	timeout := time.Duration(ms) * time.Millisecond
	if timeout > maxJSTransformTimeout {
		return maxJSTransformTimeout
	}
	return timeout
}

func boundedJSTransformOutputLimit(limit int) int {
	if limit <= 0 {
		return defaultJSTransformOutputSize
	}
	if limit > maxJSTransformOutputSize {
		return maxJSTransformOutputSize
	}
	return limit
}

func jsLogArgs(args []goja.Value) string {
	parts := make([]string, 0, len(args))
	for _, arg := range args {
		parts = append(parts, fmt.Sprint(arg.Export()))
	}
	return strings.Join(parts, " ")
}
