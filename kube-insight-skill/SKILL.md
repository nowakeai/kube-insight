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

## Operating Loop For External Agents

When using kube-insight from a generic agent instead of the built-in agent, keep
the loop simple and deterministic:

1. Keep the kube-insight instruction, tool definitions, and stable schema hints
   before volatile per-run text. Put client clock/time-zone context near the
   current user turn so provider prompt/KV caches can reuse the stable prefix.
2. Preserve the active conversation branch in order: prior user turns, assistant
   tool-call decisions, tool results or compacted tool-result summaries, and
   assistant answers. Short follow-ups such as "what about the last hour" must
   inherit the prior symptom or object target.
3. For a retry, rewind to the retried turn and discard later branch content.
   Do not append the retry as a new unrelated message after the old answer.
4. Plan the smallest terminal evidence path for the user intent. If the path
   returns enough proof, answer immediately instead of looking for optional
   corroboration.
5. Treat large tool results as artifacts. Put only compact evidence summaries,
   artifact IDs, object identities, row snippets, and citation targets back into
   the main transcript.

## Copyable Prompt Starter

Use this short instruction block when an external agent cannot load the full
skill file. Append the available MCP/CLI tool definitions after it.

```text
You investigate Kubernetes state with kube-insight evidence. Do not claim
current state, absence, topology, history, or root cause until a kube-insight
tool result supports it. Start current-state or absence answers with
collector coverage.

Use the smallest terminal path:
- Exact Service health/endpoints: health + service_investigation, then stop.
- Exact recent changes for kind/namespace/name: health + schema + one changes
  rollup SQL, then stop and answer only observed changes.
- OOM/restart existence: health + exactly one Pod OOMKilled/restart search with
  bundles and explicit UTC time bounds, then stop.
- OOM/restart ranking: health + schema + one facts aggregate SQL.
- Allocation/requests/limits: health + schema, then the
  container_resource_allocation_rollup recipe or one scoped proof query.

For follow-ups that only narrow time or scope, inherit the previous symptom or
object target. For retry, rewind to the retried turn and discard later branch
content. Compute relative time from client context into absolute UTC tool
bounds. Keep tool results provider-valid in transcript order. Treat large tool
outputs as artifacts and replay compact summaries with artifact IDs, object
identities, row snippets, and citation targets.

Stop once the typed bundle or bounded SQL result proves the answer. Do not
parallelize synonym searches, SQL-reconfirm returned facts, expand exact changes
into root cause/topology/OOM, or run wildcard scans after scoped zero results
without first checking coverage and schema-supported facts.
```

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

## Context Hygiene

- Do not rebuild follow-up context by selecting only "important-looking" past
  messages. Preserve the model-visible sequence that led to the previous answer,
  or a faithful compact form of large tool results with artifact IDs and exact
  source references.
- Keep summaries evidence-bound. A summary that says "there was an OOM" is not
  enough; include the object identity, fact/change key, timestamp, row number, or
  artifact ID that lets the next turn cite the same proof.
- Do not let hidden summaries replace the active transcript. They can reduce
  tool-output size, but prior user intent and assistant conclusions still need to
  remain visible to the next model call.
- For relative-time prompts, include a client context block with `sentAt`,
  `localTime` when known, `timeZone`, UTC offset, locale, and page/session
  context. Compute exact UTC bounds before calling tools.
- Keep tool calls provider-valid: every tool result in replayed context must
  follow the assistant tool call that produced it.

## Tool Strategy

Use the path that matches the question:

- Exact Service health/endpoints/readiness/impact:
  `kube_insight_health` + `kube_insight_service_investigation`. Stop when the
  bundle contains Service plus related EndpointSlice/Pod/Event evidence.
- Exact recent changes for known kind/namespace/name:
  `kube_insight_health` + `kube_insight_schema` + one bounded
  `kube_insight_sql` rollup over `changes`. Stop when the query returns rows;
  answer only observed changes and coverage unless the user asks for impact or
  root cause.
- Broad symptom/status discovery:
  `kube_insight_health` + targeted `kube_insight_search`, or
  `kube_insight_schema` + one aggregate facts/changes SQL when ranking/counting
  is needed.
- OOM/restart existence:
  `kube_insight_health` + exactly one `kube_insight_search` for Pod
  `OOMKilled`/restart evidence with bounded filters and `includeBundles=true`.
  Stop after that search; do not parallelize OOM synonyms unless the user asks.
- OOM/restart ranking or top-N:
  `kube_insight_health` + `kube_insight_schema` + one facts aggregate SQL.
  Stop when the aggregate confirms counts and first/last seen.
- Allocation/configuration such as requests/limits:
  `kube_insight_health` + `kube_insight_schema`, then the
  `container_resource_allocation_rollup` recipe or one scoped proof query. Stop
  when SQL returns request/limit rows or snippets.
- JavaScript interpreter:
  after `kube_insight_schema`, use `kube_insight_js` when one small script can
  run profile -> proof SQL, several independent aggregates, or SQL rows plus
  JavaScript grouping. Also use it for latest-per-object selection, JSON field
  extraction, Kubernetes CPU/memory unit normalization, and other code-shaped
  aggregation that SQL is easy to get subtly wrong. The same interpreter can
  transform bounded JSON passed as `input`, so do not split the work into a
  separate transform tool. Keep query count and `maxRows` bounded. Return
  answer-ready JSON from the script, with grouping, totals, sorting, and unit
  normalization already done. ClickHouse integer columns may arrive in JS as
  strings; use `Number(value)` or `ki.sumBy(rows, key)` for totals instead of
  raw `+` addition.
- Node inventory/capacity/allocatable totals:
  `kube_insight_health` + `kube_insight_schema`, then one bounded
  `kube_insight_js` as the first data query. Use
  `current_node_capacity_snapshot`; add `recent_node_lifecycle` in the same
  script when the user asks whether nodes changed in a time window. Current node
  count and capacity must come from the latest non-deleted Node snapshot per
  cluster/name/UID, not from counting or summing all fact rows in the time
  window. Instance type usually comes from Node labels in the latest doc; CPU
  and memory come from `status.capacity/status.allocatable`, not `spec`. If older
  data lacks these facts, run one scoped raw-doc proof query against Node
  `observations` or `versions`; do not infer CPU or memory from node names. For
  recent ADDED/DELETED lifecycle rows, enrich ADDED events from the same Node's
  following observations in a short window because initial ADDED documents can
  arrive before labels such as `node.kubernetes.io/instance-type` are complete.
  Compute `added_count`, `deleted_count`, and `net_delta`; do not claim the node
  count stayed stable unless added and deleted counts match or you have separate
  before/after snapshots that prove it.
- Namespace or object topology:
  `kube_insight_health` + candidate discovery, then one
  `kube_insight_topology` around the best root. Do not call topology for every
  node.
- Raw object history or diffs:
  discover the exact object first, then `kube_insight_history` with low limits
  and `includeDocs=false` by default. Ask for diffs when comparing retained
  versions, and answer from the returned version/change/diff fields when they
  already identify the changed path and value. Fetch raw docs only when needed.

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
  or a short fragment such as `gcp2`, proactively list available clusters before
  scoped analysis. First call health without a cluster filter and use the
  returned cluster display map to match the exact display/source/context name
  and stable `cluster_id`. Do not pass the fragment as the first health cluster
  argument.
- MCP typed tools may accept a known cluster display/context/source alias after
  discovery, but SQL still needs the stable `cluster_id` from
  health/schema/evidence rows. Do not put the display name or abbreviation into a
  `cluster_id = ...` SQL predicate unless the schema rows prove that it is the
  stored ID. If an exact short alias exists but has no objects while a fuzzy
  display/source match has data, prefer the dataful fuzzy match and state the
  stable ID used.
- For relative time words such as "recent", "today", "yesterday", "last 24
  hours", "half day", or `半天`, compute absolute UTC `from`/`to` bounds from
  the client context before calling search/history/service tools or SQL. Treat
  "half day" and `半天` as the last 12 hours unless the user clearly means a
  calendar half-day.
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

Common over-investigation patterns to avoid:

- Running search, schema, SQL, history, and topology for a question that a typed
  bundle already answered.
- Searching several synonyms for the same symptom in parallel before looking at
  the first exact result.
- Calling SQL to reconfirm a fact or change that search/history/service tools
  already returned with proof IDs.
- Expanding an exact recent-change question into Pods, Events, topology, OOM, or
  root cause when the user only asked what changed.
- Turning a zero-result scoped search into broad wildcard scans without first
  checking collector coverage and schema-supported facts.
- Passing only the main agent's prose into a condenser or subagent. Always pass
  concrete source artifacts, IDs, rows, snippets, and the specific question.

## Data Transform And Condensing

Some agent environments have helper tools equivalent to the built-in
`parallel_investigation`, `kube_insight_js`, or `evidence_condenser`. Use them
only as bounded helpers, not as permission to run broad blind searches.

- Use `parallel_investigation` proactively for broad, multi-branch prompts such
  as incident triage, cluster health overview, namespace triage, or mixed
  symptoms that split cleanly into health/coverage, OOM/restarts, recent
  changes, and topology/impact. Give it 2-4 concrete independent branches with
  absolute time bounds and known cluster/namespace scope. Do not use it for
  exact Service health, exact kind/namespace/name recent-change prompts, or Node
  inventory/capacity prompts where one health + schema + JS interpreter plan is
  sufficient.

- Use the JS interpreter after schema when dependent SQL would otherwise take
  several turns. Good examples: profile real keys, query proof rows from the
  discovered keys, and return a grouped summary; run several independent
  facts/changes aggregates in parallel; or transform already-returned JSON rows
  by passing them as `input`. Keep scripts small and query limits explicit.
- Do not use the JS interpreter to access files, access network, run shell
  commands, or run unbounded loops.
- One successful JS interpreter result over relevant rows is terminal: answer
  from its JSON result instead of running repeated SQL or another transform.
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

## Detailed References

Load these only when needed:

- `references/external-agent-recipes.md`: copyable MCP and CLI plans for OOM
  existence, exact recent changes, allocation/requests/limits, and exact Service
  health.
- `references/query-patterns.md`: ClickHouse-compatible SQL shapes for coverage,
  fact rollups, exact changes, and allocation/profile fallbacks.

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
