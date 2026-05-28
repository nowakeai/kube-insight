package agent

import "encoding/json"

func toolCallArtifactData(toolCallID, name string, input, output json.RawMessage, outputSummary, status string, durationMS int64, errorMessage string) json.RawMessage {
	record := map[string]any{
		"toolCallId":    toolCallID,
		"name":          name,
		"input":         rawMessageValue(input),
		"output":        rawMessageValue(output),
		"outputSummary": outputSummary,
		"status":        status,
		"durationMs":    durationMS,
	}
	if errorMessage != "" {
		record["error"] = errorMessage
	}
	if handles := scratchHandlesFromToolOutput(output); len(handles) > 0 {
		record["scratchHandles"] = handles
	}
	return jsonRaw(record)
}
