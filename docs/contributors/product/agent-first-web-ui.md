# Agent-First Web UI Design

Audience: product and implementation contributors. Use
[Built-in Web UI Agent Tutorial](../../users/tutorials/builtin-webui-agent.md) for the
user tutorial.

Status date: 2026-05-22

This document records the first Web UI milestone direction. The product shape is
agent-first: the first screen is a question box, the server runs an autonomous
agent, and the UI renders answers, tool progress, Kubernetes artifacts, and
historical proof. Standalone dashboards, generic evidence workbenches, and manual
explorer pages are intentionally later milestones.

## Product Direction

The Web UI should feel like a Kubernetes investigation assistant, not a generic
observability dashboard. The default flow is:

```text
open page -> ask question -> agent runs tools -> answer with evidence -> inspect artifacts
```

The agent has autonomy to decide which kube-insight tools to call. The product
should not encode a fixed incident workflow such as Service first, topology
second, history third. Structured APIs remain available as tools, and API-level
permission checks apply equally to the built-in agent, external MCP users, and
external API/skill users.

## MVP Scope

The first Web UI milestone includes:

- Agent chat home page with a search-style prompt box.
- Session and run lifecycle managed by the Go server.
- Autonomous agent loop backed by existing kube-insight read tools.
- Streaming run output to the browser.
- Tool-call timeline showing status, duration, summaries, and errors.
- Markdown answer rendering with tables, code blocks, and evidence citations.
- Kubernetes resource artifact rendering for common resources such as Pod,
  Service, EndpointSlice, Event, Node, Deployment, ConfigMap, Secret metadata,
  and RBAC resources.
- Topology artifact rendering for Service -> EndpointSlice -> Pod -> Node/Event
  and related evidence graphs.
- History travel artifacts for resource versions and topology-at-time views.
- JSON/YAML proof viewer for retained versions and diffs.
- A small server dashboard page for runtime health, collector coverage, storage
  backend status, active agent runs, and links to metrics endpoints.

The first milestone does not include:

- A standalone evidence search page.
- A standalone Service investigation page.
- A standalone topology explorer.
- A dashboard or resource health console as the primary screen.
- A free-form SQL editor in the UI.
- Fixed workflow automation for specific incident stories.

Those capabilities can still exist behind agent tools and typed artifacts. They
are not the first product surface. The dashboard is allowed as a secondary
server-status page, but it should stay operational and compact rather than
becoming a separate observability product.

## Backend Agent Runtime

The built-in agent runs in the Go server.

Decision:

- Primary runtime: CloudWeGo Eino ADK.
- Mode: autonomous ChatModelAgent.
- Tool contract: MCP-first. The built-in agent should consume kube-insight tools
  through the same MCP surface used by external agents.
- Direct Eino `tool.BaseTool` wrappers are a spike path, not the product
  contract. Because the agent is unpublished, there is no backward-compatibility
  requirement for this path; once the MCP runner shape is verified, direct
  wrappers should be deleted or moved into narrow tests instead of retained as a
  runtime fallback.
- Fallback runtime: charmbracelet/fantasy if Eino integration complexity,
  dependency cost, event mapping, or debugging overhead becomes disproportionate.
- Not selected: custom thin agent loop plus provider SDK.
- Not selected for MVP: fixed plan-execute workflows.

The agent runtime should receive a concise but directive system prompt, the user
conversation, and an allowlist of MCP-discovered kube-insight tools. It should
decide which tools to call and when to stop. kube-insight should maximize the
model's agency by giving it accurate tool descriptions, current schema context,
small and composable tool outputs, recoverable tool errors, parallel tool-call
room for independent evidence gathering, and replayable evidence.
Product-specific constraints belong in MCP tool contracts, schema resources,
artifact schemas, citations, permission checks, and output budgets rather than
in a hand-coded workflow.

The agent should not be forced through a fixed incident playbook. Mature agent
systems perform best when the model can plan and revise its own tool strategy,
while the surrounding system provides reliable primitives: discoverable tools,
clear schemas, bounded outputs, structured artifacts, durable memory, and
observable run events.

Fallback criteria for switching from Eino to Fantasy:

- Eino cannot expose enough run/tool/model events to maintain replayable
  `run.events` without brittle private hooks.
- Eino cannot consume MCP tools through a stable adapter or the adapter creates
  disproportionate transport/schema/debugging cost.
- Eino dependency shape conflicts with local-first binary size, release builds,
  or provider configuration in a way that is hard to isolate.
- Debugging failed or stalled agent runs is materially harder than with a
  simpler embeddable runtime.
- The autonomous loop becomes dominated by Eino-specific workarounds instead of
  kube-insight MCP/tool quality.

Do not switch only because Fantasy has a smaller API or because Eino needs thin
adapters. The switch is justified when integration cost threatens the first
milestone: server-owned agent chat with sessions, replayable events, evidence
citations, typed artifacts, and MCP-compatible tools.

## Session And Run Model

Because the agent loop runs on the server, sessions and runs are also owned by
the server.

A session is a user conversation. It stores messages, selected provider/model
metadata, generated artifacts, and evidence citations. A run is one agent
execution inside a session. It stores status, input, tool calls, tool results,
stream events, artifacts, final answer, timings, and errors.

Initial API shape:

```text
POST /api/v1/agent/sessions
GET  /api/v1/agent/sessions/{session_id}
POST /api/v1/agent/sessions/{session_id}/runs
GET  /api/v1/agent/runs/{run_id}/events
POST /api/v1/agent/runs/{run_id}/cancel
```

The implementation persists sessions, runs, and replayable run events in the
configured server store. SQLite remains the local-first default when the API
server has a database path. In ClickHouse mode, session/run/event control-plane
metadata is stored in ClickHouse tables alongside evidence history so API server
pods can stay stateless in Kubernetes deployments. The in-memory store remains
available only for narrow tests and no-database spikes.

ClickHouse agent metadata tables:

| Table | Purpose |
| --- | --- |
| `agent_sessions` | Conversation metadata: title, provider/model, created time, updated time. |
| `agent_runs` | One row version per run state change using `ReplacingMergeTree(updated_at)`. |
| `agent_run_events` | Replay stream for messages, tool calls, artifacts, citations, status, errors, and final answers. Agent retention may prune superseded retry-branch events and unreferenced transient artifact events after a successful replacement run. |

The API server must not depend on local filesystem state when
`storage.driver=clickhouse`. If multiple API pods run later, active run ownership,
event sequencing, and cancellation should be coordinated through a proper
runner lease or queue. The current first implementation assumes a single owner
per active run, but persisted history is already external to the server process.

## Agent Tool Architecture

MCP is the authoritative tool boundary for both the built-in agent and external
agents. kube-insight already exposes MCP over stdio, Streamable HTTP at `/mcp`,
and legacy SSE at `/sse`. The built-in Eino agent connects to this MCP surface
through the Eino MCP tool adapter over Streamable HTTP, lists allowed tools, and
passes those tools to the autonomous ChatModelAgent. Legacy SSE remains an
external compatibility transport, but it is not the built-in agent path because
the current `mark3labs/mcp-go` SSE client can hang on shutdown against the
`modelcontextprotocol/go-sdk` SSE handler.

Why MCP-first:

- One contract for internal agent, external MCP clients, and future skill/API
  users.
- One place for permission checks, redaction, RBAC inheritance, SQL safety,
  auditing, output budgets, and future tenant controls.
- Runtime tool discovery instead of duplicated native Eino wrappers.
- Less schema drift between API, MCP, docs, and the agent prompt.
- Easier external compatibility: the same tools that make the UI agent useful
  are available to Claude Desktop, Codex, custom agents, and CI assistants.

Current implementation status:

- The MCP server currently exposes `kube_insight_schema`, `kube_insight_sql`,
  `kube_insight_health`, `kube_insight_search`, `kube_insight_history`,
  `kube_insight_topology`, and `kube_insight_service_investigation`.
- The built-in Eino runner loads tools through the Eino MCP adapter over
  Streamable HTTP. It no longer wires direct native Go tool wrappers in the
  server runtime.
- The old native Go wrappers proved the spike path and have been deleted from
  the unpublished runtime. MCP is now the only built-in agent tool surface.

Initial MCP tool set:

| Tool | Purpose |
| --- | --- |
| `kube_insight_schema` | Return a compact LLM-oriented schema DSL with active backend, dialect notes, useful tables, columns, indexes, relationships, and recipes. Must be available before SQL. |
| `kube_insight_health` | Return a compact collector coverage and staleness summary plus a bounded list of problematic resource streams before current-state claims. |
| `kube_insight_search` | Find candidate objects from facts, changes, retained documents, and indexed evidence. |
| `kube_insight_history` | Load retained versions, observations, diffs, and proof metadata for one object. |
| `kube_insight_topology` | Load related objects and edges for an object or evidence target. |
| `kube_insight_service_investigation` | Load a typed Service investigation bundle when the target is a Service. |
| `kube_insight_sql` | Run guarded read-only SQL for advanced joins using the schema-reported backend dialect. |

Tool design rules:

- Tools should be intent-oriented and composable, not a one-to-one dump of every
  HTTP endpoint.
- Tool descriptions must say when to use the tool, when not to use it, required
  evidence inputs, output shape, and limits.
- SQL is an escape hatch. The model must call `kube_insight_schema` before SQL
  unless schema context is already present in the run, and SQL prompts/tool
  descriptions must explicitly avoid assuming SQLite tables on ClickHouse-compatible
  backends.
- Discovery should start compact: health before current-state claims, search
  before topology/history, and Service investigation only after an exact Service
  namespace/name is known.
- Tools should default to compact outputs: summary, ranked candidates, stable
  IDs, counts, timestamps, and artifact handles. The current MCP tools already
  compact schema, health, search, topology, and Service investigation outputs;
  raw retained documents or large proof payloads should be opt-in or represented
  as artifacts/resources.
- Kubernetes proof should prefer YAML in human-facing renderers, while tool
  payloads remain structured JSON for model consumption.
- Every important claim should be traceable to object identity, observation,
  version, fact, change, edge, SQL row, or artifact ID.
- Evidence references must not depend on the LLM remembering to cite its own
  claims. The server-side runner projects structured MCP tool outputs into
  `artifact.created` candidate artifacts, verifies which artifacts are actually
  used by the final answer, then emits `citation.created` events for those
  artifacts. The LLM may provide a short human-readable label with temporary
  `{{evidence: ...}}` markers near a supported claim; the server only preserves
  that label when it can bind the claim to a verified artifact. The rendered
  answer uses inline evidence chips such as `OOM pod history` near the supported
  claim, and the evidence list provides expandable semantic summaries instead
  of disconnected stream chips.
- Tool-call raw output is an audit trail, not a user-facing proof panel. The
  panel dock should only pin visual investigation artifacts such as Kubernetes
  resource lists, individual resources, topology, history, and diffs.
- The work group should stay expanded while the run is still investigating or
  has not produced a completed final answer. Assistant text before
  `answer.final` is a visible progress note; private chain-of-thought or
  provider reasoning tokens are not displayed or relied on.
- Each client-created run should include browser client context such as send
  time, local time, time zone, UTC offset, locale, and page URL. The server
  injects this as orientation for interpreting relative time phrases; it is not
  evidence about Kubernetes state.
- The system should let the LLM plan freely, but tool contracts must make the
  high-quality path obvious and the dangerous path hard.

MCP resources and prompts should be used where they help without constraining
the agent loop:

- Resources: schema snapshots, SQL cookbook excerpts, table dictionaries,
  investigation playbook references, and artifact payloads that the model can
  request instead of receiving by default.
- Prompts: optional user-triggered investigation starters such as service
  health, recent changes, OOM/restart, webhook failure, and topology mapping.
  These are templates, not forced workflows.

The built-in agent should prefer structured tools and cite exact objects,
versions, changes, facts, edges, and SQL rows where possible.

## Mature Agent Optimization Review

This design should borrow the parts of mature coding and operations agents that
increase model autonomy without losing control of safety, cost, or evidence.
Codex, Claude Code, Gemini CLI, and MCP converge on the same pattern: expose
powerful tools, make context discoverable, keep outputs bounded, record what
happened, and let the model choose the path.

Optimization principles:

- Tool discovery over static prompt stuffing. MCP tools should be discoverable
  at run time and returned in deterministic order for cache friendliness. Avoid
  injecting every tool schema into every prompt when a smaller tool index or
  tool-search layer can defer details until needed.
- Resources for context, tools for actions. Schema, SQL cookbooks, investigation
  notes, and large artifacts should be MCP resources. Tools should perform
  bounded actions and return IDs, summaries, and links to resources/artifacts.
- LLM-optimized schema views. The default `kube_insight_schema` MCP tool output
  is a compact DSL for the model. If machines or UI validation need full JSON
  schema later, expose it as a separate format/resource rather than making the
  agent consume raw database JSON by default. The DSL should include backend,
  dialect, tables, key columns, join hints, common predicates, and examples.
- Compact outputs by default. Large results should page, summarize, or persist
  as artifacts. Tool outputs should include counts, top candidates, stable IDs,
  timestamps, and proof handles instead of full documents unless explicitly
  requested.
- Human-visible tool execution. The UI should make exposed tools, tool calls,
  visible assistant progress notes, parameters, status, duration, outputs, and
  errors visible. Current read-only tools can run automatically, but future
  mutating tools require confirmation. Tool calls should appear as compact
  ordered stream steps interleaved with assistant text, not as a separate
  activity box that breaks the response flow.
  Tool failures are recoverable model context: the tool message must tell the
  model what failed so it can revise SQL, choose another tool, or retry instead
  of immediately failing the whole run.
- Continuous liveness feedback. Long runs must always show visible progress:
  spinner or pulse states for active assistant/tool steps, current run stage,
  and sent/received token counters. Until provider usage callbacks are wired,
  the UI may show clearly approximate token counts derived from streamed text
  and compact tool summaries.
- Permission and validation hooks at the tool layer. SQL read-only checks,
  namespace/resource allowlists, redaction, and future RBAC should live in
  MCP/API policy hooks before tool execution and in audit records after tool
  execution.
- Transcript-first memory/context. Persist the session/run event log as the
  replayable source of truth. `run.created` is lifecycle-only; each
  server-started run stores its actual model request as `completion.request` and
  non-cumulative model-visible turns as `completion.message` /
  `completion.tool_result`. Normal follow-up prompts are reconstructed from that
  ordered transcript instead of selective summaries. Keep stable product
  instructions in repo docs. Use explicit compaction, selected artifact
  references, and subagent/tool-output condensation only above that transcript
  layer, and do not inject oversized raw tool outputs into future prompts unless
  checkpoint resume requires the exact tool message.
- Checkpoints and recovery. Long agent runs should be recoverable. Store every
  run event, tool call, error, and artifact so a user can inspect, retry,
  continue, or branch from a failed run.
- Optional prompts as starters, not workflows. MCP prompts can provide service
  health, OOM/restart, recent changes, topology, and webhook-failure starters.
  They should help the model enter a good search space without forcing a fixed
  plan.
- Elicitation for missing user input. If a tool cannot proceed because it needs
  a namespace, time range, cluster, or approval, use an explicit input-required
  event or MCP elicitation-style request instead of guessing.
- Evaluation loop. Maintain a small suite of representative investigation
  questions and expected evidence properties. Use it to test prompt/tool/schema
  changes before declaring the agent usable.

## Eino Advanced Capability Plan

Use Eino as more than a simple tool-calling loop, but only in places where the
capability directly improves autonomy, observability, or context control. The
first production shape should preserve these extension points even if some are
implemented after MCP migration.

Adopt in the first hardening pass:

- Runner events as the source for replayable `run.events`. The server should map
  model deltas, tool calls, tool results, interrupts, errors, and final output
  from Eino runner events into kube-insight events instead of inventing a
  parallel lifecycle.
- Checkpoint and resume shape. Persist enough state around run steps and tool
  evidence so failed final-answer generation can retry without re-running all
  completed tools.
- ChatModelAgent middleware for context pressure. Prefer Eino-supported
  summarization, tool-output reduction, and tool-search middleware where it fits
  instead of hand-rolling prompt compaction.
- MCP tools as the normal tool source. Eino should receive tools discovered from
  the kube-insight MCP server, while MCP resources carry schema and large
  evidence context.
- Callbacks for observability. Token usage, tool latency, model latency,
  truncation, retries, and provider errors should be collected from Eino
  callbacks/runner events and surfaced in run diagnostics. Agent tool-call
  latency is also exported as the Prometheus histogram
  `kube_insight_agent_tool_call_duration_seconds` with `tool` and `status`
  labels.

Pre-wire, but do not overuse in the first UI milestone:

- `AgentAsTool` / `NewAgentTool` for specialist subagents. The first specialist
  is an evidence condenser that accepts artifact/resource handles and returns
  compact findings with citations when raw output is too large for the main
  agent. Broad triage can also use `parallel_investigation` to run 2-3 bounded
  specialist branches concurrently and return compact branch findings.
- `SetSubAgents` transfer for interactive specialist handoff remains a future
  option if the model needs to delegate an investigation rather than call a
  bounded analysis tool.
- Supervisor or DeepAgents style orchestration for broad multi-step work, such
  as cluster-wide incident triage. This should remain optional because fixed
  orchestration can reduce model autonomy if introduced too early.
- Interrupt hooks for missing input and future approvals. Read-only tools can
  run automatically, but ambiguous resource identity, missing time ranges, and
  future mutating actions should interrupt and ask the user.

Avoid for now:

- A custom workflow engine around Eino. If we need planning, task tracking,
  summarization, or subagents, first evaluate the Eino ADK mechanism that
  already owns the agent runtime.
- Passing large tool outputs through the main agent just because the UI can
  render them. Large data should become resources/artifacts, then be summarized
  by tools or specialist agents.
- Treating subagents as permission boundaries. Permission checks still belong in
  MCP/API tools; subagents only improve context management and reasoning focus.

Version note: the repo currently uses `github.com/cloudwego/eino v0.8.13`. The
advanced ADK and middleware APIs must be verified against this version before
implementation; if a feature is only available in a newer release, upgrade Eino
deliberately and validate the runner, MCP adapter, and OpenAI model adapter
together. Since this agent has not been released, prefer the cleaner runtime
shape over preserving spike-era APIs.

Agent-friendly schema resource sketch:

```text
backend: clickhouse
dialect: clickhouse

rule: call kube_insight_schema before kube_insight_sql unless this schema is already in context
rule: prefer facts/changes/edges for candidate discovery; use versions only for proof after narrowing

table facts
  purpose: queryable incident evidence and extracted status facts
  key_columns: cluster_id, object_id, kind, namespace, name, fact_key, fact_value, ts, severity
  use_for: OOMKilled, restarts, event reasons, readiness, node pressure, service load balancer facts
  common_filters:
    - fact_value = 'OOMKilled'
    - fact_key like '%restart%'
    - fact_key in ('k8s_event.reason', 'pod.container.last_termination_reason')

table observations
  purpose: object sighting timeline and current/deleted state proof
  key_columns: cluster_id, object_id, kind, namespace, name, observed_at, observation_type, resource_version

join hints:
  facts.object_id -> observations.object_id
  edges.src_id/dst_id -> observations.object_id
```

The DSL view is the default model-facing schema because it encodes operational
meaning and query strategy, not only columns. A full machine JSON schema can be
reintroduced as an explicit format or separate resource when the UI or external
clients need it.

## Frontend Stack

Decision:

- Visual system follows the nowake.ai Cloud Index direction: calm technical
  surfaces, evidence-oriented hierarchy, restrained AI treatment, 6-8px radii,
  and accessible 44px interactive targets.
- App framework: React + TypeScript + Vite.
- UI system: shadcn/ui + Tailwind + Radix primitives.
- Chat UI: assistant-ui.
- Chat runtime integration: assistant-ui ExternalStoreRuntime with a local state
  store fed by kube-insight server events.
- Client state: Zustand for session/run projection and selected artifacts.
- API cache/state: TanStack Query for non-streaming API calls.
- Markdown: react-markdown + remark-gfm.
- Topology: React Flow.
- JSON/YAML proof viewing: CodeMirror.
- Schema validation: Zod for event and artifact contracts.

Vercel AI SDK is not the primary chat runtime because the backend is Go/Eino and
should own session, run, tool, and streaming semantics. It can be revisited only
if its transport protocol becomes useful without forcing the server shape.
CopilotKit and AG-UI are also deferred until the product needs generative UI or
agent-driven UI manipulation beyond typed artifacts.

## Dashboard Shape

The Web UI may include a simple dashboard page for operators who need to confirm
the local or in-cluster server is healthy before trusting agent answers. This is
a secondary route, not the default entry point.

The dashboard should show:

- API, Web UI, MCP, metrics, and watcher component status.
- Storage driver and backend target, including SQLite path, ClickHouse database,
  or chDB database where applicable.
- Storage size, physical compression ratio where the backend exposes it, table
  footprint, and object distribution by retained document bytes.
- Compact collector coverage summary from `/api/v1/health`, including stale,
  error, skipped, queued, and not-started resource counts. Full per-resource
  stream details require `detail=full` and are intended for dashboard/debug use,
  not default agent context.
- Active, completed, failed, and cancelled agent runs once agent sessions exist.
- Current provider/model configuration without exposing API keys.
- Links to `/healthz`, `/api/v1/health?detail=full`, `/api/v1/schema`, and
  `/metrics` when the metrics listener is enabled.

The dashboard should use TanStack Query for periodic refresh. It should not use
free-form SQL, broad dashboards, or standalone investigation workflows in the
first milestone.

## Chat UI Shape

The first screen should be sparse and search-like: brand mark, one prompt box,
minimal model/provider status, and recent sessions if available. After submit,
the UI moves into an investigation run view.

The run view contains:

- Chat thread for user messages and assistant answers.
- Navigation to recent sessions and the compact server dashboard.
- Compact tool-call stream steps interleaved with visible assistant progress
  notes in the exact response order. Intermediate notes and tool-call groups
  live inside the `Worked for ...` research block; the final answer renders
  outside that block.
- Run liveness row with current stage and approximate sent/received token
  counters until backend provider usage events are available.
- Artifact panel for selected Kubernetes resource, topology, history, diff, or
  proof view.
- Citation affordances render as a collapsed evidence list below the answer.
  Expanding the list shows readable evidence summaries, per-item inline detail
  views such as Summary/Table/Markdown/JSON, and an explicit pin action for
  evidence that should stay visible in the right-side dock.
- Stop, retry, and continue controls.

Pinned panel state is a browser workspace preference, not server control-plane
state. The first implementation should store pinned panel IDs, order, selected
artifact/view, dock collapsed state, and watch interval in browser storage keyed
by session ID. The server remains authoritative for sessions, runs, replayable
events, artifacts, and citations; the browser can reconstruct a pinned panel by
loading the session run events. Do not add backend pinned-panel tables or APIs
until there is a multi-user/shared-workspace requirement.

assistant-ui provides the thread, composer, message primitives, markdown
surface, and tool/data rendering extension points. kube-insight owns the typed
artifact renderers.

## Artifact Contract

The server should stream typed artifacts instead of only markdown. Artifact data
must be renderable by the UI and useful to external clients.

Initial artifact kinds:

| Kind | Renderer |
| --- | --- |
| `markdown` | Rich answer or report section. |
| `k8s.resource` | One Kubernetes object summary plus proof links. |
| `k8s.resource_list` | Ranked or grouped resource candidates. |
| `k8s.topology` | React Flow graph backed by retained topology edges. |
| `k8s.history` | Version selector, observation trail, and optional document view. |
| `k8s.diff` | Version-to-version retained document diff. |
| `tool_call` | Tool input, output summary, duration, and status. |
| `citation` | Link from answer text to fact, edge, change, version, SQL row, or artifact. |

Example event sketch:

```json
{
  "type": "artifact.created",
  "runId": "run_123",
  "artifact": {
    "id": "artifact_456",
    "kind": "k8s.topology",
    "title": "Service default/api topology",
    "data": {}
  }
}
```

The exact event field names can be finalized during implementation, but events
should remain typed, append-only, and easy to replay into UI state.

The initial replayable event contract uses these event names:

| Event | Purpose |
| --- | --- |
| `run.created` | Run record was created and queued. |
| `run.started` | Agent execution started. |
| `run.status` | Run status changed without a terminal result. |
| `run.completed` | Run completed successfully. |
| `run.failed` | Run failed with an error. |
| `run.cancelled` | Run was cancelled. |
| `message.created` | User or assistant message was created. Tool results are not modeled as chat messages. |
| `message.delta` | Streaming text delta for a message. |
| `message.completed` | Message content is complete. |
| `usage.delta` | Future provider usage update for exact sent/received token counts and phase diagnostics. |
| `answer.final` | Final assistant answer for the run. |
| `tool.started` | Tool call started. |
| `tool.completed` | Tool call completed with output summary. |
| `tool.failed` | Tool call failed. |
| `tool.audit` | Normalized tool audit record with input, output summary, output artifact id, status, and duration. |
| `artifact.created` | Typed artifact was created. |
| `artifact.updated` | Existing artifact changed, such as history travel state. |
| `citation.created` | Verified citation target for a final-answer evidence marker was created. |
| `error` | Non-tool runtime or provider error. |

Payload shapes live in the `internal/agent` package so API handlers, the Eino
runner, SSE replay, and the React event store share one contract.

## Provider And Key Strategy

The server should support a default provider configured by environment variables
and later optional BYOK.

Initial direction:

- Default provider/model/base URL env configured in server config and environment variables.
- BYOK can be added as a runtime session option, passed to the server for that
  session/run without being persisted by default.
- Provider selection is a server concern because the server owns agent runtime,
  tool execution, and audit context.

Configuration shape can evolve from the existing `server.chat` block:

```yaml
server:
  chat:
    enabled: true
    provider: openai
    apiKeyEnv: OPENAI_API_KEY
    baseUrlEnv: OPENAI_BASE_URL
    model: gpt-5.2
    maxIterations: 32
```

## Packaging And Release

The preferred release shape is a local-first embedded Web UI:

```text
web/ React app -> build -> web/dist -> embed.FS -> Go binary
```

This keeps the normal local binary self-contained and makes Docker packaging use
the same artifact. A future deployment mode can optionally serve external static
files for development or downstream customization, but the default should be
embedded.

## Implementation Phases

1. Replace the static Web UI spike with a formal `web/` React project and build
   pipeline.
2. Add agent session/run API skeleton with SSE events and SQLite persistence.
3. Integrate Eino ADK with direct kube-insight tools only as a spike to prove
   autonomous run execution and replayable events.
4. Migrate the built-in Eino agent to consume kube-insight through MCP tools.
5. Bring MCP tool parity to the agent surface: schema, SQL, health, search,
   history, topology, Service investigation, typed artifacts, and compact output
   budgets.
6. Add assistant-ui chat home and run page backed by server events.
7. Add Markdown, Kubernetes resource, topology, and history artifact renderers.
8. Add the compact server dashboard backed by existing health/schema/metrics
   endpoints and later agent run status.
9. Add provider config and then optional BYOK.
10. Harden cancellation, retries, errors, audit records, citation validation,
    MCP permission policy, and provider timeout/output-budget handling.

## Open Implementation Details

No major product or technology decision blocks the next design step. Remaining
details should be resolved during implementation:

- Exact Eino MCP adapter transport: decided as Streamable HTTP through the
  Eino MCP adapter. Local SSE is legacy compatibility only; stdio can remain for
  external MCP clients and CLI-style integration.
- Compatibility between `github.com/modelcontextprotocol/go-sdk` server and the
  MCP client expected by `github.com/cloudwego/eino-ext/components/tool/mcp` is
  verified on Streamable HTTP.
- Exact Eino event callback mapping after MCP tool calls replace native Eino
  tools. Eino ToolsNode already executes multiple tool calls from the same
  assistant message in parallel by default; keep this default unless a specific
  kube-insight tool requires sequential execution.
- Exact SSE event schema and replay semantics.
- Exact artifact JSON schemas and citation ID format.
- Tool output budget defaults and large-payload artifact/resource handoff.
- First provider adapter and model defaults, including configurable model
  timeout.
- Whether assistant-ui uses ExternalStoreRuntime directly or a thin custom
  runtime wrapper.
- Whether BYOK lands in the first implementation or the second increment.
- Exact dashboard refresh intervals and whether metrics are embedded in the Web
  UI server or linked to the separate metrics listener.

## References

- MCP specification: https://modelcontextprotocol.io/specification/
- MCP tools: https://modelcontextprotocol.io/specification/draft/server/tools
- MCP resources: https://modelcontextprotocol.io/specification/draft/server/resources
- MCP prompts: https://modelcontextprotocol.io/specification/draft/server/prompts
- CloudWeGo Eino MCP tool adapter: https://cloudwego.cn/docs/eino/ecosystem/tool/tool_mcp/
- CloudWeGo Eino ADK: https://www.cloudwego.io/docs/eino/core_modules/eino_adk/
- CloudWeGo Eino Supervisor Agent: https://www.cloudwego.io/docs/eino/core_modules/eino_adk/agent_implementation/supervisor/
- CloudWeGo Eino DeepAgents: https://www.cloudwego.io/docs/eino/core_modules/eino_adk/agent_implementation/deepagents/
- CloudWeGo Eino Summarization Middleware: https://www.cloudwego.io/docs/eino/core_modules/eino_adk/eino_adk_chatmodelagentmiddleware/middleware_summarization/
- CloudWeGo Eino tool ecosystem index: https://www.cloudwego.io/zh/docs/eino/ecosystem_integration/tool/
- Claude Code MCP docs: https://code.claude.com/docs/en/mcp
- Claude Code subagents docs: https://code.claude.com/docs/en/sub-agents
- Gemini CLI commands and MCP inspection: https://google-gemini.github.io/gemini-cli/docs/cli/commands.html
- Gemini CLI configuration and hierarchical context: https://google-gemini.github.io/gemini-cli/docs/get-started/configuration.html
- Gemini CLI checkpointing: https://google-gemini.github.io/gemini-cli/docs/checkpointing.html
- OpenAI Codex repo and docs entry point: https://github.com/openai/codex
