import {
  AssistantRuntimeProvider,
  ComposerPrimitive,
  MessagePrimitive,
  ThreadPrimitive,
  useExternalStoreRuntime,
  type AppendMessage,
  type ThreadMessage,
} from "@assistant-ui/react"
import { ArrowUp, Bot, CircleStop, ExternalLink, Play, Plus, RotateCcw, Search, Server, Sparkles, UserRound } from "lucide-react"
import { useCallback, useEffect, useState } from "react"

import { ArtifactPanel } from "@/components/artifact-panel"
import { MarkdownContent } from "@/components/markdown-content"
import { Button } from "@/components/ui/button"
import { demoAgentAnswer, demoK8sResourceArtifact, demoK8sResourceListArtifact, demoK8sTopologyArtifact } from "@/lib/demo-agent"
import { useAgentProjectionStore, type AgentRunEvent } from "@/lib/agent-store"

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
  const [routeRun, setRouteRun] = useState(readRouteRun)
  const activeRunCount = useAgentProjectionStore((state) => {
    if (!state.activeSessionId) return 0
    return state.sessions[state.activeSessionId]?.runIds.length ?? 0
  })
  const ensureSession = useAgentProjectionStore((state) => state.ensureSession)
  const startRun = useAgentProjectionStore((state) => state.startRun)
  const appendRunEvent = useAgentProjectionStore((state) => state.appendRunEvent)
  const completeRun = useAgentProjectionStore((state) => state.completeRun)
  const cancelRun = useAgentProjectionStore((state) => state.cancelRun)
  const upsertArtifact = useAgentProjectionStore((state) => state.upsertArtifact)
  const addCitation = useAgentProjectionStore((state) => state.addCitation)
  const startNewSession = useAgentProjectionStore((state) => state.startNewSession)
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

    const sessionID = ensureSession(prompt)
    const runID = startRun(sessionID, prompt)
    openRunPage(sessionID, runID)
    setRouteRun({ sessionID, runID })
    appendRunEvent(runID, { type: "message.created", data: { role: "user", content: prompt } })

    const assistantID = newMessageID("assistant")
    setIsRunning(true)
    setMessages((current) => [
      ...current,
      userMessage(prompt),
      assistantMessage(assistantID, "", "running"),
    ])

    const toolCallID = newMessageID("tool")
    appendRunEvent(runID, {
      type: "tool.started",
      data: {
        toolCallId: toolCallID,
        name: "kube_insight_demo_search",
        status: "started",
        input: { query: prompt },
      },
    })

    await delay(350)

    appendRunEvent(runID, {
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
    const markdownArtifactID = upsertArtifact(runID, {
      kind: "markdown",
      title: "Demo answer",
      data: { markdown: answer },
    })
    const resourceArtifactID = upsertArtifact(runID, {
      kind: "k8s.resource",
      title: "Pod default/api-0",
      data: demoK8sResourceArtifact(prompt),
    })
    upsertArtifact(runID, {
      kind: "k8s.resource_list",
      title: "Demo resource candidates",
      data: demoK8sResourceListArtifact(prompt),
    })
    upsertArtifact(runID, {
      kind: "k8s.topology",
      title: "Service default/api topology",
      data: demoK8sTopologyArtifact(prompt),
    })
    addCitation(runID, {
      id: "citation_demo_runtime",
      artifactId: resourceArtifactID,
      text: "Demo runtime",
      target: { kind: "artifact", id: resourceArtifactID, relatedArtifactId: markdownArtifactID },
    })
    appendRunEvent(runID, { type: "message.created", data: { role: "assistant", content: answer } })
    completeRun(runID, answer)

    setMessages((current) =>
      current.map((item) =>
        item.id === assistantID ? assistantMessage(assistantID, answer, "complete") : item,
      ),
    )
    setIsRunning(false)
  }, [addCitation, appendRunEvent, completeRun, ensureSession, startRun, upsertArtifact])

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
    const latestRunningRun = latestRunningRunID()
    if (latestRunningRun) cancelRun(latestRunningRun)
  }, [cancelRun])

  const handleNewSession = useCallback(() => {
    startNewSession()
    setMessages([])
    setIsRunning(false)
    setRouteRun(undefined)
    if (window.location.pathname !== "/") window.history.pushState({}, "", "/")
  }, [startNewSession])

  const handleRetry = useCallback(() => {
    if (!activeRun || isRunning) return
    void runPrompt(activeRun.input)
  }, [activeRun, isRunning, runPrompt])

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
      <main className="min-h-svh bg-background text-foreground">
        <div className="mx-auto flex min-h-svh w-full max-w-5xl flex-col px-4 py-4 sm:px-6 lg:px-8">
          <header className="flex h-12 items-center justify-between border-b border-border/80">
            <div className="flex items-center gap-2 text-sm font-medium">
              <span className="flex size-7 items-center justify-center rounded-md bg-primary text-primary-foreground">
                <Sparkles className="size-4" aria-hidden="true" />
              </span>
              <span>kube-insight</span>
            </div>
            <div className="flex items-center gap-2">
              <div className="hidden items-center gap-2 text-xs text-muted-foreground sm:flex">
                <Server className="size-4" aria-hidden="true" />
                <span>{activeRunCount > 0 ? `${activeRunCount} run${activeRunCount === 1 ? "" : "s"}` : "local agent"}</span>
              </div>
              <Button type="button" size="sm" variant="outline" onClick={handleNewSession} disabled={isRunning}>
                <Plus className="size-3.5" aria-hidden="true" />
                New
              </Button>
            </div>
          </header>

          <ThreadPrimitive.Root className="flex min-h-0 flex-1 flex-col">
            <ThreadPrimitive.Viewport className="flex min-h-0 flex-1 flex-col overflow-y-auto scroll-smooth py-6">
              <ThreadPrimitive.Empty>
                <div className="mx-auto flex min-h-[calc(100svh-6.5rem)] w-full max-w-3xl flex-col justify-center gap-5 pb-16 pt-10">
                  <div className="flex flex-col items-center gap-2 text-center">
                    <h1 className="text-4xl font-semibold tracking-normal text-foreground sm:text-5xl">
                      kube-insight
                    </h1>
                    <div className="flex items-center gap-2 text-xs text-muted-foreground">
                      <span className="size-1.5 rounded-full bg-accent" aria-hidden="true" />
                      <span>{activeRunCount > 0 ? `${activeRunCount} active run${activeRunCount === 1 ? "" : "s"}` : "local agent ready"}</span>
                    </div>
                  </div>
                  <ChatComposer autoFocus variant="home" />
                  <div className="mx-auto grid w-full max-w-2xl grid-cols-1 gap-2 sm:grid-cols-2">
                    {starterPrompts.map((prompt) => (
                      <ThreadPrimitive.Suggestion
                        key={prompt}
                        prompt={prompt}
                        method="replace"
                        autoSend={false}
                        className="min-h-10 rounded-md border border-border bg-background px-3 py-2 text-left text-sm text-muted-foreground transition hover:border-primary/40 hover:bg-muted hover:text-foreground"
                      >
                        {prompt}
                      </ThreadPrimitive.Suggestion>
                    ))}
                  </div>
                </div>
              </ThreadPrimitive.Empty>

              <ThreadPrimitive.If empty={false}>
                <RunPageHeader
                  routeRun={routeRun}
                  status={activeRun?.status ?? (isRunning ? "running" : "completed")}
                  isRunning={isRunning}
                  canRetry={Boolean(activeRun)}
                  onStop={onCancel}
                  onRetry={handleRetry}
                  onContinue={handleContinue}
                />
                <RunTimeline events={activeRunEvents} />
                <CitationPanel events={activeRunEvents} selectedArtifactId={selectedArtifactId} />
                <ArtifactPanel artifacts={activeRunArtifacts} selectedArtifactId={selectedArtifactId} />
              </ThreadPrimitive.If>

              <ThreadPrimitive.Messages components={{ Message: ChatMessage }} />

              <ThreadPrimitive.If empty={false}>
                <ThreadPrimitive.ViewportFooter className="sticky bottom-0 mt-6 bg-background/95 py-4 backdrop-blur">
                  <ChatComposer />
                </ThreadPrimitive.ViewportFooter>
              </ThreadPrimitive.If>
            </ThreadPrimitive.Viewport>
          </ThreadPrimitive.Root>
        </div>
      </main>
    </AssistantRuntimeProvider>
  )
}


function RunPageHeader({
  routeRun,
  status,
  isRunning,
  canRetry,
  onStop,
  onRetry,
  onContinue,
}: {
  routeRun?: AgentRunRoute
  status: string
  isRunning: boolean
  canRetry: boolean
  onStop: () => void
  onRetry: () => void
  onContinue: () => void
}) {
  return (
    <div className="sticky top-0 z-10 mb-2 flex items-center justify-between gap-3 border-b border-border/80 bg-background/95 py-3 text-xs text-muted-foreground backdrop-blur">
      <div className="flex min-w-0 items-center gap-2">
        <span className="size-1.5 rounded-full bg-accent" aria-hidden="true" />
        <span className="truncate">{routeRun ? `run ${shortID(routeRun.runID)}` : "run"}</span>
        <span className="rounded-md border border-border px-2 py-1 capitalize">{status}</span>
      </div>
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
  )
}


function RunTimeline({ events }: { events: AgentRunEvent[] }) {
  const toolEvents = events.filter((event) => event.type.startsWith("tool."))
  if (toolEvents.length === 0) return null
  return (
    <div className="mb-4 border-b border-border/80 pb-3">
      <div className="flex flex-col gap-2">
        {toolEvents.map((event) => (
          <ToolTimelineRow key={event.id} event={event} />
        ))}
      </div>
    </div>
  )
}

function ToolTimelineRow({ event }: { event: AgentRunEvent }) {
  const data = toolEventData(event.data)
  const status = data.status || event.type.replace("tool.", "")
  return (
    <div className="grid grid-cols-[7rem_minmax(0,1fr)] gap-3 rounded-md border border-border bg-background px-3 py-2 text-xs sm:grid-cols-[8rem_minmax(0,1fr)_5rem]">
      <div className="flex min-w-0 items-center gap-2 text-muted-foreground">
        <span className={statusDotClass(status)} aria-hidden="true" />
        <span className="truncate capitalize">{status}</span>
      </div>
      <div className="min-w-0">
        <div className="truncate font-medium text-foreground">{data.name || "tool"}</div>
        <div className="truncate text-muted-foreground">{toolEventSummary(data)}</div>
      </div>
      <div className="hidden text-right text-muted-foreground sm:block">
        {typeof data.durationMs === "number" ? `${data.durationMs}ms` : ""}
      </div>
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
          ? "inline-flex min-h-8 items-center gap-1 rounded-md border border-primary bg-muted px-2.5 py-1 text-left text-xs text-foreground"
          : "inline-flex min-h-8 items-center gap-1 rounded-md border border-border bg-card px-2.5 py-1 text-left text-xs text-muted-foreground transition hover:border-primary/40 hover:text-foreground"
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
            : "max-h-44 min-h-10 flex-1 resize-none bg-transparent px-2 py-2 text-sm leading-6 outline-none placeholder:text-muted-foreground"
        }
      />
      <ThreadPrimitive.If running={false}>
        <ComposerPrimitive.Send asChild>
          <Button type="submit" size="icon" aria-label="Send message" className={isHome ? "size-11" : undefined}>
            <ArrowUp className="size-4" aria-hidden="true" />
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

function userMessage(text: string): ThreadMessage {
  return {
    id: newMessageID("user"),
    role: "user",
    createdAt: new Date(),
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
  name?: string
  status?: string
  input?: unknown
  output?: unknown
  durationMs?: number
  error?: string
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

function compactJSON(value: unknown) {
  const text = typeof value === "string" ? value : JSON.stringify(value)
  if (!text) return ""
  return text.length > 96 ? `${text.slice(0, 93)}...` : text
}

function statusDotClass(status: string) {
  const base = "size-1.5 shrink-0 rounded-full"
  if (status === "completed") return `${base} bg-accent`
  if (status === "failed") return `${base} bg-destructive`
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
