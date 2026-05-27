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

### 4. Bounded Local Data Transform

First implementation slice: `artifact_transform_js` is a native Eino tool backed
by goja. It accepts literal `input` JSON and a JavaScript function body, injects
`input`, a `rows` alias when available, small `ki`/`_` data helpers, and safe
logging/JSON helpers, and returns bounded JSON. It has no filesystem, network,
process, or environment access.

Second implementation slice: `kube_insight_scripted_query` wraps the existing
read-only `kube_insight_sql` tool and exposes bounded `sql()` plus `sqlAll()`
inside goja. Use it after schema when one tool call should perform dependent
profile -> proof SQL, several independent aggregates, or SQL rows plus compact
grouping. It is still not a shell replacement; SQL read-only validation, query
count, row count, timeout, and output limits remain mandatory.

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

### 6. Regression Matrix

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

## Current First Slice

1. Document this plan.
2. Tighten default agent instruction for schema profiling, zero-result pivot,
   and partial-answer behavior.
3. Add evaluation coverage for the generic allocation/configuration follow-up
   shape.
4. Update schema notes with generic profiling guidance.
5. Add `artifact_transform_js` as the first bounded language-shaped data tool.
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
