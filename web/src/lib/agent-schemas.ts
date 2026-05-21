import { z } from "zod"

export const agentRunStatusSchema = z.enum(["queued", "running", "completed", "failed", "cancelled"])
export const agentMessageRoleSchema = z.enum(["user", "assistant", "system", "tool"])

export const agentArtifactKindSchema = z.enum([
  "markdown",
  "k8s.resource",
  "k8s.resource_list",
  "k8s.topology",
  "k8s.history",
  "k8s.diff",
  "tool_call",
  "citation",
])

export const agentKnownRunEventTypeSchema = z.enum([
  "run.created",
  "run.started",
  "run.status",
  "run.completed",
  "run.failed",
  "run.cancelled",
  "message.created",
  "message.delta",
  "message.completed",
  "answer.final",
  "tool.started",
  "tool.completed",
  "tool.failed",
  "tool.audit",
  "artifact.created",
  "artifact.updated",
  "citation.created",
  "error",
])

export const agentMessageSchema = z.object({
  id: z.string(),
  role: agentMessageRoleSchema,
  content: z.string(),
  runId: z.string().optional(),
  createdAt: z.string(),
  metadata: z.unknown().optional(),
})

export const agentRunSchema = z.object({
  id: z.string(),
  sessionId: z.string(),
  status: agentRunStatusSchema,
  input: z.string(),
  provider: z.string().optional(),
  model: z.string().optional(),
  createdAt: z.string(),
  startedAt: z.string().optional(),
  completedAt: z.string().optional(),
  error: z.string().optional(),
  metadata: z.unknown().optional(),
})

export const agentSessionSchema = z.object({
  id: z.string(),
  title: z.string().optional(),
  provider: z.string().optional(),
  model: z.string().optional(),
  createdAt: z.string(),
  updatedAt: z.string(),
  messages: z.array(agentMessageSchema).optional(),
  runs: z.array(agentRunSchema).optional(),
})

export const runStatusEventDataSchema = z.object({
  runId: z.string(),
  sessionId: z.string().optional(),
  status: agentRunStatusSchema,
  error: z.string().optional(),
})

export const messageEventDataSchema = z.object({
  messageId: z.string().optional(),
  role: agentMessageRoleSchema,
  delta: z.string().optional(),
  content: z.string().optional(),
})

export const toolCallEventDataSchema = z.object({
  toolCallId: z.string(),
  name: z.string(),
  status: z.string(),
  input: z.unknown().optional(),
  output: z.unknown().optional(),
  durationMs: z.number().optional(),
  error: z.string().optional(),
})

export const toolAuditEventDataSchema = toolCallEventDataSchema.extend({
  runId: z.string(),
})

export const markdownArtifactDataSchema = z.object({
  markdown: z.string(),
}).passthrough()

export const agentArtifactSchema = z.object({
  id: z.string(),
  kind: agentArtifactKindSchema.or(z.string()),
  title: z.string().optional(),
  data: z.unknown().optional(),
})

export const artifactEventDataSchema = z.object({
  artifact: agentArtifactSchema,
})

export const agentCitationSchema = z.object({
  id: z.string(),
  artifactId: z.string().optional(),
  text: z.string().optional(),
  target: z.unknown().optional(),
})

export const citationEventDataSchema = z.object({
  citation: agentCitationSchema,
})

export const errorEventDataSchema = z.object({
  code: z.string().optional(),
  message: z.string(),
  retryable: z.boolean().optional(),
})

export const agentRunEventSchema = z.object({
  id: z.string(),
  runId: z.string(),
  sequence: z.number(),
  type: z.string(),
  createdAt: z.string(),
  data: z.unknown().optional(),
})

export function parseAgentSession(value: unknown): AgentSessionDTO {
  return agentSessionSchema.parse(value)
}

export function parseAgentRun(value: unknown): AgentRunDTO {
  return agentRunSchema.parse(value)
}

export function parseAgentRunEvent(value: unknown): AgentRunEventDTO {
  const event = agentRunEventSchema.parse(value)
  return {
    ...event,
    data: parseAgentRunEventData(event.type, event.data),
  }
}

export function parseAgentRunEventData(type: string, data: unknown) {
  switch (type) {
    case "run.created":
    case "run.started":
    case "run.status":
    case "run.completed":
    case "run.failed":
    case "run.cancelled":
      return runStatusEventDataSchema.parse(data)
    case "message.created":
    case "message.delta":
    case "message.completed":
    case "answer.final":
      return messageEventDataSchema.parse(data)
    case "tool.started":
    case "tool.completed":
    case "tool.failed":
      return toolCallEventDataSchema.parse(data)
    case "tool.audit":
      return toolAuditEventDataSchema.parse(data)
    case "artifact.created":
    case "artifact.updated":
      return artifactEventDataSchema.parse(data)
    case "citation.created":
      return citationEventDataSchema.parse(data)
    case "error":
      return errorEventDataSchema.parse(data)
    default:
      return data
  }
}

export type AgentRunStatusDTO = z.infer<typeof agentRunStatusSchema>
export type AgentMessageDTO = z.infer<typeof agentMessageSchema>
export type AgentRunDTO = z.infer<typeof agentRunSchema>
export type AgentSessionDTO = z.infer<typeof agentSessionSchema>
export type AgentRunEventDTO = z.infer<typeof agentRunEventSchema>
export type AgentArtifactDTO = z.infer<typeof agentArtifactSchema>
export type AgentCitationDTO = z.infer<typeof agentCitationSchema>
