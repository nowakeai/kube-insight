package filter

import (
	"context"
	"encoding/json"
	"reflect"
	"sort"

	"kube-insight/internal/core"
)

type GKEWebhookHeartbeatFilter struct{}

func (GKEWebhookHeartbeatFilter) Name() string {
	return "gke_webhook_heartbeat_normalization_filter"
}

func (GKEWebhookHeartbeatFilter) Apply(_ context.Context, obs core.Observation) (core.Observation, Decision, error) {
	if obs.Object == nil {
		return obs, Decision{Outcome: Keep, Reason: "no_object"}, nil
	}
	data, ok := obs.Object["data"].(map[string]any)
	if !ok || len(data) == 0 {
		return obs, Decision{Outcome: Keep, Reason: "data_absent"}, nil
	}
	normalized, parsed := normalizeGKEWebhookHeartbeatData(data)
	if parsed == 0 || reflect.DeepEqual(data, normalized) {
		return obs, Decision{Outcome: Keep, Reason: "gke_webhook_heartbeat_already_normalized"}, nil
	}
	next, ok := cloneAny(obs.Object).(map[string]any)
	if !ok {
		return obs, Decision{Outcome: Keep, Reason: "object_not_map"}, nil
	}
	next["data"] = normalized
	obs.Object = next
	return obs, Decision{
		Outcome: KeepModified,
		Reason:  "gke_webhook_heartbeat_normalized",
		Meta: map[string]any{
			"removedFields": parsed,
		},
	}, nil
}

type gkeWebhookHeartbeat struct {
	Hostname string `json:"hostname"`
	Version  string `json:"version"`
}

func normalizeGKEWebhookHeartbeatData(data map[string]any) (map[string]any, int) {
	out := make(map[string]any, len(data))
	keys := make([]string, 0, len(data))
	for key := range data {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parsed := 0
	for _, key := range keys {
		raw, ok := data[key].(string)
		if !ok {
			out[key] = data[key]
			continue
		}
		var heartbeat gkeWebhookHeartbeat
		if err := json.Unmarshal([]byte(raw), &heartbeat); err != nil || heartbeat.Hostname == "" {
			out[key] = data[key]
			continue
		}
		out[heartbeat.Hostname] = heartbeat.Version
		parsed++
	}
	return out, parsed
}
