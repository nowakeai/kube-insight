package agent

import "github.com/cloudwego/eino/components/tool"

const (
	jsTransformToolName     = jsInterpreterToolName
	maxJSTransformInputSize = 512 * 1024
)

// NewJSTransformTool is kept only for older tests or callers. The agent exposes
// the unified kube_insight_js interpreter instead of a separate transform tool.
func NewJSTransformTool() tool.BaseTool {
	return NewJSInterpreterTool(nil)
}
