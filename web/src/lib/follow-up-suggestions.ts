import type { AgentArtifact, AgentRun, AgentRunEvent } from "@/lib/agent-store"

const maxSuggestions = 4

export function followUpSuggestionsForRun({
  events = [],
  run,
  artifacts = [],
}: {
  events?: AgentRunEvent[]
  run?: AgentRun
  artifacts?: AgentArtifact[]
}) {
  if (!run || run.status !== "completed") return []
  const modelSuggestions = followUpSuggestionsFromEvents(events)
  if (modelSuggestions.length > 0) return modelSuggestions

  const haystack = normalizeText([
    run.input,
    run.finalAnswer,
    ...artifacts.map((artifact) => artifact.title ?? ""),
  ].join("\n"))

  const suggestions: string[] = []
  if (mentionsRestartOrOOM(haystack)) {
    suggestions.push(
      "Check resource requests and limits for the affected Pods",
      "Compare OOM and restart evidence in the last hour",
      "Find recent changes for the affected workloads",
    )
  }
  if (mentionsNodeCapacity(haystack)) {
    suggestions.push(
      "Compare node capacity and allocatable changes in the last hour",
      "Show MemoryPressure and DiskPressure evidence by node",
      "List Pods scheduled on nodes with recent pressure",
    )
  }
  if (mentionsTopology(haystack)) {
    suggestions.push(
      "Trace the related Service to EndpointSlices, Pods, Nodes, and Events",
      "Find Services with no ready backend Pods in this path",
    )
  }
  if (mentionsRecentChange(haystack)) {
    suggestions.push(
      "Narrow the previous findings to the last hour",
      "Find recent config or rollout changes related to these resources",
    )
  }
  if (mentionsEvidenceGap(haystack)) {
    suggestions.push("Fill the evidence gaps from the previous answer")
  }

  suggestions.push(
    "Summarize the impact by namespace and workload",
    "Show the raw Kubernetes objects behind the answer",
    "Find recent changes related to the findings",
  )

  return unique(suggestions).slice(0, maxSuggestions)
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

function normalizeText(text: string) {
  return text.toLowerCase()
}

function mentionsRestartOrOOM(text: string) {
  return /\boom\b|oomkilled|crashloop|restart|重启|内存溢出/.test(text)
}

function mentionsNodeCapacity(text: string) {
  return /node|节点|capacity|allocatable|memorypressure|diskpressure|\bcpu\b|memory|内存|容量/.test(text)
}

function mentionsTopology(text: string) {
  return /service|endpoint|endpointslice|topology|拓扑|服务/.test(text)
}

function mentionsRecentChange(text: string) {
  return /change|rollout|deploy|update|最近|变化|变更|过去|last hour|小时/.test(text)
}

function mentionsEvidenceGap(text: string) {
  return /gap|missing|limitation|限制|未能|缺少|未发现|未存储/.test(text)
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
