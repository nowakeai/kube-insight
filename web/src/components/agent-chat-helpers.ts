import type { AppendMessage, ThreadMessage } from "@assistant-ui/react"

import { getAgentRunEvents } from "@/lib/agent-api"
import type { AgentRunDTO, AgentRunEventDTO } from "@/lib/agent-schemas"
import { useAgentProjectionStore, type AgentRun } from "@/lib/agent-store"
import type { AgentRunRoute } from "@/components/agent-chat-stream"

export function statusFromServerEvent(event: AgentRunEventDTO) {
  if (event.type === "answer.final") return "completed"
  const data = event.data && typeof event.data === "object" ? event.data as { status?: unknown } : {}
  return typeof data.status === "string" ? data.status : undefined
}

export function isTerminalRunStatus(status: string) {
  return status === "completed" || status === "failed" || status === "cancelled"
}

export function isActiveRunStatus(status: string) {
  return status === "queued" || status === "running"
}

export async function hydrateSiblingRunEvents(
  runs: AgentRunDTO[],
  activeRunID: string,
  signal: AbortSignal,
  applyServerEvents: (events: AgentRunEventDTO[]) => void,
) {
  const pendingRuns = runs.filter((run) => run.id !== activeRunID && !runHydrationLooksComplete(run.id))
  const concurrency = 4
  for (let index = 0; index < pendingRuns.length && !signal.aborted; index += concurrency) {
    const batch = pendingRuns.slice(index, index + concurrency)
    const results = await Promise.allSettled(batch.map((run) => getAgentRunEvents(run.id, { signal })))
    for (const result of results) {
      if (signal.aborted) return
      if (result.status === "rejected") continue
      applyServerEvents(result.value)
    }
  }
}

function runHydrationLooksComplete(runID: string) {
  const run = useAgentProjectionStore.getState().runs[runID]
  if (!run || !isTerminalRunStatus(run.status)) return false
  return Boolean(run.finalAnswer || run.eventIds.length > 0)
}

export function messageUpdateFromEvent(event: AgentRunEventDTO) {
  if (event.type !== "message.created" && event.type !== "message.delta" && event.type !== "message.completed" && event.type !== "answer.final") return {}
  const data = event.data && typeof event.data === "object" ? event.data as { role?: unknown; content?: unknown; delta?: unknown } : {}
  if (data.role && data.role !== "assistant") return {}
  return {
    content: typeof data.content === "string" ? data.content : undefined,
    delta: typeof data.delta === "string" ? data.delta : undefined,
  }
}

export function errorMessageFromEvent(event: AgentRunEventDTO) {
  const data = event.data && typeof event.data === "object" ? event.data as { error?: unknown; message?: unknown } : {}
  if (typeof data.error === "string" && data.error) return data.error
  if (event.type === "error" && typeof data.message === "string" && data.message) return data.message
  return undefined
}

export function queuedRunMessage(status: string, error?: string) {
  if (status === "failed") return error ? `Run failed: ${error}` : "Run failed before an answer was produced."
  if (status === "cancelled") return "Run cancelled."
  return "Run accepted by the server. Backend agent execution is not connected yet, so this run is waiting for the next server-side agent loop step."
}

export function appendMessageText(message: AppendMessage) {
  return message.content
    .filter((part) => part.type === "text")
    .map((part) => part.text)
    .join("\n")
    .trim()
}

export function threadMessagesFromRun(run: AgentRun): ThreadMessage[] {
  const assistantStatus = assistantStatusFromRunStatus(run.status)
  const assistantText = run.finalAnswer || (assistantStatus === "running" ? "" : queuedRunMessage(run.status, run.error))
  return [
    userMessageWithID(`user_${run.id}`, run.input, run.createdAt),
    assistantMessage(`assistant_${run.id}`, assistantText, assistantStatus),
  ]
}

export function threadMessagesFromHydratedRun(
  projectedRun: AgentRun | undefined,
  serverRun: AgentRunDTO | undefined,
  events: AgentRunEventDTO[],
): ThreadMessage[] {
  const runID = projectedRun?.id ?? serverRun?.id ?? "server"
  const input = projectedRun?.input || serverRun?.input || ""
  const createdAt = projectedRun?.createdAt ?? serverRun?.createdAt
  const status = runStatusFromHydration(projectedRun, serverRun, events)
  const error = latestErrorFromEvents(events) ?? projectedRun?.error ?? serverRun?.error
  const answer = assistantTextFromEvents(events) || projectedRun?.finalAnswer || ""
  const assistantStatus = assistantStatusFromRunStatus(status)
  const assistantText = answer || (assistantStatus === "running" ? "" : queuedRunMessage(status, error))

  return [
    userMessageWithID(`user_${runID}`, input, createdAt),
    assistantMessage(`assistant_${runID}`, assistantText, assistantStatus),
  ]
}

export function runStatusFromHydration(
  projectedRun: AgentRun | undefined,
  serverRun: AgentRunDTO | undefined,
  events: AgentRunEventDTO[],
) {
  for (const event of [...events].reverse()) {
    const status = statusFromServerEvent(event)
    if (status) return status
  }
  return projectedRun?.status ?? serverRun?.status ?? "running"
}

function assistantTextFromEvents(events: AgentRunEventDTO[]) {
  let text = ""
  for (const event of events) {
    const update = messageUpdateFromEvent(event)
    if (update.content !== undefined) text = update.content
    if (update.delta !== undefined) text += update.delta
  }
  return text
}

function latestErrorFromEvents(events: AgentRunEventDTO[]) {
  for (const event of [...events].reverse()) {
    const error = errorMessageFromEvent(event)
    if (error) return error
  }
  return undefined
}

function assistantStatusFromRunStatus(status: string): "running" | "complete" | "cancelled" {
  if (status === "cancelled") return "cancelled"
  if (status === "running" || status === "queued") return "running"
  return "complete"
}

export function userMessage(text: string): ThreadMessage {
  return userMessageWithID(newMessageID("user"), text)
}

export function userMessageWithID(id: string, text: string, createdAt?: string): ThreadMessage {
  return {
    id,
    role: "user",
    createdAt: createdAt ? new Date(createdAt) : new Date(),
    content: [{ type: "text", text }],
    attachments: [],
    metadata: { custom: {} },
  }
}

export function assistantMessage(
  id: string,
  text: string,
  status: "running" | "complete" | "cancelled",
): ThreadMessage {
  return {
    id,
    role: "assistant",
    createdAt: new Date(),
    content: text ? [{ type: "text", text }] : [],
    status:
      status === "complete"
        ? { type: "complete", reason: "stop" }
        : status === "cancelled"
          ? { type: "incomplete", reason: "cancelled" }
          : { type: "running" },
    metadata: {
      unstable_state: null,
      unstable_annotations: [],
      unstable_data: [],
      steps: [],
      custom: {},
    },
  }
}

export function latestRunningRunID() {
  const state = useAgentProjectionStore.getState()
  const activeSession = state.activeSessionId ? state.sessions[state.activeSessionId] : undefined
  if (!activeSession) return undefined
  for (const runID of [...activeSession.runIds].reverse()) {
    const run = state.runs[runID]
    if (run && isActiveRunStatus(run.status)) return runID
  }
  return undefined
}

export function newMessageID(prefix: string) {
  if (globalThis.crypto?.randomUUID) return `${prefix}_${globalThis.crypto.randomUUID()}`
  return `${prefix}_${Date.now()}_${Math.random().toString(36).slice(2)}`
}

export function delay(ms: number) {
  return new Promise((resolve) => window.setTimeout(resolve, ms))
}

export function readRouteRun(): AgentRunRoute | undefined {
  const match = window.location.pathname.match(/^\/sessions\/([^/]+)\/runs\/([^/]+)$/)
  if (!match) return undefined
  return {
    sessionID: decodeURIComponent(match[1]),
    runID: decodeURIComponent(match[2]),
  }
}

export function openRunPage(sessionID: string, runID: string) {
  const nextPath = `/sessions/${encodeURIComponent(sessionID)}/runs/${encodeURIComponent(runID)}`
  if (window.location.pathname === nextPath) return
  window.history.pushState({}, "", nextPath)
}
