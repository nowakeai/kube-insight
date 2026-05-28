# Agent Evaluation

This note defines the first testing path for the built-in agent. The goal is to
keep CI deterministic while still measuring whether the agent produces useful
investigation output.

## What The Stable Tests Score

The stable harness evaluates `agent.RunEvent` transcripts rather than calling a
live model. This keeps tests fast and repeatable while checking the output shape
that the Web UI and audit flow actually consume. Evidence artifacts are candidate
proof panels; answer citations are emitted only after the final answer mentions
stable evidence IDs or object identities that the server can verify against those
candidate artifacts.

Each `agent.EvaluationCase` can require:

- specific tool calls, such as `kube_insight_health` before health claims,
- evidence artifacts, such as `markdown`, `k8s.resource_list`, `k8s.topology`,
  or `k8s.history`,
- minimum citation count,
- required answer terms for the core finding,
- per-tool, total tool latency, and maximum tool-call count budgets,
- absence of failed tool calls unless the case explicitly allows recoverable
  failures.

Run the current stable suite with:

```bash
go test ./internal/agent
```

The default case set currently covers:

| Case | Question shape | Required signal |
| --- | --- | --- |
| `service-health` | Service health | health + Service investigation tools, resource/topology artifacts, citation |
| `oom-restart` | OOMKilled or restart evidence | search + history tools, resource/history artifacts, citation |
| `oom-aggregate` | OOMKilled ranking/counting | health + schema + bounded aggregate SQL, citation |
| `allocation-followup` | User corrects intent from live usage to allocation/configuration | schema-guided SQL and readable allocation/config evidence, citation |
| `node-capacity` | Node count and total capacity | health + schema + latest Node snapshot SQL, citation |
| `scripted-query-node-capacity` | Dependent/compact SQL plan | schema + `kube_insight_js`, tool artifact, citation |
| `recent-changes` | recent object changes | search + history tools, history artifact, citation |
| `history-diff` | retained object version diff | `kube_insight_history`, history artifact with change/diff proof, citation |
| `exact-recent-changes` | exact object recent changes | health + schema + one rollup SQL, citation |
| `schema-sql-evidence` | exact aggregate proof query | schema + bounded SQL rows, citation |
| `topology-mapping` | namespace topology | search + topology tools, topology artifact, citation |
| `js-transform-aggregation` | SQL rows need grouping/counting | schema + `kube_insight_js`, cited answer |

## Live LLM Matrix

Live model tests stay opt-in because latency, provider availability, and model
behavior are not deterministic enough for normal CI. The first live path uses
real OpenAI-compatible models with controlled fake kube-insight tools. This
isolates model/tool behavior from database freshness and cluster state while
still exercising the real Eino runner, model adapter, tool calling, run events,
artifact projection, final-answer verified citations, and evaluator.

Run one model with the default OpenAI-compatible envs:

```bash
KUBE_INSIGHT_AGENT_LIVE_EVAL=1 \
OPENAI_API_KEY=... \
OPENAI_BASE_URL=https://example-compatible-endpoint/v1 \
go test ./internal/agent -run TestLiveLLMEvaluation -count=1 -timeout 25m -v
```

Run a model matrix by separating specs with `;`. Each spec is:

```text
name|model|api_key_env|base_url_env
```

Example:

```bash
KUBE_INSIGHT_AGENT_LIVE_EVAL=1 \
KUBE_INSIGHT_AGENT_LIVE_EVAL_MODELS='gpt52|gpt-5.2|OPENAI_API_KEY|OPENAI_BASE_URL;mimo|mimo-v2.5-pro|MIMO_API_KEY|MIMO_OPENAI_BASEURL' \
KUBE_INSIGHT_AGENT_LIVE_EVAL_MAX_ITERATIONS=12 \
KUBE_INSIGHT_AGENT_LIVE_EVAL_OUTPUT="$PWD/testdata/generated/agent-eval-live" \
go test ./internal/agent -run TestLiveLLMEvaluation -count=1 -timeout 25m -v
```

Use `KUBE_INSIGHT_AGENT_LIVE_EVAL_CASES` with comma-separated case IDs for
targeted reruns, for example
`allocation-followup,exact-recent-changes,scripted-query-node-capacity`.

The test fails on clear product regressions: missing required tools, missing
evidence artifacts, no citations, terminal run failure, failed tools, missing
required answer terms, missing deterministic proof-panel projection for tool
outputs, too many tool calls for the case budget, no verified answer citations,
or latency outside the case budget. Runner failures are also written into the JSON report when an output path
is configured, so max-iteration failures still leave a debuggable transcript
summary. Minor wording differences should be scored through answer terms and
evidence checks, not exact text matching.

The fake SQL tool used by `TestLiveLLMEvaluation` is input-aware: it returns
change rows for change-history SQL, allocation rows for requests/limits SQL,
latest Node snapshot rows for capacity SQL, and OOM rows for OOM/fact SQL. This
keeps synthetic eval useful for model behavior without requiring live ClickHouse
access or exporting real cluster data.

Latest synthetic provider findings after adding `js-transform-aggregation`,
fixing the fake SQL branch order, and wrapping the unified JS interpreter tool
as recoverable in live eval:

| Provider | Full 8-case snapshot | Targeted OOM+JS rerun | Notes |
| --- | ---: | ---: | --- |
| `mimo` | 7/8 | 2/2 | Strong overall. The remaining full-matrix failure was evaluator-path related for broad recent changes; schema+SQL produced a valid cited answer. |
| `deepseek` | 2/8 before targeted fixes | 2/2 | Correct but over-investigates in broad cases. The targeted OOM aggregate and JS interpreter cases pass after fixture and recoverability fixes. |
| `sub2api` | 6/8 before fixture fix | 2/2 | Good default candidate. Previous OOM/JS failures were caused by fake SQL returning allocation rows for OOM-shaped queries. |
| `moonshot` | 5/8 before fixture fix | 2/2 | Tool calling works but latency is high and it tends to verify more than needed. |
| `deepinfra` | 7/8 | 2/2 | Fast and good on simple/aggregate/JS cases, but one exact recent-change run used history instead of schema+SQL. |

Use an explicit `go test -timeout` for live matrix runs. Provider-side streaming
latency can exceed the default ten-minute Go test timeout even when each case has
a per-run timeout. For routine prompt work, prefer targeted case filters over
running every provider and every case.

Recent ClickHouse-backed live agent findings are also carried into the built-in
prompt/tool harness:

- Pod-count peak questions should use the `pod_count_peak_intervals_for_js`
  recipe and reconstruct state from per-UID interval rows. Do not count raw
  observation UIDs, export capped raw observation rows as proof, run one SQL
  query per time bucket, or raise `maxQueries` above the hard cap of 10; use a
  small number of interval queries and sweep in JS.
- EndpointSlice readiness checks must preserve empty `endpoints` arrays.
  `arrayJoin(JSONExtractArrayRaw(doc, 'endpoints'))` alone can drop zero-endpoint
  slices and turn "empty" into "absent".
- Node change answers must separate lifecycle churn from net node/capacity
  change using start/end latest non-deleted snapshots plus lifecycle rows.


## API Live Smoke Path

Use the opt-in HTTP smoke script to exercise the full service path with a
synthetic fixture-backed SQLite database. The script ingests a small Service,
Pod, EndpointSlice, and Event fixture, starts `kube-insight serve --api --mcp`
with `server.chat.enabled`, submits agent runs through
`POST /api/v1/agent/sessions/{session_id}/runs`, follows
`GET /api/v1/agent/runs/{run_id}/events?follow=true`, and fails if terminal
runs do not emit a replayable `completion.request`, final answer, candidate
artifact, and verified citation.
For multi-question smoke runs, each completed run is also audited with
`db agent-context`; follow-up runs must keep prior user turns and assistant
context in the latest provider-facing `completion.request`. The smoke also
checks provider message order so replayed tool results never appear before the
assistant tool-call message that produced them. Follow-up initial requests also
enforce a historical tool-result size budget, so large prior outputs such as
schema dumps must be replayed as compact summaries rather than raw content.
Set `KUBE_INSIGHT_AGENT_API_SMOKE_RETRY_FIRST=1` to retry the first completed
run after later questions. The retry assertion checks that the replacement
run's first provider request rewinds to the original user input and excludes
later turns. It also checks the session endpoint exposes the replacement run
with `retryOfRunId` and `retryRootRunId`; retention may already have removed
the superseded branch by the time the session is fetched.

```bash
make build
KUBE_INSIGHT_AGENT_API_SMOKE_MODEL=mimo-v2.5-pro \
KUBE_INSIGHT_AGENT_API_SMOKE_API_KEY_ENV=MIMO_API_KEY \
KUBE_INSIGHT_AGENT_API_SMOKE_BASE_URL_ENV=MIMO_OPENAI_BASEURL \
scripts/agent-api-live-smoke.sh
```

Useful overrides:

- `KUBE_INSIGHT_AGENT_API_SMOKE_OUTPUT`: output directory for DB, logs, SSE, and
  summary JSON. Default: `testdata/generated/agent-api-live-smoke`.
- `KUBE_INSIGHT_AGENT_API_SMOKE_QUESTIONS`: `;;`-separated question list.
- `KUBE_INSIGHT_AGENT_API_SMOKE_API_LISTEN` and
  `KUBE_INSIGHT_AGENT_API_SMOKE_MCP_LISTEN`: local service addresses.
- `KUBE_INSIGHT_AGENT_API_SMOKE_TIMEOUT_SECONDS`: max wait per run.
- `KUBE_INSIGHT_AGENT_API_SMOKE_MAX_HISTORICAL_TOOL_REPLAY_CHARS`: max chars
  for each historical tool result in a follow-up run's initial request.
- `KUBE_INSIGHT_AGENT_API_SMOKE_MAX_FOLLOWUP_TOOL_CALLS`: max tool calls for
  each follow-up run. Default: `3`.
- `KUBE_INSIGHT_AGENT_API_SMOKE_MAX_FAILED_TOOL_CALLS`: max failed tool calls
  per run. Default: `0`.

The script writes three report files under the per-run output directory:

- `summary.json`: per-run raw smoke summary with paths to SSE/events/context
  captures.
- `report.json`: normalized totals, per-run metrics, and tool-frequency data
  for comparing prompt/tool changes across runs.
- `report.md`: a readable table version for quick review.

Use `toolCalls`, `failedToolCalls`, `toolNames`, `initialContext`, and
`toolFrequency` to compare whether context compaction reduces prompt size
without causing extra rediscovery calls.

Use an OOM follow-up pair when validating session-context replay. The request metadata must include fresh client `sentAt`, `localTime`, `timeZone`, UTC offset, and locale for each run, and the captured completion request should place that volatile client context immediately before the current relative-time user prompt:

```bash
make build
KUBE_INSIGHT_AGENT_API_SMOKE_MODEL=mimo-v2.5-pro \
KUBE_INSIGHT_AGENT_API_SMOKE_API_KEY_ENV=MIMO_API_KEY \
KUBE_INSIGHT_AGENT_API_SMOKE_BASE_URL_ENV=MIMO_OPENAI_BASEURL \
KUBE_INSIGHT_AGENT_API_SMOKE_QUESTIONS='最近有没有 OOM 现象？;;最近1小时内呢' \
KUBE_INSIGHT_AGENT_API_SMOKE_RETRY_FIRST=1 \
KUBE_INSIGHT_AGENT_API_SMOKE_OUTPUT="$PWD/testdata/generated/agent-api-live-smoke-context" \
scripts/agent-api-live-smoke.sh
```

Use a Node capacity plus JS-interpreter prompt when validating the latest Node
snapshot and one-tool SQL planning path:

```bash
make build
KUBE_INSIGHT_AGENT_API_SMOKE_MODEL=mimo-v2.5-pro \
KUBE_INSIGHT_AGENT_API_SMOKE_API_KEY_ENV=MIMO_API_KEY \
KUBE_INSIGHT_AGENT_API_SMOKE_BASE_URL_ENV=MIMO_OPENAI_BASEURL \
KUBE_INSIGHT_AGENT_API_SMOKE_QUESTIONS='How many Nodes are there, and what are total capacity CPU and memory? Cite the proof.;;Use bounded JS interpreter SQL planning to summarize Node count plus total capacity CPU and memory, and cite the proof.' \
KUBE_INSIGHT_AGENT_API_SMOKE_MAX_FOLLOWUP_TOOL_CALLS=4 \
KUBE_INSIGHT_AGENT_API_SMOKE_OUTPUT="$PWD/testdata/generated/agent-api-live-smoke-node-capacity" \
scripts/agent-api-live-smoke.sh
```

For historical aggregation prompts, use the `aggregation` case set. It covers
namespace Pod allocation ranking, namespace resource-change comparison, peak Pod
count over a time range, and PVC resize history. The first three cases are the
core regression set for complex aggregation: after the 2026-05-27 prompt/tool
contract tuning they passed individually and in the `aggregation-v9` run using
`kube_insight_health`, `kube_insight_schema`, and `kube_insight_js`. These
checks should validate bounded staged planning and evidence quality, not require
a single JS call. The PVC resize case is intentionally kept in the set because it exposes a
harder pattern: discover candidates, read ordered object versions, and compare
previous/next storage values in JavaScript instead of SQL window functions. It
also exposed intermittent SSE transfer-close behavior during long live runs, so
use it as a follow-up stability case rather than a release gate until that path
is fixed.

```bash
KUBE_INSIGHT_AGENT_CASE_RUN_AGENT=1 \
KUBE_INSIGHT_AGENT_CASE_SET=aggregation \
KUBE_INSIGHT_AGENT_CASE_STRICT=0 \
KUBE_INSIGHT_AGENT_CASE_OUTPUT="$PWD/testdata/generated/agent-clickhouse-case-smoke-aggregation" \
scripts/agent-clickhouse-case-smoke.sh
```

For an existing ClickHouse-backed compose API server, `agent-clickhouse-case-smoke`
can also submit a real agent run for the abbreviated-cluster Node inventory case.
The optional agent check verifies the desired tool behavior: discover clusters
with unfiltered health, use schema plus `kube_insight_js` when code-shaped
aggregation is needed, avoid legacy split JS tools, keep the first health call
from treating `gcp2` as the stored cluster ID, and require the final answer to
render the relative half-day window in the supplied client time zone instead of
only UTC/server time. It should not require the investigation to finish in one JS
call.

```bash
KUBE_INSIGHT_AGENT_CASE_API_URL=http://127.0.0.1:8080 \
KUBE_INSIGHT_AGENT_CASE_RUN_AGENT=1 \
KUBE_INSIGHT_AGENT_CASE_OUTPUT="$PWD/testdata/generated/agent-clickhouse-case-smoke-gcp2" \
scripts/agent-clickhouse-case-smoke.sh
```

External skill black-box tests must use this same compose dev API/MCP/ClickHouse
data path. Start child `codex exec` runs in a fresh empty temporary directory,
not in the kube-insight checkout. Copy only `kube-insight-skill/` into that
temporary directory before launching the child. This prevents the child agent
from taking shortcuts through repo state, generated files, internal tests, or
unrelated local context while still giving it the public skill instructions. A
`codex exec` run that opens `testdata/generated/*.db` or another SQLite fixture
only proves the skill can reason about a tiny offline sample; it does not
validate behavior against the real dev evidence store, fuzzy cluster resolution,
current collector coverage, or ClickHouse-scale aggregation.
Configure the test prompt to forbid SQLite fixture/sample DB fallback and to use
the running dev API/MCP/ClickHouse endpoint. Do allow the child agent to use
normal local tools for analysis: shell, `curl`, Python, `jq`, DuckDB, and
temporary files under `/tmp` are part of the external-agent capability being
tested. The forbidden paths are wrong data sources and shortcuts, not tools:
no repo edits, no host Codex memory, no `internal/agent/*` prompt/test mining,
and no Chrome/Next DevTools unless the case is explicitly about Web UI behavior.
`kubectl` is allowed when it models a real external-agent workflow, such as
port-forwarding to a cluster-hosted kube-insight API, validating live current
state, or interacting with related cluster services. The case should still prove
that kube-insight supplied the historical, retained, or aggregated evidence that
plain `kubectl` cannot provide.
For the local dev host, run the child with
`-c features.use_legacy_landlock=false --sandbox danger-full-access` after
copying only the skill into `/tmp`. The extra access is for loopback network
reachability to the already-running dev API/ClickHouse, not permission to inspect
the repo. Keep the prompt prohibition on repo edits, host memories, and
`internal/agent/*`. If ClickHouse HTTP requires credentials, prefer
`POST /api/v1/sql` through the kube-insight API rather than reading repo config.

Use the aggregation case set when validating generic historical aggregation
behavior before adding more prompt rules. Start with `STRICT=0` to collect all
baseline traces even when one case fails, then inspect `agent-cases-report.md`
and the per-case event JSON files:

```bash
KUBE_INSIGHT_AGENT_CASE_API_URL=http://127.0.0.1:8080 \
KUBE_INSIGHT_AGENT_CASE_RUN_AGENT=1 \
KUBE_INSIGHT_AGENT_CASE_SET=aggregation \
KUBE_INSIGHT_AGENT_CASE_PARALLEL=3 \
KUBE_INSIGHT_AGENT_CASE_STRICT=0 \
KUBE_INSIGHT_AGENT_CASE_OUTPUT="$PWD/testdata/generated/agent-clickhouse-case-smoke-aggregation" \
scripts/agent-clickhouse-case-smoke.sh
```

The aggregation set currently covers namespace Pod resource ranking, namespace
resource-change ranking over a relative multi-day window, and peak Pod-count
time over the last week. The desired path is `health/schema -> kube_insight_js`
with bounded SQL inside the interpreter, answer-ready grouping, normalized
Kubernetes units when applicable, and cited proof. Treat failures as taxonomy
data before editing the prompt: relative time, fuzzy lookup, complex
aggregation, deep field extraction, parallel discovery, complex predicates,
zero-result pivot, or evidence stopping.
The peak Pod-count case should reconstruct state, not count raw observation
UIDs: seed a baseline from the latest non-deleted Pod snapshot at the start of
the window, then apply ADDED/MODIFIED/DELETED observations where ADDED and
MODIFIED mean the Pod exists after that timestamp and DELETED means it does
not. EndpointSlice readiness tests should preserve zero-endpoint slices; an
`arrayJoin(JSONExtractArrayRaw(..., 'endpoints'))` query by itself can drop
empty arrays and hide the evidence. Node change tests should report churn
(`added_count`, `deleted_count`, replacements) separately from net node/capacity
change using start/end latest snapshots plus lifecycle rows.
`KUBE_INSIGHT_AGENT_CASE_PARALLEL` submits multiple live runs concurrently for
faster prompt iteration; use it only for opt-in provider-backed testing where
parallel token spend is acceptable.
For scratch/VFS validation, add
`KUBE_INSIGHT_AGENT_CASE_REQUIRE_SCRATCH_HANDLES=1`. The case smoke will then
assert that at least one tool-call artifact promoted session scratch handles and
will include scratch-handle totals in the report.
Use `KUBE_INSIGHT_AGENT_CASE_MIN_CITATIONS=N` when a case needs distinct proof
surfaces, for example Node inventory plus lifecycle changes should cite both
the current capacity snapshot and the lifecycle rows.
By default, the case smoke also checks common JS anti-patterns that caused real
aggregation failures: `const sql = ...` shadowing the SQL helper, and returning
broad raw arrays such as `{history: history}` or `{rows: rows}` instead of a
compact result or scratch handle. It also flags ClickHouse aggregate alias reuse
such as `max(updated_at) AS updated_at`, which can be rewritten into nested
aggregates when `updated_at` is also used by `argMax`, and SQL-side Kubernetes
quantity parsing patterns where `JSONExtractString`/`replaceAll` strings are
divided by `1000`, `1024`, or `1048576` inside JS SQL. Fetch quantity strings in
SQL and parse units in JavaScript instead. Disable these checks only for debugging with
`KUBE_INSIGHT_AGENT_CASE_FORBID_JS_ANTIPATTERNS=0`.

For faster iteration on one prompt, use the `custom` case set and override the
question/checks:

```bash
KUBE_INSIGHT_AGENT_CASE_API_URL=http://127.0.0.1:8080 \
KUBE_INSIGHT_AGENT_CASE_RUN_AGENT=1 \
KUBE_INSIGHT_AGENT_CASE_SET=custom \
KUBE_INSIGHT_AGENT_CASE_SLUG=agent-namespace-pod-resource-top \
KUBE_INSIGHT_AGENT_CASE_QUESTION='帮我看看哪个namespace pod资源占用最高' \
KUBE_INSIGHT_AGENT_CASE_NODE_LIFECYCLE_CHECK=0 \
KUBE_INSIGHT_AGENT_CASE_REQUIRED_ANSWER_TERMS='namespace,Pod' \
KUBE_INSIGHT_AGENT_CASE_REQUIRED_ANSWER_ANY_TERMS='' \
KUBE_INSIGHT_AGENT_CASE_OUTPUT="$PWD/testdata/generated/agent-clickhouse-case-smoke-custom" \
scripts/agent-clickhouse-case-smoke.sh
```


## 2026-05-25 Server Flow Smoke

The full API server smoke passed with the Sub2API model and a fixture-backed
SQLite database. The script built the current binary, ingested Service, Pod,
EndpointSlice, and Event fixtures, started `kube-insight serve --api --mcp`,
created one agent session, submitted three runs, followed SSE events, and
verified terminal `run.completed`, final answer, artifacts, and citations.
Current script summaries also include `completionRequests` and `childRunIds` so
subagent fan-out can be inspected without opening the full SSE log.

| Question shape | Status | Artifacts | Citations | Notable tool path |
| --- | --- | ---: | ---: | --- |
| Service health | `run.completed` | 5 | 3 | `kube_insight_health` + `kube_insight_service_investigation` |
| Namespace topology | `run.completed` | 10 | 1 | search retries hit the budget guard, then `kube_insight_topology` completed |
| SQL + JS grouping | `run.completed` | 6 | 2 | `kube_insight_schema` + `kube_insight_js` |

A local ClickHouse-backed server smoke against `http://127.0.0.1:8080` also
passed health, schema, and representative SQL cases: clusters, recent OOM facts,
semantic profile, allocation profile, allocation doc profile, allocation rollup,
and restart-by-namespace.

## 2026-05-24 DeepSeek API Smoke

Using `deepseek-v4-flash` against the synthetic fixture DB, the API smoke passed
through the full HTTP service path after rebuilding `bin/kube-insight` from the
current checkout:

| Question | Status | Artifacts | Citations |
| --- | --- | ---: | ---: |
| Service health for `default/api` | `run.completed` | 5 | 3 |
| Namespace topology for `default` | `run.completed` | 10 | 4 |

This validates the end-to-end path that the Web UI consumes: session creation,
run creation, server-owned Eino runner, MCP tool calls, SSE replay,
`artifact.created`, final answer, and verified `citation.created` events.

## Next Test Cases

Add these after the current harness has settled:

- `zero-result-pivot`: feed a transcript where one exact query returns zero rows
  and require the next action to profile available keys/types or report a
  coverage gap instead of repeating wildcard variants.
- `real-db-js-interpreter`: run the JS interpreter case against the local
  ClickHouse dev database, then compare tool count and answer quality against
  the plain schema+SQL capacity case.
- `noisy-evidence-condenser`: feed a large SQL/search artifact and require
  `evidence_condenser` with source artifact IDs, titles, and snippets.
- `recoverable-sql-error`: feed a transcript with one failed SQL call and a
  corrected retry, allowing failed tools only when a later successful tool call
  supports the final answer.
- `missing-input`: ask an ambiguous question and require an input-required event
  once that event type exists.

See `docs/dev/generic-agent-optimization-plan.md` for the generic agent
optimization plan that these cases support.

## 2026-05-24 MIMO Smoke

Using the local `.env` MIMO OpenAI-compatible endpoint, the full live matrix
passed after tightening tool-use guidance and setting the live eval default
iteration budget to 12. The successful run covered:

| Case | Tool calls | Citations | Artifact kinds |
| --- | ---: | ---: | --- |
| `service-health` | 3 | 4 | `markdown`, `k8s.resource_list`, `k8s.topology`, `tool_call` |
| `oom-restart` | 4 | 4 | `markdown`, `k8s.resource_list`, `k8s.history`, `tool_call` |
| `recent-changes` | 4 | 4 | `markdown`, `k8s.resource_list`, `k8s.history`, `tool_call` |
| `topology-mapping` | 3 | 3 | `markdown`, `k8s.resource_list`, `k8s.topology`, `tool_call` |

The smoke caught useful issues before passing: an overly low eight-iteration
live cap for MIMO on OOM/restart, over-investigation through repeated
SQL/topology calls, and recent-change over-investigation through related Service
lookups. The default instruction and MCP tool descriptions now tell models to
stop once typed tools provide enough evidence, avoid SQL reconfirmation, and
write stable evidence IDs/object identities in final answers so the server can
verify citations without accepting hallucinated references.
