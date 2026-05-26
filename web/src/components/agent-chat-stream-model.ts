import type { AgentRun, AgentRunEvent } from "@/lib/agent-store"

export type ConversationSegment =
  | { type: "user"; id: string; content: string }
  | { type: "assistant"; id: string; messageId: string; content: string; final?: boolean; running?: boolean }
  | ToolSegment
  | ToolGroupSegment
  | { type: "error"; id: string; content: string }

export type UserSegment = Extract<ConversationSegment, { type: "user" }>
export type ResponseSegment = Exclude<ConversationSegment, UserSegment>

export type RunResponseSegments = {
  finalSegments: ResponseSegment[]
  userSegments: UserSegment[]
  workSegments: ResponseSegment[]
}

export function splitRunResponse(segments: ConversationSegment[], isRunning: boolean, status: string): RunResponseSegments {
  const userSegments = segments.filter((segment): segment is UserSegment => segment.type === "user")
  const responseSegments = segments.filter((segment): segment is ResponseSegment => segment.type !== "user")
  if (responseSegments.length === 0) return { finalSegments: [], userSegments, workSegments: [] }

  if (isRunning || !isTerminalStreamStatus(status)) {
    return { finalSegments: [], userSegments, workSegments: responseSegments }
  }

  const finalAssistantIndex = lastIndexOf(responseSegments, (segment) => segment.type === "assistant" && Boolean(segment.final) && Boolean(segment.content.trim()))
  const fallbackAssistantIndex = lastIndexOf(responseSegments, (segment) => segment.type === "assistant" && Boolean(segment.content.trim()))
  const answerIndex = finalAssistantIndex >= 0 ? finalAssistantIndex : fallbackAssistantIndex
  if (answerIndex < 0) return { finalSegments: [], userSegments, workSegments: responseSegments }

  if (answerIndex === 0) return { finalSegments: responseSegments, userSegments, workSegments: [] }

  return {
    finalSegments: responseSegments.slice(answerIndex),
    userSegments,
    workSegments: responseSegments.slice(0, answerIndex),
  }
}


export function lastIndexOf<T>(items: T[], predicate: (item: T) => boolean) {
  for (let index = items.length - 1; index >= 0; index -= 1) {
    if (predicate(items[index])) return index
  }
  return -1
}

export function isTerminalStreamStatus(status: string) {
  return status === "completed" || status === "failed" || status === "cancelled"
}

export type ToolGroupSegment = {
  type: "tool_group"
  id: string
  tools: ToolSegment[]
}

export type EvidenceSegment = {
  type: "evidence"
  id: string
  artifactId?: string
  text?: string
  target?: unknown
}

export type RunActivitySummary = {
  receivedTokens: number
  sentTokens: number
  stage: string
}

export type ToolSegment = {
  type: "tool"
  id: string
  toolCallId: string
  childRunIds?: string[]
  childRuns?: ToolChildRunSummary[]
  name?: string
  status: string
  input?: unknown
  outputSummary?: string
  outputArtifactId?: string
  durationMs?: number
  error?: string
}

export type ToolChildRunSummary = {
  id: string
  sessionId?: string
  status?: string
  subagentName?: string
  branchName?: string
  input?: string
  eventCount?: number
  artifactCount?: number
  finalAnswer?: string
}

export function runActivitySummary(
  run: AgentRun | undefined,
  events: AgentRunEvent[],
  segments: ConversationSegment[],
  status: string,
): RunActivitySummary {
  const tools = toolSegmentsIn(segments)
  const sentText = [run?.input, ...tools.map((segment) => compactJSON(segment.input))].filter(Boolean).join("\n")
  const receivedText = segments.map((segment) => {
    if (segment.type === "assistant") return segment.content
    if (segment.type === "tool") return segment.outputSummary || segment.error || ""
    if (segment.type === "tool_group") return segment.tools.map((tool) => tool.outputSummary || tool.error || "").join("\n")
    if (segment.type === "error") return segment.content
    return ""
  }).join("\n")
  const activeTool = [...tools].reverse().find((segment) => isActiveToolStatus(segment.status))
  const lastAssistant = [...segments].reverse().find((segment) => segment.type === "assistant" && segment.content)
  let stage = runStageLabel(status)
  if (activeTool) stage = `Running ${activeTool.name || "tool"}`
  else if (status === "running" && lastAssistant) stage = "Streaming answer"
  else if (status === "running" || status === "queued") stage = "Waiting for model output"
  const usage = latestUsageSummary(events)
  return {
    receivedTokens: usage.receivedTokens ?? estimateTokenCount(receivedText),
    sentTokens: usage.sentTokens ?? estimateTokenCount(sentText),
    stage: usage.phase && (status === "running" || status === "queued") ? usage.phase : stage,
  }
}

function latestUsageSummary(events: AgentRunEvent[]) {
  for (const event of [...events].reverse()) {
    if (event.type !== "usage.delta") continue
    const data = eventDataRecord(event.data)
    const promptTokens = numberField(data.promptTokens)
    const completionTokens = numberField(data.completionTokens)
    const totalTokens = numberField(data.totalTokens)
    const sentTokens = numberField(data.sentTokens) ?? promptTokens
    const receivedTokens = numberField(data.receivedTokens) ?? completionTokens ?? (totalTokens !== undefined && sentTokens !== undefined ? Math.max(0, totalTokens - sentTokens) : undefined)
    return {
      phase: typeof data.phase === "string" ? data.phase : undefined,
      receivedTokens,
      sentTokens,
    }
  }
  return {}
}

function numberField(value: unknown) {
  return typeof value === "number" && Number.isFinite(value) ? value : undefined
}

function toolDurationMs(raw: unknown, startedAt: number | undefined, eventAt: number, nowMs: number, status: string) {
  if (typeof raw === "number" && Number.isFinite(raw)) return Math.max(0, Math.round(raw))
  if (typeof startedAt !== "number" || !Number.isFinite(startedAt)) return undefined
  const end = isActiveToolStatus(status) ? nowMs : eventAt
  if (!Number.isFinite(end)) return undefined
  return Math.max(1, Math.round(end - startedAt))
}

export function conversationSegments(
  run: AgentRun | undefined,
  events: AgentRunEvent[],
  status: string,
  nowMs = Date.now(),
  childRunsByToolCallId: Record<string, ToolChildRunSummary[]> = {},
): ConversationSegment[] {
  const segments: ConversationSegment[] = []
  if (run?.input) segments.push({ type: "user", id: `user_${run.id}`, content: run.input })
  const toolIndexes = new Map<string, number>()
  const toolStartedAt = new Map<string, number>()
  const toolArtifactDurations = new Map<string, number>()
  const toolArtifactChildRunIds = new Map<string, string[]>()
  let hasAssistantText = false

  for (const event of events) {
    if (event.type === "message.created" || event.type === "message.delta" || event.type === "message.completed" || event.type === "answer.final") {
      const data = eventDataRecord(event.data)
      if (data.role !== "assistant") continue
      const delta = typeof data.delta === "string" ? data.delta : ""
      const content = typeof data.content === "string" ? data.content : ""
      if (!delta && !content) continue
      const messageId = typeof data.messageId === "string" ? data.messageId : event.id
      if (event.type === "answer.final" && content) {
        const assistantIndex = lastIndexOf(segments, (segment) => segment.type === "assistant")
        if (assistantIndex >= 0) {
          const previous = segments[assistantIndex]
          if (previous.type === "assistant") segments[assistantIndex] = { ...previous, content, final: true, messageId }
        } else {
          segments.push({ type: "assistant", id: `assistant_${event.id}`, final: true, messageId, content })
        }
        hasAssistantText = true
        continue
      }
      const previous = segments.at(-1)
      if (previous?.type === "assistant" && previous.messageId === messageId) {
        previous.content = content || `${previous.content}${delta}`
      } else {
        segments.push({ type: "assistant", id: `assistant_${event.id}`, messageId, content: content || delta })
      }
      hasAssistantText = true
      continue
    }

    if (event.type === "artifact.created") {
      const artifact = toolCallArtifactData(event.data)
      if (artifact?.toolCallId && typeof artifact.durationMs === "number") {
        toolArtifactDurations.set(artifact.toolCallId, artifact.durationMs)
      }
      if (artifact?.toolCallId && artifact.childRunIds.length > 0) {
        toolArtifactChildRunIds.set(artifact.toolCallId, uniqueValues([
          ...(toolArtifactChildRunIds.get(artifact.toolCallId) ?? []),
          ...artifact.childRunIds,
        ]))
      }
      continue
    }

    if (event.type === "tool.started" || event.type === "tool.completed" || event.type === "tool.failed") {
      const data = eventDataRecord(event.data)
      const toolCallId = typeof data.toolCallId === "string" ? data.toolCallId : event.id
      const status = typeof data.status === "string" ? data.status : toolStatusFromEventType(event.type)
      if (event.type === "tool.started") toolStartedAt.set(toolCallId, Date.parse(event.createdAt))
      const startedAt = toolStartedAt.get(toolCallId)
      const durationMs = toolDurationMs(data.durationMs ?? toolArtifactDurations.get(toolCallId), startedAt, Date.parse(event.createdAt), nowMs, status)
      const childRuns = childRunsByToolCallId[toolCallId] ?? []
      const childRunIds = uniqueValues([
        ...(toolArtifactChildRunIds.get(toolCallId) ?? []),
        ...childRuns.map((childRun) => childRun.id),
      ])
      const next: ToolSegment = {
        type: "tool",
        id: `tool_${toolCallId}`,
        toolCallId,
        childRunIds: childRunIds.length > 0 ? childRunIds : undefined,
        childRuns: childRuns.length > 0 ? childRuns : undefined,
        name: typeof data.name === "string" ? data.name : undefined,
        status,
        input: data.input,
        outputSummary: typeof data.outputSummary === "string" ? data.outputSummary : undefined,
        outputArtifactId: typeof data.outputArtifactId === "string" ? data.outputArtifactId : undefined,
        durationMs,
        error: typeof data.error === "string" ? data.error : undefined,
      }
      const index = toolIndexes.get(toolCallId)
      if (index === undefined) {
        toolIndexes.set(toolCallId, segments.length)
        segments.push(next)
      } else {
        const previous = segments[index]
        if (previous.type === "tool") segments[index] = { ...previous, ...next, input: next.input ?? previous.input, durationMs: next.durationMs ?? previous.durationMs }
      }
      continue
    }

    if (event.type === "error") {
      const data = eventDataRecord(event.data)
      const message = typeof data.message === "string" ? data.message : "Unknown run error"
      segments.push({ type: "error", id: `error_${event.id}`, content: message })
    }
  }

  if (!hasAssistantText && (status === "queued" || status === "running")) {
    segments.push({ type: "assistant", id: "assistant_running", messageId: "running", content: "", running: true })
  }
  return groupConsecutiveToolSegments(segments)
}



export function runCitations(events: AgentRunEvent[]): EvidenceSegment[] {
  const citations: EvidenceSegment[] = []
  const seen = new Set<string>()
  for (const event of events) {
    if (event.type !== "citation.created") continue
    const citation = citationData(event.data)
    if (!citation || seen.has(citation.id)) continue
    seen.add(citation.id)
    citations.push(citation)
  }
  return citations
}

function groupConsecutiveToolSegments(segments: ConversationSegment[]): ConversationSegment[] {
  const grouped: ConversationSegment[] = []
  let pendingTools: ToolSegment[] = []
  const flushTools = () => {
    if (pendingTools.length === 1) grouped.push(pendingTools[0])
    if (pendingTools.length > 1) grouped.push({ type: "tool_group", id: `tool_group_${pendingTools[0].toolCallId}_${pendingTools.at(-1)?.toolCallId ?? "last"}`, tools: pendingTools })
    pendingTools = []
  }

  for (const segment of segments) {
    if (segment.type === "tool") {
      pendingTools.push(segment)
      continue
    }
    flushTools()
    grouped.push(segment)
  }
  flushTools()
  return grouped
}

export function toolSegmentsIn(segments: ConversationSegment[]) {
  return segments.flatMap((segment) => {
    if (segment.type === "tool") return [segment]
    if (segment.type === "tool_group") return segment.tools
    return []
  })
}

export function toolGroupStatus(tools: ToolSegment[]) {
  if (tools.some((tool) => tool.status === "failed")) return "failed"
  if (tools.some((tool) => isActiveToolStatus(tool.status))) return "running"
  if (tools.every((tool) => tool.status === "completed")) return "completed"
  return tools.at(-1)?.status ?? "started"
}

export function toolGroupDuration(tools: ToolSegment[]) {
  const durations = tools.map((tool) => tool.durationMs).filter((value): value is number => typeof value === "number")
  if (durations.length === 0) return undefined
  return durations.reduce((sum, value) => sum + value, 0)
}

export function toolGroupSummary(tools: ToolSegment[]) {
  const failed = tools.filter((tool) => tool.status === "failed").length
  const active = tools.filter((tool) => isActiveToolStatus(tool.status)).length
  const completed = tools.filter((tool) => tool.status === "completed").length
  const parts = []
  if (active) parts.push(`${active} running`)
  if (failed) parts.push(`${failed} failed`)
  if (completed) parts.push(`${completed} completed`)
  return parts.length > 0 ? parts.join(", ") : `${tools.length} tool calls`
}

function toolStatusFromEventType(type: string) {
  if (type === "tool.completed") return "completed"
  if (type === "tool.failed") return "failed"
  return "started"
}

export function isActiveToolStatus(status: string) {
  return status === "started" || status === "running" || status === "queued"
}

export function toolSegmentSummary(segment: ToolSegment) {
  if (segment.error) return segment.error
  if (segment.outputSummary) return segment.outputSummary
  if (segment.input !== undefined) return `input ${compactJSON(segment.input)}`
  return "waiting"
}

export function toolSegmentDetail(segment: ToolSegment) {
  return JSON.stringify({
    name: segment.name,
    status: segment.status,
    childRunIds: segment.childRunIds,
    childRuns: segment.childRuns?.map((run) => ({
      id: run.id,
      sessionId: run.sessionId,
      status: run.status,
      subagentName: run.subagentName,
      branchName: run.branchName,
      eventCount: run.eventCount,
      artifactCount: run.artifactCount,
      hasFinalAnswer: Boolean(run.finalAnswer),
    })),
    durationMs: segment.durationMs,
    input: segment.input,
    outputSummary: segment.outputSummary,
    outputArtifactId: segment.outputArtifactId,
    error: segment.error,
  }, null, 2)
}

function citationData(value: unknown): EvidenceSegment | undefined {
  const data = eventDataRecord(value)
  const citation = eventDataRecord(data.citation ?? data)
  if (typeof citation.id !== "string") return undefined
  return {
    type: "evidence",
    id: citation.id,
    artifactId: typeof citation.artifactId === "string" ? citation.artifactId : undefined,
    text: typeof citation.text === "string" ? citation.text : undefined,
    target: citation.target,
  }
}


function toolCallArtifactData(value: unknown) {
  const data = eventDataRecord(value)
  const artifact = eventDataRecord(data.artifact ?? data)
  if (artifact.kind !== "tool_call") return undefined
  const artifactData = eventDataRecord(artifact.data)
  const toolCallId = typeof artifactData.toolCallId === "string" ? artifactData.toolCallId : undefined
  const durationMs = typeof artifactData.durationMs === "number" && Number.isFinite(artifactData.durationMs) ? artifactData.durationMs : undefined
  const childRunIds = childRunIdsFromToolOutput(artifactData.output)
  if (!toolCallId) return undefined
  return { toolCallId, durationMs, childRunIds }
}

function eventDataRecord(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== "object") return {}
  return value as Record<string, unknown>
}

function childRunIdsFromToolOutput(value: unknown) {
  const output = parseMaybeJSON(value)
  const record = eventDataRecord(output)
  const branches = Array.isArray(record.branches) ? record.branches : []
  return uniqueValues(branches
    .map((branch) => eventDataRecord(branch).childRunId)
    .filter((id): id is string => typeof id === "string" && id.length > 0))
}

function parseMaybeJSON(value: unknown): unknown {
  if (typeof value !== "string") return value
  try {
    return JSON.parse(value) as unknown
  } catch {
    return value
  }
}

function uniqueValues(values: string[]) {
  return Array.from(new Set(values))
}

function compactJSON(value: unknown) {
  const text = typeof value === "string" ? value : JSON.stringify(value)
  if (!text) return ""
  return text.length > 96 ? `${text.slice(0, 93)}...` : text
}

export function estimateTokenCount(text: string) {
  const normalized = text.trim()
  if (!normalized) return 0
  const cjk = normalized.match(/[\u3400-\u9fff]/g)?.length ?? 0
  const words = normalized.replace(/[\u3400-\u9fff]/g, " ").trim().split(/\s+/).filter(Boolean).length
  const punctuation = normalized.match(/[^\s\w\u3400-\u9fff]/g)?.length ?? 0
  return Math.max(1, Math.ceil(cjk * 0.8 + words * 1.3 + punctuation * 0.25))
}

export function formatCompactNumber(value: number) {
  if (value >= 1_000_000_000) return `${trimFixed(value / 1_000_000_000)}g`
  if (value >= 1_000_000) return `${trimFixed(value / 1_000_000)}m`
  if (value >= 1_000) return `${trimFixed(value / 1_000)}k`
  return String(value)
}

export function formatDuration(value: number) {
  if (value >= 1000) return `${trimFixed(value / 1000)}s`
  return `${value}ms`
}

export function runElapsedLabel(run: AgentRun, events: AgentRunEvent[], nowMs?: number) {
  const times = [run.createdAt, run.updatedAt, ...events.map((event) => event.createdAt)]
    .map((value) => Date.parse(value))
    .filter((value) => Number.isFinite(value))
  if (typeof nowMs === "number" && Number.isFinite(nowMs)) times.push(nowMs)
  if (times.length < 2) return "0s"
  return formatElapsed(Math.max(0, Math.max(...times) - Math.min(...times)))
}

function formatElapsed(value: number) {
  const totalSeconds = Math.max(0, Math.round(value / 1000))
  const hours = Math.floor(totalSeconds / 3600)
  const minutes = Math.floor((totalSeconds % 3600) / 60)
  const seconds = totalSeconds % 60
  if (hours > 0) return `${hours}h ${minutes}m`
  if (minutes > 0) return `${minutes}m ${seconds}s`
  return `${seconds}s`
}

function trimFixed(value: number) {
  return value.toFixed(value >= 10 ? 0 : 1).replace(/\.0$/, "")
}

function runStageLabel(status: string) {
  if (status === "completed") return "Run completed"
  if (status === "failed") return "Run failed"
  if (status === "cancelled") return "Run cancelled"
  return runStatusLabel(status)
}

export function runStatusLabel(status: string) {
  if (status === "queued") return "queued"
  if (status === "running") return "running"
  if (status === "completed") return "completed"
  if (status === "failed") return "failed"
  if (status === "cancelled") return "cancelled"
  if (status === "started") return "started"
  return status
}

export function streamStatusDot(status: string) {
  const base = "size-1.5 shrink-0 rounded-full"
  if (status === "completed") return `${base} bg-accent`
  if (status === "failed") return `${base} bg-destructive`
  if (status === "cancelled") return `${base} bg-muted-foreground`
  return `${base} bg-primary`
}

export function shortID(value: string) {
  const normalized = value.replace(/^[^_]+_/, "")
  return normalized.length > 8 ? normalized.slice(0, 8) : normalized
}


export function isPanelDockArtifact(kind: string) {
  return kind === "markdown" || kind === "k8s.resource" || kind === "k8s.resource_list" || kind === "k8s.topology" || kind === "k8s.history" || kind === "k8s.diff"
}
