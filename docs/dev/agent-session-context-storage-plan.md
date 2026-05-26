# Agent Session and Context Storage Plan

This plan defines the target storage model for agent sessions, raw
transcripts, context replay, and subagent transcript isolation.

The goal is to keep session history faithful enough to replay and debug model
behavior while avoiding cumulative context snapshots and repeated large tool
outputs in the main conversation.

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

The cumulative `run.created.transcript.messages` snapshot is not a clean raw
transcript model. It must remain legacy-only compatibility input, not a
permanent session-memory layer.

## Goals

- Store a complete raw transcript as an append-only event log, close to the
  provider's chat/completions message shape.
- Reconstruct model input from ordered transcript events, not from cumulative
  per-run snapshots or selective summaries.
- Keep large tool outputs out of the main agent prompt unless exact checkpoint
  resume requires them.
- Support subagents as first-class runs with their own raw transcript, artifacts,
  and evidence summaries.
- Preserve retry rewind semantics: a retry replaces the retried branch in the
  session projection and does not append a new unrelated conversation tail.
- Keep storage backend semantics shared across SQLite and ClickHouse.
- Avoid adding symptom-specific tables or cached context tables.

## Non-Goals

- Do not add a separate `session_memory`, `session_context`, or
  `conversation_messages` table for the main transcript.
- Do not replace raw transcript with automatic summaries.
- Do not make subagents a permission boundary. Policy enforcement remains in
  API/MCP/tool layers.
- Do not store provider SDK internals that cannot be meaningfully replayed or
  inspected.

## Target Model

`agent_run_events` is the canonical transcript and audit log.

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
| `completion.request` | raw model request | provider, model, ordered messages, tool schemas or tool names, options |
| `completion.message` | model-visible assistant/user/system/tool message | provider-shaped message content, tool calls, tool call ids |
| `completion.tool_result` | model-visible tool result message | tool call id, name, compact content sent back to model, optional artifact id |
| `tool.started` | UI/audit | tool call id, name, input |
| `tool.completed` / `tool.failed` | UI/audit | status, summary, duration, artifact id, error |
| `artifact.created` | raw proof payload | bounded raw tool output, SQL rows, rendered evidence, large JSON |
| `answer.final` | UI final answer | final rendered assistant answer with citations |

`completion.request` should represent exactly what the runner sends to the
model for that model call. For OpenAI-compatible chat completion requests, store
the `messages` array and tool definitions in the same role/content/tool_call
shape the provider receives, after local policy and compaction are applied.

`completion.message` and `completion.tool_result` should represent what is
needed to continue the next model call. UI-specific display events can still
exist, but replay should prefer the completion transcript.

### Raw Message Shape

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
outside the raw transcript.

## Context Reconstruction

Context assembly should be deterministic:

1. Load session runs in branch order, respecting retry replacement metadata.
2. For each visible run in the active branch, read ordered
   `completion.message` and `completion.tool_result` events.
3. Rebuild the next request messages from those events plus the new user input.
4. Add client context as a system message for the new run only.
5. Apply explicit compaction only after faithful replay has produced the
   candidate transcript.
6. Persist the final assembled model request as `completion.request` before
   calling the model.

The replay path must not read cumulative snapshots from previous runs. If old
runs only contain legacy events, use a compatibility adapter that reconstructs
visible user/assistant turns from `agent_runs.input`, `message.completed`, and
`answer.final`, then immediately records the next run's real
`completion.request`.

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

This keeps the transcript faithful to what the model saw while allowing the UI
and debugger to inspect raw evidence through artifacts.

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

This gives the main agent enough context to answer while preserving full raw
subagent transcripts for audit, replay, and debugging.

## Retry and Branch Projection

Retry remains rewind semantics.

Rules:

- A retry run records `retryOfRunId` and `retryRootRunId`.
- The session projection hides the retried run and later runs in that branch.
- Raw events are still retained according to retention policy unless the branch
  is pruned after a successful replacement.
- Context replay for a retry starts from the transcript before the retried run,
  then appends the replacement run input.
- UI retry actions must never silently fall back to a plain appended message.

## Retention and Compaction

Raw transcript is the source of truth. Compaction is a layer above it.

Allowed compaction:

- model-visible `completion.tool_result` can contain summaries instead of raw
  artifacts,
- old inactive retry branches can be pruned by retention,
- large unreferenced transient artifacts can be pruned after run completion and
  citation safety windows,
- explicit long-session compaction can add summary messages only as new
  transcript events with source run/event references.

Disallowed compaction:

- silently replacing transcript history with summaries,
- dropping active-branch messages needed to replay the current conversation,
- using hidden summaries as the only source for follow-up prompts.

## Migration Plan

### Phase 1: Stop Cumulative Snapshots

- [x] Remove `run.created.data.transcript.messages` from newly created runs.
- [x] Keep `run.created` lifecycle-only.
- [x] Add compatibility tests proving follow-up context still works without
  cumulative snapshots.
- [x] Keep legacy parsing only for existing data created before the change.

### Phase 2: Add Raw Completion Events

- [x] Add event constants for `completion.request`,
  `completion.message`, and `completion.tool_result`.
- [x] Record the initial `completion.request` immediately before each run's
  Eino runner call. Per-tool-loop provider request capture remains future work.
- [x] Record assistant messages with tool calls in provider-shaped form.
- [x] Record model-visible tool result messages separately from UI/audit tool
  events.
- [x] Update replay to prefer completion events.

### Phase 3: Make Context Replay Transcript-First

- Replace `agentConversationMessagesForRun` with a replay builder that reads
  completion events from the active session branch.
- Keep a legacy adapter for runs that lack completion events.
- Add tests for:
  - normal follow-up,
  - intent correction follow-up,
  - retry rewind,
  - mixed streamed and non-streamed assistant output,
  - tool-call continuation.

### Phase 4: Subagent Child Runs

- Give subagent tools a store-aware runner path that creates child runs.
- Record child run metadata: parent run, parent tool call, subagent name, branch.
- Persist full child raw transcript and artifacts.
- Return compact child summaries to the parent transcript with child run links.
- Add parallel subagent tests that prove child transcripts are complete while
  parent context stays compact.

### Phase 5: Retention and UI Projection

- Update retention to understand parent/child run references and artifact
  references.
- Ensure active branch raw transcript is retained.
- Show child runs and artifacts from parent tool-call details in the UI.
- Keep SSE event compatibility for existing frontend panels.

## Validation

Required tests:

- `go test ./internal/agent ./internal/api ./internal/storage/sqlite`
- ClickHouse agent store tests when ClickHouse test support is available.
- API test that inspects events and proves no new `run.created.transcript`
  snapshot is emitted.
- API test that follow-up context is reconstructed from completion events.
- Retry projection tests for branch rewind and replacement.
- Subagent test proving parent transcript stores compact child result while child
  run stores complete raw completion events.
- `git diff --check`

Manual inspection for a real run:

- List run events in sequence.
- Confirm every model call has a preceding `completion.request`.
- Confirm large SQL/search output exists as artifacts, not repeated in parent
  completion messages.
- Confirm follow-up prompt includes the prior user intent and assistant answer.

## Open Questions

- Whether to store full provider tool schemas in every `completion.request`, or
  store tool names plus a schema version hash for common built-in tools.
- Whether streamed delta events should remain UI-only, with
  `completion.message` as the replay source.
- Whether child subagent runs should be visible in the normal session timeline
  or only under parent tool-call details.
- How long to retain inactive retry branches by default.
