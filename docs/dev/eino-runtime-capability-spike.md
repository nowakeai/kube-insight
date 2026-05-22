# Eino Runtime Capability Spike

Status date: 2026-05-21

This note records the current Eino ADK capability boundary for the agent-first
Web UI work. It is intentionally a spike record, not a product contract. The
agent has not been released, so there is no backward-compatibility requirement
for spike-era agent internals. The product contract remains kube-insight
sessions/runs/events plus MCP tools and resources.

## Spike Isolation Rules

- Do not let direct native Eino tools become the long-term tool surface. They
  are allowed only to prove runner behavior while MCP parity and the Eino MCP
  adapter are being verified, then they should be deleted or moved into narrow
  tests.
- Keep kube-insight API, MCP, session, run, event, artifact, and audit contracts
  independent of Eino-specific types. Eino types should stay behind the
  `internal/agent` runner boundary.
- Any spike-only direct tool path must have a named replacement path in this
  checklist before more behavior is added to it. Prefer replacing the path over
  layering compatibility switches around it.
- Prefer Eino ADK extension points before writing custom orchestration: runner
  events, callbacks, checkpoint store, ChatModelAgent handlers, tool reduction,
  summarization, AgentAsTool, subagents, and interrupts.
- Do not use subagents as permission boundaries. Permissions, SQL safety,
  redaction, output budgets, and audit belong in MCP/API tool execution.
- Do not pass large raw tool output through the main agent just because the UI
  can display it. Large data should be stored as artifacts/resources and
  summarized or condensed before being returned to the model.

## Current Local Dependency Findings

The repo currently uses `github.com/cloudwego/eino v0.8.13`. Local module
inspection shows these APIs are available in that version:

| Capability | Local evidence | Initial use |
| --- | --- | --- |
| Runner events | `adk.Runner.Run` returns `*AsyncIterator[*adk.AgentEvent]` | Use as the source of replayable `run.events`. |
| Streaming | `adk.RunnerConfig.EnableStreaming` | Enable once UI/server event mapping handles deltas correctly. |
| Checkpoint store | `adk.RunnerConfig.CheckPointStore`, `Get(ctx,id)`, `Set(ctx,id,bytes)` | Add a SQLite-backed checkpoint adapter for interrupt/resume and failed-run recovery. |
| Resume | `Runner.Resume`, `Runner.ResumeWithParams` | Support continue/retry from checkpoint after interruption or partial failure. |
| ChatModelAgent handlers | `ChatModelAgentConfig.Handlers []adk.ChatModelAgentMiddleware` | Use for context control, tool-output handling, diagnostics, and future dynamic tool selection. |
| Model retry | `ChatModelAgentConfig.ModelRetryConfig` | Evaluate for provider transient failures before hand-written retry logic. |
| Tool reduction | `adk/middlewares/reduction.New` | Offload or truncate large tool outputs into resource/artifact handles. |
| Summarization | `adk/middlewares/summarization.New` | Compact long session/run context after MCP output budgets are in place. |
| Plan task | `adk/middlewares/plantask.New` | Defer; useful for visible multi-step plans, but not required for first agent-first UI. |
| Agent as tool | `adk.NewAgentTool` | Use later for an evidence-condenser subagent when compact output is still too large. |
| Subagent transfer | `adk.SetSubAgents` | Defer to broader incident triage; not the default first milestone path. |
| Supervisor/DeepAgents | `adk/prebuilt/supervisor`, `adk/prebuilt/deep` | Keep as future modes, not the first implementation. |
| Interrupt | `AgentAction.Interrupted`, `Runner.ResumeWithParams` | Map to kube-insight input-required events for missing context or future approvals. |

The Eino MCP tool adapter is now present in `go.mod` as
`github.com/cloudwego/eino-ext/components/tool/mcp`. Focused tests verify that
it can list and call kube-insight MCP tools through Streamable HTTP against the
existing `github.com/modelcontextprotocol/go-sdk` server. Legacy SSE is not the
built-in agent transport because the `mark3labs/mcp-go` SSE client can leave the
connection active during shutdown against the go-sdk SSE handler.

## Recommended Adoption Order

1. Keep native direct tools out of the product runtime. Any remaining native
   wrappers are temporary test fixtures or MCP parity references only.
2. Keep the Eino capability test harness exercising runner events, callbacks,
   streaming, checkpoint store, resume, reduction middleware, and one
   AgentAsTool subagent with fake models/tools.
3. Use the verified Eino MCP adapter over Streamable HTTP to list and call the
   existing MCP tools in the built-in agent runtime.
4. Keep the runner configuration shape able to accept tool providers, handlers,
   checkpoint store, and diagnostics sinks without exposing Eino types outside
   `internal/agent`.
5. Fill MCP parity gaps, then delete direct native wrappers that are no longer
   useful as focused fixtures.
6. Add MCP resources and compact output envelopes before enabling reduction or
   summarization middleware for production runs.
7. Add checkpoint persistence and resume semantics to the server API, then map
   final-model-call timeout recovery to continue/retry behavior.
8. Add the evidence-condenser subagent as a normal tool only if artifact handles
   plus compact outputs still leave the main model with too much context.

## Design Implications

- `run.events` must be richer than simple message/tool events. It needs room for
  model diagnostics, interrupt/input-required events, checkpoint IDs, subagent
  boundaries, truncation/offload notices, and artifact/resource handles.
- The session store should treat Eino checkpoints as opaque bytes. kube-insight
  can own checkpoint IDs, lifecycle, and retention without depending on Eino
  internal serialization.
- Tool output truncation should produce a stable artifact/resource handle that
  the UI can render and the agent can re-open later. Plain file-path offload is
  not enough for product behavior.
- The first compact MCP outputs are implemented directly in tool contracts:
  `kube_insight_schema` returns an LLM-oriented schema DSL and
  `kube_insight_health` returns a compact summary with bounded problem
  resources. Continue this pattern before relying on generic summarization.
- Subagent events should include parent run id, parent tool call id, subagent
  name, input artifact handles, compact findings, citations, status, and
  duration.
- The default agent remains autonomous MCP tool use. Supervisor, DeepAgents, and
  plan-task middleware are advanced modes that should be enabled only after the
  simple loop is reliable.
