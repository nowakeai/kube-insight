---
name: kube-insight
description: Use kube-insight to investigate Kubernetes history, collector coverage, retained facts, topology edges, and object versions through CLI, HTTP API, or MCP tools.
---

# kube-insight Agent Skill

Use this skill when the user asks what happened in a Kubernetes cluster, why an
object changed, whether an Event or relationship existed earlier, how current
kubectl output compares with retained historical evidence, or which retained
facts/changes/topology prove a cluster state claim.

The goal is not to use every tool. The goal is to make the smallest evidence plan
that can prove the answer, then stop.

## Non-Negotiable Rules

- Evidence first. Do not present Kubernetes state, absence, health, topology,
  change history, or root cause as fact until kube-insight data supports it.
- Start current-state answers with collector coverage. Missing evidence is only
  meaningful when the relevant resource streams are healthy, fresh, and in scope.
- Be explicit about uncertainty. If coverage is stale, missing, queued,
  retrying, or errored, say what cannot be proven.
- Do not invent table names, object names, labels, fields, timestamps, or
  relationships. Read the active schema before SQL.
- Prefer compact evidence and stable identifiers. Cite cluster display/context
  names when available, but include stable `cluster_id`, object identity, UID,
  SQL row numbers, fact/change/edge/version IDs, or artifact IDs when they
  support a claim.
- Ask the user only when namespace, object identity, cluster, or time window
  cannot be inferred safely from tool results.

## Tool Strategy

Use the path that matches the question:

| User intent | Preferred path | Stop when |
| --- | --- | --- |
| Exact Service health, endpoints, readiness, or impact | `kube_insight_health` + `kube_insight_service_investigation` | The bundle contains the Service plus related EndpointSlice/Pod/Event evidence. Do not add search/schema/SQL unless evidence is missing. |
| Exact recent changes for known kind/namespace/name | `kube_insight_health` + `kube_insight_schema` + one bounded `kube_insight_sql` rollup over `changes` | The changes query returns rows for the requested window. Answer only observed changes and coverage unless the user asks for impact/root cause. |
| Broad symptom/status discovery | `kube_insight_health` + targeted `kube_insight_search`, or `kube_insight_schema` + one aggregate facts/changes SQL when ranking/counting is needed | Search returns enough candidate proof, or aggregate SQL returns counts/timestamps. |
| OOM/restart existence | `kube_insight_health` + exactly one `kube_insight_search` for Pod `OOMKilled`/restart evidence, bounded filters, and `includeBundles=true` | Bundles show enough facts/history proof. Do not parallelize OOM synonyms such as Evicted, memory, CrashLoopBackOff, or restart searches unless the user explicitly asks about those separate symptoms. |
| OOM/restart ranking or top-N | `kube_insight_health` + `kube_insight_schema` + one facts aggregate SQL | Aggregate confirms counts and first/last seen. Do not call history for every row. |
| Allocation/configuration such as requests/limits | `kube_insight_health` + `kube_insight_schema`, then schema recipe `container_resource_allocation_rollup` or one scoped proof query | SQL returns request/limit rows or snippets. Do not pivot to live usage, node capacity, OOM, or root cause unless asked. |
| Namespace or object topology | `kube_insight_health` + candidate discovery through search or schema-guided SQL, then one `kube_insight_topology` around the best root | One graph explains the relevant relationship set. Do not call topology for every node. |
| Raw object history or diffs | Discover the exact object first, then `kube_insight_history` with low limits and `includeDocs=false` by default | Returned versions/changes prove the claim. Fetch raw docs only when needed. |

## MCP Tools

Prefer MCP tools when connected to a running kube-insight MCP server. MCP follows
the configured `storage.driver` for SQLite, ClickHouse, and chDB-enabled builds.

- `kube_insight_health`: collector coverage, stale streams, errors, skipped
  resources, and cluster display context. Call before current-state or absence
  claims.
- `kube_insight_schema`: active backend, SQL dialect notes, tables, columns,
  indexes, joins, and query recipes. Call before SQL unless a current schema is
  already in this run context.
- `kube_insight_sql`: read-only SQL for precise discovery, ranking,
  aggregation, and proof rows that typed tools cannot already provide.
- `kube_insight_search`: candidate discovery from symptoms, names, labels,
  statuses, facts, changes, events, and retained evidence. Use after health for
  broad discovery; do not use it just to rediscover an exact target.
- `kube_insight_history`: retained versions, observations, and optional diffs
  for one known object. Use after exact identity is known.
- `kube_insight_topology`: retained graph around one known object. Use after
  candidate discovery when relationships matter.
- `kube_insight_service_investigation`: compact Service bundle for an exact
  Service namespace/name. For exact Service health/endpoint/impact questions,
  this plus health is terminal evidence when complete.

Useful MCP prompts:

- `kube_insight_coverage_first`
- `kube_insight_event_history`
- `kube_insight_object_history`

## Backend Detection

Agents should not guess SQL shape from memory. Detect it at runtime:

- MCP route: call `kube_insight_schema` before `kube_insight_sql`.
- CLI route: run `./bin/kube-insight query schema ...` before `query sql`.
- SQLite schema notes say `Active SQL backend: SQLite` and expose tables such as
  `object_facts`, `object_edges`, `object_changes`, `object_observations`, and
  `latest_index`.
- ClickHouse/chDB schema notes say `Active SQL backend: ClickHouse-compatible`
  and expose tables such as `facts`, `edges`, `changes`, `observations`,
  `versions`, `api_resources`, and `ingestion_offsets`.
- Use SQLite examples only when the schema exposes SQLite tables. Use
  ClickHouse-compatible examples only when the schema exposes ClickHouse tables.

## Cluster And Time Scope

- For cluster-wide questions, do not add a `cluster_id` predicate unless the
  user scoped the cluster or a prior tool identified the exact cluster. Instead
  select or group by `cluster_id`.
- When the user gives a natural-language cluster name such as "gcp cluster 2",
  do not normalize it into a guessed ID. First call health without a cluster
  filter, or use schema/health cluster display maps to match the exact display
  name and stable `cluster_id`.
- For relative time words such as "recent", "today", "yesterday", or "last 24
  hours", compute absolute UTC `from`/`to` bounds from the client context before
  calling search/history/service tools or SQL.
- SQL arguments stay in UTC. Final user-facing timestamps should use the
  browser/client time zone when known, with the IANA zone or numeric offset; add
  UTC as secondary context only when useful.
- For ClickHouse `toDateTime64` literals, use `YYYY-MM-DD HH:MM:SS` UTC strings,
  not RFC3339 strings with `T` or `Z`.

## SQL Discipline

- SQL must be read-only: `SELECT`, `WITH`, or `EXPLAIN` only.
- Keep `maxRows`/`LIMIT` bounded. Select only columns needed for the claim.
- Match indexed/sorted columns before text scans. For ClickHouse facts, prefer
  exact `fact_key`, `fact_value`, `kind`, and timestamp predicates. For
  OOMKilled, use `fact_value = 'OOMKilled'`, not `LIKE` or document text search.
- For ClickHouse changes, prefer `change_family`, `path`, `kind`, name,
  namespace, and timestamp predicates before searching old/new scalar text.
- For observations and versions, narrow by `kind`, `namespace`, `name`, `uid`,
  `cluster_id` when scoped, and time before opening `doc` content.
- Avoid `ILIKE`, `LIKE`, `positionCaseInsensitive`, and broad JSON/blob scans
  until candidates are narrowed by indexed facts, changes, edges, identity, and
  time.
- For unknown semantic fields, run one bounded profile query over real keys,
  kinds, observation types, or recent timestamp ranges. Do not guess field names.
- If SQL fails, read the error, correct the dialect/table/column issue, and retry
  once when actionable. A failed tool call is diagnostic feedback, not a reason
  to fabricate an answer.

## Stop Rules And Tool Budget

- Stop once enough evidence exists. A complete typed result or bounded aggregate
  SQL result is enough; do not chase optional corroboration.
- Do not repeat the same tool with equivalent arguments.
- After two related zero-result probes for the same intent, pivot to schema
  profiling, narrower coverage checks, or a partial answer that states the data
  gap.
- Do not run wildcard or fuzzy search variants after an exact kind/namespace/name
  target is known.
- For broad OOM/restart existence prompts, use exactly one Pod
  `OOMKilled`/restart search with bundles after health. If it returns zero rows
  and collector coverage is incomplete, answer with that data gap; if coverage
  is healthy but aggregation is needed, pivot to one schema-guided facts
  aggregate instead of trying synonym searches.
- For follow-up prompts that only narrow the prior OOM/restart question by time
  or scope, inherit the prior symptom. One bounded Pod `OOMKilled`/restart
  search is terminal whether it returns matches or zero; answer from that result
  plus current/prior health coverage instead of switching to schema or SQL
  unless the user asks for ranking, counts, or raw proof.
- Do not call history/topology for related objects unless the user asked for
  impact, relationships, raw proof, or root cause and the first result is
  insufficient.

## Data Transform And Condensing

Some agent environments have helper tools equivalent to the built-in
`parallel_investigation`, `artifact_transform_js`, or `evidence_condenser`. Use
them only as bounded helpers, not as permission to run broad blind searches.

- Use `parallel_investigation` proactively for broad, multi-branch prompts such
  as incident triage, cluster health overview, namespace triage, or mixed
  symptoms that split cleanly into health/coverage, OOM/restarts, recent
  changes, and topology/impact. Give it 2-4 concrete independent branches with
  absolute time bounds and known cluster/namespace scope. Do not use it for
  exact Service health or exact kind/namespace/name recent-change prompts where
  the narrow terminal path is sufficient.

- Use a transform helper for grouping, filtering, sorting, field extraction, or
  numeric aggregation over literal JSON rows/snippets already returned by
  search, SQL, or artifacts.
- Do not use a transform helper to fetch new Kubernetes data, access files,
  access network, run shell commands, or run unbounded loops.
- One successful transform over relevant rows is terminal: answer from its JSON
  result instead of running repeated SQL or a second transform.
- Use a condenser only when useful evidence is too large or repetitive. Pass
  source artifact IDs/titles and concrete row or snippet excerpts, not only your
  own prose summary.

## CLI Fallback

When MCP is not available, use the CLI:

```bash
./bin/kube-insight db resources health --db kubeinsight.db --errors-only
./bin/kube-insight query schema --db kubeinsight.db
./bin/kube-insight query sql --db kubeinsight.db --max-rows 50 --sql 'select id, name, source from clusters order by id'
./bin/kube-insight query history --db kubeinsight.db --kind Pod --namespace default --name example --max-versions 10 --max-observations 50
```

## Query Patterns

For ClickHouse-compatible backends, prefer the schema recipes returned by
`kube_insight_schema`. These patterns show the intended shape; adapt table and
column names only after reading the active schema.

### Coverage

```sql
with latest as (
  select cluster_id, api_group, api_version, resource, kind,
         argMax(status, updated_at) as status,
         argMax(error, updated_at) as error,
         max(updated_at) as latest_update
  from ingestion_offsets
  group by cluster_id, api_group, api_version, resource, kind
)
select cluster_id, api_group, api_version, resource, kind, status, error, latest_update
from latest
where status in ('not_started','queued','retrying','list_error','watch_error')
order by latest_update desc
limit 50;
```

### Recent Fact Rollup

```sql
select cluster_id, kind, namespace, name, fact_key, fact_value,
       count() as rows,
       min(ts) as first_seen,
       max(ts) as last_seen
from facts
where ts >= toDateTime64('2026-05-25 00:00:00', 3, 'UTC')
  and kind = 'Pod'
  and fact_key = 'pod_status.last_reason'
  and fact_value = 'OOMKilled'
group by cluster_id, kind, namespace, name, fact_key, fact_value
order by rows desc, last_seen desc
limit 50;
```

### Exact Recent Changes

```sql
select cluster_id, kind, namespace, name, change_family, path, severity,
       count() as changes,
       min(ts) as first_seen,
       max(ts) as last_seen,
       any(old_scalar) as sample_old,
       any(new_scalar) as sample_new
from changes
where ts >= toDateTime64('2026-05-25 00:00:00', 3, 'UTC')
  and kind = 'Deployment'
  and namespace = 'default'
  and name = 'api'
group by cluster_id, kind, namespace, name, change_family, path, severity
order by last_seen desc, changes desc
limit 50;
```

If this answers an exact recent-change question, stop. Do not expand to Pods,
Events, topology, OOM, root cause, or spec-only follow-up unless the user asks.

### Allocation Or Requests/Limits

Use the schema recipe named `container_resource_allocation_rollup` when present.
If facts do not carry resource configuration, use one scoped `observations.doc`
profile or recipe such as `raw_doc_field_profile` before fetching proof rows.
Avoid repeated JSON syntax probing.

## Output Style

For non-trivial answers, use concise Markdown with:

- Findings: confirmed facts, changes, topology edges, or retained versions.
- Evidence: exact proof identifiers such as cluster display name plus
  `cluster_id`, kind/namespace/name, UID, observation timestamp, SQL row number,
  fact/change/edge/version ID, or artifact ID.
- Coverage limits: stale, missing, skipped, queued, retrying, or errored streams
  that weaken the conclusion.
- Next checks: the smallest query or object history request that would reduce
  remaining uncertainty.

Keep citations close to the supported claim. Do not cite unrelated tool output.
