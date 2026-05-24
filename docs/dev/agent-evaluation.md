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
| `recent-changes` | recent object changes | search + history tools, history artifact, citation |
| `topology-mapping` | namespace topology | search + topology tools, topology artifact, citation |

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
go test ./internal/agent -run TestLiveLLMEvaluation -count=1 -v
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
go test ./internal/agent -run TestLiveLLMEvaluation -count=1 -v
```

The test fails on clear product regressions: missing required tools, missing
evidence artifacts, no citations, terminal run failure, failed tools, missing
required answer terms, missing deterministic proof-panel projection for tool
outputs, too many tool calls for the case budget, no verified answer citations,
or latency outside the case budget. Runner failures are also written into the JSON report when an output path
is configured, so max-iteration failures still leave a debuggable transcript
summary. Minor wording differences should be scored through answer terms and
evidence checks, not exact text matching.

## API Live Smoke Path

The next live path should reuse the same evaluator through the HTTP API:

1. Create or reuse a small fixture-backed SQLite database with known Service,
   Pod, Event, history, and topology evidence.
2. Start `kube-insight serve --api --mcp --webui` with `server.chat.enabled`
   and the provider key env configured.
3. Submit one run per default evaluation case through
   `POST /api/v1/agent/sessions/{session_id}/runs`.
4. Read `GET /api/v1/agent/runs/{run_id}/events?follow=true` until terminal.
5. Score the transcript with `agent.EvaluateRunEvents`.
6. Save the report JSON under `testdata/generated/agent-eval-live/`.

## Next Test Cases

Add these after the first harness has settled:

- `history-diff`: ask what changed between retained versions for one object and
  require `kube_insight_history`, a `k8s.history` or `k8s.diff` artifact, and a
  cited changed field.
- `schema-sql-evidence`: ask for an aggregate proof query and require
  `kube_insight_schema` before `kube_insight_sql`, bounded rows, and cited SQL
  row identifiers.
- `recoverable-sql-error`: feed a transcript with one failed SQL call and a
  corrected retry, allowing failed tools only when a later successful tool call
  supports the final answer.
- `missing-input`: ask an ambiguous question and require an input-required event
  once that event type exists.

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

