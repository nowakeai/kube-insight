import { create } from "zustand"

import type { AgentRunDTO, AgentRunEventDTO, AgentSessionDTO } from "@/lib/agent-schemas"
import { displayRunIdsForRetryBranches } from "@/lib/agent-retry-branches"

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
  runCount?: number
}

export type AgentRun = {
  id: string
  sessionId: string
  status: AgentRunStatus
  input: string
  finalAnswer?: string
  error?: string
  metadata?: unknown
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

export type AgentPanelWorkspace = {
  pinnedArtifactIds: string[]
  selectedArtifactId?: string
  dockCollapsed: boolean
  watchIntervalSeconds?: number
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

type UpdateArtifactInput = {
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
  panelWorkspaces: Record<string, AgentPanelWorkspace>
  ensureSession: (title: string) => string
  startRun: (sessionId: string, input: string) => string
  upsertServerSession: (session: AgentSessionDTO, options?: UpsertServerOptions) => void
  upsertServerSessions: (sessions: AgentSessionDTO[], options?: UpsertServerOptions) => void
  upsertServerRun: (run: AgentRunDTO, options?: UpsertServerOptions) => void
  applyServerEvent: (event: AgentRunEventDTO) => void
  applyServerEvents: (events: AgentRunEventDTO[]) => void
  appendRunEvent: (runId: string, input: AddRunEventInput) => string
  completeRun: (runId: string, finalAnswer: string) => void
  failRun: (runId: string, error: string) => void
  cancelRun: (runId: string) => void
  upsertArtifact: (runId: string, input: UpsertArtifactInput) => string
  updateArtifact: (artifactId: string, input: UpdateArtifactInput) => void
  addCitation: (runId: string, input: AddCitationInput) => string
  selectArtifact: (artifactId?: string) => void
  setPanelDockCollapsed: (sessionId: string, collapsed: boolean) => void
  setPanelWatchIntervalSeconds: (sessionId: string, seconds?: number) => void
  unpinArtifactForSession: (sessionId: string, artifactId: string) => void
  selectSession: (sessionId?: string) => void
  removeSession: (sessionId: string) => void
  startNewSession: () => void
  reset: () => void
}

const panelWorkspaceStorageKey = "kube-insight.agent.panel-workspaces.v1"

const initialProjection = () => ({
  activeSessionId: undefined,
  selectedArtifactId: undefined,
  sessionOrder: [],
  sessions: {},
  runs: {},
  events: {},
  artifacts: {},
  citations: {},
  panelWorkspaces: readPanelWorkspaces(),
})

export const useAgentProjectionStore = create<AgentProjectionState>((set, get) => ({
  ...initialProjection(),

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

  upsertServerSession: (session, options = {}) => get().upsertServerSessions([session], options),

  upsertServerSessions: (sessions, options = {}) => {
    if (sessions.length === 0) return
    const activate = options.activate ?? true
    set((current) => {
      let runs = current.runs
      for (const session of sessions) {
        for (const run of serverSessionRuns(session)) {
          if (runs === current.runs) runs = { ...current.runs }
          const previousRun = current.runs[run.id]
          runs[run.id] = {
            id: run.id,
            sessionId: run.sessionId,
            status: run.status,
            input: run.input,
            error: run.error,
            metadata: run.metadata,
            createdAt: run.createdAt,
            updatedAt: run.completedAt ?? run.startedAt ?? run.createdAt,
            finalAnswer: previousRun?.finalAnswer,
            eventIds: previousRun?.eventIds ?? [],
            artifactIds: previousRun?.artifactIds ?? [],
            citationIds: previousRun?.citationIds ?? [],
          }
        }
      }
      const nextSessions = { ...current.sessions }
      let sessionOrder = current.sessionOrder
      for (const session of sessions) {
        const previous = current.sessions[session.id]
        const serverRuns = serverSessionRuns(session)
        const runIds = displayRunIdsForRetryBranches(uniqueValues([
          ...(previous?.runIds ?? []),
          ...serverRuns.map((run) => run.id),
        ]), runs)
        nextSessions[session.id] = {
          id: session.id,
          title: session.title || previous?.title || "New investigation",
          createdAt: session.createdAt,
          updatedAt: session.updatedAt,
          runIds,
          runCount: session.runCount ?? previous?.runCount ?? runIds.length,
        }
        sessionOrder = uniquePrepend(sessionOrder, session.id)
      }
      return {
        activeSessionId: activate ? sessions[sessions.length - 1]?.id : current.activeSessionId,
        sessionOrder,
        sessions: nextSessions,
        runs,
      }
    })
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
      const nextRun: AgentRun = {
        id: run.id,
        sessionId: run.sessionId,
        status: run.status,
        input: run.input,
        error: run.error,
        metadata: run.metadata,
        createdAt: run.createdAt,
        updatedAt: run.completedAt ?? run.startedAt ?? now,
        finalAnswer: previous?.finalAnswer,
        eventIds: previous?.eventIds ?? [],
        artifactIds: previous?.artifactIds ?? [],
        citationIds: previous?.citationIds ?? [],
      }
      const runs = { ...current.runs, [run.id]: nextRun }
      const runIds = displayRunIdsForRetryBranches(uniqueAppend(session.runIds, run.id), runs)
      return {
        activeSessionId: activate ? run.sessionId : current.activeSessionId,
        sessionOrder: uniquePrepend(current.sessionOrder, run.sessionId),
        sessions: {
          ...current.sessions,
          [run.sessionId]: {
            ...session,
            updatedAt: run.completedAt ?? run.startedAt ?? run.createdAt,
            runIds,
          },
        },
        runs,
      }
    })
  },

  applyServerEvent: (event) => get().applyServerEvents([event]),

  applyServerEvents: (events) => {
    if (events.length === 0) return
    set((current) => {
      let changed = false
      let nextEvents = current.events
      let nextRuns = current.runs
      let nextArtifacts = current.artifacts
      let nextCitations = current.citations

      const ensureEvents = () => {
        if (nextEvents === current.events) nextEvents = { ...current.events }
      }
      const ensureRuns = () => {
        if (nextRuns === current.runs) nextRuns = { ...current.runs }
      }
      const ensureArtifacts = () => {
        if (nextArtifacts === current.artifacts) nextArtifacts = { ...current.artifacts }
      }
      const ensureCitations = () => {
        if (nextCitations === current.citations) nextCitations = { ...current.citations }
      }

      for (const event of events) {
        if (nextEvents[event.id]) continue
        const previousRun = nextRuns[event.runId]
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
        const artifact = artifactFromEvent(event, nextArtifacts)
        const citation = citationFromEvent(event)

        ensureEvents()
        nextEvents[event.id] = {
            id: event.id,
            runId: event.runId,
            type: event.type,
            createdAt: event.createdAt,
            data: event.data,
        }

        ensureRuns()
        nextRuns[event.runId] = {
          ...run,
          status,
          error: errorFromEvent(event) ?? run.error,
          finalAnswer,
          updatedAt: event.createdAt,
          eventIds: uniqueAppend(run.eventIds, event.id),
          artifactIds: artifact ? uniqueAppend(run.artifactIds, artifact.id) : run.artifactIds,
          citationIds: citation ? uniqueAppend(run.citationIds, citation.id) : run.citationIds,
        }
        if (artifact) {
          ensureArtifacts()
          nextArtifacts[artifact.id] = artifact
        }
        if (citation) {
          ensureCitations()
          nextCitations[citation.id] = citation
        }
        changed = true
      }

      if (!changed) return current
      return {
        events: nextEvents,
        runs: nextRuns,
        artifacts: nextArtifacts,
        citations: nextCitations,
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
  updateArtifact: (artifactId, input) => {
    const now = nowISO()
    set((current) => {
      const artifact = current.artifacts[artifactId]
      if (!artifact) return current
      const run = current.runs[artifact.runId]
      return {
        artifacts: {
          ...current.artifacts,
          [artifactId]: {
            ...artifact,
            title: input.title ?? artifact.title,
            data: input.data ?? artifact.data,
            updatedAt: now,
          },
        },
        runs: run
          ? {
              ...current.runs,
              [artifact.runId]: { ...run, updatedAt: now },
            }
          : current.runs,
      }
    })
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

  selectArtifact: (artifactId) => {
    set((current) => {
      if (!artifactId) return { selectedArtifactId: undefined }
      const sessionId = sessionIdForArtifact(current, artifactId)
      if (!sessionId) return { selectedArtifactId: artifactId }
      const workspace = current.panelWorkspaces[sessionId] ?? emptyPanelWorkspace()
      const nextWorkspace: AgentPanelWorkspace = {
        ...workspace,
        dockCollapsed: false,
        pinnedArtifactIds: workspace.pinnedArtifactIds.includes(artifactId)
          ? workspace.pinnedArtifactIds
          : [artifactId, ...workspace.pinnedArtifactIds],
        selectedArtifactId: artifactId,
      }
      const panelWorkspaces = {
        ...current.panelWorkspaces,
        [sessionId]: nextWorkspace,
      }
      writePanelWorkspaces(panelWorkspaces)
      return { selectedArtifactId: artifactId, panelWorkspaces }
    })
  },
  setPanelDockCollapsed: (sessionId, collapsed) => {
    set((current) => {
      const workspace = current.panelWorkspaces[sessionId] ?? emptyPanelWorkspace()
      const panelWorkspaces = {
        ...current.panelWorkspaces,
        [sessionId]: { ...workspace, dockCollapsed: collapsed },
      }
      writePanelWorkspaces(panelWorkspaces)
      return { panelWorkspaces }
    })
  },
  setPanelWatchIntervalSeconds: (sessionId, seconds) => {
    set((current) => {
      const workspace = current.panelWorkspaces[sessionId] ?? emptyPanelWorkspace()
      const watchIntervalSeconds = typeof seconds === "number" && Number.isFinite(seconds) && seconds > 0
        ? seconds
        : undefined
      const panelWorkspaces = {
        ...current.panelWorkspaces,
        [sessionId]: { ...workspace, watchIntervalSeconds },
      }
      writePanelWorkspaces(panelWorkspaces)
      return { panelWorkspaces }
    })
  },
  unpinArtifactForSession: (sessionId, artifactId) => {
    set((current) => {
      const workspace = current.panelWorkspaces[sessionId] ?? emptyPanelWorkspace()
      const pinnedArtifactIds = workspace.pinnedArtifactIds.filter((id) => id !== artifactId)
      const selectedArtifactId = workspace.selectedArtifactId === artifactId ? pinnedArtifactIds[0] : workspace.selectedArtifactId
      const panelWorkspaces = {
        ...current.panelWorkspaces,
        [sessionId]: { ...workspace, pinnedArtifactIds, selectedArtifactId },
      }
      writePanelWorkspaces(panelWorkspaces)
      return {
        selectedArtifactId: current.selectedArtifactId === artifactId ? selectedArtifactId : current.selectedArtifactId,
        panelWorkspaces,
      }
    })
  },
  selectSession: (sessionId) => set((current) => ({
    activeSessionId: sessionId,
    selectedArtifactId: sessionId ? current.panelWorkspaces[sessionId]?.selectedArtifactId : undefined,
  })),
  removeSession: (sessionId) => {
    set((current) => {
      const session = current.sessions[sessionId]
      if (!session) return current
      const runIds = new Set(session.runIds)
      const eventIds = new Set<string>()
      const artifactIds = new Set<string>()
      const citationIds = new Set<string>()
      for (const runId of runIds) {
        const run = current.runs[runId]
        for (const eventId of run?.eventIds ?? []) eventIds.add(eventId)
        for (const artifactId of run?.artifactIds ?? []) artifactIds.add(artifactId)
        for (const citationId of run?.citationIds ?? []) citationIds.add(citationId)
      }
      const sessions = omitKeys(current.sessions, new Set([sessionId]))
      const runs = omitKeys(current.runs, runIds)
      const events = omitKeys(current.events, eventIds)
      const artifacts = omitKeys(current.artifacts, artifactIds)
      const citations = omitKeys(current.citations, citationIds)
      const panelWorkspaces = omitKeys(current.panelWorkspaces, new Set([sessionId]))
      writePanelWorkspaces(panelWorkspaces)
      const activeSessionId = current.activeSessionId === sessionId ? undefined : current.activeSessionId
      const selectedArtifactId = current.selectedArtifactId && !artifactIds.has(current.selectedArtifactId)
        ? current.selectedArtifactId
        : undefined
      return {
        activeSessionId,
        selectedArtifactId,
        sessionOrder: current.sessionOrder.filter((id) => id !== sessionId),
        sessions,
        runs,
        events,
        artifacts,
        citations,
        panelWorkspaces,
      }
    })
  },
  startNewSession: () => set({ activeSessionId: undefined, selectedArtifactId: undefined }),
  reset: () => set(initialProjection()),
}))


function emptyPanelWorkspace(): AgentPanelWorkspace {
  return { pinnedArtifactIds: [], dockCollapsed: false }
}

function serverSessionRuns(session: AgentSessionDTO): AgentRunDTO[] {
  const runs = [...(session.runs ?? [])]
  if (session.latestRun && !runs.some((run) => run.id === session.latestRun?.id)) {
    runs.push(session.latestRun)
  }
  return runs
}

function sessionIdForArtifact(state: AgentProjectionState, artifactId: string) {
  const artifact = state.artifacts[artifactId]
  if (!artifact) return undefined
  return state.runs[artifact.runId]?.sessionId
}

function readPanelWorkspaces(): Record<string, AgentPanelWorkspace> {
  const storage = browserLocalStorage()
  if (!storage) return {}
  try {
    return sanitizePanelWorkspaces(JSON.parse(storage.getItem(panelWorkspaceStorageKey) ?? "{}"))
  } catch {
    return {}
  }
}

function writePanelWorkspaces(workspaces: Record<string, AgentPanelWorkspace>) {
  const storage = browserLocalStorage()
  if (!storage) return
  try {
    storage.setItem(panelWorkspaceStorageKey, JSON.stringify(workspaces))
  } catch {
    // Browser storage is an enhancement; the projection store remains usable without it.
  }
}

function browserLocalStorage(): Storage | undefined {
  if (typeof window === "undefined") return undefined
  try {
    return window.localStorage
  } catch {
    return undefined
  }
}

function sanitizePanelWorkspaces(value: unknown): Record<string, AgentPanelWorkspace> {
  if (!value || typeof value !== "object" || Array.isArray(value)) return {}
  const result: Record<string, AgentPanelWorkspace> = {}
  for (const [sessionId, workspace] of Object.entries(value)) {
    if (typeof sessionId !== "string" || !workspace || typeof workspace !== "object" || Array.isArray(workspace)) continue
    const record = workspace as Record<string, unknown>
    const pinnedArtifactIds = Array.isArray(record.pinnedArtifactIds)
      ? uniqueValues(record.pinnedArtifactIds.filter((item): item is string => typeof item === "string"))
      : []
    const selectedArtifactId = typeof record.selectedArtifactId === "string" ? record.selectedArtifactId : undefined
    const dockCollapsed = typeof record.dockCollapsed === "boolean" ? record.dockCollapsed : false
    const watchIntervalSeconds = typeof record.watchIntervalSeconds === "number" && Number.isFinite(record.watchIntervalSeconds)
      ? record.watchIntervalSeconds
      : undefined
    result[sessionId] = { pinnedArtifactIds, selectedArtifactId, dockCollapsed, watchIntervalSeconds }
  }
  return result
}

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

function omitKeys<T>(record: Record<string, T>, keys: Set<string>) {
  const next: Record<string, T> = {}
  for (const [key, value] of Object.entries(record)) {
    if (!keys.has(key)) next[key] = value
  }
  return next
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

function artifactFromEvent(event: AgentRunEventDTO, artifacts: Record<string, AgentArtifact> = {}): AgentArtifact | undefined {
  if (event.type !== "artifact.created" && event.type !== "artifact.updated") return undefined
  const data = eventDataRecord(event.data)
  const value = eventDataRecord(data.artifact ?? data)
  if (typeof value.id !== "string" || typeof value.kind !== "string") return undefined
  const previous = artifacts[value.id]
  return {
    id: value.id,
    runId: event.runId,
    kind: value.kind as AgentArtifactKind,
    title: typeof value.title === "string" ? value.title : undefined,
    data: value.data,
    createdAt: previous?.createdAt ?? event.createdAt,
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
