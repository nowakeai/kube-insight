package storage

import (
	"context"
	"time"
)

type ResourceHealthOptions struct {
	ClusterID        string
	Status           string
	ErrorsOnly       bool
	StaleAfter       time.Duration
	Limit            int
	ExcludeResources []string
	IncludeExcluded  bool
}

type ResourceHealthReport struct {
	CheckedAt time.Time              `json:"checkedAt"`
	Summary   ResourceHealthSummary  `json:"summary"`
	ByStatus  map[string]int         `json:"byStatus"`
	Resources []ResourceHealthRecord `json:"resources"`
}

type ResourceHealthSummary struct {
	Resources  int      `json:"resources"`
	Healthy    int      `json:"healthy"`
	Unstable   int      `json:"unstable"`
	Errors     int      `json:"errors"`
	Stale      int      `json:"stale"`
	NotStarted int      `json:"notStarted"`
	Queued     int      `json:"queued"`
	Skipped    int      `json:"skipped"`
	Complete   bool     `json:"complete"`
	Warnings   []string `json:"warnings,omitempty"`
}

type ResourceHealthRecord struct {
	ClusterID       string     `json:"clusterId"`
	Group           string     `json:"group,omitempty"`
	Version         string     `json:"version"`
	Resource        string     `json:"resource"`
	Kind            string     `json:"kind"`
	Namespaced      bool       `json:"namespaced"`
	Namespace       string     `json:"namespace,omitempty"`
	Status          string     `json:"status"`
	Error           string     `json:"error,omitempty"`
	ResourceVersion string     `json:"resourceVersion,omitempty"`
	LastListAt      *time.Time `json:"lastListAt,omitempty"`
	LastWatchAt     *time.Time `json:"lastWatchAt,omitempty"`
	LastBookmarkAt  *time.Time `json:"lastBookmarkAt,omitempty"`
	UpdatedAt       *time.Time `json:"updatedAt,omitempty"`
	AgeSeconds      int64      `json:"ageSeconds,omitempty"`
	Stale           bool       `json:"stale,omitempty"`
	Skipped         bool       `json:"skipped,omitempty"`
	LatestObjects   int        `json:"latestObjects"`
}

type ResourceHealthStore interface {
	ResourceHealth(context.Context, ResourceHealthOptions) (ResourceHealthReport, error)
}
