package extractor

import (
	"context"
	"fmt"
	"strings"

	"kube-insight/internal/core"

	"k8s.io/apimachinery/pkg/api/resource"
)

type NodeExtractor struct{}

func (NodeExtractor) Kind() string { return "Node" }

func (NodeExtractor) Extract(_ context.Context, obs core.Observation) (Evidence, error) {
	var out Evidence
	status, ok := obs.Object["status"].(map[string]any)
	if !ok {
		return out, nil
	}
	appendNodeResourceFacts(obs, &out, status)
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

func appendNodeResourceFacts(obs core.Observation, out *Evidence, status map[string]any) {
	for _, spec := range []struct {
		section string
		field   string
		key     string
	}{
		{"capacity", "cpu", "node_capacity.cpu"},
		{"capacity", "memory", "node_capacity.memory"},
		{"capacity", "pods", "node_capacity.pods"},
		{"capacity", "ephemeral-storage", "node_capacity.ephemeral_storage"},
		{"allocatable", "cpu", "node_allocatable.cpu"},
		{"allocatable", "memory", "node_allocatable.memory"},
		{"allocatable", "pods", "node_allocatable.pods"},
		{"allocatable", "ephemeral-storage", "node_allocatable.ephemeral_storage"},
	} {
		value, ok := nodeResourceValue(status, spec.section, spec.field)
		if !ok {
			continue
		}
		f := fact(obs, spec.key, value, 10)
		if numeric, ok := nodeResourceNumeric(spec.field, value); ok {
			f.NumericValue = &numeric
		}
		out.Facts = append(out.Facts, f)
	}
}

func nodeResourceValue(status map[string]any, section, field string) (string, bool) {
	values, ok := status[section].(map[string]any)
	if !ok {
		return "", false
	}
	raw, ok := values[field]
	if !ok {
		return "", false
	}
	switch typed := raw.(type) {
	case string:
		return typed, typed != ""
	case fmt.Stringer:
		value := typed.String()
		return value, value != ""
	case int, int64, float64:
		return fmt.Sprint(typed), true
	default:
		return "", false
	}
}

func nodeResourceNumeric(field, value string) (float64, bool) {
	qty, err := resource.ParseQuantity(value)
	if err != nil {
		return 0, false
	}
	switch {
	case field == "cpu":
		return qty.AsApproximateFloat64(), true
	case field == "memory" || strings.Contains(field, "storage"):
		return float64(qty.Value()), true
	default:
		return qty.AsApproximateFloat64(), true
	}
}
