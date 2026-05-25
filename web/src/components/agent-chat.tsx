import {
  AssistantRuntimeProvider,
  ComposerPrimitive,
  ThreadPrimitive,
  useExternalStoreRuntime,
  type AppendMessage,
  type ThreadMessage,
} from "@assistant-ui/react"
import { ArrowUp, CircleStop, LayoutDashboard, Search, Sparkles } from "lucide-react"
import { useCallback, useEffect, useRef, useState, type ReactNode } from "react"

import { ArtifactDock } from "@/components/artifact-panel"
import { SessionSidebar } from "@/components/agent-session-sidebar"
import { LocalMessageConversation, RunComposerStats, SessionConversation, type AgentRunRoute } from "@/components/agent-chat-stream"
import { isPanelDockArtifact } from "@/components/agent-chat-stream-model"
import { Button } from "@/components/ui/button"
import { AgentAPIError, cancelAgentRun, createAgentRun, createAgentSession, getAgentRunEvents, getAgentSession, listAgentSessions, retryAgentRun } from "@/lib/agent-api"
import { streamAgentRunEvents, type AgentRunEventSubscription } from "@/lib/agent-events"
import { demoAgentAnswer, demoK8sDiffArtifact, demoK8sHistoryArtifact, demoK8sResourceArtifact, demoK8sResourceListArtifact, demoK8sTopologyArtifact } from "@/lib/demo-agent"
import { useAgentProjectionStore, type AgentArtifact, type AgentRun, type AgentRunEvent } from "@/lib/agent-store"
import { displayRunIdsForRetryBranches } from "@/lib/agent-retry-branches"
import { retryErrorMessage, retryReplacementMetadata, shouldCreateReplacementRunForRetryError } from "@/lib/agent-retry-policy"
import type { AgentRunDTO, AgentRunEventDTO, AgentSessionDTO } from "@/lib/agent-schemas"

const starterPrompts = [
  "Is the API service healthy right now?",
  "Show recent changes for default/api",
  "Find pods with restart evidence",
  "Map topology for namespace default",
]

const emptyEventIds: string[] = []

export function AgentChat() {
  const [messages, setMessages] = useState<ThreadMessage[]>([])
  const [isRunning, setIsRunning] = useState(false)
  const [routeHydrating, setRouteHydrating] = useState(false)
  const [routeRun, setRouteRun] = useState(readRouteRun)
  const [emptyDockExpandedBySession, setEmptyDockExpandedBySession] = useState<Record<string, boolean>>({})
  const eventSubscriptionRef = useRef<AgentRunEventSubscription | undefined>(undefined)
  const activeServerRunRef = useRef<string | undefined>(undefined)
  const hydratedRunRef = useRef<string | undefined>(undefined)
  const activeSessionId = useAgentProjectionStore((state) => state.activeSessionId)
  const sessionOrder = useAgentProjectionStore((state) => state.sessionOrder)
  const sessionsById = useAgentProjectionStore((state) => state.sessions)
  const runsById = useAgentProjectionStore((state) => state.runs)
  const ensureSession = useAgentProjectionStore((state) => state.ensureSession)
  const startRun = useAgentProjectionStore((state) => state.startRun)
  const upsertServerSession = useAgentProjectionStore((state) => state.upsertServerSession)
  const upsertServerRun = useAgentProjectionStore((state) => state.upsertServerRun)
  const applyServerEvent = useAgentProjectionStore((state) => state.applyServerEvent)
  const appendRunEvent = useAgentProjectionStore((state) => state.appendRunEvent)
  const completeRun = useAgentProjectionStore((state) => state.completeRun)
  const cancelRun = useAgentProjectionStore((state) => state.cancelRun)
  const upsertArtifact = useAgentProjectionStore((state) => state.upsertArtifact)
  const addCitation = useAgentProjectionStore((state) => state.addCitation)
  const startNewSession = useAgentProjectionStore((state) => state.startNewSession)
  const selectSession = useAgentProjectionStore((state) => state.selectSession)
  const selectArtifact = useAgentProjectionStore((state) => state.selectArtifact)
  const setPanelDockCollapsed = useAgentProjectionStore((state) => state.setPanelDockCollapsed)
  const unpinArtifactForSession = useAgentProjectionStore((state) => state.unpinArtifactForSession)
  const handleSelectArtifact = useCallback((artifactId?: string) => {
    selectArtifact(artifactId)
  }, [selectArtifact])
  const activeRun = useAgentProjectionStore((state) => routeRun ? state.runs[routeRun.runID] : undefined)
  const activeRunEventIds = useAgentProjectionStore((state) => {
    if (!routeRun) return emptyEventIds
    return state.runs[routeRun.runID]?.eventIds ?? emptyEventIds
  })
  const eventsById = useAgentProjectionStore((state) => state.events)
  const artifactsById = useAgentProjectionStore((state) => state.artifacts)
  const activeRunEvents = activeRunEventIds
    .map((eventID) => eventsById[eventID])
    .filter((event): event is AgentRunEvent => Boolean(event))
  const selectedArtifactId = useAgentProjectionStore((state) => state.selectedArtifactId)
  const visibleSessionId = routeRun?.sessionID ?? activeSessionId
  const visiblePanelWorkspace = useAgentProjectionStore((state) => visibleSessionId ? state.panelWorkspaces[visibleSessionId] : undefined)
  const panelDockCollapsed = visiblePanelWorkspace?.dockCollapsed ?? false
  const visibleSession = visibleSessionId ? sessionsById[visibleSessionId] : undefined
  const sessionRuns = (visibleSession?.runIds ?? [])
    .map((runID) => runsById[runID])
    .filter((run): run is AgentRun => Boolean(run))
  const visibleRunIds = displayRunIdsForRetryBranches(sessionRuns.map((run) => run.id), runsById)
  const visibleRuns = visibleRunIds
    .map((runID) => runsById[runID])
    .filter((run): run is AgentRun => Boolean(run))
  const visiblePanelArtifacts = (visiblePanelWorkspace?.pinnedArtifactIds ?? [])
    .map((artifactID) => artifactsById[artifactID])
    .filter((artifact): artifact is AgentArtifact => Boolean(artifact) && isPanelDockArtifact(artifact.kind))
  const emptyDockExpanded = visibleSessionId ? emptyDockExpandedBySession[visibleSessionId] ?? false : false
  const panelDockExpanded = !panelDockCollapsed && (visiblePanelArtifacts.length > 0 || emptyDockExpanded)
  const shellGridClass = panelDockExpanded
    ? "relative grid min-h-0 flex-1 grid-cols-1 lg:grid-cols-[18rem_minmax(0,1fr)_24rem] xl:grid-cols-[18rem_minmax(0,1fr)_28rem]"
    : "relative grid min-h-0 flex-1 grid-cols-1 lg:grid-cols-[18rem_minmax(0,1fr)]"
  const handlePanelDockCollapsedChange = useCallback((collapsed: boolean) => {
    if (!visibleSessionId) return
    setEmptyDockExpandedBySession((current) => ({ ...current, [visibleSessionId]: !collapsed && visiblePanelArtifacts.length === 0 }))
    setPanelDockCollapsed(visibleSessionId, collapsed)
  }, [setPanelDockCollapsed, visiblePanelArtifacts.length, visibleSessionId])
  const handleCloseArtifactPanel = useCallback((artifactId: string) => {
    if (!visibleSessionId) return
    unpinArtifactForSession(visibleSessionId, artifactId)
    if (visiblePanelArtifacts.length <= 1) {
      setEmptyDockExpandedBySession((current) => ({ ...current, [visibleSessionId]: false }))
      setPanelDockCollapsed(visibleSessionId, true)
    }
  }, [setPanelDockCollapsed, unpinArtifactForSession, visiblePanelArtifacts.length, visibleSessionId])
  const activeRunIsRunning = Boolean(activeRun && isActiveRunStatus(activeRun.status))
  const effectiveIsRunning = isRunning || activeRunIsRunning

  useEffect(() => {
    const onPopState = () => {
      const nextRouteRun = readRouteRun()
      setRouteRun(nextRouteRun)
      if (!nextRouteRun) setRouteHydrating(false)
    }
    window.addEventListener("popstate", onPopState)
    return () => window.removeEventListener("popstate", onPopState)
  }, [])

  useEffect(() => {
    const abortController = new AbortController()
    async function hydrateRecentSessions() {
      const list = await listAgentSessions({ signal: abortController.signal })
      if (abortController.signal.aborted) return
      for (const session of [...list.sessions].reverse()) upsertServerSession(session, { activate: false })
    }
    void hydrateRecentSessions().catch(() => undefined)
    return () => abortController.abort()
  }, [upsertServerSession])

  useEffect(() => {
    if (!routeRun) {
      hydratedRunRef.current = undefined
      return
    }
    if (hydratedRunRef.current === routeRun.runID) return

    const abortController = new AbortController()
    hydratedRunRef.current = routeRun.runID
    setRouteHydrating(true)

    async function hydrateRouteRun() {
      const session = await getAgentSession(routeRun!.sessionID, { signal: abortController.signal })
      upsertServerSession(session)
      const sessionRuns = session.runs ?? []
      for (const candidate of sessionRuns) upsertServerRun(candidate)
      const routeRunDTO = sessionRuns.find((candidate) => candidate.id === routeRun!.runID)
      if (!routeRunDTO) throw new AgentAPIError(404, "run not found in session")

      const routeEvents = await getAgentRunEvents(routeRun!.runID, { signal: abortController.signal })
      for (const event of routeEvents) applyServerEvent(event)
      if (abortController.signal.aborted) return

      const hydratedRun = useAgentProjectionStore.getState().runs[routeRun!.runID]
      const status = runStatusFromHydration(hydratedRun, routeRunDTO, routeEvents)
      const running = status === "queued" || status === "running"
      activeServerRunRef.current = running ? routeRun!.runID : undefined
      setIsRunning(running)
      setMessages(threadMessagesFromHydratedRun(hydratedRun, routeRunDTO, routeEvents))
      setRouteHydrating(false)

      void hydrateSiblingRunEvents(sessionRuns, routeRun!.runID, abortController.signal, applyServerEvent)
    }

    void hydrateRouteRun().catch((error: unknown) => {
      if (abortController.signal.aborted) return
      hydratedRunRef.current = undefined
      activeServerRunRef.current = undefined
      setIsRunning(false)
      setRouteHydrating(false)
      if (error instanceof AgentAPIError && error.status === 404) {
        setMessages([])
        setRouteRun(undefined)
        if (window.location.pathname !== "/") window.history.replaceState({}, "", "/")
        return
      }
      const message = error instanceof Error ? error.message : "Failed to load run."
      setMessages([
        userMessageWithID(`user_${routeRun.runID}`, "", new Date().toISOString()),
        assistantMessage(`assistant_${routeRun.runID}`, `Run load failed: ${message}`, "complete"),
      ])
    })

    return () => {
      abortController.abort()
      if (hydratedRunRef.current === routeRun.runID) hydratedRunRef.current = undefined
    }
  }, [applyServerEvent, routeRun, upsertServerRun, upsertServerSession])

  const runPrompt = useCallback(async (text: string) => {
    const prompt = text.trim()
    if (!prompt) return
    try {
      await runServerPrompt(prompt, {
        activeSessionId: useAgentProjectionStore.getState().activeSessionId,
        applyServerEvent,
        eventSubscriptionRef,
        activeServerRunRef,
        setIsRunning,
        setMessages,
        setRouteRun,
        upsertServerRun,
        upsertServerSession,
      })
    } catch {
      await runDemoPrompt(prompt, {
        addCitation,
        appendRunEvent,
        completeRun,
        ensureSession,
        setIsRunning,
        setMessages,
        setRouteRun,
        startRun,
        upsertArtifact,
      })
    }
  }, [addCitation, appendRunEvent, applyServerEvent, completeRun, ensureSession, startRun, upsertArtifact, upsertServerRun, upsertServerSession])

  const onNew = useCallback(async (message: AppendMessage) => {
    await runPrompt(appendMessageText(message))
  }, [runPrompt])

  const onCancel = useCallback(async () => {
    setIsRunning(false)
    setMessages((current) =>
      current.map((item) =>
        item.role === "assistant" && item.status?.type === "running"
          ? assistantMessage(item.id, "Run cancelled.", "cancelled")
          : item,
      ),
    )
    eventSubscriptionRef.current?.abort()
    const serverRunID = activeServerRunRef.current ?? (activeRun && isActiveRunStatus(activeRun.status) ? activeRun.id : undefined) ?? latestRunningRunID()
    if (serverRunID) void cancelAgentRun(serverRunID).catch(() => undefined)
    const latestRunningRun = latestRunningRunID()
    if (latestRunningRun) cancelRun(latestRunningRun)
  }, [activeRun, cancelRun])

  const handleNewSession = useCallback(() => {
    startNewSession()
    setMessages([])
    eventSubscriptionRef.current?.abort()
    activeServerRunRef.current = undefined
    setIsRunning(false)
    setRouteRun(undefined)
    if (window.location.pathname !== "/") window.history.pushState({}, "", "/")
  }, [startNewSession])

  const handleSelectSession = useCallback((sessionId: string) => {
    const state = useAgentProjectionStore.getState()
    const session = state.sessions[sessionId]
    if (!session) return
    selectSession(sessionId)
    const sessionRuns = session.runIds
      .map((runID) => state.runs[runID])
      .filter((run): run is AgentRun => Boolean(run))
    const runIds = displayRunIdsForRetryBranches(sessionRuns.map((candidate) => candidate.id), state.runs)
    const run = runIds.map((runID) => state.runs[runID]).filter((candidate): candidate is AgentRun => Boolean(candidate)).at(-1)
    eventSubscriptionRef.current?.abort()
    activeServerRunRef.current = run?.status === "running" ? run.id : undefined
    setIsRunning(run?.status === "running")
    if (!run) {
      setMessages([])
      setRouteRun(undefined)
      if (window.location.pathname !== "/") window.history.pushState({}, "", "/")
      return
    }
    openRunPage(run.sessionId, run.id)
    setRouteRun({ sessionID: run.sessionId, runID: run.id })
    setMessages(threadMessagesFromRun(run))
  }, [selectSession])

  const handleRetryRun = useCallback(async (run: AgentRun) => {
    if (effectiveIsRunning) return
    try {
      await runServerPrompt(run.input, {
        activeSessionId: run.sessionId,
        retryRunId: run.id,
        applyServerEvent,
        eventSubscriptionRef,
        activeServerRunRef,
        setIsRunning,
        setMessages,
        setRouteRun,
        upsertServerRun,
        upsertServerSession,
      })
    } catch (error) {
      setIsRunning(false)
      appendRunEvent(run.id, {
        type: "error",
        data: { message: `Retry failed: ${retryErrorMessage(error)}` },
      })
    }
  }, [appendRunEvent, applyServerEvent, effectiveIsRunning, upsertServerRun, upsertServerSession])

  const runtime = useExternalStoreRuntime({
    messages,
    isRunning: effectiveIsRunning,
    onNew,
    onCancel,
    unstable_capabilities: { copy: true },
  })
  const showHome = !routeHydrating && visibleRuns.length === 0 && messages.length === 0

  return (
    <AssistantRuntimeProvider runtime={runtime}>
      <main className="h-svh overflow-hidden bg-background text-foreground">
        <div className="flex h-full min-h-0 flex-col">
          <header className="flex h-14 shrink-0 items-center justify-between border-b border-border/80 px-4 sm:px-6">
            <div className="flex items-center gap-2 text-sm font-semibold">
              <span className="flex size-8 items-center justify-center rounded-md bg-primary text-primary-foreground">
                <Sparkles className="size-4" aria-hidden="true" />
              </span>
              <span>kube-insight</span>
            </div>
            <Button size="sm" variant="outline" asChild>
              <a href="/dashboard">
                <LayoutDashboard className="size-3.5" aria-hidden="true" />
                Dashboard
              </a>
            </Button>
          </header>

          <div className={shellGridClass}>
            <SessionSidebar
              activeSessionId={activeSessionId}
              disabled={effectiveIsRunning}
              onNew={handleNewSession}
              onSelect={handleSelectSession}
              runs={runsById}
              sessionOrder={sessionOrder}
              sessions={sessionsById}
            />

            <ThreadPrimitive.Root className="min-h-0 border-x border-border/80">
              <ThreadPrimitive.Viewport className="flex h-full min-h-0 flex-col overflow-y-auto scroll-smooth">
                {showHome ? (
                  <div className="mx-auto flex min-h-full w-full max-w-3xl flex-col justify-center gap-5 px-5 py-10">
                    <div className="flex flex-col gap-1">
                      <h1 className="text-3xl font-semibold tracking-normal text-foreground sm:text-4xl">
                        Hello there.
                      </h1>
                      <p className="text-lg text-muted-foreground">Ask about Kubernetes state, health, topology, or recent changes.</p>
                    </div>
                    <div className="grid w-full grid-cols-1 gap-2 sm:grid-cols-2">
                      {starterPrompts.map((prompt) => (
                        <ThreadPrimitive.Suggestion
                          key={prompt}
                          prompt={prompt}
                          method="replace"
                          autoSend={false}
                          className="min-h-16 rounded-lg border border-border bg-card px-4 py-3 text-left text-sm transition hover:border-primary/40 hover:bg-muted"
                        >
                          <span className="font-medium text-foreground">{prompt}</span>
                        </ThreadPrimitive.Suggestion>
                      ))}
                    </div>
                    <ChatComposer autoFocus isRunning={effectiveIsRunning} onCancel={onCancel} variant="home" />
                  </div>
                ) : (
                  <div className="mx-auto flex w-full max-w-4xl flex-1 flex-col px-5 py-5">
                    {routeHydrating ? (
                      <RouteRunLoading />
                    ) : visibleRuns.length > 0 ? (
                      <SessionConversation
                        activeRun={activeRun}
                        artifactsById={artifactsById}
                        isRunning={effectiveIsRunning}
                        onRetryRun={handleRetryRun}
                        onSelectArtifact={handleSelectArtifact}
                        runs={visibleRuns}
                        eventsById={eventsById}
                        status={activeRun?.status ?? (effectiveIsRunning ? "running" : "completed")}
                      />
                    ) : (
                      <LocalMessageConversation messages={messages} />
                    )}
                    <ThreadPrimitive.ViewportFooter className="sticky bottom-0 mt-auto bg-background/95 py-4 backdrop-blur">
                      <ChatComposer
                        isRunning={effectiveIsRunning}
                        onCancel={onCancel}
                        status={!routeHydrating ? (
                          <RunComposerStats
                            events={activeRunEvents}
                            isRunning={effectiveIsRunning}
                            routeRun={routeRun}
                            run={activeRun}
                            status={activeRun?.status ?? (effectiveIsRunning ? "running" : "completed")}
                          />
                        ) : null}
                      />
                    </ThreadPrimitive.ViewportFooter>
                  </div>
                )}
              </ThreadPrimitive.Viewport>
            </ThreadPrimitive.Root>

            <ArtifactDock
              artifacts={visiblePanelArtifacts}
              selectedArtifactId={selectedArtifactId}
              collapsed={!panelDockExpanded}
              onCollapsedChange={handlePanelDockCollapsedChange}
              onCloseArtifact={handleCloseArtifactPanel}
            />
          </div>
        </div>
      </main>
    </AssistantRuntimeProvider>
  )
}


type RunServerPromptOptions = {
  activeSessionId?: string
  applyServerEvent: (event: AgentRunEventDTO) => void
  eventSubscriptionRef: React.MutableRefObject<AgentRunEventSubscription | undefined>
  activeServerRunRef: React.MutableRefObject<string | undefined>
  retryRunId?: string
  setIsRunning: (value: boolean) => void
  setMessages: React.Dispatch<React.SetStateAction<ThreadMessage[]>>
  setRouteRun: React.Dispatch<React.SetStateAction<AgentRunRoute | undefined>>
  upsertServerRun: ReturnType<typeof useAgentProjectionStore.getState>["upsertServerRun"]
  upsertServerSession: ReturnType<typeof useAgentProjectionStore.getState>["upsertServerSession"]
}

async function runServerPrompt(prompt: string, options: RunServerPromptOptions) {
  const run = options.retryRunId
    ? await retryOrCreateReplacementRun(prompt, options)
    : await createRunInServerSession(prompt, options)
  options.upsertServerRun(run)
  options.activeServerRunRef.current = run.id
  openRunPage(run.sessionId, run.id)
  options.setRouteRun({ sessionID: run.sessionId, runID: run.id })

  const assistantID = newMessageID("assistant")
  let assistantText = ""
  let errorText = run.error ?? ""
  let terminal = isTerminalRunStatus(run.status)
  options.setIsRunning(!terminal)
  const pendingMessages = [
    userMessage(prompt),
    assistantMessage(assistantID, terminal ? queuedRunMessage(run.status) : "", terminal ? "complete" : "running"),
  ]
  options.setMessages((current) => options.retryRunId ? pendingMessages : [...current, ...pendingMessages])

  const subscription = streamAgentRunEvents({
    runId: run.id,
    onEvent: (event) => {
      options.applyServerEvent(event)
      const eventError = errorMessageFromEvent(event)
      if (eventError) errorText = eventError
      const update = messageUpdateFromEvent(event)
      if (update.delta) assistantText += update.delta
      if (update.content) assistantText = update.content
      if (update.content || update.delta) {
        options.setMessages((current) => current.map((item) => item.id === assistantID ? assistantMessage(assistantID, assistantText, "running") : item))
      }
      const status = statusFromServerEvent(event)
      if (status && isTerminalRunStatus(status)) {
        terminal = true
        options.setIsRunning(false)
        options.setMessages((current) => current.map((item) => item.id === assistantID ? assistantMessage(assistantID, assistantText || queuedRunMessage(status, errorText), status === "cancelled" ? "cancelled" : "complete") : item))
      }
    },
  })
  options.eventSubscriptionRef.current = subscription
  await subscription.closed
  if (!terminal) {
    options.setIsRunning(false)
    options.setMessages((current) => current.map((item) => item.id === assistantID ? assistantMessage(assistantID, queuedRunMessage(run.status, errorText), "complete") : item))
  }
}

async function retryOrCreateReplacementRun(prompt: string, options: RunServerPromptOptions) {
  if (!options.retryRunId) return createRunInServerSession(prompt, options)
  try {
    return await retryAgentRun(options.retryRunId, { metadata: agentRunClientMetadata() })
  } catch (error) {
    if (!shouldCreateReplacementRunForRetryError(error)) throw error
    return createRunInServerSession(prompt, options, retryReplacementMetadata(options.retryRunId))
  }
}

async function createRunInServerSession(prompt: string, options: RunServerPromptOptions, metadata?: Record<string, unknown>) {
  const session = await ensureServerSession(prompt, options.activeSessionId)
  options.upsertServerSession(session)
  return createAgentRun(session.id, { input: prompt, metadata: agentRunClientMetadata(metadata) })
}

function agentRunClientMetadata(metadata: Record<string, unknown> = {}) {
  const now = new Date()
  const intlOptions = Intl.DateTimeFormat().resolvedOptions()
  const timezoneOffsetMinutes = -now.getTimezoneOffset()
  return {
    ...metadata,
    clientContext: {
      sentAt: now.toISOString(),
      localTime: formatLocalTime(now),
      timeZone: intlOptions.timeZone,
      timezoneOffsetMinutes,
      locale: navigator.language,
      languages: navigator.languages,
      pageURL: window.location.href,
    },
  }
}

function formatLocalTime(value: Date) {
  const offsetMinutes = -value.getTimezoneOffset()
  const sign = offsetMinutes >= 0 ? "+" : "-"
  const abs = Math.abs(offsetMinutes)
  const hours = String(Math.floor(abs / 60)).padStart(2, "0")
  const minutes = String(abs % 60).padStart(2, "0")
  return `${value.getFullYear()}-${String(value.getMonth() + 1).padStart(2, "0")}-${String(value.getDate()).padStart(2, "0")}T${String(value.getHours()).padStart(2, "0")}:${String(value.getMinutes()).padStart(2, "0")}:${String(value.getSeconds()).padStart(2, "0")}${sign}${hours}:${minutes}`
}

async function ensureServerSession(prompt: string, activeSessionId?: string): Promise<AgentSessionDTO> {
  if (activeSessionId?.startsWith("sess_")) return getExistingServerSession(activeSessionId, prompt)
  return createAgentSession({ title: prompt })
}

async function getExistingServerSession(sessionId: string, prompt: string): Promise<AgentSessionDTO> {
  try {
    return await getAgentSession(sessionId)
  } catch {
    return createAgentSession({ title: prompt })
  }
}

type RunDemoPromptOptions = {
  addCitation: (runId: string, input: { id?: string; artifactId?: string; text?: string; target?: unknown }) => string
  appendRunEvent: (runId: string, input: { id?: string; type: string; data?: unknown }) => string
  completeRun: (runId: string, finalAnswer: string) => void
  ensureSession: (title: string) => string
  setIsRunning: (value: boolean) => void
  setMessages: React.Dispatch<React.SetStateAction<ThreadMessage[]>>
  setRouteRun: React.Dispatch<React.SetStateAction<AgentRunRoute | undefined>>
  startRun: (sessionId: string, input: string) => string
  upsertArtifact: (runId: string, input: { id?: string; kind: "markdown" | "k8s.resource" | "k8s.resource_list" | "k8s.topology" | "k8s.history" | "k8s.diff" | "tool_call" | "citation"; title?: string; data?: unknown }) => string
}

async function runDemoPrompt(prompt: string, options: RunDemoPromptOptions) {
  const sessionID = options.ensureSession(prompt)
  const runID = options.startRun(sessionID, prompt)
  openRunPage(sessionID, runID)
  options.setRouteRun({ sessionID, runID })
  options.appendRunEvent(runID, { type: "message.created", data: { role: "user", content: prompt } })

  const assistantID = newMessageID("assistant")
  options.setIsRunning(true)
  options.setMessages((current) => [
    ...current,
    userMessage(prompt),
    assistantMessage(assistantID, "", "running"),
  ])

  const toolCallID = newMessageID("tool")
  options.appendRunEvent(runID, {
    type: "tool.started",
    data: {
      toolCallId: toolCallID,
      name: "kube_insight_demo_search",
      status: "started",
      input: { query: prompt },
    },
  })

  await delay(350)

  options.appendRunEvent(runID, {
    type: "tool.completed",
    data: {
      toolCallId: toolCallID,
      name: "kube_insight_demo_search",
      status: "completed",
      output: { summary: "Demo evidence projection created" },
      durationMs: 350,
    },
  })

  const answer = demoAgentAnswer(prompt)
  const markdownArtifactID = options.upsertArtifact(runID, {
    kind: "markdown",
    title: "Demo answer",
    data: { markdown: answer },
  })
  const resourceArtifactID = options.upsertArtifact(runID, {
    kind: "k8s.resource",
    title: "Pod default/api-0",
    data: demoK8sResourceArtifact(prompt),
  })
  options.upsertArtifact(runID, { kind: "k8s.resource_list", title: "Demo resource candidates", data: demoK8sResourceListArtifact(prompt) })
  options.upsertArtifact(runID, { kind: "k8s.topology", title: "Service default/api topology", data: demoK8sTopologyArtifact(prompt) })
  options.upsertArtifact(runID, { kind: "k8s.history", title: "Pod default/api-0 history", data: demoK8sHistoryArtifact(prompt) })
  options.upsertArtifact(runID, { kind: "k8s.diff", title: "Pod default/api-0 diff", data: demoK8sDiffArtifact() })
  options.addCitation(runID, {
    id: "citation_demo_runtime",
    artifactId: resourceArtifactID,
    text: "Demo runtime",
    target: { kind: "artifact", id: resourceArtifactID, relatedArtifactId: markdownArtifactID },
  })
  options.appendRunEvent(runID, { type: "message.created", data: { role: "assistant", content: answer } })
  options.completeRun(runID, answer)
  options.setMessages((current) => current.map((item) => item.id === assistantID ? assistantMessage(assistantID, answer, "complete") : item))
  options.setIsRunning(false)
}

function statusFromServerEvent(event: AgentRunEventDTO) {
  if (event.type === "answer.final") return "completed"
  const data = event.data && typeof event.data === "object" ? event.data as { status?: unknown } : {}
  return typeof data.status === "string" ? data.status : undefined
}

function isTerminalRunStatus(status: string) {
  return status === "completed" || status === "failed" || status === "cancelled"
}

function isActiveRunStatus(status: string) {
  return status === "queued" || status === "running"
}

async function hydrateSiblingRunEvents(
  runs: AgentRunDTO[],
  activeRunID: string,
  signal: AbortSignal,
  applyServerEvent: (event: AgentRunEventDTO) => void,
) {
  for (const run of runs) {
    if (run.id === activeRunID || signal.aborted) continue
    try {
      const events = await getAgentRunEvents(run.id, { signal })
      for (const event of events) applyServerEvent(event)
    } catch {
      return
    }
  }
}

function messageUpdateFromEvent(event: AgentRunEventDTO) {
  if (event.type !== "message.created" && event.type !== "message.delta" && event.type !== "message.completed" && event.type !== "answer.final") return {}
  const data = event.data && typeof event.data === "object" ? event.data as { role?: unknown; content?: unknown; delta?: unknown } : {}
  if (data.role && data.role !== "assistant") return {}
  return {
    content: typeof data.content === "string" ? data.content : undefined,
    delta: typeof data.delta === "string" ? data.delta : undefined,
  }
}

function errorMessageFromEvent(event: AgentRunEventDTO) {
  const data = event.data && typeof event.data === "object" ? event.data as { error?: unknown; message?: unknown } : {}
  if (typeof data.error === "string" && data.error) return data.error
  if (event.type === "error" && typeof data.message === "string" && data.message) return data.message
  return undefined
}

function queuedRunMessage(status: string, error?: string) {
  if (status === "failed") return error ? `Run failed: ${error}` : "Run failed before an answer was produced."
  if (status === "cancelled") return "Run cancelled."
  return "Run accepted by the server. Backend agent execution is not connected yet, so this run is waiting for the next server-side agent loop step."
}


function RouteRunLoading() {
  return (
    <div className="grid w-full grid-cols-[2rem_minmax(0,1fr)] gap-3 text-sm text-muted-foreground">
      <div className="flex size-8 items-center justify-center rounded-md border border-border bg-muted">
        <CircleStop className="size-4 animate-pulse" aria-hidden="true" />
      </div>
      <div className="min-w-0 pt-1">
        <div className="inline-flex items-center gap-2 rounded-md bg-muted px-3 py-2">
          <span className="size-1.5 animate-pulse rounded-full bg-primary" aria-hidden="true" />
          Loading conversation...
        </div>
      </div>
    </div>
  )
}

function ChatComposer({
  autoFocus = false,
  isRunning = false,
  onCancel,
  status,
  variant = "thread",
}: {
  autoFocus?: boolean
  isRunning?: boolean
  onCancel?: () => void
  status?: ReactNode
  variant?: "home" | "thread"
}) {
  const isHome = variant === "home"
  const inputRow = (
    <ComposerPrimitive.Root
      className={
        isHome
          ? "flex min-h-16 w-full items-end gap-2 rounded-lg border border-border bg-card p-2 shadow-md shadow-muted/40"
          : "flex min-h-14 w-full items-end gap-2 bg-card p-2"
      }
    >
      {isHome ? <Search className="mb-3 ml-2 size-5 shrink-0 text-muted-foreground" aria-hidden="true" /> : null}
      <ComposerPrimitive.Input
        autoFocus={autoFocus}
        rows={1}
        submitMode="enter"
        placeholder="Ask about a Service, Pod, namespace, topology, or recent change"
        className={
          isHome
            ? "max-h-44 min-h-12 flex-1 resize-none bg-transparent px-2 py-3 text-base leading-6 outline-none placeholder:text-muted-foreground"
            : "max-h-44 min-h-11 flex-1 resize-none bg-transparent px-2 py-2 text-sm leading-6 outline-none placeholder:text-muted-foreground"
        }
      />
      {isRunning ? (
        <Button type="button" size="icon" variant="secondary" aria-label="Stop run" className={isHome ? "size-11" : undefined} onClick={onCancel}>
          <CircleStop className="size-4" aria-hidden="true" />
        </Button>
      ) : (
        <ComposerPrimitive.Send asChild>
          <Button type="submit" size="icon" aria-label="Send message" className={isHome ? "size-11" : undefined}>
            <ArrowUp className="size-4" aria-hidden="true" />
          </Button>
        </ComposerPrimitive.Send>
      )}
    </ComposerPrimitive.Root>
  )
  if (isHome) return inputRow
  return (
    <div className="overflow-hidden rounded-lg border border-border bg-card shadow-sm shadow-muted/30">
      {status}
      {inputRow}
    </div>
  )
}

function appendMessageText(message: AppendMessage) {
  return message.content
    .filter((part) => part.type === "text")
    .map((part) => part.text)
    .join("\n")
    .trim()
}

function threadMessagesFromRun(run: AgentRun): ThreadMessage[] {
  const assistantStatus = assistantStatusFromRunStatus(run.status)
  const assistantText = run.finalAnswer || (assistantStatus === "running" ? "" : queuedRunMessage(run.status, run.error))
  return [
    userMessageWithID(`user_${run.id}`, run.input, run.createdAt),
    assistantMessage(`assistant_${run.id}`, assistantText, assistantStatus),
  ]
}

function threadMessagesFromHydratedRun(
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

function runStatusFromHydration(
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

function userMessage(text: string): ThreadMessage {
  return userMessageWithID(newMessageID("user"), text)
}

function userMessageWithID(id: string, text: string, createdAt?: string): ThreadMessage {
  return {
    id,
    role: "user",
    createdAt: createdAt ? new Date(createdAt) : new Date(),
    content: [{ type: "text", text }],
    attachments: [],
    metadata: { custom: {} },
  }
}

function assistantMessage(
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

function latestRunningRunID() {
  const state = useAgentProjectionStore.getState()
  const activeSession = state.activeSessionId ? state.sessions[state.activeSessionId] : undefined
  if (!activeSession) return undefined
  for (const runID of [...activeSession.runIds].reverse()) {
    const run = state.runs[runID]
    if (run && isActiveRunStatus(run.status)) return runID
  }
  return undefined
}

function newMessageID(prefix: string) {
  if (globalThis.crypto?.randomUUID) return `${prefix}_${globalThis.crypto.randomUUID()}`
  return `${prefix}_${Date.now()}_${Math.random().toString(36).slice(2)}`
}

function delay(ms: number) {
  return new Promise((resolve) => window.setTimeout(resolve, ms))
}

function readRouteRun(): AgentRunRoute | undefined {
  const match = window.location.pathname.match(/^\/sessions\/([^/]+)\/runs\/([^/]+)$/)
  if (!match) return undefined
  return {
    sessionID: decodeURIComponent(match[1]),
    runID: decodeURIComponent(match[2]),
  }
}

function openRunPage(sessionID: string, runID: string) {
  const nextPath = `/sessions/${encodeURIComponent(sessionID)}/runs/${encodeURIComponent(runID)}`
  if (window.location.pathname === nextPath) return
  window.history.pushState({}, "", nextPath)
}
