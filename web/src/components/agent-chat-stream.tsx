import { Activity, ChevronDown, Copy, LoaderCircle, RotateCcw, UserRound } from "lucide-react"
import { useEffect, useState } from "react"
import type { ThreadMessage } from "@assistant-ui/react"

import { EvidenceList } from "@/components/evidence-list"
import { MarkdownContent } from "@/components/markdown-content"
import { Button } from "@/components/ui/button"
import type { AgentArtifact, AgentRun, AgentRunEvent } from "@/lib/agent-store"
import {
  conversationSegments,
  estimateTokenCount,
  formatCompactNumber,
  formatDuration,
  isActiveToolStatus,
  lastIndexOf,
  runActivitySummary,
  runCitations,
  runElapsedLabel,
  runStatusLabel,
  shortID,
  splitRunResponse,
  streamStatusDot,
  toolGroupDuration,
  toolGroupStatus,
  toolGroupSummary,
  toolSegmentDetail,
  toolSegmentSummary,
  toolSegmentsIn,
  type EvidenceSegment,
  type ResponseSegment,
  type RunActivitySummary,
  type ToolGroupSegment,
  type ToolSegment,
} from "@/components/agent-chat-stream-model"

export type AgentRunRoute = {
  sessionID: string
  runID: string
}

export function LocalMessageConversation({ messages }: { messages: ThreadMessage[] }) {
  return (
    <div className="space-y-4 pb-2">
      {messages.map((message) => {
        const text = threadMessageText(message)
        if (message.role === "user") return <UserStreamMessage key={message.id} text={text} />
        return <AssistantStreamMessage key={message.id} text={text} running={message.status?.type === "running"} receivedTokens={estimateTokenCount(text)} />
      })}
    </div>
  )
}

function threadMessageText(message: ThreadMessage) {
  return message.content
    .filter((part) => part.type === "text")
    .map((part) => part.text)
    .join("\n")
}

type SessionConversationProps = {
  activeRun?: AgentRun
  artifactsById: Record<string, AgentArtifact>
  eventsById: Record<string, AgentRunEvent>
  isRunning: boolean
  onRetryRun: (run: AgentRun) => void
  onSelectArtifact: (artifactId?: string) => void
  runs: AgentRun[]
  status: string
}

export function SessionConversation({
  activeRun,
  artifactsById,
  eventsById,
  isRunning,
  onRetryRun,
  onSelectArtifact,
  runs,
}: SessionConversationProps) {
  return (
    <div className="space-y-5 pb-2">
      {runs.map((run) => (
        <RunConversation
          key={run.id}
          artifactsById={artifactsById}
          events={run.eventIds.map((eventID) => eventsById[eventID]).filter((event): event is AgentRunEvent => Boolean(event))}
          isActive={run.id === activeRun?.id}
          isRunning={isRunning && run.id === activeRun?.id}
          onRetryRun={onRetryRun}
          onSelectArtifact={onSelectArtifact}
          run={run}
          status={run.status}
        />
      ))}
    </div>
  )
}

type RunConversationProps = {
  artifactsById: Record<string, AgentArtifact>
  events: AgentRunEvent[]
  isActive: boolean
  isRunning: boolean
  onRetryRun: (run: AgentRun) => void
  onSelectArtifact: (artifactId?: string) => void
  run: AgentRun
  status: string
}

function RunConversation({
  artifactsById,
  events,
  isActive,
  isRunning,
  onRetryRun,
  onSelectArtifact,
  run,
  status,
}: RunConversationProps) {
  const nowMs = useRunClock(isRunning)
  const segments = conversationSegments(run, events, status, nowMs)
  const activity = runActivitySummary(run, events, segments, status)
  const response = splitRunResponse(segments, isRunning, status)
  const citations = runCitations(events)
  const citationAnchorIndex = lastIndexOf(response.finalSegments, (segment) => segment.type === "assistant" && Boolean(segment.content.trim()))
  return (
    <div className={isActive ? "space-y-4" : "space-y-4 opacity-95"}>
      {response.userSegments.map((segment) => <UserStreamMessage key={segment.id} text={segment.content} />)}
      {response.workSegments.length > 0 ? (
        <WorkStreamMessage
          activity={activity}
          events={events}
          onSelectArtifact={onSelectArtifact}
          running={isRunning}
          segments={response.workSegments}
          status={status}
          run={run}
          nowMs={nowMs}
        />
      ) : null}
      {response.finalSegments.map((segment, index) => renderResponseSegment({
        activity,
        artifactsById,
        citations: index === citationAnchorIndex ? citations : [],
        isRunning,
        onRetryRun,
        onSelectArtifact,
        run,
        segment,
      }))}
    </div>
  )
}

export function RunComposerStats({
  events,
  isRunning,
  run,
  routeRun,
  status,
}: {
  events: AgentRunEvent[]
  isRunning: boolean
  run?: AgentRun
  routeRun?: AgentRunRoute
  status: string
}) {
  const nowMs = useRunClock(isRunning)
  if (!run && !routeRun) return null
  const segments = conversationSegments(run, events, status, nowMs)
  const activity = runActivitySummary(run, events, segments, status)
  return (
    <div className="mb-2 flex items-center justify-between gap-3 rounded-md border border-border bg-background/95 px-3 py-2 text-xs text-muted-foreground shadow-sm">
      <div className="flex min-w-0 items-center gap-2">
        <span className={streamStatusDot(status)} aria-hidden="true" />
        <span className="truncate">{routeRun ? `run ${shortID(routeRun.runID)}` : "run"}</span>
        <span className="rounded-md border border-border px-2 py-0.5 capitalize">{runStatusLabel(status)}</span>
      </div>
      <div className="flex min-w-0 flex-1 items-center justify-end gap-2 text-[0.72rem]">
        {isRunning ? <LoaderCircle className="size-3.5 shrink-0 animate-spin text-primary" aria-hidden="true" /> : <Activity className="size-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />}
        <span className="hidden truncate text-muted-foreground sm:inline">{activity.stage}</span>
        <span className="shrink-0 rounded-md bg-muted px-2 py-1 text-muted-foreground">sent ~{formatCompactNumber(activity.sentTokens)} tok</span>
        <span className="shrink-0 rounded-md bg-muted px-2 py-1 text-muted-foreground">received ~{formatCompactNumber(activity.receivedTokens)} tok</span>
      </div>
    </div>
  )
}


function renderResponseSegment({
  activity,
  artifactsById,
  citations = [],
  isRunning,
  onRetryRun,
  onSelectArtifact,
  run,
  segment,
}: {
  activity: RunActivitySummary
  artifactsById?: Record<string, AgentArtifact>
  citations?: EvidenceSegment[]
  isRunning: boolean
  onRetryRun: (run: AgentRun) => void
  onSelectArtifact: (artifactId?: string) => void
  run: AgentRun
  segment: ResponseSegment
}) {
  if (segment.type === "assistant") {
    return (
      <AssistantStreamMessage
        key={segment.id}
        artifactsById={artifactsById}
        citations={citations}
        onRetry={!isRunning ? () => onRetryRun(run) : undefined}
        onSelectArtifact={onSelectArtifact}
        receivedTokens={activity.receivedTokens}
        running={segment.running && isRunning}
        text={segment.content}
      />
    )
  }
  if (segment.type === "tool") return <ToolStreamMessage key={segment.id} segment={segment} />
  if (segment.type === "tool_group") return <ToolGroupStreamMessage key={segment.id} defaultExpanded={false} segment={segment} />
  if (segment.type === "evidence") return <EvidenceStreamMessage key={segment.id} segment={segment} onSelectArtifact={onSelectArtifact} />
  if (segment.type === "error") return <ErrorStreamMessage key={segment.id} text={segment.content} />
  return null
}

function renderWorkSegment(
  segment: ResponseSegment,
  activity: RunActivitySummary,
  onSelectArtifact: (artifactId?: string) => void,
  toolGroupDefaultExpanded: boolean,
) {
  if (segment.type === "assistant") {
    return (
      <AssistantStreamMessage
        key={segment.id}
        receivedTokens={activity.receivedTokens}
        running={segment.running}
        showActions={false}
        text={segment.content}
      />
    )
  }
  if (segment.type === "tool") return <ToolStreamMessage key={segment.id} segment={segment} />
  if (segment.type === "tool_group") return <ToolGroupStreamMessage key={segment.id} defaultExpanded={toolGroupDefaultExpanded} segment={segment} />
  if (segment.type === "evidence") return <EvidenceStreamMessage key={segment.id} segment={segment} onSelectArtifact={onSelectArtifact} />
  if (segment.type === "error") return <ErrorStreamMessage key={segment.id} text={segment.content} />
  return null
}

function WorkStreamMessage({
  activity,
  events,
  nowMs,
  onSelectArtifact,
  running,
  segments,
  status,
  run,
}: {
  activity: RunActivitySummary
  events: AgentRunEvent[]
  nowMs: number
  onSelectArtifact: (artifactId?: string) => void
  running: boolean
  segments: ResponseSegment[]
  status: string
  run: AgentRun
}) {
  const defaultExpanded = running || status === "completed" || status === "failed"
  const [manualExpanded, setManualExpanded] = useState<boolean | undefined>(undefined)
  const expanded = manualExpanded ?? defaultExpanded
  const tools = toolSegmentsIn(segments)
  const assistantSteps = segments.filter((segment) => segment.type === "assistant" && segment.content).length
  const duration = runElapsedLabel(run, events, running ? nowMs : undefined)
  const summaryParts = []
  if (assistantSteps > 0) summaryParts.push(`${assistantSteps} notes`)
  if (tools.length > 0) summaryParts.push(`${tools.length} tool call${tools.length === 1 ? "" : "s"}`)
  return (
    <div className="w-full text-sm">
      <div className="min-w-0 border-l border-border/80 pl-3">
        <button type="button" className="group flex w-full items-center gap-2 py-1 text-left" onClick={() => setManualExpanded((value) => !(value ?? defaultExpanded))}>
          <span className={streamStatusDot(status)} aria-hidden="true" />
          <span className="rounded-full bg-muted px-2.5 py-1 text-xs font-medium text-foreground group-hover:bg-secondary">{running ? `Working for ${duration}` : `Worked for ${duration}`}</span>
          <span className="hidden min-w-0 truncate text-xs text-muted-foreground sm:inline">{running ? activity.stage : summaryParts.join(" · ")}</span>
          <ChevronDown className={expanded ? "ml-auto size-3.5 rotate-180 text-muted-foreground transition" : "ml-auto size-3.5 text-muted-foreground transition"} aria-hidden="true" />
        </button>
        {expanded ? (
          <div className="mt-3 space-y-4">
            {segments.map((segment) => renderWorkSegment(segment, activity, onSelectArtifact, expanded))}
          </div>
        ) : null}
      </div>
    </div>
  )
}

function ThinkingPlaceholder({ active }: { active: boolean }) {
  return (
    <div className="flex items-center gap-2 text-muted-foreground">
      {active ? <LoaderCircle className="size-3.5 animate-spin" aria-hidden="true" /> : null}
      <span>{active ? "Thinking" : "No answer text yet."}</span>
      {active ? (
        <span className="inline-flex gap-1" aria-hidden="true">
          <span className="size-1 animate-bounce rounded-full bg-muted-foreground [animation-delay:-0.2s]" />
          <span className="size-1 animate-bounce rounded-full bg-muted-foreground [animation-delay:-0.1s]" />
          <span className="size-1 animate-bounce rounded-full bg-muted-foreground" />
        </span>
      ) : null}
    </div>
  )
}


function useRunClock(active: boolean) {
  const [nowMs, setNowMs] = useState(() => Date.now())
  useEffect(() => {
    if (!active) return undefined
    const id = window.setInterval(() => setNowMs(Date.now()), 1000)
    return () => window.clearInterval(id)
  }, [active])
  return nowMs
}


function UserStreamMessage({ text }: { text: string }) {
  return (
    <div className="ml-auto grid max-w-[80%] grid-cols-[minmax(0,1fr)_2rem] gap-3 sm:max-w-[70%]">
      <div className="min-w-0 rounded-md bg-primary px-4 py-3 text-sm text-primary-foreground">
        <p className="whitespace-pre-wrap leading-6">{text}</p>
      </div>
      <div className="flex size-8 items-center justify-center rounded-md border border-border bg-background text-muted-foreground">
        <UserRound className="size-4" aria-hidden="true" />
      </div>
    </div>
  )
}

function AssistantStreamMessage({
  artifactsById = {},
  citations = [],
  onRetry,
  onSelectArtifact,
  receivedTokens,
  running,
  showActions = true,
  text,
}: {
  artifactsById?: Record<string, AgentArtifact>
  citations?: EvidenceSegment[]
  onRetry?: () => void
  onSelectArtifact?: (artifactId?: string) => void
  receivedTokens: number
  running?: boolean
  showActions?: boolean
  text: string
}) {
  const [copied, setCopied] = useState(false)
  const canShowActions = Boolean(showActions && text && !running)
  const copyText = () => {
    void navigator.clipboard?.writeText(text).then(() => {
      setCopied(true)
      window.setTimeout(() => setCopied(false), 1200)
    })
  }
  return (
    <div className="w-full">
      <div className="min-w-0 text-sm leading-6 text-foreground">
        {running ? (
          <div className="mb-2 flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
            <span className="inline-flex items-center gap-1.5">
              <span className="size-1.5 animate-pulse rounded-full bg-primary" aria-hidden="true" />
              Streaming
            </span>
            <span className="rounded-md bg-muted px-2 py-0.5">received ~{formatCompactNumber(receivedTokens)} tok</span>
          </div>
        ) : null}
        {text ? <MarkdownContent text={text} /> : <ThinkingPlaceholder active={Boolean(running)} />}
        <EvidenceList artifactsById={artifactsById} citations={citations} onSelectArtifact={onSelectArtifact} />
        {canShowActions ? (
          <div className="mt-3 flex items-center gap-1 text-muted-foreground">
            <Button type="button" size="icon-sm" variant="ghost" className="size-8" title="Copy" aria-label="Copy response" onClick={copyText}>
              <Copy className="size-3.5" aria-hidden="true" />
            </Button>
            <Button type="button" size="icon-sm" variant="ghost" className="size-8" title="Retry from this response" aria-label="Retry from this response" onClick={onRetry} disabled={!onRetry}>
              <RotateCcw className="size-3.5" aria-hidden="true" />
            </Button>
            {copied ? <span className="px-1 text-xs text-muted-foreground">Copied</span> : null}
          </div>
        ) : null}
      </div>
    </div>
  )
}


function ToolStreamMessage({
  segment,
}: {
  segment: ToolSegment
}) {
  const [expanded, setExpanded] = useState(false)
  const detail = toolSegmentDetail(segment)
  return (
    <div className="w-full text-sm">
      <div className="min-w-0 border-l border-border/80 pl-3">
        <button type="button" className="group flex w-full items-center gap-2 py-1 text-left" onClick={() => setExpanded((value) => !value)}>
          <span className={streamStatusDot(segment.status)} aria-hidden="true" />
          <span className="max-w-full truncate rounded-full bg-muted px-2.5 py-1 text-xs font-medium text-foreground group-hover:bg-secondary">{segment.name || "tool"}</span>
          <DurationBadge durationMs={segment.durationMs} />
          <span className="shrink-0 text-xs capitalize text-muted-foreground">{runStatusLabel(segment.status)}</span>
          <ChevronDown className={expanded ? "ml-auto size-3.5 rotate-180 text-muted-foreground transition" : "ml-auto size-3.5 text-muted-foreground transition"} aria-hidden="true" />
        </button>
        <div className="flex min-w-0 flex-wrap items-center gap-2 text-xs text-muted-foreground">
          <span className="min-w-0 truncate">{toolSegmentSummary(segment)}</span>
        </div>
        {expanded ? <pre className="mt-2 max-h-48 overflow-auto border-l border-border/80 pl-3 text-[0.7rem] leading-5 text-muted-foreground">{detail}</pre> : null}
      </div>
    </div>
  )
}

function ToolGroupStreamMessage({
  defaultExpanded,
  segment,
}: {
  defaultExpanded: boolean
  segment: ToolGroupSegment
}) {
  const status = toolGroupStatus(segment.tools)
  const hasActiveTool = segment.tools.some((tool) => isActiveToolStatus(tool.status))
  const [manualExpanded, setManualExpanded] = useState<boolean | undefined>(undefined)
  const autoExpanded = defaultExpanded || hasActiveTool
  const expanded = manualExpanded ?? autoExpanded
  const durationMs = toolGroupDuration(segment.tools)
  return (
    <div className="w-full text-sm">
      <div className="min-w-0 border-l border-border/80 pl-3">
        <button type="button" className="group flex w-full items-center gap-2 py-1 text-left" onClick={() => setManualExpanded((value) => !(value ?? autoExpanded))}>
          <span className={streamStatusDot(status)} aria-hidden="true" />
          <span className="max-w-full truncate rounded-full bg-muted px-2.5 py-1 text-xs font-medium text-foreground group-hover:bg-secondary">Tool calls</span>
          <span className="shrink-0 text-xs text-muted-foreground">{segment.tools.length} calls</span>
          <span className="shrink-0 text-xs capitalize text-muted-foreground">{runStatusLabel(status)}</span>
          <DurationBadge durationMs={durationMs} label="total" />
          <ChevronDown className={expanded ? "ml-auto size-3.5 rotate-180 text-muted-foreground transition" : "ml-auto size-3.5 text-muted-foreground transition"} aria-hidden="true" />
        </button>
        <div className="flex min-w-0 flex-wrap items-center gap-2 text-xs text-muted-foreground">
          <span className="min-w-0 truncate">{toolGroupSummary(segment.tools)}</span>
        </div>
        {expanded ? (
          <div className="mt-2 flex flex-col gap-1.5">
            {segment.tools.map((tool) => <ToolGroupRow key={tool.id} segment={tool} />)}
          </div>
        ) : null}
      </div>
    </div>
  )
}

function DurationBadge({
  compact,
  durationMs,
  label,
}: {
  compact?: boolean
  durationMs?: number
  label?: string
}) {
  if (typeof durationMs !== "number") return null
  return (
    <span
      className={compact
        ? "shrink-0 rounded-md bg-muted px-1.5 py-0.5 text-[0.68rem] font-medium tabular-nums text-foreground"
        : "shrink-0 rounded-md border border-border bg-background px-2 py-0.5 text-xs font-medium tabular-nums text-foreground"}
      title={`Tool execution time: ${formatDuration(durationMs)}`}
    >
      {label ? `${label} ` : ""}{formatDuration(durationMs)}
    </span>
  )
}

function ToolGroupRow({ segment }: { segment: ToolSegment }) {
  const [expanded, setExpanded] = useState(false)
  return (
    <div className="rounded-md border border-border/80 bg-background/60 px-3 py-2">
      <button type="button" className="flex w-full min-w-0 items-center gap-2 text-left" onClick={() => setExpanded((value) => !value)}>
        <span className={streamStatusDot(segment.status)} aria-hidden="true" />
        <span className="min-w-0 truncate text-xs font-medium text-foreground">{segment.name || "tool"}</span>
        <DurationBadge durationMs={segment.durationMs} compact />
        <span className="ml-auto shrink-0 text-xs capitalize text-muted-foreground">{runStatusLabel(segment.status)}</span>
        <ChevronDown className={expanded ? "size-3.5 rotate-180 text-muted-foreground transition" : "size-3.5 text-muted-foreground transition"} aria-hidden="true" />
      </button>
      <div className="mt-1 truncate text-xs text-muted-foreground">{toolSegmentSummary(segment)}</div>
      {expanded ? <pre className="mt-2 max-h-48 overflow-auto border-l border-border/80 pl-3 text-[0.7rem] leading-5 text-muted-foreground">{toolSegmentDetail(segment)}</pre> : null}
    </div>
  )
}

function EvidenceStreamMessage({
  segment,
  onSelectArtifact,
}: {
  segment: EvidenceSegment
  onSelectArtifact: (artifactId?: string) => void
}) {
  return (
    <div className="w-full text-sm">
      <div className="min-w-0 border-l border-accent/40 pl-3">
        <button
          type="button"
          className="inline-flex max-w-full items-center gap-2 rounded-full border border-border bg-card px-3 py-1.5 text-left text-xs text-muted-foreground transition hover:border-primary/40 hover:text-foreground"
          onClick={() => onSelectArtifact(segment.artifactId)}
        >
          <span className="size-1.5 rounded-full bg-accent" aria-hidden="true" />
          <span className="truncate">{segment.text || "Evidence"}</span>
          <span className="shrink-0 text-foreground">Pin</span>
        </button>
      </div>
    </div>
  )
}

function ErrorStreamMessage({ text }: { text: string }) {
  return (
    <div className="w-full text-sm">
      <div className="min-w-0 border-l border-destructive/50 pl-3 text-destructive">{text}</div>
    </div>
  )
}
