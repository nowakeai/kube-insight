package cli

import (
	"context"
	"fmt"
	"strings"

	"kube-insight/internal/agent"
	"kube-insight/internal/api"
	"kube-insight/internal/storage"
	"kube-insight/internal/storage/sqlite"
)

const maxAgentRuntimeContextBytes = 2400

type agentRuntimeContextStore interface {
	storage.ResourceHealthStore
	storage.SQLQueryStore
	Close() error
}

type agentRuntimeContextRunner struct {
	inner api.AgentRunner
	build func(context.Context) string
}

func (r agentRuntimeContextRunner) Run(ctx context.Context, input agent.EinoRunInput) (agent.EinoRunResult, error) {
	if r.inner == nil {
		return agent.EinoRunResult{}, nil
	}
	if r.build != nil {
		if context := strings.TrimSpace(r.build(ctx)); context != "" {
			messages := make([]agent.Message, 0, len(input.Messages)+1)
			messages = append(messages, agent.Message{Role: agent.RoleSystem, Content: context})
			messages = append(messages, input.Messages...)
			input.Messages = messages
		}
	}
	return r.inner.Run(ctx, input)
}

func withAgentRuntimeContext(runner api.AgentRunner, dbPath string) api.AgentRunner {
	if runner == nil || strings.TrimSpace(dbPath) == "" {
		return runner
	}
	return agentRuntimeContextRunner{
		inner: runner,
		build: func(ctx context.Context) string {
			store, err := sqlite.OpenReadOnly(dbPath)
			if err != nil {
				return ""
			}
			defer store.Close()
			return buildAgentRuntimeContext(ctx, store)
		},
	}
}

func buildAgentRuntimeContext(ctx context.Context, store agentRuntimeContextStore) string {
	health, err := store.ResourceHealth(ctx, storage.ResourceHealthOptions{Limit: 16})
	if err != nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("Runtime kube-insight orientation:\n")
	b.WriteString("- This is routing context only, not proof. Final answers still need tool-backed citations.\n")
	b.WriteString(fmt.Sprintf("- Resource coverage: resources=%d healthy=%d queued=%d unstable=%d errors=%d stale=%d notStarted=%d complete=%t.\n",
		health.Summary.Resources,
		health.Summary.Healthy,
		health.Summary.Queued,
		health.Summary.Unstable,
		health.Summary.Errors,
		health.Summary.Stale,
		health.Summary.NotStarted,
		health.Summary.Complete,
	))
	if len(health.Resources) > 0 {
		parts := make([]string, 0, min(len(health.Resources), 8))
		for _, record := range health.Resources {
			if len(parts) >= 8 {
				break
			}
			name := record.Resource
			if record.Group != "" {
				name = record.Group + "/" + name
			}
			parts = append(parts, fmt.Sprintf("%s=%s latest=%d", name, record.Status, record.LatestObjects))
		}
		b.WriteString("- Resource streams: " + strings.Join(parts, "; ") + ".\n")
	}
	if counts := runtimeContextObjectCounts(ctx, store); counts != "" {
		b.WriteString("- Current object counts: " + counts + ".\n")
	}
	if services := runtimeContextServiceHints(ctx, store); services != "" {
		b.WriteString("- High-signal Service targets with EndpointSlice/Pod evidence: " + services + ".\n")
	}
	b.WriteString("- Exact Service health: call kube_insight_health, then kube_insight_service_investigation for namespace/name. Usually stop there.\n")
	b.WriteString("- Exact Service topology: call kube_insight_service_investigation first; call kube_insight_topology only if the bundle lacks enough related EndpointSlice or Pod evidence.\n")
	b.WriteString("- Do not call kube_insight_search for exact namespace/name Service questions unless the exact object cannot be found.\n")
	b.WriteString("- Do not call kube_insight_schema or kube_insight_sql unless typed tools cannot answer the user's question.\n")
	return clampAgentRuntimeContext(b.String())
}

func runtimeContextObjectCounts(ctx context.Context, store agentRuntimeContextStore) string {
	result, err := store.QuerySQL(ctx, storage.SQLQueryOptions{
		SQL: `select ok.kind, count(*) as objects
from latest_raw_index li
join object_kinds ok on ok.id = li.kind_id
group by ok.kind
order by objects desc, ok.kind
limit 12`,
		MaxRows: 12,
	})
	if err != nil {
		return ""
	}
	parts := make([]string, 0, len(result.Rows))
	for _, row := range result.Rows {
		kind := fmt.Sprint(row["kind"])
		count := fmt.Sprint(row["objects"])
		if kind == "" || count == "" || kind == "<nil>" || count == "<nil>" {
			continue
		}
		parts = append(parts, kind+"="+count)
	}
	return strings.Join(parts, ", ")
}

func runtimeContextServiceHints(ctx context.Context, store agentRuntimeContextStore) string {
	result, err := store.QuerySQL(ctx, storage.SQLQueryOptions{
		SQL: `select svc.namespace as namespace, svc.name as name, count(distinct pod.name) as pods
from object_edges svc_edge
join objects slice on slice.id = svc_edge.src_id
join objects svc on svc.id = svc_edge.dst_id
join object_edges pod_edge on pod_edge.src_id = slice.id and pod_edge.edge_type = 'endpointslice_targets_pod'
join objects pod on pod.id = pod_edge.dst_id
where svc_edge.edge_type = 'endpointslice_for_service'
group by svc.namespace, svc.name
order by pods desc, svc.namespace, svc.name
limit 8`,
		MaxRows: 8,
	})
	if err != nil {
		return ""
	}
	parts := make([]string, 0, len(result.Rows))
	for _, row := range result.Rows {
		namespace := fmt.Sprint(row["namespace"])
		name := fmt.Sprint(row["name"])
		pods := fmt.Sprint(row["pods"])
		if namespace == "" || name == "" || namespace == "<nil>" || name == "<nil>" {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s/%s pods=%s", namespace, name, pods))
	}
	return strings.Join(parts, "; ")
}

func clampAgentRuntimeContext(value string) string {
	if len(value) <= maxAgentRuntimeContextBytes {
		return value
	}
	return value[:maxAgentRuntimeContextBytes] + "\n- Runtime context truncated; use tools for proof.\n"
}
