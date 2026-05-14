package storage

import (
	"context"
	"sync"
	"time"

	"kube-insight/internal/core"
	"kube-insight/internal/extractor"
	"kube-insight/internal/filter"
	"kube-insight/internal/kubeapi"
)

type FilterDecisionRecord struct {
	Observation core.Observation
	Decision    filter.Decision
}

type MemoryStore struct {
	mu              sync.Mutex
	Observations    []core.Observation
	Facts           []core.Fact
	Edges           []core.Edge
	Changes         []core.Change
	Resources       []kubeapi.ResourceInfo
	FilterDecisions []FilterDecisionRecord
	Offsets         []IngestionOffset
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{}
}

func (s *MemoryStore) PutObservation(_ context.Context, obs core.Observation, evidence extractor.Evidence) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.Observations = append(s.Observations, obs)
	s.Facts = append(s.Facts, evidence.Facts...)
	s.Edges = append(s.Edges, evidence.Edges...)
	s.Changes = append(s.Changes, evidence.Changes...)
	return nil
}

func (s *MemoryStore) PutFilterDecisions(_ context.Context, obs core.Observation, decisions []filter.Decision) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, decision := range decisions {
		s.FilterDecisions = append(s.FilterDecisions, FilterDecisionRecord{
			Observation: obs,
			Decision:    decision,
		})
	}
	return nil
}

func (s *MemoryStore) GetFacts(_ context.Context, objectID string) ([]core.Fact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var out []core.Fact
	for _, fact := range s.Facts {
		if fact.ObjectID == objectID {
			out = append(out, fact)
		}
	}
	return out, nil
}

func (s *MemoryStore) GetEdges(_ context.Context, sourceID string) ([]core.Edge, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var out []core.Edge
	for _, edge := range s.Edges {
		if edge.SourceID == sourceID {
			out = append(out, edge)
		}
	}
	return out, nil
}

func (s *MemoryStore) UpsertAPIResources(_ context.Context, resources []kubeapi.ResourceInfo, _ time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	byKey := map[string]int{}
	for i, resource := range s.Resources {
		byKey[apiResourceKey(resource)] = i
	}
	for _, resource := range resources {
		key := apiResourceKey(resource)
		if i, ok := byKey[key]; ok {
			s.Resources[i] = resource
			continue
		}
		byKey[key] = len(s.Resources)
		s.Resources = append(s.Resources, resource)
	}
	return nil
}

func (s *MemoryStore) APIResources(_ context.Context) ([]kubeapi.ResourceInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]kubeapi.ResourceInfo, len(s.Resources))
	copy(out, s.Resources)
	return out, nil
}

func (s *MemoryStore) UpsertIngestionOffset(_ context.Context, offset IngestionOffset) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := offset.ClusterID + "\x00" + apiResourceKey(offset.Resource) + "\x00" + offset.Namespace
	for i, existing := range s.Offsets {
		existingKey := existing.ClusterID + "\x00" + apiResourceKey(existing.Resource) + "\x00" + existing.Namespace
		if existingKey == key {
			s.Offsets[i] = offset
			return nil
		}
	}
	s.Offsets = append(s.Offsets, offset)
	return nil
}

func (s *MemoryStore) LatestResourceRefs(_ context.Context, clusterID string, resource kubeapi.ResourceInfo, namespace string) ([]core.ResourceRef, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	latest := map[string]core.Observation{}
	for _, obs := range s.Observations {
		if obs.Ref.ClusterID != clusterID || obs.Ref.Group != resource.Group || obs.Ref.Version != resource.Version || obs.Ref.Resource != resource.Resource {
			continue
		}
		if namespace != "" && obs.Ref.Namespace != namespace {
			continue
		}
		latest[resourceRefKey(obs.Ref)] = obs
	}
	out := make([]core.ResourceRef, 0, len(latest))
	for _, obs := range latest {
		if obs.Type == core.ObservationDeleted {
			continue
		}
		out = append(out, obs.Ref)
	}
	return out, nil
}

func apiResourceKey(resource kubeapi.ResourceInfo) string {
	return resource.Group + "\x00" + resource.Version + "\x00" + resource.Resource
}

func resourceRefKey(ref core.ResourceRef) string {
	if ref.UID != "" {
		return "uid:" + ref.UID
	}
	return "name:" + ref.Namespace + "/" + ref.Name
}
