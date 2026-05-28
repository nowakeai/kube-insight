---
name: kube-insight
description: Use kube-insight as an external agent skill for Kubernetes current-state, history, topology, retained evidence, cluster discovery, SQL/export, and downstream data processing through MCP, CLI, or HTTP tools.
---

# kube-insight Agent Skill

Use this skill when the user asks what happened in a Kubernetes cluster, why an
object changed, whether something existed earlier, how current state compares
with retained history, or which kube-insight facts/changes/edges/versions prove
a claim.

This skill is for external agents. Keep it evidence-first, tool-efficient, and
portable across MCP, CLI, and HTTP access. Do not rely on built-in agent session
internals.
When working inside the kube-insight repository, do not mine
`internal/agent/*` prompts or tests for behavior rules; those are built-in
harness details. Use this skill, its references, and the active tool schema.

## Core Rules

- Prove claims with kube-insight evidence before answering. This includes
  current state, absence, health, topology, history, totals, and root cause.
- Start current-state, absence, ranking, or historical answers with collector
  coverage. Missing evidence is meaningful only when relevant streams are
  healthy, fresh, and in scope.
- Resolve user-relative time into absolute UTC bounds before tool calls. In the
  final answer, include the user's local time zone if known.
- If the user gives an approximate cluster name such as `gcp2`, first discover
  available clusters from health/coverage, then use the resolved stable
  `cluster_id` in queries. Do not query the literal fragment as a cluster ID.
- Read active schema or tool recipes before writing SQL or export queries. Do not invent table
  names, fields, labels, or fact keys.
- Prefer the shortest terminal evidence path. Stop when the returned bundle,
  SQL rows, exported data, or computed result proves the answer.
- Keep large intermediate data out of the model context. Export bounded rows to
  files or artifact/scratch handles, then process them outside the prompt when
  your agent environment supports Python, Bash, jq, DuckDB, R, notebooks, or
  other stronger data tools.
- Cite stable evidence: cluster display/context plus `cluster_id`, object
  identity, UID, timestamps, row numbers, artifact IDs, or source query labels.
- Be explicit about uncertainty when coverage is stale, missing, queued,
  retrying, errored, truncated, or schema does not expose the requested field.
- Treat kube-insight as complementary to the user's normal operations tools.
  `kubectl`, port-forwarding, cloud CLIs, logs, and service APIs can validate
  live current state or interact with the cluster. Use kube-insight for retained
  history, coverage, change evidence, historical aggregations, and other
  read-only evidence that live `kubectl` alone cannot reconstruct.

## Start Here

1. Capture user intent: object/symptom, cluster fragment, namespace/name,
   time range, and whether the question is about current state, history,
   ranking, topology, or root cause.
2. Call `kube_insight_health` first for current-state, absence, ranking,
   historical, or fuzzy-cluster questions. Use it to identify dataful clusters
   and freshness.
3. Call `kube_insight_schema` before SQL/export unless a typed investigation tool
   already returns the needed proof.
4. Choose the scenario below and run the smallest bounded plan.
5. Answer from the returned evidence. Do not add broad scans or formatting-only
   tool calls after proof already exists.

## Connect To kube-insight

Use the connection surface that the user's environment exposes. Prefer the
long-running kube-insight service over starting short-lived local processes:

1. MCP Streamable HTTP when available: configure the agent's MCP client with
   `type: "streamable-http"` and `url: "http://HOST:8090/mcp"` or the provided
   kube-insight service URL. This is the preferred agent transport for a
   running service.
2. HTTP API when MCP is unavailable: call `GET /healthz`,
   `GET /api/v1/server/info`, `GET /api/v1/health?detail=full&limit=500`,
   `GET /api/v1/schema`, and `POST /api/v1/sql`.
3. `kubectl port-forward` when kube-insight runs inside the target cluster and
   no external Service/Ingress is available. Forward the API and/or MCP port,
   then use the same MCP or HTTP paths above.
4. Stdio MCP only when the agent runtime explicitly requires a local subprocess:
   `kube-insight serve mcp --db kubeinsight.db`.

If `GET /api/v1/server/info` says MCP is disabled, continue through the HTTP API
instead of probing MCP paths. Legacy SSE may exist at `/sse` for older clients,
but new remote-agent setups should use Streamable HTTP `/mcp`.

## Scenario Index

| Scenario | Use When | Minimum Path |
| --- | --- | --- |
| Relative time | "last hour", "half day", "past week" | Convert to UTC with client context, then use bounded tools. |
| Fuzzy cluster | `gcp2`, display name, context alias | Health/coverage discovery, pick matching dataful cluster, then use stable `cluster_id`. |
| Exact Service health | Service readiness/endpoints/impact | `health` + `service_investigation`; stop if bundle proves it. |
| Exact recent change | Known kind/namespace/name changed? | `health` + `schema` + one bounded `changes` rollup. |
| OOM/restart existence | Did OOM/restart happen? | `health` + one bounded Pod search with bundles. |
| OOM/restart ranking | Which Pods/namespaces have most OOM/restarts? | `health` + `schema` + facts aggregate SQL or exported rows. |
| Allocation/config | requests/limits/capacity, not live usage | `health` + `schema` + allocation recipe or raw-doc proof. |
| Complex aggregation | rankings, peaks, deltas, multi-step grouping | `health` + `schema` + bounded SQL/export, then process with the best available external tool. |
| Deep field extraction | fields only in retained docs | `health` + `schema` + scoped raw-doc SQL/export; extract exact JSON paths. |
| Parallel lookup | independent metrics or clusters | Parallel MCP/CLI calls, SQL batches, or external scripts when available. |
| Complex conditions | "changed significantly", "top during period" | Define metric and window, profile coverage, then compute from exported evidence. |
| Node inventory/capacity | node count/type/CPU/memory/lifecycle | Latest non-deleted Node docs plus lifecycle rows; parse quantities in Python/JS/etc. |
| PVC/storage resize | "expanded from how much to how much" | PVC candidate recipe + ordered storage history; collapse transitions in an external processor or JS fallback. |
| Scheduling failures | Pending/Unschedulable Pods | Pod coverage + `PodScheduled` facts + retained Pod status messages; separate transient Pending from scheduling failures. |
| Ready transitions | Ready became False | Retained `status.conditions` rows plus window `lag`, not change rows alone. |
| Endpoint readiness | Services without ready endpoints | Latest Service and EndpointSlice snapshots; treat ExternalName separately and optionally validate current state with `kubectl`. |
| Pod churn | frequent Pod create/delete | Pod lifecycle rows + ownerReferences; join Pod->Job->CronJob and compute lifetimes externally. |
| Topology | relationships/impact path | Discover root candidate, then one topology call around the root. |
| Zero result | no rows/matches | Check coverage and schema before treating absence as meaningful. |

For copyable plans and SQL/export shapes, read:

- `references/external-agent-recipes.md` for task-level workflows.
- `references/query-patterns.md` for ClickHouse-compatible SQL, export, and
  downstream aggregation patterns.

## Tool Guidance

- `kube_insight_health`: coverage, freshness, collector errors, cluster
  discovery. Use before interpreting absence or rankings.
- `kube_insight_schema`: active tables, columns, examples, and named recipes.
  Use before SQL/export.
- `kube_insight_search`: bounded symptom or fact search, especially
  OOM/restart existence with bundles.
- `kube_insight_sql`: one focused proof query when SQL is enough.
- `kube_insight_js`: kube-insight's built-in JavaScript interpreter. Use it when
  the external agent cannot run richer local tools, or when MCP-only operation
  needs bounded SQL plus compact in-tool aggregation.
- `kube_insight_service_investigation`: exact Service/EndpointSlice/Pod/Event
  health bundle.
- `kube_insight_history`: exact object version/diff proof after the object is
  identified.
- `kube_insight_topology`: relationships around one chosen root, not every
  candidate.

## HTTP Fallback

If MCP is not exposed but the kube-insight API server is reachable, use the
stable HTTP endpoints directly before probing random paths:

- `GET /healthz`: process liveness.
- `GET /api/v1/server/info`: enabled components and storage backend. If
  `components.mcp.enabled` is false, continue with HTTP instead of probing MCP
  routes.
- `GET /api/v1/health?detail=full&limit=500`: coverage, freshness, and cluster
  display/source data.
- `GET /api/v1/schema`: active tables and query recipes.
- `POST /api/v1/sql` with JSON `{"sql":"select ...","maxRows":1000}`:
  read-only SQL/export.

The API may also expose Web UI or agent endpoints, but external-skill evidence
work should prefer the health/schema/sql surface above.
If direct ClickHouse HTTP is documented but returns `401 Unauthorized`, do not
hunt through repository config for credentials. Use kube-insight's
`POST /api/v1/sql` endpoint as the portable read-only SQL surface unless the
user explicitly provides ClickHouse credentials.

If kube-insight is deployed inside the target cluster, it is reasonable to use
`kubectl port-forward` or equivalent cluster access to reach the API. Keep the
final answer clear about which claims came from kube-insight retained evidence
and which were live validations from `kubectl` or another current-state tool.

## Export And Data Processing

For complex aggregation, treat kube-insight primarily as the evidence and query
source. Use it to discover coverage/schema, run bounded SQL, and export rows.
Then use the strongest available agent environment for computation: Python,
Bash, jq, DuckDB, R, notebooks, or other data tooling. This is preferred over
forcing complex processing into the model context.

When the external agent has a local filesystem, write large intermediate exports,
scripts, or scratch summaries to temporary files such as `/tmp`. Keep bulky row
sets out of the model context; bring back compact summaries, checks, and cited
evidence.

Use `kube_insight_js` only as the built-in fallback when the agent is MCP-only,
cannot write/read local files, or benefits from scratch handles inside the
kube-insight tool surface. The same evidence rules apply either way.

Inside JS:

- Use synchronous helpers: `sql(query, maxRows)` and
  `sqlAll([{name, sql, maxRows}, ...])`. Do not use `await`.
- Do not name a variable `sql`; it shadows the helper. Use `query`, `sqlText`,
  `capacitySQL`, or similar.
- Use `sqlAll` for independent reads. Use separate staged JS calls when it
  improves planning or keeps intermediate data bounded.
- Fetch Kubernetes CPU, memory, and storage quantities as strings and parse
  units such as `m`, `Ki`, `Mi`, `Gi`, and `Ti` in JavaScript.
- Return answer-ready compact JSON: totals, sorted top rows, selected proof
  rows, row counts, truncation flags, and scratch handles when used.
- Do not return broad raw arrays unless the user explicitly asks for raw data.
- If using scratch, treat it as temporary working state. Final answers still
  cite original source rows or artifacts, not scratch alone.

With external tools:

- Export only bounded proof/candidate rows, not whole tables.
- Prefer local files for large intermediate data and keep only summaries,
  hashes, row counts, and top/proof rows in the conversation.
- Parse Kubernetes quantities outside SQL. Python, JS, awk, jq, or DuckDB are
  all acceptable if they preserve evidence row identity.
- Keep final claims tied back to kube-insight source rows, artifact IDs, SQL
  labels, object IDs, UIDs, and timestamps.

For SQL:

- Use absolute UTC timestamps.
- Keep `cluster_id` in rows unless exactly one cluster has been resolved.
- Prefer latest non-deleted snapshots for current inventory/capacity.
- Prefer facts/changes/edges for candidate discovery and versions/observations
  for proof or deep JSON fields.
- Avoid SQL-side string arithmetic for Kubernetes quantities; parse outside SQL.

## Common Pitfalls

- Do not infer CPU/memory from node names, nodepool names, or instance type
  strings when Node docs contain `status.capacity`/`status.allocatable`.
- Do not count current objects from all observations in a time window. Collapse
  to latest non-deleted state.
- Do not compute peak or point-in-time object counts from raw observation
  distinct counts. Reconstruct state: `ADDED` and `MODIFIED` mean the object
  exists after that timestamp, while `DELETED` means it does not.
- For Pod-count peaks, use the schema recipe
  `pod_count_peak_intervals_for_js` first. Prefer per-UID intervals plus one
  event sweep over raw observation exports. Copy the final `interval_rows`
  output shape instead of separately querying baseline and window events; never
  treat `1970-01-01` sentinel values as real delete events. Do not raise
  built-in JS `maxRows` above 10000 or `maxQueries` above 10; split by
  cluster/time if interval rows truncate. A raw Pod observation export that
  hits its row cap is incomplete evidence for a peak.
- Do not answer "no data" as "no issue" until coverage proves the relevant
  stream was collected.
- Do not silently pick the first cluster from health output. For ambiguous
  prompts, resolve or report per-cluster results.
- Do not pivot from allocation/config questions to live usage unless the user
  asked for usage.
- Do not expand exact recent-change questions into root cause, topology, OOM,
  or unrelated searches unless requested.
- Do not run another tool call only to reformat rows already returned.
- Do not treat every Pending Pod as a scheduling failure. Check
  `PodScheduled=False` and `reason=Unschedulable`; a Pending Pod with
  `PodScheduled=True` may just be initializing or pulling images.
- Do not use `changes.old_scalar` alone to prove condition transitions if the
  old value is absent. Use ordered retained observations and compare consecutive
  condition values.

## Answer Requirements

Every answer should include only what the evidence supports:

- time window, with UTC and local time when available;
- resolved cluster display/context plus stable `cluster_id` when applicable;
- key findings in user-facing units such as cores, MiB/GiB/TiB, counts, and
  timestamps;
- evidence references or concise proof rows;
- coverage caveats and uncertainty when relevant;
- focused next steps only when the evidence cannot prove the requested claim.
