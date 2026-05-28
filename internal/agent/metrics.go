package agent

import "github.com/prometheus/client_golang/prometheus"

var toolCallDurationSeconds = prometheus.NewHistogramVec(prometheus.HistogramOpts{
	Name:    "kube_insight_agent_tool_call_duration_seconds",
	Help:    "Agent tool call execution duration in seconds, labeled by tool name and status.",
	Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60},
}, []string{"tool", "status"})

// ToolCallDurationCollector returns the agent tool-call duration histogram for
// explicit registration in kube-insight metrics registries.
func ToolCallDurationCollector() prometheus.Collector {
	return toolCallDurationSeconds
}

// ObserveToolCallDuration records one agent tool-call duration sample.
func ObserveToolCallDuration(toolName string, status string, durationMS int64) {
	if toolName == "" {
		toolName = "unknown"
	}
	if status == "" {
		status = "unknown"
	}
	if durationMS < 0 {
		durationMS = 0
	}
	toolCallDurationSeconds.WithLabelValues(toolName, status).Observe(float64(durationMS) / 1000)
}
