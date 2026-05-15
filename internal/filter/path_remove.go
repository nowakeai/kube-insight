package filter

import (
	"context"
	"strconv"
	"strings"

	"kube-insight/internal/core"
)

type PathRemoveFilter struct {
	FilterName string
	Reason     string
	Paths      []string
}

func (f PathRemoveFilter) Name() string {
	if f.FilterName != "" {
		return f.FilterName
	}
	return "path_remove_filter"
}

func (f PathRemoveFilter) Apply(_ context.Context, obs core.Observation) (core.Observation, Decision, error) {
	if obs.Object == nil || len(f.Paths) == 0 {
		return obs, Decision{Outcome: Keep, Reason: "remove_paths_absent"}, nil
	}
	next, ok := cloneAny(obs.Object).(map[string]any)
	if !ok {
		return obs, Decision{Outcome: Keep, Reason: "object_not_map"}, nil
	}
	removed := 0
	for _, path := range f.Paths {
		removed += removePath(next, parsePointer(path))
	}
	if removed == 0 {
		return obs, Decision{Outcome: Keep, Reason: "paths_absent"}, nil
	}
	obs.Object = next
	reason := f.Reason
	if reason == "" {
		reason = "paths_removed"
	}
	return obs, Decision{
		Outcome: KeepModified,
		Reason:  reason,
		Meta: map[string]any{
			"removedFields": removed,
		},
	}, nil
}

func parsePointer(path string) []string {
	path = strings.TrimSpace(path)
	if path == "" || path == "/" {
		return nil
	}
	path = strings.TrimPrefix(path, "/")
	parts := strings.Split(path, "/")
	for i, part := range parts {
		part = strings.ReplaceAll(part, "~1", "/")
		part = strings.ReplaceAll(part, "~0", "~")
		parts[i] = part
	}
	return parts
}

func removePath(value any, parts []string) int {
	if len(parts) == 0 {
		return 0
	}
	part := parts[0]
	switch typed := value.(type) {
	case map[string]any:
		if len(parts) == 1 {
			if _, exists := typed[part]; exists {
				delete(typed, part)
				return 1
			}
			return 0
		}
		if part == "*" {
			removed := 0
			for _, child := range typed {
				removed += removePath(child, parts[1:])
			}
			return removed
		}
		child, exists := typed[part]
		if !exists {
			return 0
		}
		return removePath(child, parts[1:])
	case []any:
		if part == "*" {
			removed := 0
			for _, child := range typed {
				removed += removePath(child, parts[1:])
			}
			return removed
		}
		index, err := strconv.Atoi(part)
		if err != nil || index < 0 || index >= len(typed) {
			return 0
		}
		return removePath(typed[index], parts[1:])
	default:
		return 0
	}
}

func cloneAny(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, child := range typed {
			out[key] = cloneAny(child)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, child := range typed {
			out[i] = cloneAny(child)
		}
		return out
	default:
		return value
	}
}
