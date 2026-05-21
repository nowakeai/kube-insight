# Agent-First Web UI Checklist

Status date: 2026-05-21

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
- [x] Visual style follows the nowake.ai Design Guide Cloud Index direction.
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
- [x] Wrap `kube_insight_health` as the first tool.
- [x] Wrap `kube_insight_search` as the second tool.
- [x] Add history, topology, service investigation, and guarded SQL tools.
  - [x] Wrap `kube_insight_history`.
  - [x] Wrap `kube_insight_topology`.
  - [x] Wrap `kube_insight_service_investigation`.
  - [x] Wrap guarded `kube_insight_sql`.
- [x] Map Eino events/tool callbacks into kube-insight run events.
- [x] Enforce answer citation expectations through prompt/tool descriptions and
  artifact contracts.
- [x] Document fallback criteria for switching to Fantasy if Eino integration
  becomes too heavy.

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
  events, with local demo fallback when the API origin is unavailable.
- [x] Implement run page with chat thread and composer.
- [x] Implement stop, retry, continue, and new session controls.
- [x] Implement tool timeline with input summary, output summary, status, duration,
  and error display.
- [x] Render markdown answers with GFM tables and code blocks.
- [x] Render evidence citations and jump targets.

## Artifact Renderers

- [x] Implement `markdown` artifact renderer.
- [x] Implement `k8s.resource` renderer.
- [x] Implement `k8s.resource_list` renderer.
- [x] Implement `k8s.topology` renderer with React Flow.
- [x] Implement `k8s.history` renderer with version/history travel controls.
- [x] Implement `k8s.diff` renderer.
- [x] Implement JSON/YAML proof viewer.
- [x] Add fallback renderer for unknown artifact kinds.

## Dashboard

- [x] Add secondary dashboard route.
- [x] Show API/Web UI/MCP/metrics/watcher component status where available.
- [x] Show storage driver and backend target.
- [x] Show collector coverage summary from `/api/v1/health`.
- [x] Show active/completed/failed/cancelled agent run counts from server-backed
  run summary, with browser projection fallback.
- [x] Show provider/model configuration without exposing secrets.
- [x] Link to `/healthz`, `/api/v1/health`, `/api/v1/schema`, and `/metrics` when
  available.
- [x] Use TanStack Query polling with conservative refresh intervals.
- [x] Add `/api/v1/server/info` for secret-safe runtime configuration used by
  the dashboard.
- [x] Add GET `/api/v1/agent/runs` with status filtering, limits, and run
  summary counts for the dashboard.

## Persistence And Hardening

- [x] Add SQLite persistence for sessions, runs, and run events.
- [x] Connect API-created runs to the Eino agent runner.
  - `POST /api/v1/agent/sessions/{session_id}/runs` now starts the server-side
    runner asynchronously, and `GET /api/v1/agent/runs/{run_id}/events?follow=true`
    streams until the run reaches a terminal status.
- [x] Add cancellation propagation from API to Eino run context.
  - Running agent executions are registered by run id and `POST /cancel` now
    cancels the runner context before recording `run.cancelled`.
- [ ] Add retry semantics for failed runs.
- [ ] Add structured audit records for agent tool calls.
- [x] Add provider configuration validation.
- [ ] Add optional BYOK after default env-provider path works.
- [x] Add user-facing errors for missing provider keys and unsupported providers.

## Validation

- [x] Run focused Go tests for API/session/agent packages.
- [x] Run frontend typecheck, lint, and build.
- [x] Run `make test`.
- [x] Run `make build`.
- [x] Smoke-test server-side Eino runner with the OpenAI-compatible MIMO test
  endpoint, including tool events and final answer events over follow SSE.
- [x] Run `git diff --check`.
- [x] Run the 800-line Go file check.
- [x] Browser-test desktop and mobile viewports with Playwright or Chrome DevTools.
- [x] Verify the embedded binary serves the built React app.
- [ ] Verify dashboard health calls work with and without metrics enabled.
  - Verified without a metrics endpoint on the Vite dev origin; metrics-enabled
    verification remains.
