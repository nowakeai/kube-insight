package filter

import (
	"context"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"kube-insight/internal/core"
)

type ClusterAutoscalerStatusFilter struct{}

func (ClusterAutoscalerStatusFilter) Name() string {
	return "cluster_autoscaler_status_normalization_filter"
}

func (ClusterAutoscalerStatusFilter) Apply(_ context.Context, obs core.Observation) (core.Observation, Decision, error) {
	if obs.Object == nil {
		return obs, Decision{Outcome: Keep, Reason: "no_object"}, nil
	}
	data, ok := obs.Object["data"].(map[string]any)
	if !ok {
		return obs, Decision{Outcome: Keep, Reason: "data_absent"}, nil
	}
	status, _ := data["status"].(string)
	next, ok := cloneAny(obs.Object).(map[string]any)
	if !ok {
		return obs, Decision{Outcome: Keep, Reason: "object_not_map"}, nil
	}
	changed := 0
	if status != "" {
		normalized, normalizedChanged := normalizeClusterAutoscalerStatus(status)
		if normalizedChanged {
			nextData := next["data"].(map[string]any)
			nextData["status"] = normalized
			changed++
		}
	}
	changed += removeClusterAutoscalerLastUpdated(next)
	if changed == 0 {
		return obs, Decision{Outcome: Keep, Reason: "autoscaler_status_already_normalized"}, nil
	}
	obs.Object = next
	return obs, Decision{
		Outcome: KeepModified,
		Reason:  "cluster_autoscaler_status_normalized",
		Meta: map[string]any{
			"removedFields": changed,
		},
	}, nil
}

func normalizeClusterAutoscalerStatus(status string) (string, bool) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(status), &doc); err != nil {
		return status, false
	}
	changed := normalizeAutoscalerYAMLNode(&doc)
	out, err := yaml.Marshal(&doc)
	if err != nil {
		return status, false
	}
	normalized := strings.TrimRight(string(out), "\n")
	return normalized, changed || normalized != strings.TrimRight(status, "\n")
}

func normalizeAutoscalerYAMLNode(node *yaml.Node) bool {
	if node == nil {
		return false
	}
	changed := false
	switch node.Kind {
	case yaml.DocumentNode:
		for _, child := range node.Content {
			if normalizeAutoscalerYAMLNode(child) {
				changed = true
			}
		}
	case yaml.MappingNode:
		if removeAutoscalerProbeKeys(node) {
			changed = true
		}
		for i := 0; i < len(node.Content)-1; i += 2 {
			key := node.Content[i]
			value := node.Content[i+1]
			if key.Value == "nodeGroups" && value.Kind == yaml.SequenceNode {
				if sortYAMLSequenceByName(value) {
					changed = true
				}
			}
			if normalizeAutoscalerYAMLNode(value) {
				changed = true
			}
		}
	case yaml.SequenceNode:
		for _, child := range node.Content {
			if normalizeAutoscalerYAMLNode(child) {
				changed = true
			}
		}
	}
	return changed
}

func removeAutoscalerProbeKeys(node *yaml.Node) bool {
	changed := false
	content := node.Content[:0]
	for i := 0; i < len(node.Content)-1; i += 2 {
		key := node.Content[i]
		value := node.Content[i+1]
		switch key.Value {
		case "time", "lastProbeTime":
			changed = true
			continue
		default:
			content = append(content, key, value)
		}
	}
	node.Content = content
	return changed
}

func sortYAMLSequenceByName(node *yaml.Node) bool {
	before := yamlSequenceNameOrder(node)
	sort.SliceStable(node.Content, func(i, j int) bool {
		return yamlMappingStringValue(node.Content[i], "name") < yamlMappingStringValue(node.Content[j], "name")
	})
	after := yamlSequenceNameOrder(node)
	if len(before) != len(after) {
		return true
	}
	for i := range before {
		if before[i] != after[i] {
			return true
		}
	}
	return false
}

func yamlSequenceNameOrder(node *yaml.Node) []string {
	out := make([]string, len(node.Content))
	for i, child := range node.Content {
		out[i] = yamlMappingStringValue(child, "name")
	}
	return out
}

func yamlMappingStringValue(node *yaml.Node, key string) string {
	if node == nil || node.Kind != yaml.MappingNode {
		return ""
	}
	for i := 0; i < len(node.Content)-1; i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1].Value
		}
	}
	return ""
}

func removeClusterAutoscalerLastUpdated(obj map[string]any) int {
	metadata, ok := obj["metadata"].(map[string]any)
	if !ok {
		return 0
	}
	annotations, ok := metadata["annotations"].(map[string]any)
	if !ok {
		return 0
	}
	if _, exists := annotations["cluster-autoscaler.kubernetes.io/last-updated"]; !exists {
		return 0
	}
	delete(annotations, "cluster-autoscaler.kubernetes.io/last-updated")
	if len(annotations) == 0 {
		delete(metadata, "annotations")
	}
	return 1
}
