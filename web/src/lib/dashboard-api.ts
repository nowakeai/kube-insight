import { z } from "zod"

import { agentFetch, agentRequestJSON, type AgentAPIOptions } from "@/lib/agent-api"

export const dashboardQueryKeys = {
  all: ["dashboard"] as const,
  healthz: () => [...dashboardQueryKeys.all, "healthz"] as const,
  resourceHealth: () => [...dashboardQueryKeys.all, "resource-health"] as const,
  schema: () => [...dashboardQueryKeys.all, "schema"] as const,
  serverInfo: () => [...dashboardQueryKeys.all, "server-info"] as const,
  metrics: () => [...dashboardQueryKeys.all, "metrics"] as const,
}

const healthzSchema = z.object({
  ok: z.boolean(),
}).passthrough()

const resourceHealthSummarySchema = z.object({
  resources: z.number().optional(),
  healthy: z.number().optional(),
  unstable: z.number().optional(),
  errors: z.number().optional(),
  stale: z.number().optional(),
  notStarted: z.number().optional(),
  queued: z.number().optional(),
  skipped: z.number().optional(),
  complete: z.boolean().optional(),
  warnings: z.array(z.string()).optional(),
}).passthrough()

const resourceHealthRecordSchema = z.object({
  clusterId: z.string().optional(),
  group: z.string().optional(),
  version: z.string().optional(),
  resource: z.string(),
  kind: z.string().optional(),
  namespaced: z.boolean().optional(),
  namespace: z.string().optional(),
  status: z.string(),
  error: z.string().optional(),
  resourceVersion: z.string().optional(),
  lastListAt: z.string().optional(),
  lastWatchAt: z.string().optional(),
  lastBookmarkAt: z.string().optional(),
  updatedAt: z.string().optional(),
  ageSeconds: z.number().optional(),
  stale: z.boolean().optional(),
  skipped: z.boolean().optional(),
  latestObjects: z.number().optional(),
}).passthrough()

const resourceHealthReportSchema = z.object({
  checkedAt: z.string().optional(),
  summary: resourceHealthSummarySchema.optional().default({}),
  byStatus: z.record(z.string(), z.number()).optional().default({}),
  resources: z.array(resourceHealthRecordSchema).optional().default([]),
}).passthrough()

const serverComponentInfoSchema = z.object({
  enabled: z.boolean(),
  listen: z.string().optional(),
  url: z.string().optional(),
}).passthrough()

const serverInfoSchema = z.object({
  checkedAt: z.string().optional(),
  storage: z.object({
    driver: z.string(),
    target: z.string(),
  }).passthrough(),
  components: z.record(z.string(), serverComponentInfoSchema).optional().default({}),
  chat: z.object({
    enabled: z.boolean(),
    provider: z.string().optional(),
    model: z.string().optional(),
    apiKeyEnv: z.string().optional(),
    apiKeyConfigured: z.boolean(),
  }).passthrough(),
}).passthrough()

export type HealthzDTO = z.infer<typeof healthzSchema>
export type ResourceHealthReportDTO = z.infer<typeof resourceHealthReportSchema>
export type ResourceHealthRecordDTO = z.infer<typeof resourceHealthRecordSchema>
export type ServerInfoDTO = z.infer<typeof serverInfoSchema>

export type MetricsProbeDTO = {
  ok: boolean
  status: number
  bodyPreview: string
}

export function getHealthz(options?: AgentAPIOptions) {
  return agentRequestJSON<HealthzDTO>("/healthz", options, (value) => healthzSchema.parse(value))
}

export function getResourceHealth(options?: AgentAPIOptions) {
  return agentRequestJSON<ResourceHealthReportDTO>(
    "/api/v1/health?limit=500",
    options,
    (value) => resourceHealthReportSchema.parse(value),
  )
}

export function getSchema(options?: AgentAPIOptions) {
  return agentRequestJSON<unknown>("/api/v1/schema", options)
}

export function getServerInfo(options?: AgentAPIOptions) {
  return agentRequestJSON<ServerInfoDTO>("/api/v1/server/info", options, (value) => serverInfoSchema.parse(value))
}

export async function getMetricsProbe(options?: AgentAPIOptions): Promise<MetricsProbeDTO> {
  const response = await agentFetch("/metrics", options)
  const body = await response.text()
  return {
    ok: response.ok && looksLikePrometheusMetrics(body),
    status: response.status,
    bodyPreview: body.slice(0, 240),
  }
}

function looksLikePrometheusMetrics(body: string) {
  const trimmed = body.trimStart()
  if (!trimmed || trimmed.startsWith("<")) return false
  return trimmed.includes("# HELP") || trimmed.includes("# TYPE") || /^[a-zA-Z_:][a-zA-Z0-9_:]*[{ ]/m.test(trimmed)
}
