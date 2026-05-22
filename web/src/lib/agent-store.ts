import { create } from "zustand"

import type { AgentRunDTO, AgentRunEventDTO, AgentSessionDTO } from "@/lib/agent-schemas"

export type AgentRunStatus = "queued" | "running" | "completed" | "failed" | "cancelled"

export type AgentArtifactKind =
  | "markdown"
  | "k8s.resource"
  | "k8s.resource_list"
  | "k8s.topology"
  | "k8s.history"
  | "k8s.diff"
  | "tool_call"
  | "citation"

export type AgentSession = {
  id: string
  title: string
  createdAt: string
  updatedAt: string
  runIds: string[]
}

export type AgentRun = {
  id: string
  sessionId: string
  status: AgentRunStatus
  input: string
  finalAnswer?: string
  error?: string
  createdAt: string
  updatedAt: string
  eventIds: string[]
  artifactIds: string[]
  citationIds: string[]
}

export type AgentRunEvent = {
  id: string
  runId: string
  type: string
  createdAt: string
  data?: unknown
}

export type AgentArtifact = {
  id: string
  runId: string
  kind: AgentArtifactKind
  title?: string
  data?: unknown
  createdAt: string
  updatedAt: string
}

export type AgentCitation = {
  id: string
  runId: string
  artifactId?: string
  text?: string
  target?: unknown
  createdAt: string
}

type AddRunEventInput = {
  id?: string
  type: string
  data?: unknown
}

type UpsertArtifactInput = {
  id?: string
  kind: AgentArtifactKind
  title?: string
  data?: unknown
}

type AddCitationInput = {
  id?: string
  artifactId?: string
  text?: string
  target?: unknown
}

type UpsertServerOptions = {
  activate?: boolean
}

type AgentProjectionState = {
  activeSessionId?: string
  selectedArtifactId?: string
  sessionOrder: string[]
  sessions: Record<string, AgentSession>
  runs: Record<string, AgentRun>
  events: Record<string, AgentRunEvent>
  artifacts: Record<string, AgentArtifact>
  citations: Record<string, AgentCitation>
  ensureSession: (title: string) => string
  startRun: (sessionId: string, input: string) => string
  upsertServerSession: (session: AgentSessionDTO, options?: UpsertServerOptions) => void
  upsertServerRun: (run: AgentRunDTO, options?: UpsertServerOptions) => void
  applyServerEvent: (event: AgentRunEventDTO) => void
  appendRunEvent: (runId: string, input: AddRunEventInput) => string
  completeRun: (runId: string, finalAnswer: string) => void
  failRun: (runId: string, error: string) => void
  cancelRun: (runId: string) => void
  upsertArtifact: (runId: string, input: UpsertArtifactInput) => string
  addCitation: (runId: string, input: AddCitationInput) => string
  selectArtifact: (artifactId?: string) => void
  selectSession: (sessionId?: string) => void
  startNewSession: () => void
  reset: () => void
}

const initialProjection = {
  activeSessionId: undefined,
  selectedArtifactId: undefined,
  sessionOrder: [],
  sessions: {},
  runs: {},
  events: {},
  artifacts: {},
  citations: {},
}

export const useAgentProjectionStore = create<AgentProjectionState>((set, get) => ({
  ...initialProjection,

  ensureSession: (title) => {
    const state = get()
    if (state.activeSessionId && state.sessions[state.activeSessionId]) {
      const now = nowISO()
      set((current) => ({
        sessions: {
          ...current.sessions,
          [state.activeSessionId!]: {
            ...current.sessions[state.activeSessionId!],
            updatedAt: now,
          },
        },
      }))
      return state.activeSessionId
    }

    const id = newProjectionId("session")
    const now = nowISO()
    const session: AgentSession = {
      id,
      title: sessionTitle(title),
      createdAt: now,
      updatedAt: now,
      runIds: [],
    }
    set((current) => ({
      activeSessionId: id,
      sessionOrder: [id, ...current.sessionOrder],
      sessions: { ...current.sessions, [id]: session },
    }))
    return id
  },

  startRun: (sessionId, input) => {
    const id = newProjectionId("run")
    const now = nowISO()
    const run: AgentRun = {
      id,
      sessionId,
      status: "running",
      input,
      createdAt: now,
      updatedAt: now,
      eventIds: [],
      artifactIds: [],
      citationIds: [],
    }
    set((current) => ({
      sessions: {
        ...current.sessions,
        [sessionId]: {
          ...current.sessions[sessionId],
          updatedAt: now,
          runIds: [...(current.sessions[sessionId]?.runIds ?? []), id],
        },
      },
      runs: { ...current.runs, [id]: run },
    }))
    get().appendRunEvent(id, { type: "run.started", data: { input } })
    return id
  },

  upsertServerSession: (session, options = {}) => {
    const activate = options.activate ?? true
    set((current) => {
      const previous = current.sessions[session.id]
      const runIds = uniqueValues([
        ...(previous?.runIds ?? []),
        ...(session.runs ?? []).map((run) => run.id),
      ])
      return {
        activeSessionId: activate ? session.id : current.activeSessionId,
        sessionOrder: uniquePrepend(current.sessionOrder, session.id),
        sessions: {
          ...current.sessions,
          [session.id]: {
            id: session.id,
            title: session.title || previous?.title || "New investigation",
            createdAt: session.createdAt,
            updatedAt: session.updatedAt,
            runIds,
          },
        },
      }
    })
    for (const run of session.runs ?? []) get().upsertServerRun(run, { activate })
  },

  upsertServerRun: (run, options = {}) => {
    const activate = options.activate ?? true
    const now = nowISO()
    set((current) => {
      const previous = current.runs[run.id]
      const session = current.sessions[run.sessionId] ?? {
        id: run.sessionId,
        title: "Server session",
        createdAt: run.createdAt,
        updatedAt: run.createdAt,
        runIds: [],
      }
      return {
        activeSessionId: activate ? run.sessionId : current.activeSessionId,
        sessionOrder: uniquePrepend(current.sessionOrder, run.sessionId),
        sessions: {
          ...current.sessions,
          [run.sessionId]: {
            ...session,
            updatedAt: run.completedAt ?? run.startedAt ?? run.createdAt,
            runIds: uniqueAppend(session.runIds, run.id),
          },
        },
        runs: {
          ...current.runs,
          [run.id]: {
            id: run.id,
            sessionId: run.sessionId,
            status: run.status,
            input: run.input,
            error: run.error,
            createdAt: run.createdAt,
            updatedAt: run.completedAt ?? run.startedAt ?? now,
            finalAnswer: previous?.finalAnswer,
            eventIds: previous?.eventIds ?? [],
            artifactIds: previous?.artifactIds ?? [],
            citationIds: previous?.citationIds ?? [],
          },
        },
      }
    })
  },

  applyServerEvent: (event) => {
    set((current) => {
      if (current.events[event.id]) return current
      const previousRun = current.runs[event.runId]
      const status = runStatusFromEvent(event) ?? previousRun?.status ?? "running"
      const finalAnswer = finalAnswerFromEvent(event) ?? previousRun?.finalAnswer
      const run: AgentRun = previousRun ?? {
        id: event.runId,
        sessionId: runSessionFromEvent(event) ?? "server",
        status,
        input: "",
        createdAt: event.createdAt,
        updatedAt: event.createdAt,
        eventIds: [],
        artifactIds: [],
        citationIds: [],
      }
      const artifact = artifactFromEvent(event)
      const citation = citationFromEvent(event)
      return {
        events: {
          ...current.events,
          [event.id]: {
            id: event.id,
            runId: event.runId,
            type: event.type,
            createdAt: event.createdAt,
            data: event.data,
          },
        },
        runs: {
          ...current.runs,
          [event.runId]: {
            ...run,
            status,
            error: errorFromEvent(event) ?? run.error,
            finalAnswer,
            updatedAt: event.createdAt,
            eventIds: uniqueAppend(run.eventIds, event.id),
            artifactIds: artifact ? uniqueAppend(run.artifactIds, artifact.id) : run.artifactIds,
            citationIds: citation ? uniqueAppend(run.citationIds, citation.id) : run.citationIds,
          },
        },
        artifacts: artifact ? { ...current.artifacts, [artifact.id]: artifact } : current.artifacts,
        citations: citation ? { ...current.citations, [citation.id]: citation } : current.citations,
      }
    })
  },

  appendRunEvent: (runId, input) => {
    const id = input.id ?? newProjectionId("event")
    const now = nowISO()
    const event: AgentRunEvent = { id, runId, type: input.type, createdAt: now, data: input.data }
    set((current) => ({
      events: { ...current.events, [id]: event },
      runs: {
        ...current.runs,
        [runId]: {
          ...current.runs[runId],
          updatedAt: now,
          eventIds: [...(current.runs[runId]?.eventIds ?? []), id],
        },
      },
    }))
    return id
  },

  completeRun: (runId, finalAnswer) => {
    const now = nowISO()
    set((current) => ({
      runs: {
        ...current.runs,
        [runId]: {
          ...current.runs[runId],
          status: "completed",
          finalAnswer,
          updatedAt: now,
        },
      },
    }))
    get().appendRunEvent(runId, { type: "run.completed", data: { finalAnswer } })
  },

  failRun: (runId, error) => {
    const now = nowISO()
    set((current) => ({
      runs: {
        ...current.runs,
        [runId]: {
          ...current.runs[runId],
          status: "failed",
          error,
          updatedAt: now,
        },
      },
    }))
    get().appendRunEvent(runId, { type: "run.failed", data: { error } })
  },

  cancelRun: (runId) => {
    const now = nowISO()
    set((current) => ({
      runs: {
        ...current.runs,
        [runId]: {
          ...current.runs[runId],
          status: "cancelled",
          updatedAt: now,
        },
      },
    }))
    get().appendRunEvent(runId, { type: "run.cancelled" })
  },

  upsertArtifact: (runId, input) => {
    const id = input.id ?? newProjectionId("artifact")
    const now = nowISO()
    const artifact: AgentArtifact = {
      id,
      runId,
      kind: input.kind,
      title: input.title,
      data: input.data,
      createdAt: get().artifacts[id]?.createdAt ?? now,
      updatedAt: now,
    }
    set((current) => ({
      artifacts: { ...current.artifacts, [id]: artifact },
      runs: {
        ...current.runs,
        [runId]: {
          ...current.runs[runId],
          updatedAt: now,
          artifactIds: uniqueAppend(current.runs[runId]?.artifactIds ?? [], id),
        },
      },
    }))
    get().appendRunEvent(runId, { type: "artifact.created", data: artifact })
    return id
  },

  addCitation: (runId, input) => {
    const id = input.id ?? newProjectionId("citation")
    const now = nowISO()
    const citation: AgentCitation = {
      id,
      runId,
      artifactId: input.artifactId,
      text: input.text,
      target: input.target,
      createdAt: now,
    }
    set((current) => ({
      citations: { ...current.citations, [id]: citation },
      runs: {
        ...current.runs,
        [runId]: {
          ...current.runs[runId],
          updatedAt: now,
          citationIds: uniqueAppend(current.runs[runId]?.citationIds ?? [], id),
        },
      },
    }))
    get().appendRunEvent(runId, { type: "citation.created", data: citation })
    return id
  },

  selectArtifact: (artifactId) => set({ selectedArtifactId: artifactId }),
  selectSession: (sessionId) => set({ activeSessionId: sessionId, selectedArtifactId: undefined }),
  startNewSession: () => set({ activeSessionId: undefined, selectedArtifactId: undefined }),
  reset: () => set(initialProjection),
}))


function sessionTitle(input: string) {
  const title = input.trim().replace(/\s+/g, " ")
  if (!title) return "New investigation"
  return title.length > 64 ? `${title.slice(0, 61)}...` : title
}

function uniqueAppend(values: string[], value: string) {
  return values.includes(value) ? values : [...values, value]
}

function nowISO() {
  return new Date().toISOString()
}

function newProjectionId(prefix: string) {
  if (globalThis.crypto?.randomUUID) return `${prefix}_${globalThis.crypto.randomUUID()}`
  return `${prefix}_${Date.now()}_${Math.random().toString(36).slice(2)}`
}

function uniqueValues(values: string[]) {
  return Array.from(new Set(values))
}

function uniquePrepend(values: string[], value: string) {
  return [value, ...values.filter((item) => item !== value)]
}

function runStatusFromEvent(event: AgentRunEventDTO): AgentRunStatus | undefined {
  if (event.type === "answer.final") return "completed"
  if (!event.type.startsWith("run.")) return undefined
  const status = eventDataRecord(event.data).status
  return isRunStatus(status) ? status : undefined
}

function runSessionFromEvent(event: AgentRunEventDTO) {
  const sessionId = eventDataRecord(event.data).sessionId
  return typeof sessionId === "string" ? sessionId : undefined
}

function errorFromEvent(event: AgentRunEventDTO) {
  const data = eventDataRecord(event.data)
  if (typeof data.error === "string") return data.error
  if (typeof data.message === "string" && event.type === "error") return data.message
  return undefined
}

function finalAnswerFromEvent(event: AgentRunEventDTO) {
  if (event.type !== "answer.final" && event.type !== "run.completed") return undefined
  const data = eventDataRecord(event.data)
  if (typeof data.content === "string") return data.content
  if (typeof data.finalAnswer === "string") return data.finalAnswer
  return undefined
}

function artifactFromEvent(event: AgentRunEventDTO): AgentArtifact | undefined {
  if (event.type !== "artifact.created" && event.type !== "artifact.updated") return undefined
  const data = eventDataRecord(event.data)
  const value = eventDataRecord(data.artifact ?? data)
  if (typeof value.id !== "string" || typeof value.kind !== "string") return undefined
  return {
    id: value.id,
    runId: event.runId,
    kind: value.kind as AgentArtifactKind,
    title: typeof value.title === "string" ? value.title : undefined,
    data: value.data,
    createdAt: event.createdAt,
    updatedAt: event.createdAt,
  }
}

function citationFromEvent(event: AgentRunEventDTO): AgentCitation | undefined {
  if (event.type !== "citation.created") return undefined
  const data = eventDataRecord(event.data)
  const value = eventDataRecord(data.citation ?? data)
  if (typeof value.id !== "string") return undefined
  return {
    id: value.id,
    runId: event.runId,
    artifactId: typeof value.artifactId === "string" ? value.artifactId : undefined,
    text: typeof value.text === "string" ? value.text : undefined,
    target: value.target,
    createdAt: event.createdAt,
  }
}

function eventDataRecord(value: unknown): Record<string, unknown> {
  return value && typeof value === "object" ? value as Record<string, unknown> : {}
}

function isRunStatus(value: unknown): value is AgentRunStatus {
  return value === "queued" || value === "running" || value === "completed" || value === "failed" || value === "cancelled"
}
