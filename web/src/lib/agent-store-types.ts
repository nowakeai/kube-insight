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
