package storage

import (
	"context"
	"time"

	"kube-insight/internal/core"
	"kube-insight/internal/extractor"
	"kube-insight/internal/filter"
	"kube-insight/internal/kubeapi"
)

type Store interface {
	PutObservation(context.Context, core.Observation, extractor.Evidence) error
	GetFacts(context.Context, string) ([]core.Fact, error)
	GetEdges(context.Context, string) ([]core.Edge, error)
}

type APIResourceStore interface {
	UpsertAPIResources(context.Context, []kubeapi.ResourceInfo, time.Time) error
	APIResources(context.Context) ([]kubeapi.ResourceInfo, error)
}

type ClusterRecord struct {
	Name      string
	UID       string
	Source    string
	CreatedAt time.Time
	Objects   int64
	Versions  int64
	Latest    int64
}

type ClusterStore interface {
	UpsertCluster(context.Context, ClusterRecord) error
}

type FilterDecisionStore interface {
	PutFilterDecisions(context.Context, core.Observation, []filter.Decision) error
}

type OffsetEvent string

const (
	OffsetEventList     OffsetEvent = "list"
	OffsetEventWatch    OffsetEvent = "watch"
	OffsetEventBookmark OffsetEvent = "bookmark"
)

type IngestionOffset struct {
	ClusterID       string
	Resource        kubeapi.ResourceInfo
	Namespace       string
	ResourceVersion string
	Event           OffsetEvent
	Status          string
	Error           string
	At              time.Time
}

type IngestionOffsetStore interface {
	UpsertIngestionOffset(context.Context, IngestionOffset) error
}

type LatestResourceRefStore interface {
	LatestResourceRefs(context.Context, string, kubeapi.ResourceInfo, string) ([]core.ResourceRef, error)
}
