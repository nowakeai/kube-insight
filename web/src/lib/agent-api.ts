import type { QueryFunctionContext } from "@tanstack/react-query"

import type { AgentRunStatus } from "@/lib/agent-store"

export type AgentMessageDTO = {
  id: string
  role: "user" | "assistant" | "system" | "tool"
  content: string
  runId?: string
  createdAt: string
  metadata?: unknown
}

export type AgentRunDTO = {
  id: string
  sessionId: string
  status: AgentRunStatus
  input: string
  provider?: string
  model?: string
  createdAt: string
  startedAt?: string
  completedAt?: string
  error?: string
  metadata?: unknown
}

export type AgentSessionDTO = {
  id: string
  title?: string
  provider?: string
  model?: string
  createdAt: string
  updatedAt: string
  messages?: AgentMessageDTO[]
  runs?: AgentRunDTO[]
}

export type AgentRunEventDTO = {
  id: string
  runId: string
  sequence: number
  type: string
  createdAt: string
  data?: unknown
}

export type CreateAgentSessionRequest = {
  title?: string
  provider?: string
  model?: string
}

export type CreateAgentRunRequest = {
  input: string
  provider?: string
  model?: string
  metadata?: unknown
}

export type AgentAPIOptions = {
  baseURL?: string
  signal?: AbortSignal
  fetcher?: typeof fetch
}

export class AgentAPIError extends Error {
  readonly status: number
  readonly body: string

  constructor(status: number, body: string) {
    super(`kube-insight API request failed with status ${status}`)
    this.name = "AgentAPIError"
    this.status = status
    this.body = body
  }
}

export const agentQueryKeys = {
  all: ["agent"] as const,
  sessions: () => [...agentQueryKeys.all, "sessions"] as const,
  session: (sessionId: string) => [...agentQueryKeys.sessions(), sessionId] as const,
  runs: () => [...agentQueryKeys.all, "runs"] as const,
  runEvents: (runId: string) => [...agentQueryKeys.runs(), runId, "events"] as const,
}

export function createAgentSession(input: CreateAgentSessionRequest = {}, options?: AgentAPIOptions) {
  return agentRequestJSON<AgentSessionDTO>("/api/v1/agent/sessions", {
    ...options,
    method: "POST",
    body: input,
  })
}

export function getAgentSession(sessionId: string, options?: AgentAPIOptions) {
  return agentRequestJSON<AgentSessionDTO>(`/api/v1/agent/sessions/${encodeURIComponent(sessionId)}`, options)
}

export function createAgentRun(sessionId: string, input: CreateAgentRunRequest, options?: AgentAPIOptions) {
  return agentRequestJSON<AgentRunDTO>(`/api/v1/agent/sessions/${encodeURIComponent(sessionId)}/runs`, {
    ...options,
    method: "POST",
    body: input,
  })
}

export function cancelAgentRun(runId: string, options?: AgentAPIOptions) {
  return agentRequestJSON<AgentRunDTO>(`/api/v1/agent/runs/${encodeURIComponent(runId)}/cancel`, {
    ...options,
    method: "POST",
    body: {},
  })
}

export async function getAgentRunEvents(runId: string, options?: AgentAPIOptions) {
  const response = await agentFetch(`/api/v1/agent/runs/${encodeURIComponent(runId)}/events`, options)
  const text = await response.text()
  if (!response.ok) throw new AgentAPIError(response.status, text)
  return parseRunEventsSSE(text)
}

export function agentSessionQuery(sessionId: string, options?: AgentAPIOptions) {
  return {
    queryKey: agentQueryKeys.session(sessionId),
    queryFn: ({ signal }: QueryFunctionContext) => getAgentSession(sessionId, { ...options, signal }),
  }
}

export function agentRunEventsQuery(runId: string, options?: AgentAPIOptions) {
  return {
    queryKey: agentQueryKeys.runEvents(runId),
    queryFn: ({ signal }: QueryFunctionContext) => getAgentRunEvents(runId, { ...options, signal }),
  }
}

export async function agentRequestJSON<T>(
  path: string,
  options: AgentAPIOptions & { method?: string; body?: unknown } = {},
): Promise<T> {
  const response = await agentFetch(path, options)
  const text = await response.text()
  if (!response.ok) throw new AgentAPIError(response.status, text)
  if (!text) return undefined as T
  return JSON.parse(text) as T
}

export function parseRunEventsSSE(text: string) {
  const events: AgentRunEventDTO[] = []
  for (const block of text.split(/\n\n+/)) {
    const dataLines = block
      .split("\n")
      .filter((line) => line.startsWith("data:"))
      .map((line) => line.slice("data:".length).trimStart())
    if (dataLines.length === 0) continue
    events.push(JSON.parse(dataLines.join("\n")) as AgentRunEventDTO)
  }
  return events
}

export async function agentFetch(path: string, options: AgentAPIOptions & { method?: string; body?: unknown } = {}) {
  const fetcher = options.fetcher ?? fetch
  const response = await fetcher(agentURL(path, options.baseURL), {
    method: options.method ?? "GET",
    signal: options.signal,
    headers: options.body === undefined ? undefined : { "Content-Type": "application/json" },
    body: options.body === undefined ? undefined : JSON.stringify(options.body),
  })
  return response
}

export function agentURL(path: string, baseURL = defaultAgentBaseURL()) {
  const normalizedBase = baseURL.replace(/\/$/, "")
  return `${normalizedBase}${path}`
}

function defaultAgentBaseURL() {
  return import.meta.env.VITE_KUBE_INSIGHT_API_BASE_URL ?? ""
}
