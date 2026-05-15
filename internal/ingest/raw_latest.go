package ingest

import (
	"encoding/json"
	"strings"

	"kube-insight/internal/core"
)

func rawLatestObservation(obs core.Observation) (core.Observation, error) {
	if obs.Object == nil {
		return obs, nil
	}
	var object map[string]any
	data, err := json.Marshal(obs.Object)
	if err != nil {
		return obs, err
	}
	if err := json.Unmarshal(data, &object); err != nil {
		return obs, err
	}
	obs.Object = object
	redactSecretPayloadKeys(obs)
	return obs, nil
}

func redactSecretPayloadKeys(obs core.Observation) {
	if obs.Ref.Group != "" || !strings.EqualFold(obs.Ref.Resource, "secrets") || obs.Object == nil {
		return
	}
	redactStringMapValues(obs.Object, "data")
	redactStringMapValues(obs.Object, "stringData")
}

func redactStringMapValues(object map[string]any, field string) {
	values, ok := object[field].(map[string]any)
	if !ok {
		return
	}
	redacted := make(map[string]any, len(values))
	for key := range values {
		redacted[key] = "<redacted>"
	}
	object[field] = redacted
}
