package clickhouse

import (
	"context"
	"encoding/json"
	"fmt"

	"kube-insight/internal/core"
	"kube-insight/internal/filter"
)

func (s *Store) PutFilterDecisions(ctx context.Context, obs core.Observation, decisions []filter.Decision) error {
	if s == nil || len(decisions) == 0 {
		return nil
	}
	rows := make([]map[string]any, 0, len(decisions))
	for _, decision := range decisions {
		if !shouldPersistClickHouseFilterDecision(decision) {
			continue
		}
		meta, err := json.Marshal(decision.Meta)
		if err != nil {
			return err
		}
		filterName, _ := decision.Meta["filter"].(string)
		if filterName == "" {
			filterName = "unknown"
		}
		rows = append(rows, map[string]any{
			"ts":               clickHouseTime(obs.ObservedAt),
			"cluster_id":       obs.Ref.ClusterID,
			"api_group":        obs.Ref.Group,
			"api_version":      obs.Ref.Version,
			"resource":         obs.Ref.Resource,
			"kind":             obs.Ref.Kind,
			"namespace":        obs.Ref.Namespace,
			"name":             obs.Ref.Name,
			"uid":              obs.Ref.UID,
			"resource_version": obs.ResourceVersion,
			"observation_type": string(obs.Type),
			"filter_name":      filterName,
			"outcome":          string(decision.Outcome),
			"reason":           decision.Reason,
			"destructive":      isClickHouseDestructiveFilterDecision(decision),
			"meta":             string(meta),
		})
	}
	return s.client().InsertRows(ctx, s.database(), "filter_decisions", rows)
}

func shouldPersistClickHouseFilterDecision(decision filter.Decision) bool {
	switch decision.Outcome {
	case filter.DiscardChange, filter.DiscardResource:
		return true
	case filter.KeepModified:
		return isClickHouseSecretPayloadRemoval(decision)
	default:
		return false
	}
}

func isClickHouseDestructiveFilterDecision(decision filter.Decision) bool {
	switch decision.Outcome {
	case filter.DiscardChange, filter.DiscardResource:
		return true
	case filter.KeepModified:
		return isClickHouseSecretPayloadRemoval(decision)
	default:
		return false
	}
}

func isClickHouseSecretPayloadRemoval(decision filter.Decision) bool {
	if decision.Reason == "secret_payload_removed" {
		return true
	}
	filterName, _ := decision.Meta["filter"].(string)
	if filterName != "secret_redaction_filter" && filterName != "secret_metadata_only" {
		return false
	}
	if value, _ := decision.Meta["secretPayloadRemoved"].(bool); value {
		return true
	}
	for _, key := range []string{"redactedFields", "removedFields"} {
		if count, ok := clickHouseNumericMeta(decision.Meta[key]); ok && count > 0 {
			return true
		}
	}
	return false
}

func clickHouseNumericMeta(value any) (float64, bool) {
	switch v := value.(type) {
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case float64:
		return v, true
	case json.Number:
		f, err := v.Float64()
		return f, err == nil
	case string:
		if v == "" {
			return 0, false
		}
		var f float64
		_, err := fmt.Sscan(v, &f)
		return f, err == nil
	default:
		return 0, false
	}
}
