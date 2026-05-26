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
| `node-capacity` | Node count and total capacity | health + schema + Node capacity facts SQL, citation |
| `scripted-query-node-capacity` | Dependent/compact SQL plan | schema + `kube_insight_scripted_query`, tool artifact, citation |
| `recent-changes` | recent object changes | search + history tools, history artifact, citation |
| `exact-recent-changes` | exact object recent changes | health + schema + one rollup SQL, citation |
| `topology-mapping` | namespace topology | search + topology tools, topology artifact, citation |
| `js-transform-aggregation` | SQL rows need grouping/counting | schema + SQL + `artifact_transform_js`, cited answer |

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
Node capacity fact rows for capacity SQL, and OOM rows for OOM/fact SQL. This
keeps synthetic eval useful for model behavior without requiring live ClickHouse
access or exporting real cluster data.

Latest synthetic provider findings after adding `js-transform-aggregation`,
fixing the fake SQL branch order, and wrapping the JS transform tool as
recoverable in live eval:

| Provider | Full 8-case snapshot | Targeted OOM+JS rerun | Notes |
| --- | ---: | ---: | --- |
| `mimo` | 7/8 | 2/2 | Strong overall. The remaining full-matrix failure was evaluator-path related for broad recent changes; schema+SQL produced a valid cited answer. |
| `deepseek` | 2/8 before targeted fixes | 2/2 | Correct but over-investigates in broad cases. The targeted OOM aggregate and JS transform cases pass after fixture and recoverability fixes. |
| `sub2api` | 6/8 before fixture fix | 2/2 | Good default candidate. Previous OOM/JS failures were caused by fake SQL returning allocation rows for OOM-shaped queries. |
| `moonshot` | 5/8 before fixture fix | 2/2 | Tool calling works but latency is high and it tends to verify more than needed. |
| `deepinfra` | 7/8 | 2/2 | Fast and good on simple/aggregate/JS cases, but one exact recent-change run used history instead of schema+SQL. |

Use an explicit `go test -timeout` for live matrix runs. Provider-side streaming
latency can exceed the default ten-minute Go test timeout even when each case has
a per-run timeout. For routine prompt work, prefer targeted case filters over
running every provider and every case.


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

The summary JSON records `initialContext` size metrics plus `toolCalls`,
`failedToolCalls`, and `toolNames` for each run. Use these to compare whether
context compaction reduces prompt size without causing extra rediscovery calls.

Use an OOM follow-up pair when validating session-context replay:

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

Use a Node capacity plus scripted-query prompt when validating the capacity
facts and one-tool SQL planning path:

```bash
make build
KUBE_INSIGHT_AGENT_API_SMOKE_MODEL=mimo-v2.5-pro \
KUBE_INSIGHT_AGENT_API_SMOKE_API_KEY_ENV=MIMO_API_KEY \
KUBE_INSIGHT_AGENT_API_SMOKE_BASE_URL_ENV=MIMO_OPENAI_BASEURL \
KUBE_INSIGHT_AGENT_API_SMOKE_QUESTIONS='How many Nodes are there, and what are total capacity CPU and memory? Cite the proof.;;Use one bounded scripted SQL plan to summarize Node count plus total capacity CPU and memory, and cite the proof.' \
KUBE_INSIGHT_AGENT_API_SMOKE_MAX_FOLLOWUP_TOOL_CALLS=4 \
KUBE_INSIGHT_AGENT_API_SMOKE_OUTPUT="$PWD/testdata/generated/agent-api-live-smoke-node-capacity" \
scripts/agent-api-live-smoke.sh
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
| SQL + JS grouping | `run.completed` | 6 | 2 | `kube_insight_schema` + `kube_insight_sql` + `artifact_transform_js` |

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

Add these after the first harness has settled:

- `history-diff`: ask what changed between retained versions for one object and
  require `kube_insight_history`, a `k8s.history` or `k8s.diff` artifact, and a
  cited changed field.
- `schema-sql-evidence`: ask for an aggregate proof query and require
  `kube_insight_schema` before `kube_insight_sql`, bounded rows, and cited SQL
  row identifiers.
- `zero-result-pivot`: feed a transcript where one exact query returns zero rows
  and require the next action to profile available keys/types or report a
  coverage gap instead of repeating wildcard variants.
- `real-db-scripted-query`: run the scripted query case against the local
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
