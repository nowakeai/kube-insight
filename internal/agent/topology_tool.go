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

const TopologyToolName = "kube_insight_topology"

type TopologyTool struct {
	store storage.TopologyStore
}

type topologyToolArgs struct {
	ClusterID string `json:"clusterId,omitempty"`
	Kind      string `json:"kind,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name,omitempty"`
	UID       string `json:"uid,omitempty"`
}

var _ tool.InvokableTool = (*TopologyTool)(nil)

func NewTopologyTool(store storage.TopologyStore) *TopologyTool {
	return &TopologyTool{store: store}
}

func (t *TopologyTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: TopologyToolName,
		Desc: "Load the retained topology graph around one Kubernetes object. Use this to inspect Service, EndpointSlice, Pod, Node, owner, and event relationships after search identifies a target.",
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"clusterId": {Type: schema.String, Desc: "Optional cluster name."},
			"kind":      {Type: schema.String, Desc: "Kubernetes kind, such as Service, Pod, Node, Deployment, or Event.", Required: true},
			"namespace": {Type: schema.String, Desc: "Object namespace when namespaced."},
			"name":      {Type: schema.String, Desc: "Object name.", Required: true},
			"uid":       {Type: schema.String, Desc: "Optional Kubernetes UID to disambiguate recreated objects."},
		}),
	}, nil
}

func (t *TopologyTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	if t == nil || t.store == nil {
		return "", fmt.Errorf("%s store is not configured", TopologyToolName)
	}
	args, err := parseTopologyToolArgs(argumentsInJSON)
	if err != nil {
		return "", err
	}
	graph, err := t.store.Topology(ctx, storage.ObjectTarget{ClusterID: args.ClusterID, Kind: args.Kind, Namespace: args.Namespace, Name: args.Name, UID: args.UID})
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(graph)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func parseTopologyToolArgs(argumentsInJSON string) (topologyToolArgs, error) {
	var args topologyToolArgs
	if strings.TrimSpace(argumentsInJSON) == "" {
		return args, fmt.Errorf("%s requires kind and name arguments", TopologyToolName)
	}
	if err := json.Unmarshal([]byte(argumentsInJSON), &args); err != nil {
		return topologyToolArgs{}, fmt.Errorf("invalid %s arguments: %w", TopologyToolName, err)
	}
	if strings.TrimSpace(args.Kind) == "" || strings.TrimSpace(args.Name) == "" {
		return topologyToolArgs{}, fmt.Errorf("%s requires kind and name arguments", TopologyToolName)
	}
	return args, nil
}
