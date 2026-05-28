package mcp

import (
	"context"
	"fmt"
	"strings"
	"time"

	"kube-insight/internal/storage"
)

const (
	defaultSearchLimit              = 20
	maxSearchLimit                  = 100
	maxSearchVersionsPerObject      = 5
	defaultServiceEvidenceObjects   = 20
	maxServiceEvidenceObjects       = 100
	defaultServiceVersionsPerObject = 3
	maxServiceVersionsPerObject     = 10
	defaultServiceFactsPerObject    = 20
	maxServiceFactsPerObject        = 100
	defaultServiceChangesPerObject  = 20
	maxServiceChangesPerObject      = 100
)

type searchArguments struct {
	Query                string `json:"query,omitempty"`
	ClusterID            string `json:"clusterId,omitempty"`
	Kind                 string `json:"kind,omitempty"`
	Namespace            string `json:"namespace,omitempty"`
	From                 string `json:"from,omitempty"`
	To                   string `json:"to,omitempty"`
	Limit                int    `json:"limit,omitempty"`
	MaxVersionsPerObject int    `json:"maxVersionsPerObject,omitempty"`
	IncludeBundles       *bool  `json:"includeBundles,omitempty"`
	IncludeHealth        *bool  `json:"includeHealth,omitempty"`
	HealthStaleAfter     string `json:"healthStaleAfter,omitempty"`
}

type historyArguments struct {
	ClusterID       string `json:"cluster,omitempty"`
	UID             string `json:"uid,omitempty"`
	Kind            string `json:"kind,omitempty"`
	Namespace       string `json:"namespace,omitempty"`
	Name            string `json:"name,omitempty"`
	From            string `json:"from,omitempty"`
	To              string `json:"to,omitempty"`
	MaxVersions     int    `json:"maxVersions,omitempty"`
	MaxObservations int    `json:"maxObservations,omitempty"`
	IncludeDocs     bool   `json:"includeDocs,omitempty"`
	Diffs           *bool  `json:"diffs,omitempty"`
}

type topologyArguments struct {
	ClusterID string `json:"clusterId,omitempty"`
	Kind      string `json:"kind,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name,omitempty"`
	UID       string `json:"uid,omitempty"`
}

type serviceInvestigationArguments struct {
	ClusterID            string `json:"clusterId,omitempty"`
	Namespace            string `json:"namespace,omitempty"`
	Name                 string `json:"name,omitempty"`
	From                 string `json:"from,omitempty"`
	To                   string `json:"to,omitempty"`
	MaxEvidenceObjects   int    `json:"maxEvidenceObjects,omitempty"`
	MaxVersionsPerObject int    `json:"maxVersionsPerObject,omitempty"`
	MaxFactsPerObject    int    `json:"maxFactsPerObject,omitempty"`
	MaxChangesPerObject  int    `json:"maxChangesPerObject,omitempty"`
}

func (s *Server) querySearch(ctx context.Context, args searchArguments) (any, error) {
	if strings.TrimSpace(args.Query) == "" {
		return nil, fmt.Errorf("kube_insight_search requires a query argument")
	}
	store, err := s.openReadStore(ctx)
	if err != nil {
		return nil, err
	}
	defer s.closeReadStore(store)
	clusterID, err := resolveClusterID(ctx, store, args.ClusterID)
	if err != nil {
		return nil, err
	}
	opts := storage.EvidenceSearchOptions{
		Query:                args.Query,
		ClusterID:            clusterID,
		Kind:                 args.Kind,
		Namespace:            args.Namespace,
		Limit:                boundedLimit(args.Limit, defaultSearchLimit, maxSearchLimit),
		MaxVersionsPerObject: boundedOptionalLimit(args.MaxVersionsPerObject, maxSearchVersionsPerObject),
		IncludeHealth:        true,
	}
	if args.IncludeBundles != nil {
		opts.IncludeBundles = *args.IncludeBundles
	}
	if args.IncludeHealth != nil {
		opts.IncludeHealth = *args.IncludeHealth
	}
	if args.From != "" {
		parsed, err := parseHistoryTime(args.From)
		if err != nil {
			return nil, fmt.Errorf("from: %w", err)
		}
		opts.From = parsed
	}
	if args.To != "" {
		parsed, err := parseHistoryTime(args.To)
		if err != nil {
			return nil, fmt.Errorf("to: %w", err)
		}
		opts.To = parsed
	}
	if !opts.From.IsZero() && !opts.To.IsZero() && opts.From.After(opts.To) {
		return nil, fmt.Errorf("from must be before to")
	}
	if args.HealthStaleAfter != "" {
		parsed, err := time.ParseDuration(args.HealthStaleAfter)
		if err != nil {
			return nil, fmt.Errorf("healthStaleAfter: %w", err)
		}
		opts.HealthStaleAfter = parsed
	}
	searchStore, ok := store.(storage.EvidenceSearchStore)
	if !ok {
		return nil, fmt.Errorf("configured store does not support evidence search")
	}
	result, err := searchStore.SearchEvidence(ctx, opts)
	if err != nil {
		return nil, err
	}
	return compactEvidenceSearchResult(result), nil
}

func (s *Server) queryTopology(ctx context.Context, args topologyArguments) (any, error) {
	if strings.TrimSpace(args.Kind) == "" || strings.TrimSpace(args.Name) == "" {
		return nil, fmt.Errorf("kube_insight_topology requires kind and name arguments")
	}
	store, err := s.openReadStore(ctx)
	if err != nil {
		return nil, err
	}
	defer s.closeReadStore(store)
	clusterID, err := resolveClusterID(ctx, store, args.ClusterID)
	if err != nil {
		return nil, err
	}
	topologyStore, ok := store.(storage.TopologyStore)
	if !ok {
		return nil, fmt.Errorf("configured store does not support topology")
	}
	graph, err := topologyStore.Topology(ctx, storage.ObjectTarget{ClusterID: clusterID, Kind: args.Kind, Namespace: args.Namespace, Name: args.Name, UID: args.UID})
	if err != nil {
		return nil, err
	}
	return compactTopologyGraph(graph), nil
}

func (s *Server) queryServiceInvestigation(ctx context.Context, args serviceInvestigationArguments) (any, error) {
	if strings.TrimSpace(args.Namespace) == "" || strings.TrimSpace(args.Name) == "" {
		return nil, fmt.Errorf("kube_insight_service_investigation requires namespace and name arguments")
	}
	opts := storage.InvestigationOptions{
		MaxEvidenceObjects:   boundedLimit(args.MaxEvidenceObjects, defaultServiceEvidenceObjects, maxServiceEvidenceObjects),
		MaxVersionsPerObject: boundedLimit(args.MaxVersionsPerObject, defaultServiceVersionsPerObject, maxServiceVersionsPerObject),
		MaxFactsPerObject:    boundedLimit(args.MaxFactsPerObject, defaultServiceFactsPerObject, maxServiceFactsPerObject),
		MaxChangesPerObject:  boundedLimit(args.MaxChangesPerObject, defaultServiceChangesPerObject, maxServiceChangesPerObject),
	}
	if args.From != "" {
		parsed, err := parseHistoryTime(args.From)
		if err != nil {
			return nil, fmt.Errorf("from: %w", err)
		}
		opts.From = parsed
	}
	if args.To != "" {
		parsed, err := parseHistoryTime(args.To)
		if err != nil {
			return nil, fmt.Errorf("to: %w", err)
		}
		opts.To = parsed
	}
	if !opts.From.IsZero() && !opts.To.IsZero() && opts.From.After(opts.To) {
		return nil, fmt.Errorf("from must be before to")
	}
	store, err := s.openReadStore(ctx)
	if err != nil {
		return nil, err
	}
	defer s.closeReadStore(store)
	clusterID, err := resolveClusterID(ctx, store, args.ClusterID)
	if err != nil {
		return nil, err
	}
	serviceStore, ok := store.(storage.ServiceInvestigationStore)
	if !ok {
		return nil, fmt.Errorf("configured store does not support service investigation")
	}
	result, err := serviceStore.InvestigateServiceWithOptions(ctx, storage.ObjectTarget{ClusterID: clusterID, Kind: "Service", Namespace: args.Namespace, Name: args.Name}, opts)
	if err != nil {
		return nil, err
	}
	return compactServiceInvestigation(result), nil
}

func boundedLimit(requested, defaultLimit, maxLimit int) int {
	if requested <= 0 {
		return defaultLimit
	}
	if requested > maxLimit {
		return maxLimit
	}
	return requested
}

func boundedOptionalLimit(requested, maxLimit int) int {
	if requested <= 0 {
		return 0
	}
	if requested > maxLimit {
		return maxLimit
	}
	return requested
}
