# Agent-First Web UI Checklist

Status date: 2026-05-20

This checklist tracks the first Web UI milestone. Keep it updated during
development so handoffs show what is done, what is in progress, and what should
happen next.

## Design Decisions

- [x] Product shape is agent-first, not dashboard-first or evidence-workbench-first.
- [x] Opening screen is a search-style chat prompt.
- [x] Built-in agent loop runs in the Go server.
- [x] Server owns sessions, runs, cancellation, and streaming events.
- [x] Primary agent runtime is CloudWeGo Eino ADK.
- [x] Fallback agent runtime is charmbracelet/fantasy if Eino complexity is too high.
- [x] Frontend app stack is React + TypeScript + Vite.
- [x] UI stack is shadcn/ui + Tailwind + Radix.
- [x] Chat UI foundation is assistant-ui.
- [x] Client state uses Zustand for run/artifact UI projection.
- [x] Server data fetching uses TanStack Query for non-streaming API calls.
- [x] Topology artifacts use React Flow.
- [x] JSON/YAML proof views use CodeMirror.
- [x] Formal artifact kinds are part of the Web UI contract.
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
  and `server.chat.model`.
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

- [ ] Add Eino dependency after checking the current compatible version.
- [ ] Build a minimal autonomous ChatModelAgent spike.
- [ ] Wrap `kube_insight_health` as the first tool.
- [ ] Wrap `kube_insight_search` as the second tool.
- [ ] Add history, topology, service investigation, and guarded SQL tools.
- [ ] Map Eino events/tool callbacks into kube-insight run events.
- [ ] Enforce answer citation expectations through prompt/tool descriptions and
  artifact contracts.
- [ ] Document fallback criteria for switching to Fantasy if Eino integration
  becomes too heavy.

## Frontend Foundation

- [ ] Create `web/` React + TypeScript + Vite project.
- [ ] Install and configure Tailwind and shadcn/ui.
- [ ] Add assistant-ui and baseline chat thread/composer components.
- [ ] Add Zustand store for session/run/artifact projection.
- [ ] Add TanStack Query client and API helpers.
- [ ] Add SSE client helper with reconnect and cancellation behavior.
- [ ] Add Zod schemas for run events and artifact payloads.
- [ ] Add build command that outputs `web/dist`.
- [ ] Embed `web/dist` in the Go binary.

## Chat Experience

- [ ] Implement search-style chat home page.
- [ ] Implement run page with chat thread and composer.
- [ ] Implement stop, retry, continue, and new session controls.
- [ ] Implement tool timeline with input summary, output summary, status, duration,
  and error display.
- [ ] Render markdown answers with GFM tables and code blocks.
- [ ] Render evidence citations and jump targets.

## Artifact Renderers

- [ ] Implement `markdown` artifact renderer.
- [ ] Implement `k8s.resource` renderer.
- [ ] Implement `k8s.resource_list` renderer.
- [ ] Implement `k8s.topology` renderer with React Flow.
- [ ] Implement `k8s.history` renderer with version/history travel controls.
- [ ] Implement `k8s.diff` renderer.
- [ ] Implement JSON/YAML proof viewer.
- [ ] Add fallback renderer for unknown artifact kinds.

## Dashboard

- [ ] Add secondary dashboard route.
- [ ] Show API/Web UI/MCP/metrics/watcher component status where available.
- [ ] Show storage driver and backend target.
- [ ] Show collector coverage summary from `/api/v1/health`.
- [ ] Show active/completed/failed/cancelled agent run counts after runs exist.
- [ ] Show provider/model configuration without exposing secrets.
- [ ] Link to `/healthz`, `/api/v1/health`, `/api/v1/schema`, and `/metrics` when
  available.
- [ ] Use TanStack Query polling with conservative refresh intervals.

## Persistence And Hardening

- [ ] Add SQLite persistence for sessions and runs.
- [ ] Add cancellation propagation from API to Eino run context.
- [ ] Add retry semantics for failed runs.
- [ ] Add structured audit records for agent tool calls.
- [ ] Add provider configuration validation.
- [ ] Add optional BYOK after default env-provider path works.
- [ ] Add user-facing errors for missing provider keys and unsupported models.

## Validation

- [ ] Run focused Go tests for API/session/agent packages.
- [ ] Run frontend typecheck, lint, and build.
- [ ] Run `make test`.
- [ ] Run `make build`.
- [ ] Run `git diff --check`.
- [ ] Run the 800-line Go file check.
- [ ] Browser-test desktop and mobile viewports with Playwright or Chrome DevTools.
- [ ] Verify the embedded binary serves the built React app.
- [ ] Verify dashboard health calls work with and without metrics enabled.
