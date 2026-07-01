# kagent Integration Tutorial

This tutorial connects kube-insight to kagent so a kagent Agent can answer
Kubernetes investigation questions with retained evidence, not only live
cluster state.

Use this integration when you want:

- kagent to coordinate the conversation and live operational tools;
- kube-insight to provide historical Kubernetes state, coverage, changes,
  topology, and read-only proof;
- reusable kube-insight prompt guidance inside kagent's prompt library model.

## User Path

1. Install kube-insight with a storage backend sized for retained history.
2. Verify kube-insight API and MCP are reachable.
3. Apply the kube-insight kagent prompt library and Agent examples.
4. Confirm kagent discovered the kube-insight tools.
5. Ask questions that need retained history.

## Prerequisites

- kagent installed with `kagent.dev/v1alpha2` CRDs.
- A ready kagent `ModelConfig` in the Agent namespace.
- kube-insight installed with API and MCP enabled. The Helm chart defaults to
  chDB; use external ClickHouse for shared team storage. SQLite is only for
  tests, demos, and temporary runs.

Check the kagent model provider before creating Agents:

```bash
kubectl -n kagent get modelconfig
kubectl -n kagent get modelconfig <MODEL_CONFIG_NAME> -o yaml
```

If the ModelConfig references a missing Secret, create the provider Secret
first. For example:

```bash
kubectl -n kagent create secret generic kagent-openai \
  --from-literal=OPENAI_API_KEY='<API_KEY>'
```

## 1. Install kube-insight

Use the Helm chart default when you want embedded chDB storage on a PVC:

```bash
helm install kube-insight oci://ghcr.io/nowakeai/charts/kube-insight \
  --namespace kube-insight \
  --create-namespace \
  --version <CHART_VERSION>
```

For external ClickHouse, SQLite smoke tests, retention, metrics, and service
settings, use the [Helm Chart](../../../charts/kube-insight/README.md) as the
source of truth.

## 2. Verify kube-insight

```bash
kubectl -n kube-insight get deploy,pod,svc
kubectl -n kube-insight port-forward svc/kube-insight 8090:8090
```

In another shell:

```bash
curl -fsS http://127.0.0.1:8090/healthz
curl -fsS http://127.0.0.1:8090/api/v1/server/info
curl -fsS http://127.0.0.1:8090/api/v1/storage/stats
curl -fsS 'http://127.0.0.1:8090/api/v1/health?limit=20'
```

Expected:

- `server/info` reports API and MCP enabled.
- storage driver is `clickhouse`, `chdb`, or the short-demo `sqlite`.
- health eventually reports `complete=true`; queued streams can appear during
  initial startup while watch streams are being scheduled.

Stop the port-forward after the check.

## 3. Apply The kagent Integration

Choose one integration path.

### Option A: Helm-managed kagent resources

If you install kube-insight with Helm, enable the built-in kagent integration
and let the chart create the prompt library and `RemoteMCPServer`:

```bash
helm upgrade kube-insight oci://ghcr.io/nowakeai/charts/kube-insight \
  --namespace kube-insight \
  --reuse-values \
  --version <CHART_VERSION> \
  --set kagent.enabled=true
```

This creates two kagent-facing resources in the `kagent` namespace by default:

- `kube-insight-prompts`: reusable kagent prompt library.
- `RemoteMCPServer/kube-insight`: points kagent at kube-insight `/mcp`.

Optionally let the chart create a basic troubleshooting Agent:

```bash
helm upgrade kube-insight oci://ghcr.io/nowakeai/charts/kube-insight \
  --namespace kube-insight \
  --reuse-values \
  --version <CHART_VERSION> \
  --set kagent.enabled=true \
  --set kagent.agent.create=true \
  --set kagent.agent.modelConfig='<MODEL_CONFIG_NAME>'
```

Do not also run `kubectl apply -k examples/kagent` for the same resource names,
or the chart and manual manifests will fight over the prompt library,
`RemoteMCPServer`, and troubleshooting Agent.

### Option B: Dedicated kagent Agent chart

Use this path when kube-insight is already installed and you want to manage the
kagent resources as a separate package:

```bash
helm install kube-insight-kagent-agent \
  oci://ghcr.io/nowakeai/charts/kube-insight-kagent-agent \
  --namespace kagent \
  --version <CHART_VERSION> \
  --set agent.modelConfig='<MODEL_CONFIG_NAME>'
```

This chart installs only kagent-facing resources. It does not install
kube-insight. The default profile creates the same troubleshooting Agent and
kube-insight MCP connection as the examples.

For a broader Agent that combines kube-insight with kagent Kubernetes,
Prometheus, Helm, and shell tools:

```bash
helm upgrade kube-insight-kagent-agent \
  oci://ghcr.io/nowakeai/charts/kube-insight-kagent-agent \
  --namespace kagent \
  --reuse-values \
  --set agent.profile=aiops-fullstack \
  --set agent.name=kube-insight-aiops-fullstack
```

Before using `aiops-fullstack`, confirm your kagent tool server exposes the
referenced tool names.

### Option C: Manual or GitOps-managed examples

Use this path when kube-insight itself is not Helm-managed, or when you want
GitOps to own the kagent resources separately from the kube-insight release.
The example kustomization installs three pieces:

- `kube-insight-prompts`: reusable kagent prompt library.
- `RemoteMCPServer/kube-insight`: points kagent at kube-insight `/mcp`.
- `Agent/kube-insight-troubleshooter`: a retained-evidence troubleshooting
  Agent.

Review the default Service URL in
`examples/kagent/remote-mcpserver.yaml`:

```text
http://kube-insight.kube-insight.svc.cluster.local:8090/mcp
```

Apply the default integration:

```bash
kubectl apply -k examples/kagent
```

Confirm kagent accepted the MCP server and discovered tools:

```bash
kubectl -n kagent get remotemcpserver kube-insight -o yaml
```

Expected discovered tools:

- `kube_insight_health`
- `kube_insight_schema`
- `kube_insight_sql`
- `kube_insight_js`
- `kube_insight_search`
- `kube_insight_history`
- `kube_insight_topology`
- `kube_insight_service_investigation`

If you created `Agent/kube-insight-troubleshooter` through Helm or the manual
examples, confirm the Agent runtime is ready:

```bash
kubectl -n kagent get agent kube-insight-troubleshooter -o yaml
kubectl -n kagent get deploy,pod | grep kube-insight-troubleshooter
```

## 4. Why The Prompt Library Matters

kube-insight's kagent integration is not only a tool list. The validated prompt
library teaches the Agent how to use retained evidence efficiently:

- start current-state, absence, ranking, and historical questions with
  `kube_insight_health`;
- call `kube_insight_schema` before SQL or JS-backed SQL;
- use `kube_insight_js` for code-shaped aggregation, unit normalization,
  time buckets, latest-per-object selection, and compact summaries;
- prefer exact typed tools before broad SQL when possible;
- cite evidence and mention coverage gaps.

The prompt library is a ConfigMap labeled `kagent.dev/prompt-library: "true"`.
Agents include it through `spec.declarative.promptTemplate.dataSources`, so the
same guidance can be reused by lightweight and full-stack AIOps Agents.

## 5. Optional Full-Stack AIOps Agent

The dedicated kagent Agent chart `aiops-fullstack` profile and
`examples/kagent/aiops-fullstack-agent.yaml` both show a broader Agent that
combines:

- kube-insight retained evidence;
- kagent built-in Kubernetes tools;
- Prometheus tools;
- Helm tools;
- shell and mutating tools with approval requirements;
- optional Loki tools.

Before applying it, review the tool list, approval policy, and confirm your
kagent tool server exposes the referenced Kubernetes, Prometheus, Helm, and
shell tools:

```bash
kubectl -n kagent get remotemcpserver kagent-tool-server -o yaml
```

If any tool family is unavailable, remove or edit that `toolNames` block before
applying the full-stack Agent.

Apply the full-stack Agent:

```bash
kubectl apply -f examples/kagent/aiops-fullstack-agent.yaml
```

If you install a Loki or Grafana MCP server, register it with kagent using
`examples/kagent/loki-remote-mcpserver.yaml`, then enable the optional Loki tool
block in the full-stack Agent. The tested lightweight Loki MCP discovers:

- `loki_query`
- `loki_label_names`
- `loki_label_values`

## 6. Use It In kagent

Open the kagent UI:

```bash
kubectl -n kagent port-forward svc/kagent-ui 8080:8080
```

Visit `http://127.0.0.1:8080`, select `kube-insight-troubleshooter`, and ask:

```text
What changed in namespace <namespace> in the last 30 minutes?
```

```text
Why did Service <namespace>/<service> lose ready endpoints after the deploy?
```

```text
Which objects changed after the last GitOps sync in namespace <namespace>?
```

Good answers should include:

- the kube-insight tool path used;
- collector coverage status;
- exact object kind, namespace/name, UID when available;
- timestamps and time window;
- confidence gaps when coverage is incomplete.

For broader AIOps scenarios that combine kube-insight with Prometheus, Loki,
Helm, shell, and live Kubernetes tools, use the
[kagent AIOps Collaboration Cases](../workflows/kagent-aiops-collaboration-cases.md).
Those cases are designed to show where kube-insight adds retained Kubernetes
history that metrics and logs do not provide.

## Troubleshooting

- `RemoteMCPServer` has no discovered tools: check the Service DNS name, port,
  and `GET /api/v1/server/info`.
- Agent is accepted but not ready: inspect the referenced kagent `ModelConfig`
  and provider Secret.
- Agent guesses table names: confirm the Agent includes
  `kube-insight-prompts`, calls `kube_insight_schema` before SQL, and uses the
  active schema's exact table names instead of generic names from another
  backend.
- Agent claims absence without coverage: require `kube_insight_health` first.
- Agent says kube-insight is unhealthy because many streams are `queued`: treat
  `queued` as completed LIST snapshot evidence waiting for a watch slot. It is
  a freshness caveat for those resource types, not a hard collector failure.
  Prioritize `retrying`, `list_error`, `watch_error`, `stale`, `skipped`, and
  `not_started` when judging degraded coverage.
- Responses use only live Kubernetes tools: use the troubleshooting Agent or
  prompt library so retained-history claims stay tied to kube-insight.
- SQLite grows quickly: use chDB or ClickHouse for continuous kagent use.

## Next Steps

- [A2A Integration](a2a-integration.md) explains kube-insight's direct A2A
  surface for whole-investigation delegation.
- [External Agent Skill Tutorial](external-agent-skill.md) explains the generic
  kube-insight skill outside kagent.
- [Agent SQL Cookbook](../workflows/agent-sql-cookbook.md) has schema-first SQL
  patterns.
- [Storage Modes And Performance](../reference/storage-mode-comparison.md)
  compares SQLite, chDB, and ClickHouse tradeoffs.
