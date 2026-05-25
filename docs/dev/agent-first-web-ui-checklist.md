# Agent-First Web UI Checklist

Status date: 2026-05-22

This checklist tracks the first Web UI milestone. Keep it updated during
development so handoffs show what is done, what is in progress, and what should
happen next.

## Design Decisions

- [x] Product shape is agent-first, not dashboard-first or evidence-workbench-first.
- [x] Opening screen is a search-style chat prompt.
- [x] Built-in agent loop runs in the Go server.
- [x] Server owns sessions, runs, cancellation, and streaming events.
- [x] Primary agent runtime is CloudWeGo Eino ADK.
- [x] Built-in agent architecture is MCP-first: Eino consumes kube-insight tools through MCP, and direct native Eino tools are spike-only.
- [x] Fallback agent runtime is charmbracelet/fantasy if Eino complexity is too high.
- [x] Frontend app stack is React + TypeScript + Vite.
- [x] UI stack is shadcn/ui + Tailwind + Radix.
- [x] Visual style follows the nowake.ai Design Guide Cloud Index direction.
- [x] Chat UI foundation is assistant-ui.
- [x] Client state uses Zustand for run/artifact UI projection.
- [x] Server data fetching uses TanStack Query for non-streaming API calls.
- [x] Topology artifacts use React Flow.
- [x] JSON/YAML proof views use CodeMirror.
- [x] Formal artifact kinds are part of the Web UI contract.
- [x] Evidence artifacts are deterministic server/tool projections; final
  answer citations are LLM-selected and server-verified against artifact IDs,
  object identities, version IDs, changes, facts, edges, or SQL rows from the
  current run.
- [x] A compact server dashboard is included as a secondary page.
- [x] Web assets should build from `web/` and embed into the Go binary.

## Cleanup Before Implementation

- [x] Remove the static `internal/webui/static` spike or replace it with a
  temporary compatibility shell that serves `web/dist`.
- [x] Revert spike-only README/quickstart wording if it describes behavior not
  present in the formal React implementation.
- [x] Decide whether the first implementation keeps `serve --webui` as the flag
  name or introduces a clearer alias later.

Decision: keep `serve --webui` as the service flag for the first implementation.

## Backend Foundation

- [x] Add agent config fields for `server.chat.provider`, `server.chat.apiKeyEnv`,
  `server.chat.baseUrlEnv`, and `server.chat.model`.
- [x] Add agent session and run domain types.
- [x] Add in-memory session/run store for the first spike.
- [x] Add `POST /api/v1/agent/sessions`.
- [x] Add `GET /api/v1/agent/sessions/{session_id}`.
- [x] Add `POST /api/v1/agent/sessions/{session_id}/runs`.
- [x] Add `GET /api/v1/agent/runs/{run_id}/events` with SSE.
- [x] Add `POST /api/v1/agent/runs/{run_id}/cancel`.
- [x] Define replayable run event types for messages, tool calls, artifacts,
  citations, status, errors, and final answers.
- [x] Add focused API tests for session/run lifecycle and SSE event formatting.

## Agent Runtime

- [x] Add Eino dependency after checking the current compatible version.
- [x] Build a minimal autonomous ChatModelAgent spike.
- [x] Prove direct native Eino tool wrappers can execute kube-insight reads.
  - [x] Wrap `kube_insight_health` as the first spike tool.
  - [x] Wrap `kube_insight_search` as the second spike tool.
  - [x] Wrap `kube_insight_history`.
  - [x] Wrap `kube_insight_topology`.
  - [x] Wrap `kube_insight_service_investigation`.
  - [x] Wrap guarded `kube_insight_sql`.
- [x] Map Eino events/tool callbacks into kube-insight run events for the spike.
- [x] Enforce answer citation expectations through prompt/tool descriptions and
  artifact contracts.
- [x] Document fallback criteria for switching to Fantasy if Eino integration
  becomes too heavy.

## MCP-First Agent Hardening

- [x] Record architecture decision: MCP is the authoritative tool contract for
  both the built-in Eino agent and external agents.
- [x] Keep direct native Eino tools classified as spike-only code, not the
  product tool contract or a long-term runtime fallback.
- [x] Verify Eino MCP adapter compatibility with the current
  `github.com/modelcontextprotocol/go-sdk` MCP server over Streamable HTTP.
  Legacy SSE stays external-only because the current mcp-go SSE client can hang
  during shutdown against the go-sdk SSE handler.
- [x] Add and verify the compatible Eino MCP adapter dependency
  `github.com/cloudwego/eino-ext/components/tool/mcp`.
- [x] Replace `configuredAgentTools` direct runtime wiring with MCP-discovered
  tools loaded through the Eino MCP adapter, then delete the unpublished direct
  native tool wrappers after MCP parity is covered.
- [x] Ensure built-in agent and external MCP clients see the same tool names,
  descriptions, input schemas, permission checks, audit records, and output
  budgets for the current MCP tool surface. Eino MCP adapter tests now discover
  the full MCP tool list.
- [x] Add MCP parity for currently direct-only tools.
  - [x] `kube_insight_schema` exists in MCP.
  - [x] `kube_insight_sql` exists in MCP.
  - [x] `kube_insight_health` exists in MCP.
  - [x] `kube_insight_history` exists in MCP.
  - [x] Add `kube_insight_search` to MCP.
  - [x] Add `kube_insight_topology` to MCP.
  - [x] Add `kube_insight_service_investigation` to MCP.
- [x] Make `kube_insight_schema` mandatory context for SQL-capable agent runs,
  either by tool-use policy in the prompt or by injecting a compact schema
  resource into the run. The default system prompt and MCP SQL description now
  require schema-first SQL planning.
- [x] Strengthen the default system prompt with explicit evidence-first rules,
  ClickHouse SQL discipline, recoverable tool-error retry behavior, parallel
  tool-call guidance, visible user-facing progress notes between tool-call
  groups, and answer evidence formatting.
- [ ] Update SQL tool descriptions dynamically for the active backend dialect so
  ClickHouse agents do not guess SQLite table names such as `latest_index`.
  Current static descriptions point agents to schema DSL/backend notes first;
  dynamic descriptions remain a later improvement.
- [ ] Add compact output budgets to MCP tools: summaries, ranked candidates,
  stable IDs, timestamps, row IDs, and artifact handles by default; large proof
  payloads opt in.
  - [x] `kube_insight_schema` defaults to compact LLM-oriented DSL output.
  - [x] `kube_insight_health` defaults to compact summary plus bounded problem
    resources, and truncates long watch/list errors for agent context.
  - [x] `kube_insight_search`, `kube_insight_topology`, and
    `kube_insight_service_investigation` default to compact evidence summaries
    instead of raw Kubernetes documents.
- [ ] Add MCP resources for schema snapshots, SQL cookbook excerpts, table
  dictionaries, and artifact payloads.
- [ ] Add optional MCP prompts for common entry points such as service health,
  recent changes, OOM/restart, webhook failure, and topology mapping. These are
  starter templates, not fixed workflows.
- [x] Make MCP tool errors recoverable model context instead of immediate run
  failure. The Eino tool wrapper now returns structured `isError` tool messages
  so the model can inspect the failure and retry with corrected SQL or another
  tool.
- [ ] Make model timeout configurable and surface provider/model timeout errors
  as first-class run diagnostics.

## Eino Advanced Runtime Plan

- [x] Review Eino ADK, runner, middleware, multi-agent, and MCP adapter docs
  before deepening the spike implementation.
- [x] Record spike isolation rules and current local capability findings in
  `docs/dev/eino-runtime-capability-spike.md`.
- [x] Verify which advanced ADK and middleware APIs are available in the current
  `github.com/cloudwego/eino v0.8.13` dependency, and upgrade deliberately if
  required. Since the agent is unpublished, prefer the cleaner structure over
  spike-era API preservation.
- [x] Add focused Eino capability harness tests for runner events,
  ChatModelAgent handlers, streaming, checkpoint/resume, reduction middleware,
  and AgentAsTool.
- [x] Extend `EinoRunnerConfig` with Eino ADK extension points for handlers,
  middlewares, model retry, checkpoint store, streaming, run options, tool
  return-directly, and internal subagent event forwarding.
- [x] Map Eino streaming message output into replayable `message.delta` and
  `message.completed` events.
- [ ] Refactor the runner integration so Eino runner events and callbacks are
  the source of truth for model deltas, tool calls, tool results, interrupts,
  errors, token usage, latency, and final output.
- [ ] Persist enough checkpoint metadata to retry a failed final model call or
  continue from the last completed tool result without re-running successful
  evidence collection.
- [ ] Evaluate Eino ChatModelAgent middleware for summarization, tool-output
  reduction, and tool search before building custom prompt compaction logic.
- [x] Design first-pass artifact handles so large tool outputs can be analyzed
  later without repeating the full payload in `tool.completed` and `tool.audit`.
  Eino tool results now create one `tool_call` artifact and tool events carry
  `outputSummary` plus `outputArtifactId`.
- [x] Add a specialist evidence-condenser subagent as `AgentAsTool` or
  `NewAgentTool` for noisy evidence summaries; the main agent must pass
  source artifact IDs/titles and row/snippet excerpts, not only prose.
- [ ] Pre-wire the run event model for subagent start/completion/error events,
  including parent tool call id, input artifact handles, compact findings, and
  citations.
- [ ] Keep Supervisor/DeepAgents orchestration as an optional future mode for
  broad incident triage; do not replace the default autonomous MCP tool loop
  with a fixed workflow in the first milestone.
- [ ] Map Eino interrupt behavior to kube-insight input-required events for
  missing namespace, cluster, time range, ambiguous resource identity, or future
  mutating-action approval.

## Mature Agent Optimization

- [x] Review mature agent patterns from MCP, Claude Code, Gemini CLI, and Codex
  references and capture the applicable design principles in the product doc.
- [x] Add an LLM-optimized schema DSL view for `kube_insight_schema`. Full JSON
  schema can be added later as an explicit machine format/resource if needed.
- [ ] Expose compact MCP resources for schema snapshots, SQL cookbook excerpts,
  table dictionaries, artifact payload handles, and recent run summaries so the
  model can fetch context instead of receiving large prompt dumps.
- [ ] Add a compact tool index resource with short names, purposes, input hints,
  output shape, and when-to-use guidance; defer detailed schemas until the model
  needs a specific tool.
- [ ] Standardize MCP tool output envelopes with summary text, stable IDs,
  evidence handles, row counts, truncation flags, and follow-up calls for full
  proof payloads.
  - [x] First pass: the server projects `kube_insight_search`, topology,
    history, and Service-investigation outputs into visual artifact events and
    citation events without relying on model-authored citations.
  - [x] Extend deterministic artifact projection to `kube_insight_health` and
    `kube_insight_sql` so health and SQL-only runs still produce dockable proof
    panels.
  - [x] Split candidate evidence artifacts from final answer citations: tools
    create candidate proof panels, and `citation.created` is emitted only after
    the final answer mentions stable evidence that the server verifies in the
    run's artifact registry.
  - [x] Remove legacy evidence stream chips. The final answer now carries
    inline evidence citation chips. The LLM supplies short labels through
    temporary `{{evidence: ...}}` markers, the server removes unverified labels
    and binds verified labels to `citation.created`, and the evidence list
    renders semantic summaries plus expandable table/markdown/JSON views for
    the verified supporting artifacts.
  - [x] Keep the work group expanded until a run completes. Only `answer.final`
    is treated as the final answer; earlier assistant text stays in the work
    group as visible progress notes. Private chain-of-thought/reasoning tokens
    are not surfaced as UI content.
- [ ] Add preflight and postflight hooks at the MCP/API tool layer for
  permission checks, SQL validation, output redaction, timeout control, output
  budgets, and audit records.
- [ ] Add run checkpoint and recovery semantics: retry from failed model call,
  continue from last completed tool result, branch from an earlier run, and
  preserve tool evidence IDs.
- [ ] Add session memory compaction: short rolling run summaries, pinned user
  facts, artifact references, and explicit exclusion of oversized raw tool
  outputs from future prompts.
- [ ] Add optional MCP prompts for common investigations, but keep them as
  launch templates that the model can adapt rather than fixed workflows.
- [ ] Add missing-input events for namespace, cluster, time range, destructive
  action approval, or ambiguous resource identity so the UI can ask the user
  instead of letting the agent guess.
- [x] Add a first-pass agent evaluation harness with representative questions
  for service health, OOMKilled investigation, recent changes, and topology
  mapping; score tool choice, evidence artifacts, citation coverage, answer
  terms, failed tools, and latency from replayable run events.
  - [x] Add an opt-in live LLM matrix that runs real OpenAI-compatible models
    against controlled kube-insight fake tools and scores the emitted run
    events with the same harness, including tool-call count efficiency budgets
    and runner failure reports.
  - [x] Smoke the matrix against MIMO `mimo-v2.5-pro`; the four default cases
    pass with deterministic candidate artifacts, verified answer citations, and
    tightened tool-use guidance.
  - [x] Extend the case set with SQL evidence lookup for OOM aggregate and
    exact recent-change rollups.
  - [ ] Extend the case set with history diff.
  - [ ] Add an opt-in API live smoke that submits runs through the API and
    scores returned SSE transcripts with the same harness.
- [x] Export agent tool-call duration as Prometheus histogram
  `kube_insight_agent_tool_call_duration_seconds{tool,status}` and include
  duration in tool completed/failed/audit events.
- [x] Confirm Eino ToolsNode supports parallel tool calls by default when one
  assistant message contains multiple tool calls; kube-insight keeps that
  default.
- [ ] Add provider/model diagnostics for timeout phase, token usage, tool-output
  truncation, retry count, and final-answer generation so stuck runs are
  explainable from the UI and logs.

## Frontend Foundation

- [x] Create `web/` React + TypeScript + Vite project.
- [x] Install and configure Tailwind and shadcn/ui.
- [x] Add assistant-ui and baseline chat thread/composer components.
- [x] Add Zustand store for session/run/artifact projection.
- [x] Add TanStack Query client and API helpers.
- [x] Add SSE client helper with reconnect and cancellation behavior.
- [x] Add Zod schemas for run events and artifact payloads.
- [x] Add build command that outputs `web/dist`.
- [x] Embed `web/dist` in the Go binary.

## Chat Experience

- [x] Implement search-style chat home page.
- [x] Chat composer submits to server sessions/runs first and replays SSE run
  events, with local demo fallback when the API origin is unavailable. New
  browser-created runs include client time, local time, time zone, UTC offset,
  locale, languages, and page URL metadata so the backend can orient the LLM for
  relative time phrases.
- [x] Implement run page with chat thread and composer.
- [x] Render the current session as a continuous chat history across all runs so
  submitting a follow-up does not hide earlier messages.
- [x] Implement stop, retry, copy, and new session controls in conventional chat locations.
  - Stop replaces the composer submit button while a run is active; copy and
    retry live under each assistant response; the old always-visible continue
    control was removed.
- [x] Implement tool calls as compact ordered chat-stream steps with input
  summary, output summary, status, duration, and error display. Consecutive
  tool calls collapse into one `Tool calls` group, and intermediate visible
  progress notes plus tool calls render together inside a single `Worked for ...`
  research block so the final answer stays distinct. The research block and
  grouped tool calls stay expanded by default while visible; the user can
  collapse them manually. Once the model starts streaming the formal answer,
  that answer renders outside the research block. Tool-call raw output is
  retained as an audit artifact but is not pinned into the right-side panel dock.
- [x] Render answer evidence as a collapsed count-first list. Expanding the list
  shows readable per-evidence summaries, inline detail expansion, Summary/Table/
  Markdown/JSON views when data is available, and a separate pin action for
  evidence that should stay watched in the right-side dock.
- [x] Add visible in-run liveness feedback with spinner/pulse states, current
  run stage, and approximate sent/received token counters while provider usage
  events are not available yet. Run stats are anchored above the sticky composer
  so they remain visible when long conversations scroll.
- [x] Render markdown answers with GFM tables and code blocks.
- [x] Render evidence citations and jump targets.
- [x] Render server-created evidence chips in the ordered chat stream while a
  run is active, then summarize run citations under the final assistant answer
  after completion. Clicking a citation selects the corresponding visual
  artifact and opens the right-side panel dock.
- [x] Add API/SSE coverage proving server-created artifact and citation events
  stream through `GET /api/v1/agent/runs/{run_id}/events?follow=true` in the
  shape consumed by the Web UI projection.

## Artifact Renderers

- [x] Implement `markdown` artifact renderer.
- [x] Implement `k8s.resource` renderer.
- [x] Implement `k8s.resource_list` renderer.
- [x] Implement `k8s.topology` renderer with React Flow.
- [x] Implement `k8s.history` renderer with version/history travel controls.
- [x] Implement `k8s.diff` renderer.
- [x] Implement JSON/YAML proof viewer.
- [x] Add fallback renderer for unknown artifact kinds.
- [x] Keep panel dock scoped to investigation artifacts: markdown proof panels,
  Kubernetes resources, resource lists, topology, history, and diff.
- [x] Store pinned panel workspace state in browser storage keyed by session ID:
  pinned artifact IDs, order, selected view, dock collapsed state, and watch
  interval. Do not add backend pinned-panel persistence for the first milestone;
  reconstruct panel contents from server-owned run events/artifacts/citations. The
  first implementation preserves the watch interval field for the refresh-control
  milestone.

## Dashboard

- [x] Add secondary dashboard route.
- [x] Show API/Web UI/MCP/metrics/watcher component status where available.
- [x] Show storage driver and backend target.
- [x] Add GET `/api/v1/storage/stats` for storage size, compression, table footprint, and object distribution.
- [x] Make storage stats the main dashboard section and keep collector coverage compact.
- [x] Show collector coverage summary from `/api/v1/health`; default HTTP
  response is compact, and full per-resource details require `detail=full`.
- [x] Show active/completed/failed/cancelled agent run counts from server-backed
  run summary, with browser projection fallback.
- [x] Show provider/model configuration without exposing secrets.
- [x] Link to `/healthz`, `/api/v1/health?detail=full`, `/api/v1/schema`,
  and `/metrics` when available.
- [x] Use TanStack Query polling with conservative refresh intervals.
- [x] Add `/api/v1/server/info` for secret-safe runtime configuration used by
  the dashboard.
- [x] Add GET `/api/v1/agent/runs` with status filtering, limits, and run
  summary counts for the dashboard.

## Persistence And Hardening

- [x] Add SQLite persistence for sessions, runs, and run events.
- [x] Add ClickHouse persistence for sessions, runs, and run events so
  `storage.driver=clickhouse` API servers can run stateless in Kubernetes and
  survive pod restarts without local filesystem state.
- [x] Connect API-created runs to the Eino agent runner.
  - `POST /api/v1/agent/sessions/{session_id}/runs` now starts the server-side
    runner asynchronously, and `GET /api/v1/agent/runs/{run_id}/events?follow=true`
    streams until the run reaches a terminal status.
- [x] Add cancellation propagation from API to Eino run context.
  - Running agent executions are registered by run id and `POST /cancel` now
    cancels the runner context before recording `run.cancelled`.
- [x] Add retry semantics for terminal runs.
  - `POST /api/v1/agent/runs/{run_id}/retry` creates a new run in the same
    session for completed, failed, or cancelled runs, preserves the original
    run history, copies input/provider/model, records `retryOfRunId`, and accepts
    fresh client metadata so relative-time prompts such as "recent" and
    "today" do not reuse stale browser time from the original run. Queued/running
    runs still require stop before retry. The Web UI uses `retryOfRunId` as
    branch metadata: retry replaces the original run and hides later runs in
    that visible branch instead of appending as a new conversational turn.
- [x] Add structured audit records for agent tool calls.
  - Eino tool completions now emit `tool.audit` events with run id, tool call
    id, name, input, output summary, output artifact id, status, and duration
    for later permission/audit UI without repeating full tool output.
- [x] Add provider configuration validation.
- [ ] Add optional BYOK after default env-provider path works.
- [x] Add user-facing errors for missing provider keys and unsupported providers.
  - Chat runs now surface `error`/`run.failed` messages in the ordered stream,
    and retry uses the server retry endpoint instead of resubmitting as a new prompt.


## Agent Query Efficiency

- [x] Time-scoped relative prompts.
  - Browser client context now gives the runner a concrete local/UTC time base;
    retry sends fresh metadata so "recent" and "today" are recalculated per run.
  - Agent instructions now require absolute `from`/`to` bounds for relative-time
    tool calls and SQL predicates instead of leaving broad scans unbounded.
- [x] General indexed-first investigation guidance.
  - Prompt and ClickHouse/SQLite schema notes steer models toward exact
    facts/changes/edges with cluster, kind, namespace, name, and timestamp
    predicates before raw JSON, blob, `doc`, `detail`, `ILIKE`, `LIKE`, or
    `positionCaseInsensitive` scans.
  - ClickHouse schema recipes include reusable fast paths for collector
    coverage, recent fact rollups, recent change rollups, and post-candidate
    proof queries. These are generic patterns, not Pod-only or OOM-only rules.
- [ ] Add an automated agent efficiency eval suite.
  - Suggested pass criteria for broad symptom prompts: <=5 tool calls, <=1
    zero-result broad search, every "recent" query carries a time bound, and no
    proof-document scan unless indexed facts/changes/edges were insufficient.


## Agent Retention And Compaction

- [x] Add an agent retention job for hidden retry branches and unused artifacts.
  - Successful runs trigger a default retention pass. Failed or cancelled runs do
    not trigger automatic artifact cleanup, preserving debugging context.
  - The API server runs the default retention job periodically when
    `server.agentRetention.enabled` is true. The default config runs on startup
    and every 600 seconds.
  - `POST /api/v1/agent/retention/compact` runs the same job manually. Request
    body fields are optional: `pruneSupersededRuns`,
    `pruneUnreferencedArtifacts`, and `dryRun`.
  - Retry branch compaction follows the Web UI projection semantics: when a
    completed retry replaces an earlier root run, terminal runs hidden by that
    branch replacement are pruned from `agent_runs` and `agent_run_events`.
  - Artifact compaction removes terminal-run `artifact.created` /
    `artifact.updated` events that are not referenced by any `citation.created`
    event in the same retained run. In-progress runs are skipped because
    citations may arrive after artifacts. Referenced evidence artifacts remain
    available for final-answer citations; compact `tool.completed` and
    `tool.audit` summaries remain even when full tool output artifacts are
    removed.
  - SQLite deletes rows in a transaction. ClickHouse emits `ALTER TABLE ...
    DELETE` mutations, so physical cleanup is asynchronous and should not be run
    in a tight loop.

## Validation

- [x] Run focused Go tests for API/session/agent packages.
- [x] Run frontend typecheck, lint, and build.
- [x] Run `make test`.
- [x] Run `make build`.
- [x] Smoke-test server-side Eino runner with the OpenAI-compatible MIMO test
  endpoint, including tool events, `tool.audit`, and final answer events over follow SSE.
- [x] Run `git diff --check`.
- [x] Run the 800-line Go file check.
- [x] Browser-test desktop and mobile viewports with Playwright or Chrome DevTools.
- [x] Verify the embedded binary serves the built React app.
- [x] Verify dashboard health calls work with and without metrics enabled.
  - Compose Web UI service runs Vite on 5173 and proxies `/api`, `/healthz`,
    and `/metrics` to the compose watcher/API service; verified collector
    coverage and metrics through the 5173 dev origin and allowed authproxy host.
