package ingest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"kube-insight/internal/core"
	"kube-insight/internal/extractor"
	"kube-insight/internal/filter"
	"kube-insight/internal/kubeapi"
	"kube-insight/internal/logging"
	"kube-insight/internal/storage"
)

type Summary struct {
	Observations         int
	StoredObservations   int
	ModifiedObservations int
	DiscardedChanges     int
	DiscardedResources   int
	Facts                int
	Edges                int
	Changes              int
}

type Pipeline struct {
	ClusterID  string
	Filters    filter.Chain
	Extractors *extractor.Registry
	Resolver   *kubeapi.Resolver
	Store      storage.Store
	Now        func() time.Time
}

func DefaultPipeline(store storage.Store) Pipeline {
	resolver := kubeapi.NewResolver()
	return Pipeline{
		ClusterID: "local",
		Filters: filter.Chain{
			filter.LeaseSkipFilter{},
			filter.SecretRedactionFilter{},
			filter.ManagedFieldsFilter{},
		},
		Extractors: extractor.NewRegistry(
			extractor.ReferenceExtractor{},
			extractor.PodExtractor{},
			extractor.NodeExtractor{},
			extractor.EventExtractor{},
			extractor.EndpointSliceExtractor{},
		),
		Resolver: resolver,
		Store:    store,
		Now:      time.Now,
	}
}

func (p Pipeline) IngestJSON(ctx context.Context, data []byte) (Summary, error) {
	logger := logging.FromContext(ctx).With("component", "ingest")
	inputs, err := decodeObservationInputs(data)
	if err != nil {
		return Summary{}, err
	}
	logger.Info("decoded observations", "observations", len(inputs), "bytes", len(data))
	resolver := p.Resolver
	if resolver == nil {
		resolver = kubeapi.NewResolver()
	}
	if apiStore, ok := p.Store.(storage.APIResourceStore); ok {
		resources, err := apiStore.APIResources(ctx)
		if err != nil {
			return Summary{}, err
		}
		logger.Debug("loaded api resource registry", "resources", len(resources))
		for _, resource := range resources {
			resolver.Register(resource)
		}
	}
	for _, input := range inputs {
		resolver.RegisterFromObject(input.Object)
		extractor.RegisterAPIObject(input.Object)
	}

	now := time.Now
	if p.Now != nil {
		now = p.Now
	}
	clusterID := p.ClusterID
	if clusterID == "" {
		clusterID = "local"
	}

	var summary Summary
	for _, input := range inputs {
		summary.Observations++
		obs := observationFromObject(clusterID, input.Type, now(), resolver, input.Object)
		logger.Debug("ingesting observation", "cluster", obs.Ref.ClusterID, "resource", obs.Ref.Resource, "namespace", obs.Ref.Namespace, "name", obs.Ref.Name, "type", obs.Type)
		filtered, decisions, err := p.Filters.Apply(ctx, obs)
		if err != nil {
			return summary, err
		}
		if decisionStore, ok := p.Store.(storage.FilterDecisionStore); ok {
			if err := decisionStore.PutFilterDecisions(ctx, filtered, decisions); err != nil {
				return summary, err
			}
		}
		if discarded(decisions, filter.DiscardResource) {
			summary.DiscardedResources++
			logger.Debug("discarded resource", "resource", obs.Ref.Resource, "namespace", obs.Ref.Namespace, "name", obs.Ref.Name)
			continue
		}
		if discarded(decisions, filter.DiscardChange) && filtered.Type != core.ObservationDeleted {
			summary.DiscardedChanges++
			logger.Debug("discarded change", "resource", obs.Ref.Resource, "namespace", obs.Ref.Namespace, "name", obs.Ref.Name)
			continue
		}
		if hasDecision(decisions, filter.KeepModified) {
			summary.ModifiedObservations++
		}

		evidence, err := p.Extractors.Extract(extractor.WithResolver(ctx, resolver), filtered)
		if err != nil {
			return summary, err
		}
		if err := p.Store.PutObservation(ctx, filtered, evidence); err != nil {
			return summary, err
		}
		summary.StoredObservations++
		summary.Facts += len(evidence.Facts)
		summary.Edges += len(evidence.Edges)
		summary.Changes += len(evidence.Changes)
	}

	logger.Info("ingest completed", "observations", summary.Observations, "stored", summary.StoredObservations, "modified", summary.ModifiedObservations, "discardedChanges", summary.DiscardedChanges, "discardedResources", summary.DiscardedResources, "facts", summary.Facts, "edges", summary.Edges, "changes", summary.Changes)
	return summary, nil
}

type observationInput struct {
	Type   core.ObservationType
	Object map[string]any
}

func decodeObjects(data []byte) ([]map[string]any, error) {
	inputs, err := decodeObservationInputs(data)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(inputs))
	for _, input := range inputs {
		out = append(out, input.Object)
	}
	return out, nil
}

func decodeObservationInputs(data []byte) ([]observationInput, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()

	var root map[string]any
	if err := decoder.Decode(&root); err != nil {
		return nil, err
	}

	if items, ok := root["items"].([]any); ok {
		out := make([]observationInput, 0, len(items))
		for i, item := range items {
			obj, ok := item.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("items[%d] is not an object", i)
			}
			input, err := decodeObservationInput(obj)
			if err != nil {
				return nil, fmt.Errorf("items[%d]: %w", i, err)
			}
			out = append(out, input)
		}
		return out, nil
	}

	input, err := decodeObservationInput(root)
	if err != nil {
		return nil, err
	}
	return []observationInput{input}, nil
}

func decodeObservationInput(obj map[string]any) (observationInput, error) {
	if wrapped, ok := obj["object"].(map[string]any); ok {
		typ, _ := obj["type"].(string)
		return observationInput{
			Type:   observationType(typ),
			Object: normalizeNumbers(wrapped),
		}, nil
	}
	return observationInput{Type: core.ObservationModified, Object: normalizeNumbers(obj)}, nil
}

func observationType(value string) core.ObservationType {
	switch strings.ToUpper(value) {
	case string(core.ObservationAdded):
		return core.ObservationAdded
	case string(core.ObservationDeleted):
		return core.ObservationDeleted
	case string(core.ObservationBookmark):
		return core.ObservationBookmark
	default:
		return core.ObservationModified
	}
}

func observationFromObject(clusterID string, obsType core.ObservationType, observedAt time.Time, resolver *kubeapi.Resolver, obj map[string]any) core.Observation {
	metadata, _ := obj["metadata"].(map[string]any)
	resourceVersion, _ := metadata["resourceVersion"].(string)
	ref := resolver.ResourceRefFromObject(clusterID, obj)

	return core.Observation{
		Type:            obsType,
		ObservedAt:      observedAt,
		ResourceVersion: resourceVersion,
		Ref:             ref,
		Object:          obj,
	}
}

func discarded(decisions []filter.Decision, outcome filter.Outcome) bool {
	return hasDecision(decisions, outcome)
}

func hasDecision(decisions []filter.Decision, outcome filter.Outcome) bool {
	for _, decision := range decisions {
		if decision.Outcome == outcome {
			return true
		}
	}
	return false
}

func normalizeNumbers(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = normalizeValue(v)
	}
	return out
}

func normalizeValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return normalizeNumbers(typed)
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = normalizeValue(item)
		}
		return out
	case json.Number:
		if n, err := typed.Int64(); err == nil {
			return n
		}
		if n, err := typed.Float64(); err == nil {
			return n
		}
		return typed.String()
	default:
		return value
	}
}
