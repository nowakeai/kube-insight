import {
  AssistantRuntimeProvider,
  ComposerPrimitive,
  MessagePrimitive,
  ThreadPrimitive,
  useExternalStoreRuntime,
  type AppendMessage,
  type ThreadMessage,
} from "@assistant-ui/react"
import { ArrowRight, Bot, ChevronDown, CircleStop, ExternalLink, LayoutDashboard, MessageSquareText, Play, Plus, RotateCcw, Search, Sparkles, UserRound } from "lucide-react"
import { useCallback, useEffect, useMemo, useRef, useState } from "react"

import { ArtifactDock } from "@/components/artifact-panel"
import { MarkdownContent } from "@/components/markdown-content"
import { Button } from "@/components/ui/button"
import { cancelAgentRun, createAgentRun, createAgentSession, getAgentSession, retryAgentRun } from "@/lib/agent-api"
import { streamAgentRunEvents, type AgentRunEventSubscription } from "@/lib/agent-events"
import { demoAgentAnswer, demoK8sDiffArtifact, demoK8sHistoryArtifact, demoK8sResourceArtifact, demoK8sResourceListArtifact, demoK8sTopologyArtifact } from "@/lib/demo-agent"
import { useAgentProjectionStore, type AgentRun, type AgentRunEvent, type AgentSession } from "@/lib/agent-store"
import type { AgentRunEventDTO, AgentSessionDTO } from "@/lib/agent-schemas"

const starterPrompts = [
  "Is the API service healthy right now?",
  "Show recent changes for default/api",
  "Find pods with restart evidence",
  "Map topology for namespace default",
]

const emptyEventIds: string[] = []

export function AgentChat() {
  const [messages, setMessages] = useState<ThreadMessage[]>([])
  const [panelDockCollapsed, setPanelDockCollapsed] = useState(false)
  const [isRunning, setIsRunning] = useState(false)
  const [routeRun, setRouteRun] = useState(readRouteRun)
  const eventSubscriptionRef = useRef<AgentRunEventSubscription | undefined>(undefined)
  const activeServerRunRef = useRef<string | undefined>(undefined)
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
  const activeRun = useAgentProjectionStore((state) => routeRun ? state.runs[routeRun.runID] : undefined)
  const activeRunEventIds = useAgentProjectionStore((state) => {
    if (!routeRun) return emptyEventIds
    return state.runs[routeRun.runID]?.eventIds ?? emptyEventIds
  })
  const activeRunArtifactIds = useAgentProjectionStore((state) => {
    if (!routeRun) return emptyEventIds
    return state.runs[routeRun.runID]?.artifactIds ?? emptyEventIds
  })
  const eventsById = useAgentProjectionStore((state) => state.events)
  const artifactsById = useAgentProjectionStore((state) => state.artifacts)
  const activeRunEvents = activeRunEventIds
    .map((eventID) => eventsById[eventID])
    .filter((event): event is AgentRunEvent => Boolean(event))
  const activeRunArtifacts = activeRunArtifactIds
    .map((artifactID) => artifactsById[artifactID])
    .filter((artifact) => Boolean(artifact))
  const selectedArtifactId = useAgentProjectionStore((state) => state.selectedArtifactId)

  useEffect(() => {
    const onPopState = () => setRouteRun(readRouteRun())
    window.addEventListener("popstate", onPopState)
    return () => window.removeEventListener("popstate", onPopState)
  }, [])

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
    const serverRunID = activeServerRunRef.current
    if (serverRunID) void cancelAgentRun(serverRunID).catch(() => undefined)
    const latestRunningRun = latestRunningRunID()
    if (latestRunningRun) cancelRun(latestRunningRun)
  }, [cancelRun])

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
    const runId = session.runIds.at(-1)
    const run = runId ? state.runs[runId] : undefined
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

  const handleRetry = useCallback(async () => {
    if (!activeRun || activeRun.status !== "failed" || isRunning) return
    try {
      await runServerPrompt(activeRun.input, {
        activeSessionId: activeRun.sessionId,
        retryRunId: activeRun.id,
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
      await runPrompt(activeRun.input)
    }
  }, [activeRun, applyServerEvent, isRunning, runPrompt, upsertServerRun, upsertServerSession])

  const handleContinue = useCallback(() => {
    if (isRunning) return
    void runPrompt("Continue the investigation")
  }, [isRunning, runPrompt])

  const runtime = useExternalStoreRuntime({
    messages,
    isRunning,
    onNew,
    onCancel,
    unstable_capabilities: { copy: true },
  })

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

          <div className={panelDockCollapsed ? "grid min-h-0 flex-1 grid-cols-1 lg:grid-cols-[18rem_minmax(0,1fr)_5rem]" : "grid min-h-0 flex-1 grid-cols-1 lg:grid-cols-[18rem_minmax(0,1fr)_24rem] xl:grid-cols-[18rem_minmax(0,1fr)_28rem]"}>
            <SessionSidebar
              activeSessionId={activeSessionId}
              disabled={isRunning}
              onNew={handleNewSession}
              onSelect={handleSelectSession}
              runs={runsById}
              sessionOrder={sessionOrder}
              sessions={sessionsById}
            />

            <ThreadPrimitive.Root className="min-h-0 border-x border-border/80">
              <ThreadPrimitive.Viewport className="flex h-full min-h-0 flex-col overflow-y-auto scroll-smooth">
                <ThreadPrimitive.Empty>
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
                    <ChatComposer autoFocus variant="home" />
                  </div>
                </ThreadPrimitive.Empty>

                <ThreadPrimitive.If empty={false}>
                  <div className="mx-auto flex w-full max-w-4xl flex-1 flex-col px-5 py-5">
                    <ThreadPrimitive.Messages components={{ Message: ChatMessage }} />
                    <CitationPanel events={activeRunEvents} selectedArtifactId={selectedArtifactId} />
                    <RunActivity
                      key={routeRun?.runID ?? "run-activity"}
                      canRetry={Boolean(activeRun?.status === "failed")}
                      events={activeRunEvents}
                      isRunning={isRunning}
                      onContinue={handleContinue}
                      onRetry={handleRetry}
                      onStop={onCancel}
                      routeRun={routeRun}
                      status={activeRun?.status ?? (isRunning ? "running" : "completed")}
                    />
                    <ThreadPrimitive.ViewportFooter className="sticky bottom-0 mt-auto bg-background/95 py-4 backdrop-blur">
                      <ChatComposer />
                    </ThreadPrimitive.ViewportFooter>
                  </div>
                </ThreadPrimitive.If>
              </ThreadPrimitive.Viewport>
            </ThreadPrimitive.Root>

            <ArtifactDock artifacts={activeRunArtifacts} selectedArtifactId={selectedArtifactId} collapsed={panelDockCollapsed} onCollapsedChange={setPanelDockCollapsed} />
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
    ? await retryAgentRun(options.retryRunId)
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
  options.setMessages((current) => [
    ...current,
    userMessage(prompt),
    assistantMessage(assistantID, terminal ? queuedRunMessage(run.status) : "", terminal ? "complete" : "running"),
  ])

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

async function createRunInServerSession(prompt: string, options: RunServerPromptOptions) {
  const session = await ensureServerSession(prompt, options.activeSessionId)
  options.upsertServerSession(session)
  return createAgentRun(session.id, { input: prompt })
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
  const data = event.data && typeof event.data === "object" ? event.data as { status?: unknown } : {}
  return typeof data.status === "string" ? data.status : undefined
}

function isTerminalRunStatus(status: string) {
  return status === "completed" || status === "failed" || status === "cancelled"
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

function SessionSidebar({
  activeSessionId,
  disabled,
  onNew,
  onSelect,
  runs,
  sessionOrder,
  sessions,
}: {
  activeSessionId?: string
  disabled: boolean
  onNew: () => void
  onSelect: (sessionId: string) => void
  runs: Record<string, AgentRun>
  sessionOrder: string[]
  sessions: Record<string, AgentSession>
}) {
  const visibleSessions = sessionOrder
    .map((sessionId) => sessions[sessionId])
    .filter((session): session is AgentSession => Boolean(session))

  return (
    <aside className="hidden min-h-0 border-r border-border/80 bg-card/60 lg:flex lg:flex-col" aria-label="Sessions">
      <div className="border-b border-border/80 p-4">
        <Button type="button" variant="outline" className="w-full justify-start" onClick={onNew} disabled={disabled}>
          <Plus className="size-4" aria-hidden="true" />
          New thread
        </Button>
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto p-3">
        {visibleSessions.length === 0 ? (
          <div className="rounded-md border border-dashed border-border bg-background px-3 py-6 text-sm text-muted-foreground">
            Sessions will appear here after the first run.
          </div>
        ) : (
          <div className="flex flex-col gap-1">
            {visibleSessions.map((session) => {
              const latestRunId = session.runIds.at(-1)
              const latestRun = latestRunId ? runs[latestRunId] : undefined
              const selected = session.id === activeSessionId
              return (
                <button
                  key={session.id}
                  type="button"
                  className={
                    selected
                      ? "rounded-md border border-border bg-background px-3 py-2 text-left shadow-sm"
                      : "rounded-md border border-transparent px-3 py-2 text-left text-muted-foreground transition hover:border-border hover:bg-background hover:text-foreground"
                  }
                  onClick={() => onSelect(session.id)}
                >
                  <div className="flex items-center gap-2 text-sm font-medium">
                    <MessageSquareText className="size-3.5 shrink-0" aria-hidden="true" />
                    <span className="truncate">{session.title || "New investigation"}</span>
                  </div>
                  <div className="mt-1 flex items-center justify-between gap-2 text-[0.7rem] text-muted-foreground">
                    <span>{latestRun ? runStatusLabel(latestRun.status) : "No runs"}</span>
                    <span>{session.runIds.length} run{session.runIds.length === 1 ? "" : "s"}</span>
                  </div>
                </button>
              )
            })}
          </div>
        )}
      </div>
    </aside>
  )
}

function RunActivity({
  canRetry,
  events,
  isRunning,
  onContinue,
  onRetry,
  onStop,
  routeRun,
  status,
}: {
  canRetry: boolean
  events: AgentRunEvent[]
  isRunning: boolean
  onContinue: () => void
  onRetry: () => void
  onStop: () => void
  routeRun?: AgentRunRoute
  status: string
}) {
  const toolCalls = useMemo(() => groupToolCalls(events), [events])
  const terminal = isTerminalRunStatus(status)
  const [manualExpanded, setManualExpanded] = useState<boolean | undefined>(undefined)
  const expanded = manualExpanded ?? !terminal

  if (!routeRun && toolCalls.length === 0) return null

  return (
    <div className="mt-2 rounded-md border border-border bg-card text-sm shadow-sm">
      <button
        type="button"
        className="flex w-full items-center justify-between gap-3 px-3 py-2 text-left"
        onClick={() => setManualExpanded((value) => !(value ?? !terminal))}
      >
        <div className="flex min-w-0 items-center gap-2">
          <span className={statusDotClass(status)} aria-hidden="true" />
          <span className="truncate font-medium text-foreground">Agent activity</span>
          <span className="rounded-md border border-border px-2 py-0.5 text-xs capitalize text-muted-foreground">{runStatusLabel(status)}</span>
          {toolCalls.length > 0 ? <span className="text-xs text-muted-foreground">{toolCalls.length} tool call{toolCalls.length === 1 ? "" : "s"}</span> : null}
        </div>
        <ChevronDown className={expanded ? "size-4 rotate-180 text-muted-foreground transition" : "size-4 text-muted-foreground transition"} aria-hidden="true" />
      </button>

      {expanded ? (
        <div className="border-t border-border px-3 py-3">
          <div className="mb-3 flex items-center justify-between gap-2 text-xs text-muted-foreground">
            <span>{routeRun ? `run ${shortID(routeRun.runID)}` : "run"}</span>
            <div className="flex shrink-0 items-center gap-1">
              <Button type="button" size="icon-sm" variant="ghost" title="Retry" aria-label="Retry run" onClick={onRetry} disabled={!canRetry || isRunning}>
                <RotateCcw className="size-3.5" aria-hidden="true" />
              </Button>
              <Button type="button" size="icon-sm" variant="ghost" title="Continue" aria-label="Continue run" onClick={onContinue} disabled={isRunning}>
                <Play className="size-3.5" aria-hidden="true" />
              </Button>
              <Button type="button" size="icon-sm" variant="ghost" title="Stop" aria-label="Stop run" onClick={onStop} disabled={!isRunning}>
                <CircleStop className="size-3.5" aria-hidden="true" />
              </Button>
            </div>
          </div>
          {toolCalls.length > 0 ? (
            <div className="flex flex-col gap-1.5">
              {toolCalls.map((call) => (
                <ToolTimelineRow key={call.id} call={call} />
              ))}
            </div>
          ) : (
            <div className="rounded-md border border-dashed border-border bg-background px-3 py-4 text-xs text-muted-foreground">
              No tool calls recorded for this run.
            </div>
          )}
        </div>
      ) : null}
    </div>
  )
}

function ToolTimelineRow({ call }: { call: ToolCallView }) {
  const [expanded, setExpanded] = useState(false)
  const detail = toolEventDetail(call)
  return (
    <div className="rounded-md border border-border bg-background text-xs">
      <button
        type="button"
        className="grid w-full grid-cols-[5.5rem_minmax(0,1fr)_1.5rem] gap-3 px-3 py-2 text-left sm:grid-cols-[6.5rem_minmax(0,1fr)_5rem_1.5rem]"
        onClick={() => setExpanded((value) => !value)}
      >
        <div className="flex min-w-0 items-center gap-2 text-muted-foreground">
          <span className={statusDotClass(call.status)} aria-hidden="true" />
          <span className="truncate capitalize">{runStatusLabel(call.status)}</span>
        </div>
        <div className="min-w-0">
          <div className="truncate font-medium text-foreground">{call.name || "tool"}</div>
          <div className="truncate text-muted-foreground">{toolEventSummary(call)}</div>
        </div>
        <div className="hidden text-right text-muted-foreground sm:block">
          {typeof call.durationMs === "number" ? `${call.durationMs}ms` : ""}
        </div>
        <ChevronDown className={expanded ? "mt-1 size-3.5 rotate-180 text-muted-foreground transition" : "mt-1 size-3.5 text-muted-foreground transition"} aria-hidden="true" />
      </button>
      {expanded ? (
        <pre className="max-h-56 overflow-auto border-t border-border bg-muted px-3 py-2 text-[0.7rem] leading-5 text-muted-foreground">
          {detail}
        </pre>
      ) : null}
    </div>
  )
}


function CitationPanel({
  events,
  selectedArtifactId,
}: {
  events: AgentRunEvent[]
  selectedArtifactId?: string
}) {
  const citations = events
    .filter((event) => event.type === "citation.created")
    .map((event) => citationEventData(event.data))
    .filter((citation): citation is AgentCitationView => Boolean(citation?.id))
  if (citations.length === 0) return null
  return (
    <div id="evidence" className="mb-4 rounded-md border border-border bg-background p-3">
      <div className="mb-2 text-xs font-medium text-muted-foreground">Evidence</div>
      <div className="flex flex-wrap gap-2">
        {citations.map((citation) => (
          <CitationChip
            key={citation.id}
            citation={citation}
            selected={Boolean(citation.artifactId && citation.artifactId === selectedArtifactId)}
          />
        ))}
      </div>
    </div>
  )
}

function CitationChip({ citation, selected }: { citation: AgentCitationView; selected: boolean }) {
  const selectArtifact = useAgentProjectionStore((state) => state.selectArtifact)
  const onClick = () => {
    if (citation.artifactId) selectArtifact(citation.artifactId)
    document.getElementById("evidence")?.scrollIntoView({ behavior: "smooth", block: "start" })
  }
  return (
    <button
      type="button"
      className={
        selected
          ? "inline-flex min-h-11 items-center gap-1 rounded-md border border-primary bg-muted px-3 py-2 text-left text-xs text-foreground"
          : "inline-flex min-h-11 items-center gap-1 rounded-md border border-border bg-card px-3 py-2 text-left text-xs text-muted-foreground transition hover:border-primary/40 hover:text-foreground"
      }
      onClick={onClick}
    >
      <ExternalLink className="size-3" aria-hidden="true" />
      <span>{citation.text || citation.id}</span>
    </button>
  )
}

function ChatMessage() {
  return (
    <MessagePrimitive.Root className="w-full py-4">
      <MessagePrimitive.If assistant>
        <div className="grid w-full grid-cols-[2rem_minmax(0,1fr)] gap-3">
          <div className="flex size-8 items-center justify-center rounded-md border border-border bg-muted text-muted-foreground">
            <Bot className="size-4" aria-hidden="true" />
          </div>
          <div className="min-w-0 rounded-md border border-border bg-card px-4 py-3 text-sm shadow-sm">
            <MessagePrimitive.Content
              components={{
                Text: MarkdownContent,
                Empty: RunningMessage,
                tools: { Fallback: ToolCallPart },
              }}
            />
          </div>
        </div>
      </MessagePrimitive.If>

      <MessagePrimitive.If user>
        <div className="ml-auto grid max-w-[80%] grid-cols-[minmax(0,1fr)_2rem] gap-3 sm:max-w-[70%]">
          <div className="min-w-0 rounded-md bg-primary px-4 py-3 text-sm text-primary-foreground">
            <MessagePrimitive.Content components={{ Text: PlainText }} />
          </div>
          <div className="flex size-8 items-center justify-center rounded-md border border-border bg-background text-muted-foreground">
            <UserRound className="size-4" aria-hidden="true" />
          </div>
        </div>
      </MessagePrimitive.If>
    </MessagePrimitive.Root>
  )
}

function ChatComposer({
  autoFocus = false,
  variant = "thread",
}: {
  autoFocus?: boolean
  variant?: "home" | "thread"
}) {
  const isHome = variant === "home"
  return (
    <ComposerPrimitive.Root
      className={
        isHome
          ? "flex min-h-16 w-full items-end gap-2 rounded-lg border border-border bg-card p-2 shadow-md shadow-muted/40"
          : "flex min-h-14 w-full items-end gap-2 rounded-lg border border-border bg-card p-2 shadow-sm"
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
      <ThreadPrimitive.If running={false}>
        <ComposerPrimitive.Send asChild>
          <Button type="submit" size="icon" aria-label="Send message" className={isHome ? "size-11" : undefined}>
            <ArrowRight className="size-4" aria-hidden="true" />
          </Button>
        </ComposerPrimitive.Send>
      </ThreadPrimitive.If>
      <ThreadPrimitive.If running>
        <ComposerPrimitive.Cancel asChild>
          <Button type="button" size="icon" variant="secondary" aria-label="Cancel run" className={isHome ? "size-11" : undefined}>
            <CircleStop className="size-4" aria-hidden="true" />
          </Button>
        </ComposerPrimitive.Cancel>
      </ThreadPrimitive.If>
    </ComposerPrimitive.Root>
  )
}

function PlainText({ text }: { text: string }) {
  return <p className="whitespace-pre-wrap leading-6">{text}</p>
}

function RunningMessage() {
  return <p className="text-sm text-muted-foreground">Working...</p>
}

function ToolCallPart({ toolName }: { toolName: string }) {
  return (
    <div className="rounded-md border border-border bg-muted px-3 py-2 text-xs text-muted-foreground">
      {toolName}
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
  const assistantStatus = run.status === "cancelled" ? "cancelled" : run.status === "running" || run.status === "queued" ? "running" : "complete"
  const assistantText = run.finalAnswer || (assistantStatus === "running" ? "" : queuedRunMessage(run.status, run.error))
  return [
    userMessageWithID(`user_${run.id}`, run.input, run.createdAt),
    assistantMessage(`assistant_${run.id}`, assistantText, assistantStatus),
  ]
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
    if (state.runs[runID]?.status === "running") return runID
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

type AgentRunRoute = {
  sessionID: string
  runID: string
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

function shortID(value: string) {
  const normalized = value.replace(/^[^_]+_/, "")
  return normalized.length > 8 ? normalized.slice(0, 8) : normalized
}

type ToolEventData = {
  toolCallId?: string
  name?: string
  status?: string
  input?: unknown
  output?: unknown
  durationMs?: number
  error?: string
}

type ToolCallView = Required<Pick<ToolEventData, "status">> & Omit<ToolEventData, "status"> & {
  id: string
}

function groupToolCalls(events: AgentRunEvent[]): ToolCallView[] {
  const calls = new Map<string, ToolCallView>()
  for (const event of events) {
    if (!event.type.startsWith("tool.")) continue
    const data = toolEventData(event.data)
    const id = data.toolCallId || event.id
    const previous = calls.get(id)
    const status = toolStatusFromEvent(event.type, data.status, previous?.status)
    calls.set(id, {
      id,
      toolCallId: data.toolCallId,
      name: data.name ?? previous?.name,
      status,
      input: data.input !== undefined ? data.input : previous?.input,
      output: data.output !== undefined ? data.output : previous?.output,
      durationMs: data.durationMs ?? previous?.durationMs,
      error: data.error ?? previous?.error,
    })
  }
  return Array.from(calls.values())
}

function toolStatusFromEvent(eventType: string, status?: string, previous = "started") {
  if (eventType === "tool.failed") return "failed"
  if (eventType === "tool.completed" || status === "completed") return "completed"
  if (status) return status
  if (eventType === "tool.audit") return previous
  return eventType.replace("tool.", "")
}

function toolEventData(value: unknown): ToolEventData {
  if (!value || typeof value !== "object") return {}
  return value as ToolEventData
}

function toolEventSummary(data: ToolEventData) {
  if (data.error) return data.error
  if (data.output !== undefined) return `output ${compactJSON(data.output)}`
  if (data.input !== undefined) return `input ${compactJSON(data.input)}`
  return "waiting"
}

function toolEventDetail(data: ToolEventData) {
  const detail = {
    name: data.name,
    status: data.status,
    durationMs: data.durationMs,
    input: data.input,
    output: data.output,
    error: data.error,
  }
  return JSON.stringify(detail, null, 2)
}

function compactJSON(value: unknown) {
  const text = typeof value === "string" ? value : JSON.stringify(value)
  if (!text) return ""
  return text.length > 96 ? `${text.slice(0, 93)}...` : text
}

function runStatusLabel(status: string) {
  if (status === "queued") return "queued"
  if (status === "running") return "running"
  if (status === "completed") return "completed"
  if (status === "failed") return "failed"
  if (status === "cancelled") return "cancelled"
  if (status === "started") return "started"
  return status
}

function statusDotClass(status: string) {
  const base = "size-1.5 shrink-0 rounded-full"
  if (status === "completed") return `${base} bg-accent`
  if (status === "failed") return `${base} bg-destructive`
  if (status === "cancelled") return `${base} bg-muted-foreground`
  return `${base} bg-primary`
}

type AgentCitationView = {
  id: string
  artifactId?: string
  text?: string
  target?: unknown
}

function citationEventData(value: unknown): AgentCitationView | undefined {
  if (!value || typeof value !== "object") return undefined
  const wrappedCitation = (value as { citation?: unknown }).citation
  const citation = wrappedCitation && typeof wrappedCitation === "object" ? wrappedCitation : value
  if (!hasCitationId(citation)) return undefined
  return citation
}

function hasCitationId(value: unknown): value is AgentCitationView {
  return Boolean(value && typeof value === "object" && typeof (value as { id?: unknown }).id === "string")
}
