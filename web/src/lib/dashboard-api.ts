import { z } from "zod"

import { agentFetch, agentRequestJSON, type AgentAPIOptions } from "@/lib/agent-api"

export const dashboardQueryKeys = {
  all: ["dashboard"] as const,
  healthz: () => [...dashboardQueryKeys.all, "healthz"] as const,
  resourceHealth: () => [...dashboardQueryKeys.all, "resource-health"] as const,
  storageStats: () => [...dashboardQueryKeys.all, "storage-stats"] as const,
  schema: () => [...dashboardQueryKeys.all, "schema"] as const,
  serverInfo: () => [...dashboardQueryKeys.all, "server-info"] as const,
  agentRuns: () => [...dashboardQueryKeys.all, "agent-runs"] as const,
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

const storageStatsSummarySchema = z.object({
  clusters: z.number().optional().default(0),
  apiResources: z.number().optional().default(0),
  objects: z.number().optional().default(0),
  deletedObjects: z.number().optional().default(0),
  latestObjects: z.number().optional().default(0),
  observations: z.number().optional().default(0),
  versions: z.number().optional().default(0),
  blobs: z.number().optional().default(0),
  facts: z.number().optional().default(0),
  edges: z.number().optional().default(0),
  changes: z.number().optional().default(0),
  filterDecisions: z.number().optional().default(0),
  ingestionOffsets: z.number().optional().default(0),
  rawBytes: z.number().optional().default(0),
  storedBytes: z.number().optional().default(0),
  databaseBytes: z.number().optional().default(0),
  bytesOnDisk: z.number().optional().default(0),
  compressedBytes: z.number().optional().default(0),
  uncompressedBytes: z.number().optional().default(0),
  compressionRatio: z.number().optional().default(0),
}).passthrough()

const storageTableStatSchema = z.object({
  name: z.string(),
  rows: z.number().optional().default(0),
  bytes: z.number().optional().default(0),
  bytesOnDisk: z.number().optional().default(0),
  compressedBytes: z.number().optional().default(0),
  uncompressedBytes: z.number().optional().default(0),
}).passthrough()

const storageObjectKindStatSchema = z.object({
  group: z.string().optional(),
  version: z.string().optional(),
  resource: z.string().optional(),
  kind: z.string(),
  objects: z.number().optional().default(0),
  latestObjects: z.number().optional().default(0),
  versions: z.number().optional().default(0),
  rawBytes: z.number().optional().default(0),
  storedBytes: z.number().optional().default(0),
}).passthrough()

const storageStatsSchema = z.object({
  checkedAt: z.string().optional(),
  backend: z.string(),
  summary: storageStatsSummarySchema,
  tables: z.array(storageTableStatSchema).optional().default([]),
  objectKinds: z.array(storageObjectKindStatSchema).optional().default([]),
  warnings: z.array(z.string()).optional().default([]),
}).passthrough()

const agentRunSummarySchema = z.object({
  queued: z.number().optional().default(0),
  running: z.number().optional().default(0),
  completed: z.number().optional().default(0),
  failed: z.number().optional().default(0),
  cancelled: z.number().optional().default(0),
  total: z.number().optional().default(0),
}).passthrough()

const agentRunListSchema = z.object({
  summary: agentRunSummarySchema,
  runs: z.array(z.unknown()).optional().default([]),
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
export type StorageStatsDTO = z.infer<typeof storageStatsSchema>
export type StorageObjectKindStatDTO = z.infer<typeof storageObjectKindStatSchema>
export type StorageTableStatDTO = z.infer<typeof storageTableStatSchema>
export type ServerInfoDTO = z.infer<typeof serverInfoSchema>
export type AgentRunSummaryDTO = z.infer<typeof agentRunSummarySchema>
export type AgentRunListDTO = z.infer<typeof agentRunListSchema>

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

export function getStorageStats(options?: AgentAPIOptions) {
  return agentRequestJSON<StorageStatsDTO>("/api/v1/storage/stats", options, (value) => storageStatsSchema.parse(value))
}

export function getSchema(options?: AgentAPIOptions) {
  return agentRequestJSON<unknown>("/api/v1/schema", options)
}

export function getServerInfo(options?: AgentAPIOptions) {
  return agentRequestJSON<ServerInfoDTO>("/api/v1/server/info", options, (value) => serverInfoSchema.parse(value))
}

export function getAgentRunList(options?: AgentAPIOptions) {
  return agentRequestJSON<AgentRunListDTO>("/api/v1/agent/runs?limit=20", options, (value) => agentRunListSchema.parse(value))
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
