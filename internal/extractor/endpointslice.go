package extractor

import (
	"context"

	"kube-insight/internal/core"
)

type EndpointSliceExtractor struct{}

func (EndpointSliceExtractor) Kind() string { return "EndpointSlice" }

func (EndpointSliceExtractor) Extract(_ context.Context, obs core.Observation) (Evidence, error) {
	var out Evidence
	serviceName, _ := stringAt(obs.Object, "metadata", "labels", "kubernetes.io/service-name")
	endpoints, ok := obs.Object["endpoints"].([]any)
	if !ok {
		return out, nil
	}
	if serviceName != "" {
		out.Edges = append(out.Edges, edge(obs, "endpointslice_for_service", "services/"+obs.Ref.Namespace+"/"+serviceName))
	}
	for _, raw := range endpoints {
		endpoint, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		targetName := ""
		if serviceName != "" {
			if targetRef, ok := endpoint["targetRef"].(map[string]any); ok {
				if kind, _ := targetRef["kind"].(string); kind == "Pod" {
					if name, _ := targetRef["name"].(string); name != "" {
						targetName = name
						targetNamespace, _ := targetRef["namespace"].(string)
						if targetNamespace == "" {
							targetNamespace = obs.Ref.Namespace
						}
						out.Edges = append(out.Edges, edge(obs, "endpointslice_targets_pod", "pods/"+targetNamespace+"/"+name))
						out.Changes = append(out.Changes, change(obs, "topology", "endpoints.targetRef", "add", "", "Pod/"+name, 30))
					}
				}
			}
		}
		if conditions, ok := endpoint["conditions"].(map[string]any); ok {
			for _, key := range []string{"ready", "serving", "terminating"} {
				if value, ok := conditions[key].(bool); ok {
					stringValue := "false"
					if value {
						stringValue = "true"
					}
					out.Facts = append(out.Facts, fact(obs, "endpoint."+key, stringValue, 20))
					path := "endpoints.conditions." + key
					if targetName != "" {
						path = "endpoints." + targetName + ".conditions." + key
					}
					out.Changes = append(out.Changes, change(obs, "status", path, "replace", "", stringValue, 20))
				}
			}
		}
	}
	return out, nil
}
