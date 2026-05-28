# Generic Agent Optimization Plan

This plan keeps kube-insight agent-first and generic. It avoids adding
case-specific storage tables or one-off business tools for individual symptoms.
The goal is to help different LLMs investigate faster by improving orientation,
query discipline, bounded local data handling, and regression tests.

## Problem Statement

The failed run `sess_1a5d85fa88641c24/run_31090549c3e16b06` was a generic
agent failure, not a resource-allocation storage failure. The user clarified
that they wanted allocated resources rather than live usage. The agent spent
many calls guessing fact keys, observation types, and search variants, then hit
the iteration limit after finally finding useful document snippets.

The failure mode to prevent is:

1. guessing field names or semantic keys before profiling the real data,
2. repeating equivalent zero-result probes,
3. opening raw documents too late or with unbounded scans,
4. exhausting the iteration budget before summarizing evidence already found.

## Design Principles

- Keep storage generic: observations, versions, facts, changes, edges, schema,
  health, and artifacts remain the core evidence model.
- Prefer prompt/tool-contract improvements before adding cached summaries or
  duplicated space-for-time data.
- Give the model truthful orientation data: active backend, table semantics,
  timestamp columns, cluster display names, useful indexes, common key discovery
  patterns, and sample/profiling recipes.
- Let the LLM choose evidence, but make bad search loops cheap to detect and
  stop.
- Use bounded data transformation for returned artifacts. Prefer familiar
  JavaScript, Python, and shell-like surfaces over custom DSLs because LLMs are
  better trained on those native tools, but keep the runtime sandboxed and
  side-effect-free. Do not expose an unconstrained shell.

## Workstreams

Session and context storage are tracked in
`docs/dev/agent-session-context-storage-plan.md`. That plan is authoritative for
stable model-context persistence, context replay, retry branch projection, and
subagent child-run context storage. The target is deterministic context
reproduction and cache-friendly provider requests, not preserving opaque provider
SDK internals for their own sake.

Current status: provider-facing `completion.request` events are now the primary
replay source for follow-up context. This preserves assistant tool-call and tool
result order across streamed runs, compacts oversized historical tool outputs
before replaying them into follow-up requests, and keeps retry replacement
semantics as rewind rather than append. The API live smoke now enforces
tool-order, retry, session-projection, historical tool-size, failed-tool, and
follow-up tool-count budgets.

MCP `kube_insight_health` is a high-frequency orientation tool, so the MCP
server keeps a short in-memory cache for health reports keyed by normalized tool
arguments. The first call registers that argument set; subsequent identical
calls reuse the report within the TTL, and a single MCP-server-owned refresh
loop periodically refreshes registered health reports. Storage backends still
own the authoritative `ResourceHealth` read semantics, including any pending
flush needed before a direct read.

### 1. Schema-Guided Query Planning

Improve the default agent instruction and schema notes so the model does not
guess names such as fact keys, observation types, or materialization values.

Required behavior:

- Call schema before SQL unless current schema is already in context.
- For unknown semantic fields, run one bounded profiling query before exact
  filtering. Examples: distinct fact keys, observed observation types, recent
  kinds, and timestamp ranges.
- Include time bounds for recent/today/last-N prompts.
- Prefer indexed identity/time predicates before raw JSON/document scans.
- If two related probes return zero rows, switch strategy or report the coverage
  gap instead of trying wildcard/fuzzy variants.

### 2. Iteration-Budget Discipline

The agent should treat tool calls as a finite budget.

Required behavior:

- Use progress notes to state a compact plan and the reason for changing plan.
- Stop once a result can answer the user's question with uncertainty caveats.
- When the run has already collected useful evidence and the next step is only
  optional corroboration, answer instead of calling more tools.
- If repeated tool errors or empty results prevent proof, return a partial
  answer that includes exact gaps and the next query that would be needed.

Implementation options:

- Short term: prompt-level budget rules plus regression tests.
- Medium term: runtime guard that detects high tool counts, repeated failed
  tools, or repeated zero-result summaries and injects a visible budget warning.
- Long term: pre-stop synthesis hook before hard max-iteration failure, if the
  Eino runtime exposes a suitable middleware point.

### 3. Generic Artifact Condensing

The existing `evidence_condenser` should become the normal way to turn noisy
artifact rows into readable evidence when a result is too large for direct
answering.

Required behavior:

- The main agent must pass source artifact IDs, titles, row snippets, and the
  user question.
- The condenser must not fetch new data or invent facts.
- The condenser returns concise bullets or a small table with source IDs.
- The main agent still decides which condensed evidence supports final claims.

Current implementation status: historical replay of prior tool results now uses
a deterministic compact payload for oversized tool messages, preserving the
tool-call id, tool name, output summary, artifact id, original size, and a
bounded preview. This is a cheap replay-time compaction, not a replacement for
`evidence_condenser`. Use `evidence_condenser` when the model needs a
higher-quality human-readable synthesis of noisy current-run evidence before
answering.

### 4. Bounded JavaScript Interpreter

Current design: expose one native Eino tool, `kube_insight_js`, backed by goja.
It accepts literal `input` JSON, injects `input`, `inputRows`, small `ki`/`_`
data helpers, safe logging/JSON helpers, and bounded read-only SQL helpers
`sql()` plus `sqlAll()`. Use it after collector coverage and schema when
code-shaped aggregation should perform dependent profile -> proof SQL, several
independent aggregates, SQL rows plus compact grouping, or pure JSON
transformation over current-run evidence. `kube_insight_health` remains required
coverage evidence for current-state, historical lookback, ranking, aggregation,
and absence claims; schema/SQL rows alone do not prove the collector was
healthy. The interpreter has no filesystem, network, process, or environment
access; SQL read-only validation, query count, row count, timeout, and output
limits remain mandatory.

Add a safe transform capability after prompt and condenser improvements settle.
This is not a shell replacement.

Preferred shape:

- Input: artifact IDs or literal JSON rows already returned in this run.
- Operations: select fields, filter, group by, count, sum numeric columns, sort,
  limit, and extract JSON paths.
- Limits: no filesystem, network, process execution, environment access, or
  unbounded loops; hard input/output size and timeout.
- Candidate implementation order: JavaScript first via goja for in-process
  side-effect-free transforms; then evaluate Python and shell-like runners for
  familiar syntax where they can be isolated with equivalent limits. Avoid
  inventing a custom transform DSL unless native-tool sandboxes prove too risky.

`mvdan/sh` is worth evaluating for a later shell-shaped transform layer because it
provides a Go shell parser/interpreter surface with replaceable execution and
I/O hooks. It should not be wired as a general Bash tool. If used, expose only
a tiny allowlisted command set over in-memory artifact data, disable external
process execution, filesystem access, networking, environment expansion, and
unbounded loops, and keep strict time/size limits.

Avoid exposing Bash to the model. Shell access is too broad for the product
surface and does not solve query-planning failures.

### 5. Subagent Use

Current implementation status: the specialist `evidence_condenser` exists
as an `AgentAsTool` subagent, and `parallel_investigation` can fan out one
main-agent tool call into 2-4 bounded specialist investigation subagents that
run concurrently. Full `SetSubAgents` supervisor/deep-agent transfer remains
deferred broader incident-triage work.

Subagents should speed up independent investigation branches, not multiply blind
searches. Eino tool calls can run in parallel within one assistant turn;
`parallel_investigation` is the first explicit parallel-subagent wrapper for
branches such as health/coverage, OOM/restarts, recent changes, and topology or
impact.

Good use:

- schema/profile,
- health/coverage,
- candidate discovery,
- proof condensation.

Bad use:

- several agents independently guessing equivalent SQL/search variants,
- letting subagents open raw documents before candidate narrowing.

### 6. Session Scratch Store

Large intermediate rows should not be replayed through the LLM context just so a
later tool step can reuse them. Add a session-scoped scratch layer that stores
large temporary artifacts by handle and passes only compact metadata back to the
model. The product concept is a scratch store, not necessarily a real
filesystem: it may be backed by a temp directory, a KV store, object storage, or
artifact events. A filesystem-shaped virtual path is still useful because it is
easy for generic agents and skill docs to reason about.

Target semantics:

- Scope: session branch plus run provenance. Retry rewind must see the scratch
  handles that belonged to the replayed branch and must not silently read later
  branch data.
- Lifetime: temporary and server-owned. Retention can delete scratch data after
  the session expires or after no live run can cite it.
- Addressing: stable virtual paths such as `/rows/top.json` inside a session
  scope, plus immutable content hashes and source run ids. Do not expose real
  host paths to the model.
- Context replay: provider requests include path, MIME type, byte count, row
  count, hash, source tool, short summary, and a small preview. They do not
  include the full payload unless the model explicitly reads a bounded slice.
- Evidence binding: final citations should be able to point at the scratch
  handle or the source artifact that produced it, so compact answers remain
  auditable.
- Safety: no process, filesystem, network, or environment access. Enforce max
  files, max bytes per file, max bytes per session, TTL, MIME allowlist,
  read/write timeouts, and explicit provenance.

First implementation slice:

1. Start with a temp-folder backend under
   `$TMP/kube-insight-agent-scratch/{session_id}`. This is intentionally simple
   and easy to replace later.
2. Expose only virtual paths and bounded handle metadata to the model. The temp
   backend is an implementation detail.
3. Reuse the existing artifact/event retention path where possible instead of
   adding a broad new persistence model. Add a small scratch index only if path
   lookup cannot be made efficient from temp handles and run events.
4. Extend `kube_insight_js` with scratch-aware helpers:
   - `scratch.write(path, value, metadata)` returns handle metadata only.
   - `scratch.load(path)` returns the full JSON value inside the JS interpreter for later aggregation.
   - `scratch.read(path, {offset, limit})` returns a metadata envelope plus bounded content or value.
   - `scratch.list(prefix)` returns handle metadata.
5. Prefer immutable writes or versioned updates. Avoid mutable global session
   state that would make retry and provider-context replay non-deterministic.
6. Treat scratch handles as a complement to subagents: subagents can summarize a
   handle, while the main agent keeps only the handle metadata in context.

Current implementation status: `kube_insight_js` exposes `scratch.write`,
`scratch.load`, `scratch.read`, and `scratch.list`. The backing store is a
session-scoped temp folder under
`$TMP/kube-insight-agent-scratch/{session_id}`; the model only sees virtual
paths, hashes, byte counts, previews, and run/session provenance. Agent
retention also owns scratch cleanup: by default it protects queued/running
sessions and deletes scratch directories older than 24 hours. Manual retention
compaction can override that TTL with `scratchMaxAgeSeconds`.
When a JS tool result contains scratch handles, the run recorder promotes those
handles to the top-level `scratchHandles` field of the tool-call artifact. This
keeps the final answer auditable even when the large payload itself stays out of
the model context.

Do not add a second JS tool just for scratch. The goal is one strong JS
interpreter surface with bounded SQL, bounded scratch IO, and answer-ready
results; this does not mean an investigation must finish in one JS call.

### 7. Regression Matrix

Add cases that score tool efficiency and evidence quality, not exact wording.

Required cases:

| Case | Prompt shape | Target behavior |
| --- | --- | --- |
| intent correction follow-up | User says the previous answer addressed usage but they wanted allocation/configuration | profile schema, then one or two scoped proof queries; no blind repeated search |
| unknown semantic field | User asks for a field not in prompt examples | discover available keys/types before exact filtering |
| zero-result pivot | First exact query returns 0 rows | switch to profile or coverage gap after at most two related probes |
| noisy evidence | SQL/search returns large or repetitive rows | call `evidence_condenser` with source artifacts/snippets |
| iteration pressure | useful evidence exists near budget | produce partial answer instead of failing without answer |

HTTP API smoke now covers the session/context-specific regression path that the
synthetic matrix does not: multi-turn OOM follow-up, retry rewind, session
projection after retry, provider message order for tool calls, historical tool
result compaction, failed-tool budget, follow-up tool-call budget, and
provider-facing context-size metrics.

Live model testing should compare MIMO, DeepSeek, and any OpenAI-compatible
provider available in the local `.env` after explicit approval for sending the
selected ClickHouse evidence snippets to that endpoint. The stable CI suite and
ClickHouse case smoke must remain deterministic, offline, and local-only. The server default `server.chat.maxIterations` is
`32` so capable models have room for longer investigations; efficiency is still
scored by tool-call budgets and zero-result pivot behavior rather than by
letting every run consume the full iteration limit.

## Live LLM Evaluation Findings

Real ClickHouse runs against MIMO and DeepSeek showed two generic efficiency and
correctness issues:

- More runtime context is not automatically better. Rich context helped MIMO pick
  the correct cluster id for the allocation question, but DeepSeek rich mode used
  more SQL calls than baseline on allocation and restart questions. Keep rich
  context compact and route-oriented rather than stuffing broad evidence into the
  system prompt.
- Natural-language cluster fragments must be resolved through the health cluster
  display map before SQL. A model may otherwise turn `gcp cluster 2` into a
  guessed `cluster_id` and produce a fast but wrong answer.
- Container requests/limits questions need an explicit generic ClickHouse recipe.
  Without it, models repeatedly probe JSON extraction syntax even though each SQL
  call is fast. The `container_resource_allocation_rollup` recipe is a prompt and
  schema-contract optimization, not a specialized storage table. In MIMO testing,
  this reduced the allocation case from 18-20 SQL calls plus timeout to 4 tool
  calls with a completed answer; remaining latency came mostly from long model
  generation, not SQL execution.

## Model Compatibility Findings

Synthetic live evals are the safe first step for newly added OpenAI-compatible
providers because they validate tool calling without exporting real cluster
evidence. Real ClickHouse snippets were used only with previously approved
endpoints; for newly added endpoints, keep using synthetic fixtures unless an
explicit data-export approval path is available.

After making the fake SQL tool input-aware for changes, allocation, and OOM
queries, adding the JS transform evaluation case, and validating targeted reruns,
the current synthetic results are:

| Provider | Result | Current finding |
| --- | ---: | --- |
| `mimo` | 7/8 full, 2/2 targeted OOM+JS | Strong overall. Broad recent changes used a valid schema+SQL path while the older evaluator expected history. |
| `deepseek` | 2/8 full before targeted fixes, 2/2 targeted OOM+JS | Correct answers but over-investigates broad cases. Use for focused tasks or with stronger runtime budget guardrails. |
| `sub2api` / proxied subscription model | 6/8 full before fixture fix, 2/2 targeted OOM+JS | Good default candidate. Earlier OOM/JS failures were mostly fake SQL fixture issues. |
| `moonshot` / Kimi | 5/8 full before fixture fix, 2/2 targeted OOM+JS | Tool calling works, but latency is high and it tends to verify more than needed. |
| `deepinfra` / Qwen small model | 7/8 full, 2/2 targeted OOM+JS | Fast and good on simple/aggregate/JS cases; one exact recent-change run used history instead of schema+SQL. |

Do not treat max iteration increases as the main fix for smaller or
over-verifying models. Prefer prompt/schema recipes, terminal SQL-result rules,
and runtime budget feedback that nudges the model to answer from existing
evidence. A lightweight runtime budget middleware now injects a model-visible
warning when a run has already used several tools or repeats the same tool,
asking the model to audit whether existing evidence is enough before calling
another tool. It no longer hard-blocks the Nth SQL/search call with a synthetic
tool result; the model remains responsible for deciding whether another
materially different query is justified. This keeps the product generic while
still making over-investigation visible in the transcript and evaluation
metrics.

The API smoke adds an outer regression gate for this behavior: follow-up runs
default to at most three tool calls, failed tool calls default to zero, and the
summary records `initialContext`, `toolCalls`, `failedToolCalls`, and
`toolNames` so prompt/context changes can be compared for cost and rediscovery
regressions.

## ClickHouse Case-Smoke Findings

Local ClickHouse smoke tests should exercise both the fast facts path and the
bounded raw-document fallback. In the current real data, `allocation_profile`
over `facts` mostly exposes generic resource-exhaustion condition facts, while
`allocation_doc_profile` over recent `observations.doc` finds the workload kinds
that actually contain `requests`/`limits` configuration. This is a generic
agent behavior requirement: profile facts first, then pivot to a scoped raw-doc
profile when the fact model does not carry the requested configuration detail.
Do not add allocation-specific storage just to satisfy this case.

## Generalization Reset After Node Inventory Work

The `gcp2` Node inventory/capacity case exposed useful generic gaps, but the
next iteration must avoid turning one successful case into a long tail of
case-specific prompt patches. Treat the Node recipe as one fixture in a broader
agent-evaluation matrix, not as the shape of the whole agent.

Industry guidance points in the same direction:

- Prompt changes should be driven by task-specific evaluations with measurable
  criteria, not by anecdotal prompt edits.
- Tool surfaces should be clear, high-signal, and consolidated enough that the
  model can navigate them without selection ambiguity.
- Stable system/tool/schema instructions should stay early and reusable in the
  request so provider prompt caches can match common prefixes.
- Production traces and failed real sessions should feed back into offline eval
  datasets before new rules are added.

### Scenario Taxonomy

Refactor the built-in prompt, schema recipes, and external skill references
around reusable investigation scenarios. This makes rules easier for the model
to retrieve and prevents unrelated instructions from competing in one flat list.

Initial categories:

| Category | Purpose | Examples |
| --- | --- | --- |
| Relative time | Convert natural time words to absolute UTC query bounds from client context | `半天`, `过去几天`, `过去一周`, follow-up `最近1小时内呢` |
| Fuzzy lookup | Resolve user-facing aliases before scoped queries | `gcp2` -> cluster display/source/context -> stable `cluster_id` |
| Complex aggregation | Use schema plus bounded JS planning for ranking, bucketing, latest-per-object, totals, and deltas | top namespace by Pod resources, largest namespace allocation change, peak Pod count time |
| Deep field extraction | Profile facts first, then extract bounded raw-doc fields when facts do not carry the needed semantics | container requests/limits, Node labels/capacity, nested status fields |
| Stateful capacity delta | Reconstruct before/after values from retained object history, then normalize units | PVC storage resize, "从多少扩到多少" follow-up |
| Parallel discovery | Split independent symptom branches only when the question is broad and branches do not depend on each other | health + schema; OOM/restarts vs recent changes vs topology impact |
| Complex predicates | Build queries from discovered keys and object identities rather than guessing text patterns | exact kind/namespace/name changes, label/condition/status filters |
| Zero-result pivot | After one or two scoped empty probes, profile coverage/schema or answer with a data gap | no OOM rows, missing collector stream, missing fact key |
| Evidence stopping | Stop when a typed bundle, bounded SQL table, or JS answer-ready result proves the claim | avoid optional history/topology/raw-doc follow-ups |

### New Complex Aggregation Cases

Add these cases before the next prompt rewrite. They should run first as local
ClickHouse case-smoke questions against the compose API server, then become
synthetic live-eval fixtures once the desired tool path is understood.

| Case ID | User prompt | Intended generic behavior | Key checks |
| --- | --- | --- | --- |
| `namespace-pod-resource-top` | `帮我看看哪个namespace pod资源占用最高` | Clarify or infer that kube-insight stores allocation/configuration unless live metrics are present; call health + schema, then use bounded JS planning to profile Pod resource fields and group latest non-deleted Pods by namespace. | no broad search loop; schema-guided JS planning; CPU/memory units normalized; cites exact source rows/artifact |
| `namespace-resource-delta` | `帮我看看过去几天哪个namespace资源占用有较大变化` | Compute absolute time bounds, reconstruct comparable namespace-level snapshots or rollups at window start/end, and rank deltas with caveats about sampling/freshness. | uses client time; does not compare raw observation counts; explains whether values are requests/limits/capacity/usage |
| `pod-count-peak` | `帮我看看过去一周pod数量最多的时间点是什么时候` | Bucket retained Pod state over the last week, count latest live Pods per bucket or event-derived state when available, and return the peak bucket plus runner-up/context. | counts object state, not raw observations; bounded bucket count; handles deleted Pods; cites bucket/proof rows |

These prompts intentionally overlap in required skills: fuzzy cluster resolution
may or may not be present, all require relative time or default-time handling,
and all benefit from code-shaped aggregation. Passing them should improve broad
historical analysis, not only the Node inventory question.

Initial baseline on 2026-05-27 against the local ClickHouse-backed compose API
server with `mimo-v2.5-pro`:

| Case | Result | Observed behavior | Classification |
| --- | --- | --- | --- |
| `namespace-pod-resource-top` | Passed | Used `health -> schema -> kube_insight_js`; produced a cited namespace ranking. | Good baseline for complex aggregation plus deep field extraction. |
| `namespace-resource-delta` | Failed checks, run completed | Used `health -> schema` then 11 serial `kube_insight_sql` calls, no JS. It mixed Pod-count deltas, restart increases, and OOM deltas without first fixing the semantic meaning of "resource". | Complex aggregation, stopping, and semantic predicate/metric selection. |
| `pod-count-peak` | Failed checks, run completed | Used JS, but repeated equivalent reconstruction before answering. The answer did include coverage caveats, which is useful behavior to preserve. | Complex aggregation and evidence stopping. |

This baseline confirms that the next change should not be another
Node-specific rule. The generic gap is: when the task needs historical
reconstruction, bucketing, top-N ranking, unit normalization, or metric
disambiguation, the model should use schema-informed JS planning that profiles,
queries, computes, and returns answer-ready JSON without repeated equivalent
work. For ambiguous terms
like "resource usage", the agent should either define the available kube-insight
metric in the answer or ask one clarification when live usage/configuration
cannot be inferred safely.

After adding generic JS-oriented schema recipes and rerunning the aggregation
set, the tool path shifted toward JS but exposed two lower-level harness issues:

- Some models shadow the JS helper by writing `const sql = ...`, then fail when
  calling `sql(sql, 2000)`. The tool contract must explicitly say to use
  `query`/`sqlText` variable names.
- Some models try `await sql(...)` or `sql({sql, maxRows})`, even though
  `sql(query, maxRows)` is synchronous and positional.
- Some models return `JSON.stringify(rawRows, null, 2)` from JS. That defeats
  the condenser goal and can exceed output limits. The contract should require
  compact answer-ready objects: top rows, totals, deltas, caveats, and source
  identities only.
- In the parallel 3-case aggregation rerun, one model set `maxQueries: 2` but
  issued three SQL reads in the script. The fix is a generic tool contract
  clarification: usually omit `maxQueries`; when set, count all `sql()` calls
  and `sqlAll()` specs first. `sqlAll` already executes independent reads
  concurrently in the Go interpreter, so the next prompt work should encourage
	  `sqlAll` rather than adding another JS tool surface.
- A later parallel rerun showed two separate effects: one run failed from a
  provider stream timeout after health/schema, and the Pod-count peak run
  completed but used four JS calls. Treat provider timeout as stress noise in
  parallel live smoke, but keep the repeated-JS behavior as a real generic
  stopping gap: after a JS result contains a complete bucket/ranking answer, the
  model should answer instead of re-running the same reconstruction for optional
  refinements.

After tightening the JS contract around `maxQueries` and bounded
profile/proof/aggregation, targeted reruns against the compose ClickHouse API
passed all three complex aggregation prompts:

| Case | Result | Tool sequence | Report |
| --- | --- | --- | --- |
| `namespace-pod-resource-top` | Passed | `kube_insight_health,kube_insight_schema,kube_insight_js` | archived local smoke report |
| `namespace-resource-delta` | Passed | `kube_insight_health,kube_insight_schema,kube_insight_js` | archived local smoke report |
| `pod-count-peak` | Passed | `kube_insight_health,kube_insight_schema,kube_insight_js` | archived local smoke report |

These are generic JS interpreter contract fixes, not Kubernetes-specific prompt
rules.

Later parallel aggregation observation on 2026-05-27 showed the next generic
failure mode. The model still sometimes wrote `const sql = ...` and then called
`sql(sql, maxRows)`, which shadows the interpreter helper and fails immediately.
Another PVC run returned broad raw `history` rows from JS and hit the output
limit instead of collapsing transitions or writing reusable rows to scratch. The
follow-up fix is a general tool-contract and eval check: never name SQL strings
`sql`, and never return large raw arrays such as `{history: history}` or
`{rows: rows}` from JS. Aggregate, slice proof rows, or return a scratch handle.

After rebuilding the compose watcher with those changes, the next parallel
aggregation run improved to 3/4 passed. The remaining PVC failure still produced
a correct final answer, but had one failed JS call caused by ClickHouse aggregate
alias substitution: `max(updated_at) AS updated_at` next to
`argMax(status, updated_at)` can be rewritten as a nested aggregate. Treat this
as another generic SQL discipline rule: aggregate aliases should not reuse the
same source column name when that source column is also an argument to another
aggregate in the same SELECT.

The single PVC rerun after this change passed with 0 failed tool calls. It still
showed a presentation issue: the final answer printed raw byte integers next to
GiB values, and one byte value was formatted incorrectly. Treat this as a
general final-answer rule for Kubernetes quantities: normalize memory/storage to
MiB/GiB/TiB in user-facing prose and tables, and leave raw byte proof in tool
evidence unless the user explicitly asks for raw units.

After adding the quantity-normalization final-answer rule and rebuilding the
watcher, the PVC single-case rerun passed again with 0 failed tool calls and no
raw byte integers in the answer. The path was `schema -> health -> js`; this is
acceptable because health and schema were gathered before data interpretation.

The next full aggregation rerun passed 3/4 cases. Namespace Pod resource
ranking, Pod-count peak, and PVC resize passed, while namespace resource-delta
failed because the model attempted to normalize Kubernetes quantity strings
inside ClickHouse SQL, for example by dividing `JSONExtractString`/`replaceAll`
string expressions by `1000` or `1024`. The general fix is a harness rule, not a
case patch: SQL should fetch Kubernetes CPU/memory/storage quantities as raw
strings, and `kube_insight_js` should parse `m`, `Ki`, `Mi`, `Gi`, `Ti`, and
related units in JavaScript before grouping or ranking.

After applying that rule to the system prompt, JS tool descriptions, schema
notes, skill docs, and smoke anti-pattern checks, the focused namespace
resource-delta case passed with 0 failed tool calls. A subsequent full
aggregation rerun passed 4/4 cases with 0 failed tool calls. This validates the
general rule across namespace resource ranking, namespace resource changes,
Pod-count peak, and PVC resize. The schema recipe for Node capacity was also
changed to return raw Node capacity/allocatable quantity strings for JS-side
normalization, because schema examples are part of the prompt harness and can
otherwise teach the model brittle SQL-side string arithmetic.

The first post-change fuzzy Node inventory case passed on numeric correctness
and did resolve `gcp2` through the cluster display map, but it still called
schema before health and produced only one citation for a two-surface answer
(current capacity plus recent lifecycle). Treat this as a harness quality gap:
cluster fragments should make health-without-filter the first data tool, and
Node inventory plus lifecycle answers should cite current Node capacity snapshot
evidence separately from lifecycle rows. The smoke harness now has
`KUBE_INSIGHT_AGENT_CASE_MIN_CITATIONS` so these citation expectations can be
checked directly.

The stricter rerun passed tool order and citation checks, but the JS lifecycle
details still contained several `instance_type: unknown` rows. That is a useful
quality signal rather than a schema failure: initial Node lifecycle events may
still lack labels even after a short enrichment window. The harness rule is to
first fill missing lifecycle labels from same-name lifecycle rows with known
labels and current capacity snapshot rows in JavaScript, then run one focused
fallback by affected node names against later/latest Node observations only if
needed. The smoke check now fails Node lifecycle artifacts that still surface
missing/unknown instance types.

The next rerun showed three additional harness refinements: the model computed
relative time inside JS with `Date`, used `evidence_condenser` for an already
compact Node JS result, and cited only the capacity snapshot. Keep relative time
as literal UTC bounds before calling JS; answer directly from compact Node
inventory JS summaries; and emit separate evidence labels for current capacity
and lifecycle changes.

After moving the cluster-fragment rule into the schema tool description and
strengthening the `recent_node_lifecycle` recipe to enrich labels from nearby
same-name observations, the Node inventory case converged on the important
runtime behavior: health first, schema second, JS third; literal UTC bounds;
current capacity from latest non-deleted Node snapshots; lifecycle rows without
unknown instance types; and correct CPU/memory totals. The remaining gap is
citation granularity: current capacity and lifecycle are returned by one JS
artifact, so the UI currently creates one citation even when the answer contains
both claims. Treat that as an artifact/citation partitioning improvement rather
than more prompt tuning.

### PVC Resize Follow-Up Trace

Real session `sess_3f829529533c9ad6` / `run_7a3cb7a6488b8da2` exposed a
stateful capacity-delta gap. The follow-up "从多少扩到多少？" correctly inherited
the prior PVC resize candidates and eventually found six exact before/after PVC
observation rows, but it reached them through serial `kube_insight_sql` calls
with two truncated 50-row history pages. After the exact rows were available,
the assistant only emitted a progress sentence and then called the model again;
that final provider request timed out before headers.

The durable fix is not a session-specific patch. Treat PVC expansion as a
generic stateful capacity-delta scenario:

1. Inherit the target object set from prior answer/tool context.
2. Use schema plus `kube_insight_js` with `pvc_storage_history_for_js` or
   equivalent bounded observations SQL when JS makes the reconstruction clearer.
3. Collapse adjacent equal request/capacity values in JavaScript, convert
   bytes/Ki/Gi quantities to GiB, pair requested/capacity changes into unique
   PVC-level resize records, and return compact before/after deltas.
4. Stop after exact before/after rows are available; do not stream raw history
   pages or re-query only to restate the same values.

Parallel aggregation rerun after adding the first PVC recipe showed the next
generic gaps: one PVC run returned field-level request/capacity events and then
needed a follow-up JS step to merge them, and the namespace resource-delta case still
hit the ClickHouse aggregate-alias-in-WHERE error before correcting itself.
Strengthen the schema/prompt contract around unique object-level delta outputs
and the safe `latest_doc` HAVING/outer SELECT shape.

Targeted reruns after that change showed two remaining harness issues:

- PVC resize can still tempt the model to stream broad candidate pages before
  JS. Candidate discovery should be a focused SQL or JS step, and broad raw
  pages should stay out of the LLM context.
- Historical aggregation answers may omit timezone context even when the client
  context provides it. Final answers for relative-time prompts must state the
  actual query window and display timezone, not just bare dates.
- Namespace resource-delta runs sometimes add a follow-up JS call only to add
  absolute deltas after an initial percent-based result. The schema now exposes
  `namespace_resource_delta_for_js`, and the prompt should ask for start/end
  values, absolute deltas, percent deltas, new namespace classification, and the
  final ranking when available.
- A later resource-delta run pivoted from allocation snapshots to
  changes/status-event volume and then deep-dived key namespaces. That is a
  semantic routing failure: "resource change" should mean Pod requests/limits
  allocation deltas unless the user explicitly asks for churn/change events.
  Sparse start/end snapshots should produce a coverage caveat, not a new
  exploratory JS sequence.
- Another run picked arbitrary one-hour start/end snapshot windows, then
  re-ran coverage and re-ran the real comparison after discovering the first
  window was sparse. Namespace resource deltas should query actual Pod min/max
  coverage and derive usable snapshot windows before comparing snapshots.

### Implementation Plan

1. Freeze new `gcp2`/Node-specific prompt edits unless an eval shows a
   regression in the broader matrix.
2. Add an `agent_aggregation_cases` mode to the ClickHouse case-smoke script or
   a sibling script that can run the three prompts above against the compose API
   server and capture `report.json`, tool sequence, answer text, citations, and
   token/latency metrics.
3. Run baseline model traces without changing the prompt. Classify each failure
   by taxonomy category: time, fuzzy lookup, aggregation, deep extraction,
   parallelism, predicate construction, zero-result pivot, or stopping.
4. Only then move rules into reusable locations:
   - compact top-level decision rules in `internal/agent/contracts.go`,
   - scenario-oriented query recipes in `kube_insight_schema`,
   - external-agent guidance in `kube-insight-skill/SKILL.md`,
   - detailed examples in `kube-insight-skill/references/query-patterns.md`.
5. Prefer schema/tool contracts over prompt prose when a rule is about data
   shape, available fields, latest-object semantics, unit normalization, or
   useful indexes.
6. Keep `kube_insight_js` as the single language-shaped data tool. Improve its
   examples and helper docs before adding any shell-like tool. Do not add a
   shell surface unless repeated evals show JS plus SQL cannot express the task
   safely.
7. Re-run the mixed matrix after each change and compare:
   - correctness of numeric totals/rankings,
   - tool-call count and failed-tool count,
   - whether JS is used for code-shaped aggregation,
   - prompt cached-token ratio where provider usage exposes it,
   - answer citation coverage and evidence-card usefulness.

### Prompt And Skill Refactor Shape

The current instruction block is too flat. The next refactor should move from
"many one-off bullets" to a small router plus scenario modules:

1. Always-on invariants: evidence first, health before absence/current-state
   claims, provider-valid transcript, client-time relative bounds, stable IDs,
   and citation labels.
2. Intent router: exact service, exact change, broad symptom, topology,
   allocation/configuration, historical aggregation, raw proof.
3. Scenario modules: the taxonomy above, each with entry conditions, preferred
   tool path, stopping rule, and common mistakes.
4. Data recipes: backend-discoverable examples returned by schema so the model
   learns current tables/columns from tools instead of stale prompt memory.
5. External skill index: the same scenario names, with short copyable recipes
   for users who run kube-insight from Codex, Claude, or another generic agent.

This keeps the stable prefix cache-friendly while allowing future work to load
or mention the relevant scenario by name instead of appending more global rules.

## Current First Slice

1. Document this plan.
2. Tighten default agent instruction for schema profiling, zero-result pivot,
   and partial-answer behavior.
3. Add evaluation coverage for the generic allocation/configuration follow-up
   shape.
4. Update schema notes with generic profiling guidance.
5. Add `kube_insight_js` as the single bounded language-shaped data tool.
6. Add ClickHouse support to real prompt eval and a local no-LLM ClickHouse case smoke.
7. Run narrow tests for agent contracts, config, API, and evaluation.

Completed follow-up slice:

1. Persist and audit provider-facing `completion.request` events.
2. Replay follow-up context from the latest provider request when available.
3. Preserve streamed assistant tool-call messages and provider-valid tool result
   ordering.
4. Compact oversized historical tool results before replaying them into
   follow-up requests.
5. Add retry rewind, session projection, context-size, and tool-efficiency
   checks to the API live smoke.
