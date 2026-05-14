package extractor

import (
	"context"

	"kube-insight/internal/core"
)

type NodeExtractor struct{}

func (NodeExtractor) Kind() string { return "Node" }

func (NodeExtractor) Extract(_ context.Context, obs core.Observation) (Evidence, error) {
	var out Evidence
	status, ok := obs.Object["status"].(map[string]any)
	if !ok {
		return out, nil
	}
	conditions, ok := status["conditions"].([]any)
	if !ok {
		return out, nil
	}
	for _, raw := range conditions {
		condition, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		typ, _ := condition["type"].(string)
		value, _ := condition["status"].(string)
		if typ == "" || value == "" {
			continue
		}
		severity := 20
		if typ != "Ready" && value == "True" {
			severity = 80
		}
		out.Facts = append(out.Facts, fact(obs, "node_condition."+typ, value, severity))
		out.Changes = append(out.Changes, change(obs, "status", "status.conditions."+typ, "replace", "", value, severity))
	}
	return out, nil
}
