package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"kube-insight/internal/storage"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

const HealthToolName = "kube_insight_health"

const defaultHealthToolLimit = 50
const maxHealthToolLimit = 200

type HealthTool struct {
	store        storage.ResourceHealthStore
	defaultLimit int
	maxLimit     int
}

type HealthToolOptions struct {
	DefaultLimit int
	MaxLimit     int
}

type healthToolArgs struct {
	ClusterID        string   `json:"clusterId,omitempty"`
	Status           string   `json:"status,omitempty"`
	ErrorsOnly       bool     `json:"errorsOnly,omitempty"`
	StaleAfter       string   `json:"staleAfter,omitempty"`
	Limit            int      `json:"limit,omitempty"`
	ExcludeResources []string `json:"excludeResources,omitempty"`
	IncludeSkipped   bool     `json:"includeSkipped,omitempty"`
}

var _ tool.InvokableTool = (*HealthTool)(nil)

func NewHealthTool(store storage.ResourceHealthStore, opts HealthToolOptions) *HealthTool {
	defaultLimit := opts.DefaultLimit
	if defaultLimit <= 0 {
		defaultLimit = defaultHealthToolLimit
	}
	maxLimit := opts.MaxLimit
	if maxLimit <= 0 {
		maxLimit = maxHealthToolLimit
	}
	return &HealthTool{store: store, defaultLimit: defaultLimit, maxLimit: maxLimit}
}

func (t *HealthTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: HealthToolName,
		Desc: "Check kube-insight collector coverage and freshness before making current-state Kubernetes claims. Use this first when a question depends on whether watched resources are current.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"clusterId":        {Type: schema.String, Desc: "Optional cluster name to inspect."},
			"status":           {Type: schema.String, Desc: "Optional status filter such as watching, bookmark, queued, retrying, list_error, watch_error, or not_started."},
			"errorsOnly":       {Type: schema.Boolean, Desc: "Only return resources with collection errors."},
			"staleAfter":       {Type: schema.String, Desc: "Go duration such as 5m or 1h. Marks resources stale when their offset is older than this duration."},
			"limit":            {Type: schema.Integer, Desc: fmt.Sprintf("Maximum resource rows to return. Defaults to %d and caps at %d.", t.defaultLimit, t.maxLimit)},
			"excludeResources": {Type: schema.Array, ElemInfo: &schema.ParameterInfo{Type: schema.String}, Desc: "Resource names to skip, such as events or pods."},
			"includeSkipped":   {Type: schema.Boolean, Desc: "Include excluded resources in the returned rows with status skipped."},
		}),
	}, nil
}

func (t *HealthTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	if t == nil || t.store == nil {
		return "", fmt.Errorf("%s store is not configured", HealthToolName)
	}
	args, err := parseHealthToolArgs(argumentsInJSON)
	if err != nil {
		return "", err
	}
	opts := storage.ResourceHealthOptions{
		ClusterID:        args.ClusterID,
		Status:           args.Status,
		ErrorsOnly:       args.ErrorsOnly,
		Limit:            healthToolLimit(args.Limit, t.defaultLimit, t.maxLimit),
		ExcludeResources: append([]string(nil), args.ExcludeResources...),
		IncludeExcluded:  args.IncludeSkipped,
	}
	if args.StaleAfter != "" {
		duration, err := time.ParseDuration(args.StaleAfter)
		if err != nil {
			return "", fmt.Errorf("staleAfter: %w", err)
		}
		opts.StaleAfter = duration
	}
	report, err := t.store.ResourceHealth(ctx, opts)
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(report)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func parseHealthToolArgs(argumentsInJSON string) (healthToolArgs, error) {
	if argumentsInJSON == "" {
		return healthToolArgs{}, nil
	}
	var args healthToolArgs
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return healthToolArgs{}, fmt.Errorf("invalid %s arguments: %w", HealthToolName, err)
	}
	return args, nil
}

func healthToolLimit(requested, defaultLimit, maxLimit int) int {
	if requested <= 0 {
		return defaultLimit
	}
	if requested > maxLimit {
		return maxLimit
	}
	return requested
}
