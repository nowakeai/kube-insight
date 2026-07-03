# kagent Integration Guidance

Use this reference when kube-insight is exposed to kagent through a
`RemoteMCPServer`.

kagent is an orchestrator and runtime for cloud-native agents. kube-insight
should act as the retained-evidence tool server in that workflow. Let kagent
coordinate the user conversation, live tools, and other knowledge sources; use
kube-insight for coverage, historical state, changes, topology, and read-only
proof.

## Runtime Shape

Recommended deployment:

- kube-insight runs in Kubernetes from the Helm chart.
- The Helm chart defaults to embedded chDB storage on a PVC, with retained
  evidence kept for 180 days, about 6 months.
- Team/shared installs can use external ClickHouse storage with the same
  long-retention default.
- SQLite is only for tests, demos, and temporary local runs.
- kagent references kube-insight with `RemoteMCPServer` and Streamable HTTP.
- kube-insight evidence rules are distributed as a kagent prompt library
  ConfigMap labeled `kagent.dev/prompt-library: "true"` and included by Agents
  through `promptTemplate.dataSources`.
- Keep MCP prompts out of `toolNames`; this kagent version discovers runtime
  tools, while reusable prompt guidance belongs in prompt libraries or skills.

Validated CRD shape:

- `RemoteMCPServer`: `kagent.dev/v1alpha2`
- `Agent`: `kagent.dev/v1alpha2`
- `Agent.spec.declarative.tools[].mcpServer` references the remote server.

## Tool Set

Pin the Agent to the discovered runtime tools:

- `kube_insight_health`
- `kube_insight_schema`
- `kube_insight_sql`
- `kube_insight_js`
- `kube_insight_search`
- `kube_insight_history`
- `kube_insight_topology`
- `kube_insight_service_investigation`

Do not add MCP prompt names such as `kube_insight_coverage_first` to
`toolNames` unless the target kagent version lists them as discovered tools.
Keep those prompt workflows in the Agent system message or skill instructions
instead.

## Prompt Library Strategy

Use a Kubernetes ConfigMap as the kagent-facing prompt library:

- label it with `kagent.dev/prompt-library: "true"`;
- keep each validated kube-insight behavior as a small key, such as
  `retained-evidence.md`, `sql-and-js.md`, `tool-routing.md`, and
  `answer-contract.md`;
- include it from Agents with `spec.declarative.promptTemplate.dataSources`;
- compose it with `kagent-builtin-prompts` rather than copying kagent's generic
  Kubernetes and safety guidance.

This is preferred over MCP prompt names for kagent because it is visible in the
kagent ecosystem, reusable across Agents, versionable in manifests, and does
not depend on kagent treating MCP prompts as tool-callable runtime objects.

## Agent System Message Rules

Include these rules in the kagent Agent:

1. Start current-state, absence, ranking, and historical questions with
   `kube_insight_health`.
2. If the user gives a cluster fragment or display name, call health without a
   cluster filter first, then use the resolved stable `cluster_id`.
3. Call `kube_insight_schema` before SQL.
   Use the returned table and column names exactly; do not invent generic
   names such as `facts`, `changes`, `edges`, `observations`, or `versions`
   unless the active schema lists them for this backend.
4. Prefer typed tools before SQL when they are exact:
   `kube_insight_service_investigation` for a known Service,
   `kube_insight_history` for a known object, and `kube_insight_topology` for a
   chosen root object.
5. Use `kube_insight_js` after schema for code-shaped analysis such as dependent
   SQL queries, latest-per-object selection, JSON field extraction, Kubernetes
   unit normalization, time bucketing, top-N ranking, start/end snapshot
   comparison, and compact answer-ready summaries.
6. Keep SQL bounded and use absolute UTC timestamps.
7. Do not treat schema or SQL rows alone as proof of absence. Absence needs
   healthy relevant collector coverage.
8. Cite returned evidence: cluster ID/display source, object kind,
   namespace/name, UID when available, timestamps, and row or artifact labels.
9. Use live Kubernetes tools only for current validation or actions. Keep
   root-cause and historical claims tied to kube-insight retained evidence.

## Copyable Prompt Patterns

### Recent Changes

User prompt:

```text
What changed in namespace <namespace> in the last 30 minutes?
```

Expected kagent flow:

1. Convert the time range to absolute UTC bounds.
2. Call `kube_insight_health` to confirm coverage for relevant resources.
3. Call `kube_insight_schema`.
4. Run one bounded SQL query over change/fact tables for the namespace and
   time range.
5. Return top changed objects, timestamps, and evidence rows. Say when coverage
   was stale or incomplete.

### Service Impact

User prompt:

```text
Why did Service <namespace>/<service> lose ready endpoints after the deploy?
```

Expected kagent flow:

1. Call `kube_insight_health` for Service, EndpointSlice, Pod, Node, and Event
   coverage.
2. Call `kube_insight_service_investigation` with namespace and Service name.
3. Use the bundle's EndpointSlice, Pod, Event, change, and topology evidence.
4. Call history or SQL only if the bundle does not identify the relevant
   rollout/config change.
5. Answer with exact object identities and timestamps. Distinguish retained
   evidence from any live `kubectl` validation.

### GitOps Incident

User prompt:

```text
Which objects changed after the last GitOps sync in namespace <namespace>?
```

Expected kagent flow:

1. Call `kube_insight_health` for GitOps CRDs and workload resources.
2. Call `kube_insight_schema`.
3. Query GitOps sync objects and workload changes in a bounded time window.
4. Use history for final candidate objects only when raw proof or diffs are
   needed.
5. Return changed objects grouped by owner/controller when evidence supports
   it.

## Operational Notes

- Put `RemoteMCPServer` in the same namespace as the kagent Agent unless cross
  namespace references are intentionally configured.
- The `RemoteMCPServer.spec.url` can point at a kube-insight Service in another
  namespace.
- Use `allowedHeaders` only when a trusted auth propagation design exists.
- If kagent discovers no kube-insight tools, first verify the kube-insight
  Service URL, then check `GET /api/v1/server/info` through port-forward or a
  debug client.
- kagent-created Agents use kagent `ModelConfig` for their LLM provider.
  kube-insight's built-in Web UI/A2A agent is configured separately with
  `server.chat`; do not assume kube-insight reads kagent `ModelConfig` unless a
  future adapter explicitly documents that behavior.
- Do not store private cluster hostnames, staging details, provider keys, or
  internal domains in manifests committed to the repository.
