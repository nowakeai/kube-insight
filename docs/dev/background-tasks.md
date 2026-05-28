# Background Tasks

This note records background and timer-driven work in the current runtime. Keep
it updated when adding a new loop, periodic refresh, cleanup job, or long-lived
goroutine.

## Ownership Rules

- Long-lived loops must have one owner that starts and stops them.
- API-server loops should be started from `api.NewServer` and stopped from
  `Server.Close`.
- MCP-server loops should be started lazily by MCP tool use or server startup
  and stopped from `mcp.Server.Close`.
- Collector watch loops are owned by the caller's context. They should exit when
  the watch command or service context is cancelled.
- Periodic cleanup must be idempotent. A successful agent run may trigger an
  opportunistic pass, but the periodic server-owned pass is still the safety net.
- Wait loops used only to stream one HTTP response are request-scoped behavior,
  not background jobs.

## Current Tasks

| Task | Owner | Start | Stop | Interval | Purpose |
| --- | --- | --- | --- | --- | --- |
| Agent run goroutine | API server | `startAgentRun` after create/retry | run completion, cancellation, or `Server.Close` | none | Executes one Eino run and records run events. |
| Agent retention loop | API server | `NewServer` when `server.agentRetention.enabled` config passes an interval | `Server.Close` | default `600s` | Prunes hidden retry branches, unreferenced agent artifacts, and expired session scratch stores. |
| Opportunistic agent retention | API server | after a successful agent run | same call returns | none | Runs the same default retention pass so completed replacement branches and expired scratch stores compact promptly. |
| MCP health refresh loop | MCP server | first `kube_insight_health` call registers cached options | `mcp.Server.Close` | `10s` refresh, `15s` TTL | Refreshes previously used health reports and cluster display maps without reopening storage for every high-frequency tool call. |
| Collector resource watch workers | collector caller | `WatchResourcesClientGo` | caller context cancellation, timeout, max events, or unrecoverable error | retry backoff `500ms` to `30s` by default | Performs initial list, then watch streams with bounded concurrency and stream start staggering. |

## Request-Scoped Wait Loops

These loops use timers but should not be treated as independent scheduled jobs:

- Agent SSE follow polling: `/api/v1/agent/runs/{run_id}/events?follow=1` polls
  the store every `250ms` while the HTTP request is open.
- Final SSE flush: after a terminal run status, the API polls every `25ms` for up
  to `1s` to flush the terminal event if storage writes arrive slightly later.
- Collector retry sleep: watch reconnect backoff waits on the caller context and
  is part of the watch worker lifecycle.

## Configuration

`server.agentRetention` controls the only API-server periodic cleanup loop:

```yaml
server:
  agentRetention:
    enabled: true
    intervalSeconds: 600
    runOnStart: true
```

The MCP health cache intervals are currently internal constants because they are
a small performance guard for high-frequency tool calls, not a product retention
policy.

Agent scratch data is server-owned temporary state under
`$TMP/kube-insight-agent-scratch/{session_id}`. Default agent retention protects
sessions with queued or running runs, and removes scratch directories whose most
recent file or directory timestamp is older than 24 hours. Manual compaction can
override the scratch TTL with `scratchMaxAgeSeconds`.

## Open Follow-Ups

- Decide whether dashboard runtime visibility should expose last agent retention
  pass, last health refresh, and watcher last-observed timestamps from one
  server-info endpoint or from separate store-backed endpoints.
