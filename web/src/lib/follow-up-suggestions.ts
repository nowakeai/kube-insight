import type { AgentRun, AgentRunEvent } from "@/lib/agent-store"

const maxSuggestions = 4

export function followUpSuggestionsForRun({
  events = [],
  run,
}: {
  events?: AgentRunEvent[]
  run?: AgentRun
}) {
  if (!run || run.status !== "completed") return []
  return followUpSuggestionsFromEvents(events)
}

export function followUpSuggestionsFromEvents(events: AgentRunEvent[]) {
  const suggestions: string[] = []
  for (const event of events) {
    if (event.type !== "followup.suggestions") continue
    const data = eventDataRecord(event.data)
    const rawSuggestions = Array.isArray(data.suggestions) ? data.suggestions : []
    for (const raw of rawSuggestions) {
      if (typeof raw !== "string") continue
      const suggestion = sanitizeSuggestion(raw)
      if (suggestion) suggestions.push(suggestion)
    }
  }
  return unique(suggestions).slice(0, maxSuggestions)
}

function unique(values: string[]) {
  return [...new Set(values)]
}

function sanitizeSuggestion(value: string) {
  const normalized = value.trim().replace(/\s+/g, " ")
  if (!normalized) return ""
  return [...normalized].slice(0, 120).join("").trim()
}

function eventDataRecord(value: unknown): Record<string, unknown> {
  return value && typeof value === "object" ? value as Record<string, unknown> : {}
}
