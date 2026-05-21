import { AgentAPIError, agentFetch, agentURL, type AgentAPIOptions, type AgentRunEventDTO } from "@/lib/agent-api"
import { parseAgentRunEvent } from "@/lib/agent-schemas"

export type AgentRunEventStreamOptions = AgentAPIOptions & {
  runId: string
  onEvent: (event: AgentRunEventDTO) => void
  onError?: (error: unknown) => void
  retry?: {
    maxAttempts?: number
    initialDelayMs?: number
    maxDelayMs?: number
  }
}

export type AgentRunEventSubscription = {
  abort: () => void
  closed: Promise<void>
}

type SSEFrame = {
  id?: string
  event?: string
  data?: string
}

export function streamAgentRunEvents(options: AgentRunEventStreamOptions): AgentRunEventSubscription {
  const controller = new AbortController()
  const externalSignal = options.signal
  const abortFromExternal = () => controller.abort(externalSignal?.reason)
  if (externalSignal?.aborted) abortFromExternal()
  externalSignal?.addEventListener("abort", abortFromExternal, { once: true })

  const closed = runEventStreamLoop(options, controller.signal).finally(() => {
    externalSignal?.removeEventListener("abort", abortFromExternal)
  })

  return {
    abort: () => controller.abort(),
    closed,
  }
}

async function runEventStreamLoop(options: AgentRunEventStreamOptions, signal: AbortSignal) {
  const seen = new Set<string>()
  const maxAttempts = options.retry?.maxAttempts ?? 3
  const initialDelayMs = options.retry?.initialDelayMs ?? 500
  const maxDelayMs = options.retry?.maxDelayMs ?? 5_000
  let attempt = 0

  while (!signal.aborted) {
    try {
      await readRunEventStream(options, signal, seen)
      return
    } catch (error) {
      if (signal.aborted) return
      attempt += 1
      options.onError?.(error)
      if (attempt >= maxAttempts) throw error
      await abortableDelay(Math.min(initialDelayMs * 2 ** (attempt - 1), maxDelayMs), signal)
    }
  }
}

async function readRunEventStream(
  options: AgentRunEventStreamOptions,
  signal: AbortSignal,
  seen: Set<string>,
) {
  const response = await agentFetch(`/api/v1/agent/runs/${encodeURIComponent(options.runId)}/events?follow=true`, {
    baseURL: options.baseURL,
    fetcher: options.fetcher,
    signal,
  })
  if (!response.ok) throw new AgentAPIError(response.status, await response.text())
  if (!response.body) return

  const reader = response.body.getReader()
  const decoder = new TextDecoder()
  let buffer = ""

  for (;;) {
    const { done, value } = await reader.read()
    if (done) break
    buffer += decoder.decode(value, { stream: true })
    buffer = dispatchCompleteFrames(buffer, seen, options.onEvent)
  }

  buffer += decoder.decode()
  dispatchCompleteFrames(`${buffer}\n\n`, seen, options.onEvent)
}

function dispatchCompleteFrames(
  buffer: string,
  seen: Set<string>,
  onEvent: (event: AgentRunEventDTO) => void,
) {
  const frames = buffer.split(/\n\n+/)
  const remainder = frames.pop() ?? ""
  for (const frameText of frames) {
    const frame = parseSSEFrame(frameText)
    if (!frame.data) continue
    const event = parseAgentRunEvent(JSON.parse(frame.data))
    const key = event.sequence ? `${event.runId}:${event.sequence}` : event.id
    if (seen.has(key)) continue
    seen.add(key)
    onEvent(event)
  }
  return remainder
}

function parseSSEFrame(frameText: string): SSEFrame {
  const frame: SSEFrame = {}
  const data: string[] = []
  for (const line of frameText.split("\n")) {
    if (line === "" || line.startsWith(":")) continue
    const separator = line.indexOf(":")
    const field = separator === -1 ? line : line.slice(0, separator)
    const value = separator === -1 ? "" : line.slice(separator + 1).replace(/^ /, "")
    switch (field) {
      case "id":
        frame.id = value
        break
      case "event":
        frame.event = value
        break
      case "data":
        data.push(value)
        break
    }
  }
  if (data.length > 0) frame.data = data.join("\n")
  return frame
}

function abortableDelay(ms: number, signal: AbortSignal) {
  return new Promise<void>((resolve, reject) => {
    const cleanup = () => signal.removeEventListener("abort", abort)
    const timeout = window.setTimeout(() => {
      cleanup()
      resolve()
    }, ms)
    const abort = () => {
      window.clearTimeout(timeout)
      cleanup()
      reject(signal.reason ?? new DOMException("Aborted", "AbortError"))
    }
    signal.addEventListener("abort", abort, { once: true })
  })
}

export function agentRunEventStreamURL(runId: string, baseURL?: string) {
  return agentURL(`/api/v1/agent/runs/${encodeURIComponent(runId)}/events?follow=true`, baseURL)
}
