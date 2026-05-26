# Agent Session and Context Storage Plan

This plan defines the target storage model for agent sessions, stable
model-context replay, and subagent context isolation.

The goal is to reproduce the model's useful context deterministically across
runs, so follow-up questions do not drift and API providers can reuse prompt/KV
cache-friendly prefixes where supported. The stored context must be faithful to
what the model actually saw after local policy, tool-output reduction, and
compaction are applied, without repeatedly injecting large raw tool payloads.

## Problem

The current implementation has the right durable primitives, but the context
reconstruction layer is drifting toward duplicated state.

Current storage shape:

- `agent_sessions` stores conversation metadata only.
- `agent_runs` stores one logical run row with status, input, provider, model,
  timestamps, error, and metadata.
- `agent_run_events` stores append-only run events such as lifecycle status,
  messages, tool calls, artifacts, citations, and final answers.

Redundancy addressed by this plan:

- `agent_runs.input` stores the user input for each run.
- Earlier development briefly stored `run.created.data.transcript.messages` as
  the runner input snapshot. For follow-up runs, that snapshot included prior
  visible conversation messages plus the new user message, so long sessions
  became cumulative and approached quadratic duplication. New runs must keep
  `run.created` lifecycle-only.
- Streaming assistant output is represented as `message.delta` events and then
  again as a full `message.completed` event. The final answer is also emitted
  as `answer.final`.
- Tool input appears in `tool.started`, `tool.audit`, and the tool-output
  artifact metadata. Large tool output is mostly stored once in
  `artifact.created`, with `tool.completed` and `tool.audit` pointing at the
  artifact through `outputArtifactId`.

The cumulative `run.created.transcript.messages` snapshot is not a clean context
model. It must remain legacy-only compatibility input, not a permanent
session-memory layer.

## Goals

- Store a complete, deterministic model-context ledger as an append-only event
  log, close enough to the provider's chat/completions message shape to replay
  behavior and inspect drift.
- Reconstruct model input from ordered model-visible context events, not from
  cumulative per-run snapshots or selective summaries.
- Keep stable prefix material stable across follow-up runs: base instruction,
  skill/tool contracts, replayed prior messages, and compact model-visible tool
  results should keep the same order and bytes unless the underlying content
  intentionally changes.
- Place volatile per-run material such as client clock/time-zone context near the
  suffix of the request so provider prompt caches can still reuse the stable
  prefix.
- Keep large tool outputs out of the main agent prompt unless exact checkpoint
  resume requires them.
- Support subagents as first-class runs with their own model-context ledgers,
  artifacts, and evidence summaries.
- Preserve retry rewind semantics: a retry replaces the retried branch in the
  session projection and does not append a new unrelated conversation tail.
- Keep storage backend semantics shared across SQLite and ClickHouse.
- Avoid adding symptom-specific tables or cached context tables.

## Non-Goals

- Do not add a separate `session_memory`, `session_context`, or
  `conversation_messages` table for the main transcript.
- Do not replace active-branch model-visible context with hidden automatic
  summaries.
- Do not make subagents a permission boundary. Policy enforcement remains in
  API/MCP/tool layers.
- Do not store provider SDK internals that cannot be meaningfully replayed or
  inspected.

## Target Model

`agent_run_events` is the canonical model-context and audit log.

`agent_runs` remains a control-plane index for UI and status queries. It may
keep `input` as a convenience summary, but it must not be the source of truth for
context replay.

`agent_sessions` remains metadata only.

### Event Families

Use the existing event stream and add transcript-oriented events only where they
remove ambiguity:

| Event | Purpose | Stored data |
| --- | --- | --- |
| `run.created` | lifecycle | run id, session id, queued status, metadata pointers only |
| `completion.request` | canonical model request | provider, model, ordered messages, tool schemas or tool names, options, stable prefix/version metadata |
| `completion.message` | model-visible assistant/user/system/tool message | provider-shaped message content, tool calls, tool call ids |
| `completion.tool_result` | model-visible tool result message | tool call id, name, compact content sent back to model, optional artifact id |
| `tool.started` | UI/audit | tool call id, name, input |
| `tool.completed` / `tool.failed` | UI/audit | status, summary, duration, artifact id, error |
| `artifact.created` | raw proof payload | bounded raw tool output, SQL rows, rendered evidence, large JSON |
| `answer.final` | UI final answer | final rendered assistant answer with citations |

`completion.request` should represent exactly what the runner sends to the model
for that model call after local policy, context replay, and tool-output
reduction. For OpenAI-compatible chat completion requests, store the `messages`
array and tool definitions in the same role/content/tool_call shape the provider
receives, after local policy and compaction are applied.

The request record is not required to preserve opaque provider SDK internals, but
it must preserve all behavior-affecting inputs: ordered messages, tool
definitions or stable tool schema hashes, tool-choice/options when used,
temperature/top-p/stop/max-token settings, model id, and any provider-specific
fields that can change model behavior.

`completion.message` and `completion.tool_result` should represent what is
needed to continue the next model call. UI-specific display events can still
exist, but replay should prefer the completion transcript.

### Stable Message Shape

Use a stable internal envelope, with provider-shaped payload inside:

```json
{
  "format": "kube-insight.agent.completion.v1",
  "provider": "openai-compatible",
  "model": "model-name",
  "messages": [
    {"role": "system", "content": "..."},
    {"role": "user", "content": "..."},
    {
      "role": "assistant",
      "content": "",
      "tool_calls": [
        {
          "id": "call_...",
          "type": "function",
          "function": {"name": "kube_insight_sql", "arguments": "{...}"}
        }
      ]
    },
    {
      "role": "tool",
      "tool_call_id": "call_...",
      "name": "kube_insight_sql",
      "content": "compact result sent to the model",
      "artifact_id": "artifact_..."
    }
  ],
  "options": {
    "temperature": 0,
    "max_iterations": 32
  }
}
```

Provider-specific fields that affect behavior should be preserved under
`providerRequest` when available. Fields used only for UI rendering should stay
outside the model-context ledger.

To improve provider-side cache reuse, the canonical request builder should keep
the longest possible stable prefix byte-identical between related runs. Do not
put changing values such as "request started at" before the replayed conversation
unless that value is required to interpret the whole prefix. Prefer a short
per-run system message immediately before the current user message for volatile
client context.

## Context Reconstruction

Context assembly should be deterministic:

1. Load session runs in branch order, respecting retry replacement metadata.
2. For each visible run in the active branch, read ordered model-visible
   `completion.message` and `completion.tool_result` events.
3. Rebuild the next request messages from those events plus the new user input,
   preserving prior byte/order stability where possible.
4. Add volatile client context as late as possible, normally as a system message
   for the new run immediately before the current user message.
5. Apply explicit compaction only after faithful replay has produced the
   candidate context, and record compaction as visible model-context events with
   source references.
6. Persist the final assembled model request as `completion.request` before
   calling the model.

The replay path must not read cumulative snapshots from previous runs. If old
runs only contain legacy events, use a compatibility adapter that reconstructs
visible user/assistant turns from `agent_runs.input`, `message.completed`, and
`answer.final`, then immediately records the next run's real
`completion.request`.

The replay path must not reconstruct context from UI-only events when
model-context events are available. UI deltas, rendered final answers, and
display summaries can differ from model-visible messages and should not become
the authoritative replay source.

## Tool Output Policy

Large tool output should not be repeatedly injected into the main transcript.

Target behavior:

- Store raw tool output in `artifact.created`.
- Store the compact model-visible tool message in `completion.tool_result`.
- Include `artifact_id`, title, row count, and a concise summary in the tool
  result message.
- Let the final answer cite artifacts, facts, rows, and snippets.
- Re-inject exact raw tool output only for checkpoint resume or if the model
  explicitly needs a small proof snippet.

This keeps the model context faithful to what the model saw while allowing the
UI and debugger to inspect raw evidence through artifacts.

## Subagent Storage

Subagents are the main mechanism for reducing main-context pressure from noisy
tool investigations.

Each subagent invocation should be represented as:

- a parent `tool.started` event for `parallel_investigation`,
  `evidence_condenser`, or another subagent tool,
- one child run per branch with `parentRunId`, `parentToolCallId`,
  `subagentName`, and `branchName` metadata,
- complete child `completion.request`, `completion.message`,
  `completion.tool_result`, tool, artifact, and answer events,
- one compact parent `completion.tool_result` containing branch findings,
  evidence labels, and child run IDs.

The parent transcript should not inline every child tool result. It should store
only the subagent's compact return value plus references to child run IDs and
artifact IDs.

This gives the main agent enough context to answer while preserving complete
subagent model-context ledgers for audit, replay, and debugging.

## Retry and Branch Projection

Retry remains rewind semantics.

Rules:

- A retry run records `retryOfRunId` and `retryRootRunId`.
- The session projection hides the retried run and later runs in that branch.
- Model-context events are still retained according to retention policy unless
  the branch is pruned after a successful replacement.
- Context replay for a retry starts from the context before the retried run,
  then appends the replacement run input.
- UI retry actions must never silently fall back to a plain appended message.

## Retention and Compaction

The model-context ledger is the source of truth. Compaction is a layer above it.

Allowed compaction:

- model-visible `completion.tool_result` can contain summaries instead of raw
  artifacts,
- old inactive retry branches can be pruned by retention,
- large unreferenced transient artifacts can be pruned after run completion and
  citation safety windows,
- explicit long-session compaction can add summary messages only as new
  transcript events with source run/event references.

Disallowed compaction:

- silently replacing active-branch context history with summaries,
- dropping active-branch messages needed to replay the current conversation,
- using hidden summaries as the only source for follow-up prompts.

## Migration Plan

### Phase 1: Stop Cumulative Snapshots

- [x] Remove `run.created.data.transcript.messages` from newly created runs.
- [x] Keep `run.created` lifecycle-only.
- [x] Add compatibility tests proving follow-up context still works without
  cumulative snapshots.
- [x] Keep legacy parsing only for existing data created before the change.

### Phase 2: Add Model-Context Completion Events

- [x] Add event constants for `completion.request`,
  `completion.message`, and `completion.tool_result`.
- [x] Record each Eino model call's canonical `completion.request` through
  `ChatModelAgentMiddleware.WrapModel` immediately before invoking the provider
  model. API-layer user-message recording remains separate from model-call
  request capture.
- [x] Record assistant messages with tool calls in provider-shaped form.
- [x] Record model-visible tool result messages separately from UI/audit tool
  events.
- [x] Update replay to prefer completion events.

### Phase 3: Make Context Replay Model-Context-First

- [x] Replace `agentConversationMessagesForRun` with a replay builder that reads
  model-context completion events from the active session branch.
- [x] Add a model-call recorder through Eino `ChatModelAgentMiddleware.WrapModel` so
  every provider-facing model call records its exact ordered messages, tools, and
  behavior-affecting options before invocation.
- [x] Restore assistant tool calls and tool-result messages during replay instead of
  replaying only visible user/assistant text.
- [x] Keep volatile client context late in the request to preserve stable cacheable
  prefixes.
- [x] Keep a legacy adapter for runs that lack completion events.
- [ ] Add tests for:
  - [x] normal follow-up,
  - [x] intent correction follow-up,
  - [x] cache-friendly stable prefix ordering,
  - [x] retry rewind,
  - [x] mixed streamed and non-streamed assistant output,
  - [x] tool-call continuation.

### Phase 4: Subagent Child Runs

- [x] Give `parallel_investigation` a store-aware runner path that creates child
  runs when parent run context is available.
- [x] Propagate parent run and parent tool-call context through Eino tool
  middleware.
- [x] Record child run metadata: parent run, parent tool call, subagent name,
  branch, and context policy.
- [x] Persist complete child model-context events for `parallel_investigation`
  branches.
- [x] Return compact child summaries to the parent transcript with child run IDs.
- [x] Skip child runs during normal parent session context replay.
- [x] Give `evidence_condenser` the same store-aware child-run path.
- [x] Extend retention to understand parent/child run references and artifact
  references.
- [x] Add UI projection for child runs under parent tool-call details.

### Phase 5: Retention and UI Projection

- [x] Update retention to understand parent/child run references and artifact
  references.
- [x] Ensure active branch model context is retained by pruning retry descendants
  only after a successful replacement and by protecting child artifacts while a
  parent run is still in progress.
- [x] Show child runs and artifacts from parent tool-call details in the UI.
- [x] Keep SSE event compatibility for existing frontend panels.

## Validation

Required tests:

- `go test ./internal/agent ./internal/api ./internal/storage/sqlite`
- ClickHouse agent store tests when ClickHouse test support is available.
- API test that inspects events and proves no new `run.created.transcript`
  snapshot is emitted.
- API test that follow-up context is reconstructed from completion events.
- Retry projection tests for branch rewind and replacement.
- Subagent test proving parent context stores compact child result while child
  run stores complete completion events.
- `git diff --check`

Manual inspection for a real run:

- List run events in sequence.
- Confirm every model call has a preceding `completion.request`.
- Confirm large SQL/search output exists as artifacts, not repeated in parent
  completion messages.
- Confirm follow-up prompt includes the prior user intent and assistant answer.
- Confirm stable prefix bytes/order do not change between follow-up runs except
  where the conversation itself intentionally appends new model-visible context.

## Open Questions

- Whether to store full provider tool schemas in every `completion.request`, or
  store tool names plus a schema version hash for common built-in tools.
- Whether streamed delta events should remain UI-only, with
  `completion.message` as the replay source.
- How long to retain inactive retry branches by default.
