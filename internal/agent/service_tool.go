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

const ServiceInvestigationToolName = "kube_insight_service_investigation"

const defaultServiceToolEvidenceObjects = 20
const maxServiceToolEvidenceObjects = 100
const defaultServiceToolVersionsPerObject = 3
const maxServiceToolVersionsPerObject = 10
const defaultServiceToolFactsPerObject = 20
const maxServiceToolFactsPerObject = 100
const defaultServiceToolChangesPerObject = 20
const maxServiceToolChangesPerObject = 100

type ServiceInvestigationTool struct {
	store storage.ServiceInvestigationStore
}

type serviceInvestigationToolArgs struct {
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

var _ tool.InvokableTool = (*ServiceInvestigationTool)(nil)

func NewServiceInvestigationTool(store storage.ServiceInvestigationStore) *ServiceInvestigationTool {
	return &ServiceInvestigationTool{store: store}
}

func (t *ServiceInvestigationTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: ServiceInvestigationToolName,
		Desc: "Load a typed Service investigation bundle, including Service evidence, related EndpointSlices, Pods, Nodes, Events, facts, changes, and topology edges. Use this when the target Kubernetes object is a Service.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"clusterId":            {Type: schema.String, Desc: "Optional cluster name."},
			"namespace":            {Type: schema.String, Desc: "Service namespace.", Required: true},
			"name":                 {Type: schema.String, Desc: "Service name.", Required: true},
			"from":                 {Type: schema.String, Desc: "Optional RFC3339 timestamp or YYYY-MM-DD lower bound."},
			"to":                   {Type: schema.String, Desc: "Optional RFC3339 timestamp or YYYY-MM-DD upper bound."},
			"maxEvidenceObjects":   {Type: schema.Integer, Desc: fmt.Sprintf("Maximum related evidence objects. Defaults to %d and caps at %d.", defaultServiceToolEvidenceObjects, maxServiceToolEvidenceObjects)},
			"maxVersionsPerObject": {Type: schema.Integer, Desc: fmt.Sprintf("Maximum retained versions per object. Defaults to %d and caps at %d.", defaultServiceToolVersionsPerObject, maxServiceToolVersionsPerObject)},
			"maxFactsPerObject":    {Type: schema.Integer, Desc: fmt.Sprintf("Maximum facts per object. Defaults to %d and caps at %d.", defaultServiceToolFactsPerObject, maxServiceToolFactsPerObject)},
			"maxChangesPerObject":  {Type: schema.Integer, Desc: fmt.Sprintf("Maximum changes per object. Defaults to %d and caps at %d.", defaultServiceToolChangesPerObject, maxServiceToolChangesPerObject)},
		}),
	}, nil
}

func (t *ServiceInvestigationTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	if t == nil || t.store == nil {
		return "", fmt.Errorf("%s store is not configured", ServiceInvestigationToolName)
	}
	args, err := parseServiceInvestigationToolArgs(argumentsInJSON)
	if err != nil {
		return "", err
	}
	target := storage.ObjectTarget{ClusterID: args.ClusterID, Kind: "Service", Namespace: args.Namespace, Name: args.Name}
	opts := storage.InvestigationOptions{
		MaxEvidenceObjects:   serviceToolLimit(args.MaxEvidenceObjects, defaultServiceToolEvidenceObjects, maxServiceToolEvidenceObjects),
		MaxVersionsPerObject: serviceToolLimit(args.MaxVersionsPerObject, defaultServiceToolVersionsPerObject, maxServiceToolVersionsPerObject),
		MaxFactsPerObject:    serviceToolLimit(args.MaxFactsPerObject, defaultServiceToolFactsPerObject, maxServiceToolFactsPerObject),
		MaxChangesPerObject:  serviceToolLimit(args.MaxChangesPerObject, defaultServiceToolChangesPerObject, maxServiceToolChangesPerObject),
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
	result, err := t.store.InvestigateServiceWithOptions(ctx, target, opts)
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func parseServiceInvestigationToolArgs(argumentsInJSON string) (serviceInvestigationToolArgs, error) {
	var args serviceInvestigationToolArgs
	if strings.TrimSpace(argumentsInJSON) == "" {
		return args, fmt.Errorf("%s requires namespace and name arguments", ServiceInvestigationToolName)
	}
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return serviceInvestigationToolArgs{}, fmt.Errorf("invalid %s arguments: %w", ServiceInvestigationToolName, err)
	}
	if strings.TrimSpace(args.Namespace) == "" || strings.TrimSpace(args.Name) == "" {
		return serviceInvestigationToolArgs{}, fmt.Errorf("%s requires namespace and name arguments", ServiceInvestigationToolName)
	}
	return args, nil
}

func serviceToolLimit(requested, defaultLimit, maxLimit int) int {
	if requested <= 0 {
		return defaultLimit
	}
	if requested > maxLimit {
		return maxLimit
	}
	return requested
}
