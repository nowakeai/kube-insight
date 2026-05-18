package storage

import (
	"context"
	"time"

	"kube-insight/internal/core"
)

type ObjectTarget struct {
	ClusterID string
	Kind      string
	Namespace string
	Name      string
	UID       string
}

type ObjectRecord struct {
	LogicalID        string    `json:"logicalId"`
	ClusterID        string    `json:"clusterId"`
	Group            string    `json:"group"`
	Version          string    `json:"version"`
	Resource         string    `json:"resource"`
	Kind             string    `json:"kind"`
	Namespace        string    `json:"namespace,omitempty"`
	Name             string    `json:"name"`
	UID              string    `json:"uid,omitempty"`
	LatestObservedAt time.Time `json:"latestObservedAt"`
}

type EvidenceBundle struct {
	Object       ObjectRecord      `json:"object"`
	Latest       map[string]any    `json:"latest,omitempty"`
	Versions     []VersionEvidence `json:"versions,omitempty"`
	VersionDiffs []VersionDiff     `json:"versionDiffs,omitempty"`
	Facts        []core.Fact       `json:"facts,omitempty"`
	Edges        []core.Edge       `json:"edges,omitempty"`
	Changes      []core.Change     `json:"changes,omitempty"`
	Summary      BundleSummary     `json:"summary"`
}

type VersionEvidence struct {
	ID              int64          `json:"id"`
	Sequence        int64          `json:"sequence"`
	ObservedAt      time.Time      `json:"observedAt"`
	ResourceVersion string         `json:"resourceVersion,omitempty"`
	DocumentHash    string         `json:"documentHash"`
	Materialization string         `json:"materialization"`
	Strategy        string         `json:"strategy"`
	ReplayDepth     int            `json:"replayDepth"`
	Summary         map[string]any `json:"summary,omitempty"`
	Document        map[string]any `json:"document,omitempty"`
}

type VersionDiff struct {
	FromVersionID int64                `json:"fromVersionId"`
	ToVersionID   int64                `json:"toVersionId"`
	FromSequence  int64                `json:"fromSequence"`
	ToSequence    int64                `json:"toSequence"`
	Changes       []DocumentDiffChange `json:"changes"`
	Truncated     bool                 `json:"truncated,omitempty"`
}

type DocumentDiffChange struct {
	Path   string `json:"path"`
	Before string `json:"before,omitempty"`
	After  string `json:"after,omitempty"`
}

type BundleSummary struct {
	Facts         int `json:"facts"`
	Edges         int `json:"edges"`
	Changes       int `json:"changes"`
	Versions      int `json:"versions,omitempty"`
	VersionDiffs  int `json:"versionDiffs,omitempty"`
	EvidenceScore int `json:"evidenceScore,omitempty"`
	Rank          int `json:"rank,omitempty"`
}

type InvestigationOptions struct {
	From                 time.Time
	To                   time.Time
	MaxEvidenceObjects   int
	MaxVersionsPerObject int
	MaxFactsPerObject    int
	MaxChangesPerObject  int
}

type ObjectInvestigationStore interface {
	InvestigateWithOptions(context.Context, ObjectTarget, InvestigationOptions) (EvidenceBundle, error)
}

type ServiceInvestigation struct {
	Service  EvidenceBundle              `json:"service"`
	Objects  []EvidenceBundle            `json:"objects"`
	Topology []TopologyEdge              `json:"topology"`
	Summary  ServiceInvestigationSummary `json:"summary"`
}

type ServiceInvestigationSummary struct {
	Objects        int `json:"objects"`
	EndpointSlices int `json:"endpointSlices"`
	Pods           int `json:"pods"`
	Nodes          int `json:"nodes"`
	Events         int `json:"events"`
	Facts          int `json:"facts"`
	Changes        int `json:"changes"`
	Edges          int `json:"edges"`
	Versions       int `json:"versions"`
	VersionDiffs   int `json:"versionDiffs"`
}

type ObjectHistoryOptions struct {
	From            time.Time
	To              time.Time
	MaxVersions     int
	MaxObservations int
	IncludeDocs     bool
	IncludeDiffs    bool
}

type ObjectHistory struct {
	Object       ObjectRecord         `json:"object"`
	Versions     []HistoryVersion     `json:"versions"`
	Observations []HistoryObservation `json:"observations,omitempty"`
	VersionDiffs []VersionDiff        `json:"versionDiffs,omitempty"`
	Summary      ObjectHistorySummary `json:"summary"`
}

type HistoryVersion struct {
	ID                         int64          `json:"id"`
	Sequence                   int64          `json:"sequence"`
	ObservedAt                 time.Time      `json:"observedAt"`
	ResourceVersion            string         `json:"resourceVersion,omitempty"`
	DocumentHash               string         `json:"documentHash"`
	Materialization            string         `json:"materialization"`
	Strategy                   string         `json:"strategy"`
	ReplayDepth                int            `json:"replayDepth"`
	RawSize                    int64          `json:"rawSize"`
	StoredSize                 int64          `json:"storedSize"`
	ObservationCount           int64          `json:"observationCount"`
	ContentChangedObservations int64          `json:"contentChangedObservations"`
	UnchangedObservations      int64          `json:"unchangedObservations"`
	FirstObservedAt            time.Time      `json:"firstObservedAt"`
	LastObservedAt             time.Time      `json:"lastObservedAt"`
	LatestResourceVersion      string         `json:"latestResourceVersion,omitempty"`
	Document                   map[string]any `json:"document,omitempty"`
}

type HistoryObservation struct {
	ID              int64     `json:"id"`
	ObservedAt      time.Time `json:"observedAt"`
	ObservationType string    `json:"observationType"`
	ResourceVersion string    `json:"resourceVersion,omitempty"`
	VersionID       int64     `json:"versionId,omitempty"`
	VersionSequence int64     `json:"versionSequence,omitempty"`
	DocumentHash    string    `json:"documentHash,omitempty"`
	ContentChanged  bool      `json:"contentChanged"`
}

type ObjectHistorySummary struct {
	Versions                   int   `json:"versions"`
	VersionDiffs               int   `json:"versionDiffs,omitempty"`
	Observations               int64 `json:"observations"`
	ReturnedObservations       int   `json:"returnedObservations"`
	ContentChangedObservations int64 `json:"contentChangedObservations"`
	UnchangedObservations      int64 `json:"unchangedObservations"`
}

type EvidenceSearchOptions struct {
	Query                string        `json:"query,omitempty"`
	ClusterID            string        `json:"clusterId,omitempty"`
	Kind                 string        `json:"kind,omitempty"`
	Namespace            string        `json:"namespace,omitempty"`
	From                 time.Time     `json:"from,omitempty"`
	To                   time.Time     `json:"to,omitempty"`
	Limit                int           `json:"limit,omitempty"`
	MaxVersionsPerObject int           `json:"maxVersionsPerObject,omitempty"`
	IncludeBundles       bool          `json:"includeBundles,omitempty"`
	IncludeHealth        bool          `json:"includeHealth,omitempty"`
	HealthStaleAfter     time.Duration `json:"-"`
}

type EvidenceSearchResult struct {
	Input    EvidenceSearchOptions   `json:"input"`
	Matches  []EvidenceSearchMatch   `json:"matches"`
	Bundles  []EvidenceBundle        `json:"bundles,omitempty"`
	Summary  EvidenceSearchSummary   `json:"summary"`
	Coverage *EvidenceSearchCoverage `json:"coverage,omitempty"`
}

type EvidenceSearchMatch struct {
	Object  ObjectRecord `json:"object"`
	Score   int          `json:"score"`
	Reasons []string     `json:"reasons"`
}

type EvidenceSearchSummary struct {
	Matches int `json:"matches"`
	Bundles int `json:"bundles"`
}

type EvidenceSearchCoverage struct {
	Summary  ResourceHealthSummary `json:"summary"`
	ByStatus map[string]int        `json:"byStatus,omitempty"`
}

type TopologyGraph struct {
	Root    ObjectRecord    `json:"root"`
	Nodes   []ObjectRecord  `json:"nodes"`
	Edges   []TopologyEdge  `json:"edges"`
	Summary TopologySummary `json:"summary"`
}

type TopologyEdge struct {
	Type      string       `json:"type"`
	Source    ObjectRecord `json:"source"`
	Target    ObjectRecord `json:"target"`
	Direction string       `json:"direction"`
	ValidFrom time.Time    `json:"validFrom"`
	ValidTo   *time.Time   `json:"validTo,omitempty"`
}

type TopologySummary struct {
	Nodes int `json:"nodes"`
	Edges int `json:"edges"`
}

type SQLQueryOptions struct {
	SQL     string `json:"sql"`
	MaxRows int    `json:"maxRows"`
}

type SQLQueryResult struct {
	SQL       string           `json:"sql"`
	Columns   []string         `json:"columns"`
	Rows      []map[string]any `json:"rows"`
	RowCount  int              `json:"rowCount"`
	MaxRows   int              `json:"maxRows"`
	Truncated bool             `json:"truncated"`
	ElapsedMS float64          `json:"elapsedMs"`
}

type SQLSchema struct {
	Tables        []SQLSchemaTable        `json:"tables"`
	Relationships []SQLSchemaRelationship `json:"relationships,omitempty"`
	Recipes       []SQLSchemaRecipe       `json:"recipes,omitempty"`
	Notes         []string                `json:"notes,omitempty"`
}

type SQLSchemaTable struct {
	Name        string            `json:"name"`
	Type        string            `json:"type,omitempty"`
	Description string            `json:"description,omitempty"`
	Columns     []SQLSchemaColumn `json:"columns"`
	Indexes     []SQLSchemaIndex  `json:"indexes,omitempty"`
}

type SQLSchemaColumn struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	NotNull bool   `json:"notNull"`
	Primary bool   `json:"primary"`
}

type SQLSchemaIndex struct {
	Name    string   `json:"name"`
	Unique  bool     `json:"unique"`
	Columns []string `json:"columns"`
}

type SQLSchemaRelationship struct {
	Name        string `json:"name"`
	From        string `json:"from"`
	To          string `json:"to"`
	SQL         string `json:"sql"`
	Description string `json:"description,omitempty"`
}

type SQLSchemaRecipe struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	SQL         string `json:"sql"`
}

type SQLQueryStore interface {
	QuerySQL(context.Context, SQLQueryOptions) (SQLQueryResult, error)
	QuerySchema(context.Context) (SQLSchema, error)
}

type ObjectHistoryStore interface {
	ObjectHistory(context.Context, ObjectTarget, ObjectHistoryOptions) (ObjectHistory, error)
}

type EvidenceSearchStore interface {
	SearchEvidence(context.Context, EvidenceSearchOptions) (EvidenceSearchResult, error)
}

type ServiceInvestigationStore interface {
	InvestigateServiceWithOptions(context.Context, ObjectTarget, InvestigationOptions) (ServiceInvestigation, error)
}

type TopologyStore interface {
	Topology(context.Context, ObjectTarget) (TopologyGraph, error)
}
