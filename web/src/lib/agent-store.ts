import { create } from "zustand"

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
  appendRunEvent: (runId: string, input: AddRunEventInput) => string
  completeRun: (runId: string, finalAnswer: string) => void
  failRun: (runId: string, error: string) => void
  cancelRun: (runId: string) => void
  upsertArtifact: (runId: string, input: UpsertArtifactInput) => string
  addCitation: (runId: string, input: AddCitationInput) => string
  selectArtifact: (artifactId?: string) => void
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
