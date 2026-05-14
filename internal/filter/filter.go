package filter

import (
	"context"
	"strings"

	"kube-insight/internal/core"
	"kube-insight/internal/logging"
)

type Outcome string

const (
	Keep            Outcome = "keep"
	KeepModified    Outcome = "keep_modified"
	DiscardChange   Outcome = "discard_change"
	DiscardResource Outcome = "discard_resource"
)

type Decision struct {
	Outcome Outcome
	Reason  string
	Meta    map[string]any
}

type Filter interface {
	Name() string
	Apply(context.Context, core.Observation) (core.Observation, Decision, error)
}

type Chain []Filter

func (c Chain) Apply(ctx context.Context, obs core.Observation) (core.Observation, []Decision, error) {
	logger := logging.FromContext(ctx).With("component", "filter")
	decisions := make([]Decision, 0, len(c))
	for _, f := range c {
		next, decision, err := f.Apply(ctx, obs)
		if err != nil {
			return obs, decisions, err
		}
		if decision.Meta == nil {
			decision.Meta = map[string]any{"filter": f.Name()}
		} else {
			decision.Meta["filter"] = f.Name()
		}
		decisions = append(decisions, decision)
		logger.Debug("filter decision", "filter", f.Name(), "outcome", decision.Outcome, "reason", decision.Reason, "resource", obs.Ref.Resource, "namespace", obs.Ref.Namespace, "name", obs.Ref.Name)
		obs = next
		if decision.Outcome == DiscardResource || decision.Outcome == DiscardChange {
			break
		}
	}
	return obs, decisions, nil
}

type SecretRedactionFilter struct{}

func (SecretRedactionFilter) Name() string { return "secret_redaction_filter" }

func (SecretRedactionFilter) Apply(_ context.Context, obs core.Observation) (core.Observation, Decision, error) {
	if obs.Ref.Resource != "secrets" {
		return obs, Decision{Outcome: Keep, Reason: "not_secret"}, nil
	}
	if obs.Object == nil {
		return obs, Decision{Outcome: Keep, Reason: "no_object"}, nil
	}
	removed := 0
	redacted := cloneMap(obs.Object)
	if _, exists := redacted["data"]; exists {
		delete(redacted, "data")
		removed++
	}
	if _, exists := redacted["stringData"]; exists {
		delete(redacted, "stringData")
		removed++
	}
	if removed == 0 {
		return obs, Decision{Outcome: Keep, Reason: "secret_payload_absent"}, nil
	}
	obs.Object = redacted
	return obs, Decision{
		Outcome: KeepModified,
		Reason:  "secret_payload_removed",
		Meta: map[string]any{
			"redactedFields":       removed,
			"removedFields":        removed,
			"secretPayloadRemoved": true,
		},
	}, nil
}

type ManagedFieldsFilter struct{}

func (ManagedFieldsFilter) Name() string { return "metadata_normalization_filter" }

func (ManagedFieldsFilter) Apply(_ context.Context, obs core.Observation) (core.Observation, Decision, error) {
	metadata, ok := obs.Object["metadata"].(map[string]any)
	if !ok {
		return obs, Decision{Outcome: Keep, Reason: "no_metadata"}, nil
	}
	if _, exists := metadata["managedFields"]; !exists {
		return obs, Decision{Outcome: Keep, Reason: "managed_fields_absent"}, nil
	}
	next := cloneMap(obs.Object)
	nextMetadata := cloneMap(metadata)
	delete(nextMetadata, "managedFields")
	next["metadata"] = nextMetadata
	obs.Object = next
	return obs, Decision{
		Outcome: KeepModified,
		Reason:  "managed_fields_removed",
		Meta: map[string]any{
			"removedFields": 1,
		},
	}, nil
}

type LeaseSkipFilter struct{}

func (LeaseSkipFilter) Name() string { return "lease_skip_filter" }

func (LeaseSkipFilter) Apply(_ context.Context, obs core.Observation) (core.Observation, Decision, error) {
	if obs.Ref.Group == "coordination.k8s.io" && strings.EqualFold(obs.Ref.Resource, "leases") {
		return obs, Decision{Outcome: DiscardResource, Reason: "lease_skipped"}, nil
	}
	return obs, Decision{Outcome: Keep, Reason: "not_lease"}, nil
}

func cloneMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
