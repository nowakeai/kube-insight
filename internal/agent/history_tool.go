package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"kube-insight/internal/storage"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

const HistoryToolName = "kube_insight_history"

const defaultHistoryToolVersions = 10
const maxHistoryToolVersions = 50
const defaultHistoryToolObservations = 50
const maxHistoryToolObservations = 200

type HistoryTool struct {
	store storage.ObjectHistoryStore
}

type historyToolArgs struct {
	ClusterID       string `json:"clusterId,omitempty"`
	Kind            string `json:"kind,omitempty"`
	Namespace       string `json:"namespace,omitempty"`
	Name            string `json:"name,omitempty"`
	UID             string `json:"uid,omitempty"`
	From            string `json:"from,omitempty"`
	To              string `json:"to,omitempty"`
	MaxVersions     int    `json:"maxVersions,omitempty"`
	MaxObservations int    `json:"maxObservations,omitempty"`
	IncludeDocs     bool   `json:"includeDocs,omitempty"`
	IncludeDiffs    bool   `json:"includeDiffs,omitempty"`
}

var _ tool.InvokableTool = (*HistoryTool)(nil)

func NewHistoryTool(store storage.ObjectHistoryStore) *HistoryTool {
	return &HistoryTool{store: store}
}

func (t *HistoryTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: HistoryToolName,
		Desc: "Load retained versions, observations, diffs, and proof metadata for one Kubernetes object. Use this after search identifies a target object that needs timeline or history proof.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"clusterId":       {Type: schema.String, Desc: "Optional cluster name."},
			"kind":            {Type: schema.String, Desc: "Kubernetes kind, such as Pod, Service, Event, Node, or Deployment.", Required: true},
			"namespace":       {Type: schema.String, Desc: "Object namespace when namespaced."},
			"name":            {Type: schema.String, Desc: "Object name.", Required: true},
			"uid":             {Type: schema.String, Desc: "Optional Kubernetes UID to disambiguate recreated objects."},
			"from":            {Type: schema.String, Desc: "Optional RFC3339 timestamp or YYYY-MM-DD lower bound."},
			"to":              {Type: schema.String, Desc: "Optional RFC3339 timestamp or YYYY-MM-DD upper bound."},
			"maxVersions":     {Type: schema.Integer, Desc: fmt.Sprintf("Maximum retained versions. Defaults to %d and caps at %d.", defaultHistoryToolVersions, maxHistoryToolVersions)},
			"maxObservations": {Type: schema.Integer, Desc: fmt.Sprintf("Maximum observation rows. Defaults to %d and caps at %d.", defaultHistoryToolObservations, maxHistoryToolObservations)},
			"includeDocs":     {Type: schema.Boolean, Desc: "Include retained JSON documents. Use sparingly because documents can be large."},
			"includeDiffs":    {Type: schema.Boolean, Desc: "Include version-to-version diffs when available."},
		}),
	}, nil
}

func (t *HistoryTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	if t == nil || t.store == nil {
		return "", fmt.Errorf("%s store is not configured", HistoryToolName)
	}
	args, err := parseHistoryToolArgs(argumentsInJSON)
	if err != nil {
		return "", err
	}
	target := storage.ObjectTarget{ClusterID: args.ClusterID, Kind: args.Kind, Namespace: args.Namespace, Name: args.Name, UID: args.UID}
	opts := storage.ObjectHistoryOptions{
		MaxVersions:     historyToolLimit(args.MaxVersions, defaultHistoryToolVersions, maxHistoryToolVersions),
		MaxObservations: historyToolLimit(args.MaxObservations, defaultHistoryToolObservations, maxHistoryToolObservations),
		IncludeDocs:     args.IncludeDocs,
		IncludeDiffs:    args.IncludeDiffs,
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
	history, err := t.store.ObjectHistory(ctx, target, opts)
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(history)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func parseHistoryToolArgs(argumentsInJSON string) (historyToolArgs, error) {
	var args historyToolArgs
	if strings.TrimSpace(argumentsInJSON) == "" {
		return args, fmt.Errorf("%s requires kind and name arguments", HistoryToolName)
	}
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return historyToolArgs{}, fmt.Errorf("invalid %s arguments: %w", HistoryToolName, err)
	}
	if strings.TrimSpace(args.Kind) == "" || strings.TrimSpace(args.Name) == "" {
		return historyToolArgs{}, fmt.Errorf("%s requires kind and name arguments", HistoryToolName)
	}
	return args, nil
}

func historyToolLimit(requested, defaultLimit, maxLimit int) int {
	if requested <= 0 {
		return defaultLimit
	}
	if requested > maxLimit {
		return maxLimit
	}
	return requested
}
