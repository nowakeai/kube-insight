# Agent-First Web UI Design

Status date: 2026-05-20

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
- Mode: autonomous ChatModelAgent with kube-insight tools.
- Fallback runtime: charmbracelet/fantasy if Eino integration complexity,
  dependency cost, event mapping, or debugging overhead becomes disproportionate.
- Not selected: custom thin agent loop plus provider SDK.
- Not selected for MVP: fixed plan-execute workflows.

The agent runtime should receive a concise system prompt, the user conversation,
and an allowlist of kube-insight tools. It should decide which tools to call and
when to stop. The product-specific constraints live in tool descriptions,
artifact schemas, and citation requirements rather than in a hand-coded workflow.

Fallback criteria for switching from Eino to Fantasy:

- Eino cannot expose enough run/tool/model events to maintain replayable
  `run.events` without brittle private hooks.
- Eino tool schemas require disproportionate adapter code compared with the
  kube-insight API/tool surface.
- Eino dependency shape conflicts with local-first binary size, release builds,
  or provider configuration in a way that is hard to isolate.
- Debugging failed or stalled agent runs is materially harder than with a
  simpler embeddable runtime.
- The autonomous loop becomes dominated by Eino-specific workarounds instead of
  kube-insight tool quality.

Do not switch only because Fantasy has a smaller API or because Eino needs thin
adapters. The switch is justified when integration cost threatens the first
milestone: server-owned agent chat with sessions, replayable events, evidence
citations, and typed artifacts.

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

The implementation persists sessions, runs, and replayable run events in SQLite
when the API server has a database path. The in-memory store remains available
for narrow tests and no-database spikes. SQLite is the preferred persistence
backend for session/run control-plane metadata because this state is small and
local-first. Evidence history remains in the existing evidence store.

## Agent Tools

The built-in agent should call the same read surfaces exposed through API and
MCP. Permissions, redaction, RBAC inheritance, SQL safety, and audit controls
belong at this API/tool layer rather than inside the agent framework.

Initial tools:

| Tool | Purpose |
| --- | --- |
| `kube_insight_health` | Check collector coverage and staleness before making current-state claims. |
| `kube_insight_search` | Find candidate objects from facts, changes, retained JSON, and indexed evidence. |
| `kube_insight_history` | Load retained versions, observations, diffs, and proof metadata for one object. |
| `kube_insight_topology` | Load related objects and edges for an object or evidence target. |
| `kube_insight_service_investigation` | Load the typed Service investigation bundle when useful. |
| `kube_insight_sql` | Run guarded read-only SQL for advanced joins and backend-specific recipes. |

The agent should prefer structured tools and cite exact objects, versions,
changes, facts, edges, and SQL rows where possible.

## Frontend Stack

Decision:

- Visual system follows `/home/coder/code/nowake-ai/website/DESIGN.md`: Cloud Index palette, calm technical surfaces, evidence-oriented hierarchy, restrained AI treatment, 6-8px radii, and accessible 44px interactive targets.
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
- Collector coverage summary from `/api/v1/health`, including stale, error,
  skipped, queued, and not-started resource counts.
- Active, completed, failed, and cancelled agent runs once agent sessions exist.
- Current provider/model configuration without exposing API keys.
- Links to `/healthz`, `/api/v1/health`, `/api/v1/schema`, and `/metrics` when
  the metrics listener is enabled.

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
- Tool timeline for active/completed tool calls.
- Artifact panel for selected Kubernetes resource, topology, history, diff, or
  proof view.
- Citation affordances that jump from answer text to exact proof artifacts.
- Stop, retry, and continue controls.

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
| `message.created` | Assistant/user/tool message was created. |
| `message.delta` | Streaming text delta for a message. |
| `message.completed` | Message content is complete. |
| `answer.final` | Final assistant answer for the run. |
| `tool.started` | Tool call started. |
| `tool.completed` | Tool call completed with output summary. |
| `tool.failed` | Tool call failed. |
| `artifact.created` | Typed artifact was created. |
| `artifact.updated` | Existing artifact changed, such as history travel state. |
| `citation.created` | Citation target was created. |
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
3. Integrate Eino ADK with one or two kube-insight read tools in a spike run.
4. Add assistant-ui chat home and run page backed by server events.
5. Add Markdown, Kubernetes resource, topology, and history artifact renderers.
6. Add the compact server dashboard backed by existing health/schema/metrics
   endpoints and later agent run status.
7. Add provider config and then optional BYOK.
8. Harden cancellation, retries, errors, audit records, and citation validation.

## Open Implementation Details

No major product or technology decision blocks the next design step. Remaining
details should be resolved during implementation:

- Exact Eino package boundaries and event callback mapping.
- Exact SSE event schema and replay semantics.
- Exact artifact JSON schemas and citation ID format.
- First provider adapter and model defaults.
- Whether assistant-ui uses ExternalStoreRuntime directly or a thin custom
  runtime wrapper.
- Whether BYOK lands in the first implementation or the second increment.
- Exact dashboard refresh intervals and whether metrics are embedded in the Web
  UI server or linked to the separate metrics listener.
