package extractor

import (
	"context"
	"fmt"

	"kube-insight/internal/core"
	"kube-insight/internal/kubeapi"
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
	byKind map[string]Extractor
	common []Extractor
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
	r := &Registry{byKind: map[string]Extractor{}}
	for _, e := range extractors {
		if e.Kind() == "*" {
			r.common = append(r.common, e)
			continue
		}
		r.byKind[e.Kind()] = e
	}
	return r
}

func (r *Registry) Extract(ctx context.Context, obs core.Observation) (Evidence, error) {
	var out Evidence
	for _, e := range r.common {
		evidence, err := e.Extract(ctx, obs)
		if err != nil {
			return out, err
		}
		out = mergeEvidence(out, evidence)
	}
	e, ok := r.byKind[obs.Ref.Kind]
	if !ok {
		return out, nil
	}
	evidence, err := e.Extract(ctx, obs)
	if err != nil {
		return out, err
	}
	return mergeEvidence(out, evidence), nil
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
