package filter

import (
	"context"
	"strings"

	"kube-insight/internal/core"
	"kube-insight/internal/logging"
	"kube-insight/internal/resourcematch"
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

type StaticDecisionFilter struct {
	FilterName string
	Outcome    Outcome
	Reason     string
}

func (f StaticDecisionFilter) Name() string {
	if f.FilterName == "" {
		return "static_decision_filter"
	}
	return f.FilterName
}

func (f StaticDecisionFilter) Apply(_ context.Context, obs core.Observation) (core.Observation, Decision, error) {
	outcome := f.Outcome
	if outcome == "" {
		outcome = Keep
	}
	reason := f.Reason
	if reason == "" {
		reason = "static_decision"
	}
	return obs, Decision{Outcome: outcome, Reason: reason}, nil
}

type Scope struct {
	Resources  []string
	Kinds      []string
	Namespaces []string
	Names      []string
}

func (s Scope) Empty() bool {
	return len(s.Resources) == 0 && len(s.Kinds) == 0 && len(s.Namespaces) == 0 && len(s.Names) == 0
}

func (s Scope) Matches(ref core.ResourceRef) bool {
	if s.Empty() {
		return true
	}
	if len(s.Namespaces) > 0 && !resourcematch.MatchAnyString(s.Namespaces, ref.Namespace) {
		return false
	}
	if len(s.Names) > 0 && !resourcematch.MatchAnyString(s.Names, ref.Name) {
		return false
	}
	resourceScoped := len(s.Resources) > 0 || len(s.Kinds) > 0
	if !resourceScoped {
		return true
	}
	for _, kind := range s.Kinds {
		if resourcematch.MatchAnyString([]string{kind}, ref.Kind) {
			return true
		}
	}
	return resourcematch.MatchAnyResource(s.Resources, resourcematch.FromCoreRef(ref))
}

type ScopedFilter struct {
	Inner Filter
	Scope Scope
}

func (f ScopedFilter) Name() string { return f.Inner.Name() }

func (f ScopedFilter) Apply(ctx context.Context, obs core.Observation) (core.Observation, Decision, error) {
	if !f.Scope.Matches(obs.Ref) {
		return obs, Decision{Outcome: Keep, Reason: "scope_mismatch"}, nil
	}
	return f.Inner.Apply(ctx, obs)
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

type ResourceVersionFilter struct{}

func (ResourceVersionFilter) Name() string { return "resource_version_normalization_filter" }

func (ResourceVersionFilter) Apply(_ context.Context, obs core.Observation) (core.Observation, Decision, error) {
	metadata, ok := obs.Object["metadata"].(map[string]any)
	if !ok {
		return obs, Decision{Outcome: Keep, Reason: "no_metadata"}, nil
	}
	if _, exists := metadata["resourceVersion"]; !exists {
		return obs, Decision{Outcome: Keep, Reason: "resource_version_absent"}, nil
	}
	next := cloneMap(obs.Object)
	nextMetadata := cloneMap(metadata)
	delete(nextMetadata, "resourceVersion")
	next["metadata"] = nextMetadata
	obs.Object = next
	return obs, Decision{
		Outcome: KeepModified,
		Reason:  "resource_version_removed",
		Meta: map[string]any{
			"removedFields": 1,
		},
	}, nil
}

type StatusConditionTimestampFilter struct{}

func (StatusConditionTimestampFilter) Name() string {
	return "status_condition_timestamp_normalization_filter"
}

func (StatusConditionTimestampFilter) Apply(_ context.Context, obs core.Observation) (core.Observation, Decision, error) {
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
		for _, field := range []string{"lastHeartbeatTime", "lastTransitionTime"} {
			if _, exists := nextCondition[field]; exists {
				delete(nextCondition, field)
				removed++
			}
		}
		nextConditions[i] = nextCondition
	}
	if removed == 0 {
		return obs, Decision{Outcome: Keep, Reason: "condition_timestamps_absent"}, nil
	}
	next := cloneMap(obs.Object)
	nextStatus := cloneMap(status)
	nextStatus["conditions"] = nextConditions
	next["status"] = nextStatus
	obs.Object = next
	return obs, Decision{
		Outcome: KeepModified,
		Reason:  "condition_timestamps_removed",
		Meta: map[string]any{
			"removedFields": removed,
		},
	}, nil
}

type LeaderElectionConfigMapFilter struct{}

func (LeaderElectionConfigMapFilter) Name() string {
	return "leader_election_configmap_normalization_filter"
}

func (LeaderElectionConfigMapFilter) Apply(_ context.Context, obs core.Observation) (core.Observation, Decision, error) {
	if obs.Ref.Group != "" || !strings.EqualFold(obs.Ref.Resource, "configmaps") {
		return obs, Decision{Outcome: Keep, Reason: "not_configmap"}, nil
	}
	metadata, ok := obs.Object["metadata"].(map[string]any)
	if !ok {
		return obs, Decision{Outcome: Keep, Reason: "no_metadata"}, nil
	}
	annotations, ok := metadata["annotations"].(map[string]any)
	if !ok {
		return obs, Decision{Outcome: Keep, Reason: "annotations_absent"}, nil
	}
	const leaderAnnotation = "control-plane.alpha.kubernetes.io/leader"
	if _, exists := annotations[leaderAnnotation]; !exists {
		return obs, Decision{Outcome: Keep, Reason: "leader_annotation_absent"}, nil
	}
	nextAnnotations := cloneMap(annotations)
	delete(nextAnnotations, leaderAnnotation)
	nextMetadata := cloneMap(metadata)
	if len(nextAnnotations) == 0 {
		delete(nextMetadata, "annotations")
	} else {
		nextMetadata["annotations"] = nextAnnotations
	}
	next := cloneMap(obs.Object)
	next["metadata"] = nextMetadata
	obs.Object = next
	return obs, Decision{
		Outcome: KeepModified,
		Reason:  "leader_annotation_removed",
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
