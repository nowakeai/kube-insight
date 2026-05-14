package extractor

import (
	"context"
	"strconv"

	"kube-insight/internal/core"
)

type PodExtractor struct{}

func (PodExtractor) Kind() string { return "Pod" }

func (PodExtractor) Extract(_ context.Context, obs core.Observation) (Evidence, error) {
	var out Evidence
	if phase, ok := stringAt(obs.Object, "status", "phase"); ok {
		out.Facts = append(out.Facts, fact(obs, "pod_status.phase", phase, 10))
	}
	if ready, ok := podReady(obs.Object); ok {
		severity := 20
		if ready == "False" {
			severity = 50
		}
		out.Facts = append(out.Facts, fact(obs, "pod_status.ready", ready, severity))
		out.Changes = append(out.Changes, change(obs, "status", "status.conditions.Ready", "replace", "", ready, severity))
	}
	if nodeName, ok := stringAt(obs.Object, "spec", "nodeName"); ok && nodeName != "" {
		out.Facts = append(out.Facts, fact(obs, "pod_placement.node_assigned", nodeName, 20))
		out.Edges = append(out.Edges, edge(obs, "pod_on_node", "nodes/"+nodeName))
	}
	appendContainerStatusEvidence(obs, &out)
	if obs.Type == core.ObservationDeleted {
		out.Facts = append(out.Facts, fact(obs, "pod_placement.deleted", "true", 60))
		out.Changes = append(out.Changes, change(obs, "status", "metadata.deletionTimestamp", "delete", "", "deleted", 60))
	}
	return out, nil
}

func podReady(obj map[string]any) (string, bool) {
	status, _ := obj["status"].(map[string]any)
	conditions, _ := status["conditions"].([]any)
	for _, raw := range conditions {
		condition, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if typ, _ := condition["type"].(string); typ != "Ready" {
			continue
		}
		value, _ := condition["status"].(string)
		if value == "" {
			return "", false
		}
		return value, true
	}
	return "", false
}

func appendContainerStatusEvidence(obs core.Observation, out *Evidence) {
	status, _ := obs.Object["status"].(map[string]any)
	for _, key := range []string{"initContainerStatuses", "containerStatuses", "ephemeralContainerStatuses"} {
		items, _ := status[key].([]any)
		for _, raw := range items {
			container, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			name, _ := container["name"].(string)
			if restartCount, ok := numericFromAny(container["restartCount"]); ok {
				value := strconv.FormatFloat(restartCount, 'f', -1, 64)
				f := fact(obs, "pod_status.restart_count", value, 30)
				f.NumericValue = &restartCount
				out.Facts = append(out.Facts, f)
				if restartCount > 0 {
					out.Changes = append(out.Changes, change(obs, "status", "status.containerStatuses."+name+".restartCount", "replace", "", value, 50))
				}
			}
			if reason := containerReason(container, "state"); reason != "" {
				severity := podReasonSeverity(reason)
				out.Facts = append(out.Facts, fact(obs, "pod_status.reason", reason, severity))
				out.Changes = append(out.Changes, change(obs, "status", "status.containerStatuses."+name+".state.reason", "replace", "", reason, severity))
			}
			if reason := containerReason(container, "lastState"); reason != "" {
				severity := podReasonSeverity(reason)
				out.Facts = append(out.Facts, fact(obs, "pod_status.last_reason", reason, severity))
				out.Changes = append(out.Changes, change(obs, "status", "status.containerStatuses."+name+".lastState.reason", "replace", "", reason, severity))
			}
		}
	}
}

func containerReason(container map[string]any, stateKey string) string {
	state, _ := container[stateKey].(map[string]any)
	for _, key := range []string{"waiting", "terminated"} {
		item, _ := state[key].(map[string]any)
		if reason, _ := item["reason"].(string); reason != "" {
			return reason
		}
	}
	return ""
}

func podReasonSeverity(reason string) int {
	switch reason {
	case "OOMKilled", "CrashLoopBackOff", "Error", "Evicted":
		return 90
	default:
		return 50
	}
}

func numericFromAny(value any) (float64, bool) {
	switch typed := value.(type) {
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case float64:
		return typed, true
	default:
		return 0, false
	}
}
