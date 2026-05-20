package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"kube-insight/internal/storage"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

const SearchToolName = "kube_insight_search"

const defaultSearchToolLimit = 20
const maxSearchToolLimit = 100
const maxSearchToolVersionsPerObject = 5

type SearchTool struct {
	store        storage.EvidenceSearchStore
	defaultLimit int
	maxLimit     int
}

type SearchToolOptions struct {
	DefaultLimit int
	MaxLimit     int
}

type searchToolArgs struct {
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

var _ tool.InvokableTool = (*SearchTool)(nil)

func NewSearchTool(store storage.EvidenceSearchStore, opts SearchToolOptions) *SearchTool {
	defaultLimit := opts.DefaultLimit
	if defaultLimit <= 0 {
		defaultLimit = defaultSearchToolLimit
	}
	maxLimit := opts.MaxLimit
	if maxLimit <= 0 {
		maxLimit = maxSearchToolLimit
	}
	return &SearchTool{store: store, defaultLimit: defaultLimit, maxLimit: maxLimit}
}

func (t *SearchTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: SearchToolName,
		Desc: "Search kube-insight evidence to find candidate Kubernetes objects from facts, changes, retained documents, and indexed evidence. Use after kube_insight_health when you need relevant objects to investigate.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"query":                {Type: schema.String, Desc: "Search terms, symptoms, object names, labels, statuses, error text, or other evidence keywords.", Required: true},
			"clusterId":            {Type: schema.String, Desc: "Optional cluster name to search."},
			"kind":                 {Type: schema.String, Desc: "Optional Kubernetes kind filter such as Pod, Service, Event, Node, or Deployment."},
			"namespace":            {Type: schema.String, Desc: "Optional namespace filter."},
			"from":                 {Type: schema.String, Desc: "Optional RFC3339 timestamp or YYYY-MM-DD lower bound."},
			"to":                   {Type: schema.String, Desc: "Optional RFC3339 timestamp or YYYY-MM-DD upper bound."},
			"limit":                {Type: schema.Integer, Desc: fmt.Sprintf("Maximum matches to return. Defaults to %d and caps at %d.", t.defaultLimit, t.maxLimit)},
			"maxVersionsPerObject": {Type: schema.Integer, Desc: fmt.Sprintf("When including bundles, cap retained versions per object. Caps at %d.", maxSearchToolVersionsPerObject)},
			"includeBundles":       {Type: schema.Boolean, Desc: "Include compact evidence bundles for matched objects."},
			"includeHealth":        {Type: schema.Boolean, Desc: "Include collector coverage summary. Defaults to true."},
			"healthStaleAfter":     {Type: schema.String, Desc: "Go duration such as 5m or 1h for coverage freshness when includeHealth is true."},
		}),
	}, nil
}

func (t *SearchTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	if t == nil || t.store == nil {
		return "", fmt.Errorf("%s store is not configured", SearchToolName)
	}
	args, err := parseSearchToolArgs(argumentsInJSON)
	if err != nil {
		return "", err
	}
	opts := storage.EvidenceSearchOptions{
		Query:                args.Query,
		ClusterID:            args.ClusterID,
		Kind:                 args.Kind,
		Namespace:            args.Namespace,
		Limit:                searchToolLimit(args.Limit, t.defaultLimit, t.maxLimit),
		MaxVersionsPerObject: searchToolVersionsPerObject(args.MaxVersionsPerObject),
		IncludeHealth:        true,
	}
	if args.IncludeBundles != nil {
		opts.IncludeBundles = *args.IncludeBundles
	}
	if args.IncludeHealth != nil {
		opts.IncludeHealth = *args.IncludeHealth
	}
	if args.From != "" {
		opts.From, err = parseSearchToolTime(args.From)
		if err != nil {
			return "", fmt.Errorf("from: %w", err)
		}
	}
	if args.To != "" {
		opts.To, err = parseSearchToolTime(args.To)
		if err != nil {
			return "", fmt.Errorf("to: %w", err)
		}
	}
	if !opts.From.IsZero() && !opts.To.IsZero() && opts.From.After(opts.To) {
		return "", fmt.Errorf("from must be before to")
	}
	if args.HealthStaleAfter != "" {
		opts.HealthStaleAfter, err = time.ParseDuration(args.HealthStaleAfter)
		if err != nil {
			return "", fmt.Errorf("healthStaleAfter: %w", err)
		}
	}
	result, err := t.store.SearchEvidence(ctx, opts)
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func parseSearchToolArgs(argumentsInJSON string) (searchToolArgs, error) {
	var args searchToolArgs
	if strings.TrimSpace(argumentsInJSON) == "" {
		return args, fmt.Errorf("%s requires a query argument", SearchToolName)
	}
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return searchToolArgs{}, fmt.Errorf("invalid %s arguments: %w", SearchToolName, err)
	}
	if strings.TrimSpace(args.Query) == "" {
		return searchToolArgs{}, fmt.Errorf("%s requires a query argument", SearchToolName)
	}
	return args, nil
}

func searchToolLimit(requested, defaultLimit, maxLimit int) int {
	if requested <= 0 {
		return defaultLimit
	}
	if requested > maxLimit {
		return maxLimit
	}
	return requested
}

func searchToolVersionsPerObject(requested int) int {
	if requested <= 0 {
		return 0
	}
	if requested > maxSearchToolVersionsPerObject {
		return maxSearchToolVersionsPerObject
	}
	return requested
}

func parseSearchToolTime(value string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t, nil
	}
	return time.Parse("2006-01-02", value)
}
