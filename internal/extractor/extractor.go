package extractor

import (
	"context"
	"fmt"

	"kube-insight/internal/core"
	"kube-insight/internal/kubeapi"
	"kube-insight/internal/logging"
	"kube-insight/internal/resourcematch"
)

type Evidence struct {
	Facts   []core.Fact
	Edges   []core.Edge
	Changes []core.Change
}

type Extractor interface {
	Kind() string
	Extract(context.Context, core.Observation) (Evidence, error)
}

type Registry struct {
	byKind map[string][]Entry
	common []Entry
	sets   map[string]map[string]bool
}

type Scope struct {
	Resources []string
	Kinds     []string
}

type Entry struct {
	Name      string
	Extractor Extractor
	Scope     Scope
}

type resolverContextKey struct{}

func WithResolver(ctx context.Context, resolver *kubeapi.Resolver) context.Context {
	if resolver == nil {
		return ctx
	}
	return context.WithValue(ctx, resolverContextKey{}, resolver)
}

func resolverFromContext(ctx context.Context) *kubeapi.Resolver {
	if resolver, ok := ctx.Value(resolverContextKey{}).(*kubeapi.Resolver); ok && resolver != nil {
		return resolver
	}
	return defaultResolver
}

func NewRegistry(extractors ...Extractor) *Registry {
	entries := make([]Entry, 0, len(extractors))
	for _, e := range extractors {
		entries = append(entries, Entry{Name: defaultExtractorName(e), Extractor: e})
	}
	return NewScopedRegistry(entries...)
}

func NewScopedRegistry(entries ...Entry) *Registry {
	return NewScopedRegistryWithSets(nil, entries...)
}

func NewScopedRegistryWithSets(sets map[string][]string, entries ...Entry) *Registry {
	r := &Registry{byKind: map[string][]Entry{}, sets: normalizeSets(sets)}
	for _, entry := range entries {
		if entry.Extractor == nil {
			continue
		}
		if entry.Name == "" {
			entry.Name = defaultExtractorName(entry.Extractor)
		}
		if entry.Extractor.Kind() == "*" {
			r.common = append(r.common, entry)
			continue
		}
		r.byKind[entry.Extractor.Kind()] = append(r.byKind[entry.Extractor.Kind()], entry)
	}
	return r
}

func (r *Registry) Extract(ctx context.Context, obs core.Observation) (Evidence, error) {
	return r.extract(ctx, obs, "")
}

func (r *Registry) ExtractSet(ctx context.Context, obs core.Observation, set string) (Evidence, error) {
	return r.extract(ctx, obs, set)
}

func (r *Registry) extract(ctx context.Context, obs core.Observation, set string) (Evidence, error) {
	logger := logging.FromContext(ctx).With("component", "extractor")
	var out Evidence
	if set == "none" {
		return out, nil
	}
	for _, entry := range r.common {
		if !entry.matches(obs) || !r.entryMatchesSet(entry, set) {
			continue
		}
		evidence, err := entry.Extractor.Extract(ctx, obs)
		if err != nil {
			return out, err
		}
		logger.Debug("extractor evidence", "extractor", entry.Name, "resource", obs.Ref.Resource, "namespace", obs.Ref.Namespace, "name", obs.Ref.Name, "facts", len(evidence.Facts), "edges", len(evidence.Edges), "changes", len(evidence.Changes))
		out = mergeEvidence(out, evidence)
	}
	entries := r.byKind[obs.Ref.Kind]
	if len(entries) == 0 {
		logger.Debug("no kind extractor", "kind", obs.Ref.Kind, "resource", obs.Ref.Resource)
		return out, nil
	}
	for _, entry := range entries {
		if !entry.matches(obs) || !r.entryMatchesSet(entry, set) {
			continue
		}
		evidence, err := entry.Extractor.Extract(ctx, obs)
		if err != nil {
			return out, err
		}
		logger.Debug("extractor evidence", "extractor", entry.Name, "resource", obs.Ref.Resource, "namespace", obs.Ref.Namespace, "name", obs.Ref.Name, "facts", len(evidence.Facts), "edges", len(evidence.Edges), "changes", len(evidence.Changes))
		out = mergeEvidence(out, evidence)
	}
	return out, nil
}

func (entry Entry) matches(obs core.Observation) bool {
	if len(entry.Scope.Kinds) > 0 && !resourcematch.MatchAnyString(entry.Scope.Kinds, obs.Ref.Kind) {
		return false
	}
	return resourcematch.MatchAnyResource(entry.Scope.Resources, resourcematch.FromCoreRef(obs.Ref))
}

func (r *Registry) entryMatchesSet(entry Entry, set string) bool {
	if len(r.sets) > 0 {
		switch set {
		case "":
			set = "default"
		case "none":
			return false
		}
		members, ok := r.sets[set]
		if !ok {
			return false
		}
		return members[entry.Name]
	}
	switch set {
	case "", "default":
		return true
	case "generic", "none":
		return false
	default:
		return entry.Name == set
	}
}

func normalizeSets(sets map[string][]string) map[string]map[string]bool {
	if len(sets) == 0 {
		return nil
	}
	out := make(map[string]map[string]bool, len(sets))
	for setName, names := range sets {
		members := make(map[string]bool, len(names))
		for _, name := range names {
			members[name] = true
		}
		out[setName] = members
	}
	return out
}

func defaultExtractorName(e Extractor) string {
	switch e.(type) {
	case ReferenceExtractor:
		return "reference"
	case PodExtractor:
		return "pod"
	case NodeExtractor:
		return "node"
	case EventExtractor:
		return "event"
	case EndpointSliceExtractor:
		return "endpointslice"
	default:
		return e.Kind()
	}
}

func mergeEvidence(a, b Evidence) Evidence {
	a.Facts = append(a.Facts, b.Facts...)
	a.Edges = append(a.Edges, b.Edges...)
	a.Changes = append(a.Changes, b.Changes...)
	return a
}

func objectID(obs core.Observation) string {
	if obs.Ref.UID != "" {
		return obs.Ref.ClusterID + "/" + obs.Ref.UID
	}
	return obs.Ref.ClusterID + "/" + obs.Ref.HumanKey()
}

func stringAt(m map[string]any, path ...string) (string, bool) {
	var cur any = m
	for _, key := range path {
		next, ok := cur.(map[string]any)
		if !ok {
			return "", false
		}
		cur, ok = next[key]
		if !ok {
			return "", false
		}
	}
	s, ok := cur.(string)
	return s, ok
}

func numericAt(m map[string]any, path ...string) (float64, bool) {
	var cur any = m
	for _, key := range path {
		next, ok := cur.(map[string]any)
		if !ok {
			return 0, false
		}
		cur, ok = next[key]
		if !ok {
			return 0, false
		}
	}
	switch n := cur.(type) {
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case float64:
		return n, true
	default:
		return 0, false
	}
}

func fact(obs core.Observation, key, value string, severity int) core.Fact {
	return core.Fact{
		Time:     obs.ObservedAt,
		ObjectID: objectID(obs),
		Key:      key,
		Value:    value,
		Severity: severity,
	}
}

func change(obs core.Observation, family, path, op, oldValue, newValue string, severity int) core.Change {
	return core.Change{
		Time:     obs.ObservedAt,
		ObjectID: objectID(obs),
		Family:   family,
		Path:     path,
		Op:       op,
		Old:      oldValue,
		New:      newValue,
		Severity: severity,
	}
}

func edge(obs core.Observation, edgeType, target string) core.Edge {
	return core.Edge{
		Type:      edgeType,
		SourceID:  objectID(obs),
		TargetID:  fmt.Sprintf("%s/%s", obs.Ref.ClusterID, target),
		ValidFrom: obs.ObservedAt,
	}
}

func edgeToRef(ctx context.Context, obs core.Observation, edgeType string, refTarget targetObjectRef) (core.Edge, bool) {
	target := refTarget.Target(resolverFromContext(ctx))
	if target == "" {
		return core.Edge{}, false
	}
	return edge(obs, edgeType, target), true
}

type targetObjectRef struct {
	APIVersion string
	Group      string
	Kind       string
	Resource   string
	Namespace  string
	Name       string
}

var defaultResolver = kubeapi.NewResolver()

func RegisterAPIObject(obj map[string]any) {
	defaultResolver.RegisterFromObject(obj)
}

func (r targetObjectRef) Target(resolver *kubeapi.Resolver) string {
	if r.Name == "" {
		return ""
	}
	group := r.Group
	if group == "" && r.APIVersion != "" {
		group, _ = kubeapi.SplitAPIVersion(r.APIVersion)
	}
	version := ""
	if r.APIVersion != "" {
		_, version = kubeapi.SplitAPIVersion(r.APIVersion)
	}
	ref, _ := resolver.ResolveTarget(group, version, r.Kind, r.Resource, r.Namespace, r.Name)
	if ref.Resource == "" {
		return ""
	}
	return kubeapi.LogicalPath(ref)
}
