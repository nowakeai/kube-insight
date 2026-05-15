package filter

import (
	"context"
	"sort"

	"kube-insight/internal/core"
)

type StatusConditionSetFilter struct{}

func (StatusConditionSetFilter) Name() string {
	return "status_condition_set_normalization_filter"
}

func (StatusConditionSetFilter) Apply(_ context.Context, obs core.Observation) (core.Observation, Decision, error) {
	if obs.Object == nil {
		return obs, Decision{Outcome: Keep, Reason: "no_object"}, nil
	}
	status, ok := obs.Object["status"].(map[string]any)
	if !ok {
		return obs, Decision{Outcome: Keep, Reason: "no_status"}, nil
	}
	conditions, ok := status["conditions"].([]any)
	if !ok {
		return obs, Decision{Outcome: Keep, Reason: "conditions_absent"}, nil
	}
	nextConditions := make([]any, len(conditions))
	removed := 0
	for i, condition := range conditions {
		conditionMap, ok := condition.(map[string]any)
		if !ok {
			nextConditions[i] = condition
			continue
		}
		nextCondition := cloneMap(conditionMap)
		for _, field := range []string{"lastHeartbeatTime", "lastTransitionTime", "lastUpdateTime"} {
			if _, exists := nextCondition[field]; exists {
				delete(nextCondition, field)
				removed++
			}
		}
		nextConditions[i] = nextCondition
	}
	sorted := sortConditionsByType(nextConditions)
	if removed == 0 && !sorted {
		return obs, Decision{Outcome: Keep, Reason: "condition_set_already_normalized"}, nil
	}
	next := cloneMap(obs.Object)
	nextStatus := cloneMap(status)
	nextStatus["conditions"] = nextConditions
	next["status"] = nextStatus
	obs.Object = next
	return obs, Decision{
		Outcome: KeepModified,
		Reason:  "condition_set_normalized",
		Meta: map[string]any{
			"removedFields": removed,
			"sortedLists":   boolToInt(sorted),
		},
	}, nil
}

func sortConditionsByType(conditions []any) bool {
	before := conditionOrder(conditions)
	sort.SliceStable(conditions, func(i, j int) bool {
		return conditionSortKey(conditions[i]) < conditionSortKey(conditions[j])
	})
	after := conditionOrder(conditions)
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

func conditionOrder(conditions []any) []string {
	out := make([]string, len(conditions))
	for i, condition := range conditions {
		out[i] = conditionSortKey(condition)
	}
	return out
}

func conditionSortKey(condition any) string {
	conditionMap, ok := condition.(map[string]any)
	if !ok {
		return ""
	}
	typ, _ := conditionMap["type"].(string)
	return typ
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
