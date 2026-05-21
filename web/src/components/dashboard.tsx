import { useQuery } from "@tanstack/react-query"
import { useMemo, type ComponentType, type ReactNode } from "react"
import { Activity, ArrowLeft, Boxes, Database, ExternalLink, Gauge, HardDrive, Radio, RotateCw, Server, ShieldCheck } from "lucide-react"

import { Button } from "@/components/ui/button"
import {
  dashboardQueryKeys,
  getAgentRunList,
  getHealthz,
  getMetricsProbe,
  getResourceHealth,
  getSchema,
  getStorageStats,
  getServerInfo,
  type ResourceHealthRecordDTO,
  type ResourceHealthReportDTO,
  type StorageObjectKindStatDTO,
  type StorageStatsDTO,
  type StorageTableStatDTO,
  type AgentRunSummaryDTO,
  type ServerInfoDTO,
} from "@/lib/dashboard-api"
import { useAgentProjectionStore, type AgentRunStatus } from "@/lib/agent-store"
import { cn } from "@/lib/utils"

const refreshMs = 15_000

export function Dashboard() {
  const healthz = useQuery({
    queryKey: dashboardQueryKeys.healthz(),
    queryFn: ({ signal }) => getHealthz({ signal }),
    refetchInterval: refreshMs,
  })
  const resourceHealth = useQuery({
    queryKey: dashboardQueryKeys.resourceHealth(),
    queryFn: ({ signal }) => getResourceHealth({ signal }),
    refetchInterval: refreshMs,
  })
  const storageStats = useQuery({
    queryKey: dashboardQueryKeys.storageStats(),
    queryFn: ({ signal }) => getStorageStats({ signal }),
    refetchInterval: refreshMs,
  })
  const schema = useQuery({
    queryKey: dashboardQueryKeys.schema(),
    queryFn: ({ signal }) => getSchema({ signal }),
    refetchInterval: 60_000,
  })
  const serverInfo = useQuery({
    queryKey: dashboardQueryKeys.serverInfo(),
    queryFn: ({ signal }) => getServerInfo({ signal }),
    refetchInterval: refreshMs,
  })
  const agentRuns = useQuery({
    queryKey: dashboardQueryKeys.agentRuns(),
    queryFn: ({ signal }) => getAgentRunList({ signal }),
    refetchInterval: refreshMs,
  })
  const metrics = useQuery({
    queryKey: dashboardQueryKeys.metrics(),
    queryFn: ({ signal }) => getMetricsProbe({ signal }),
    refetchInterval: 30_000,
  })
  const runsById = useAgentProjectionStore((state) => state.runs)
  const runCounts = useMemo(() => countRuns(Object.values(runsById)), [runsById])
  const displayRunSummary = runSummary(agentRuns.data?.summary, runCounts)
  const activeRuns = displayRunSummary.running + displayRunSummary.queued
  const health = resourceHealth.data
  const info = serverInfo.data
  const summary = health?.summary
  const resources = health?.resources ?? []
  const storage = storageStats.data
  const storageSummary = storage?.summary

  return (
    <main className="min-h-svh bg-background text-foreground">
      <div className="mx-auto flex min-h-svh w-full max-w-6xl flex-col px-4 py-4 sm:px-6 lg:px-8">
        <header className="flex min-h-12 items-center justify-between gap-3 border-b border-border/80 pb-3">
          <div className="flex min-w-0 items-center gap-3">
            <span className="flex size-8 items-center justify-center rounded-md bg-primary text-primary-foreground">
              <Gauge className="size-4" aria-hidden="true" />
            </span>
            <div className="min-w-0">
              <h1 className="truncate text-base font-semibold">Server dashboard</h1>
              <p className="truncate text-xs text-muted-foreground">Operational status for this kube-insight server</p>
            </div>
          </div>
          <div className="flex shrink-0 items-center gap-2">
            <Button type="button" size="sm" variant="outline" onClick={() => void Promise.all([healthz.refetch(), resourceHealth.refetch(), storageStats.refetch(), schema.refetch(), serverInfo.refetch(), agentRuns.refetch(), metrics.refetch()])}>
              <RotateCw className="size-3.5" aria-hidden="true" />
              Refresh
            </Button>
            <Button size="sm" variant="outline" asChild>
              <a href="/">
                <ArrowLeft className="size-3.5" aria-hidden="true" />
                Chat
              </a>
            </Button>
          </div>
        </header>

        <section className="grid gap-3 py-5 md:grid-cols-2 xl:grid-cols-5">
          <ComponentStatus
            icon={Server}
            label="API"
            detail={apiDetail(info, healthz.data?.ok, healthz.isPending, healthz.isError)}
            tone={healthz.data?.ok ? "good" : healthz.isError ? "bad" : "neutral"}
          />
          <ComponentStatus
            icon={Activity}
            label="Web UI"
            detail={componentDetail(info?.components.webui, "serving this page")}
            tone={componentTone(info?.components.webui, true)}
          />
          <ComponentStatus
            icon={ShieldCheck}
            label="MCP"
            detail={componentDetail(info?.components.mcp, "unknown")}
            tone={componentTone(info?.components.mcp)}
          />
          <ComponentStatus
            icon={Radio}
            label="Watcher"
            detail={watcherDetail(health, info)}
            tone={watcherTone(health, info)}
          />
          <ComponentStatus
            icon={Gauge}
            label="Metrics"
            detail={metricsDetail(metrics.data, metrics.isPending, metrics.isError, info)}
            tone={metricsTone(metrics.data, metrics.isError, info)}
          />
        </section>

        <section className="grid gap-4 lg:grid-cols-[minmax(0,1.55fr)_minmax(20rem,0.75fr)]">
          <div className="space-y-4">
            <Panel title="Storage overview" description={storage?.checkedAt ? `checked ${formatDate(storage.checkedAt)}` : "from /api/v1/storage/stats"}>
              {storageStats.isError ? (
                <p className="text-sm text-destructive">Storage stats are unavailable from this origin.</p>
              ) : (
                <>
                  <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
                    <Metric label="On-disk size" value={formatBytes(primaryStorageBytes(storageSummary))} tone={primaryStorageBytes(storageSummary) > 0 ? "good" : "neutral"} />
                    <Metric label="Table compression" value={formatRatio(storageSummary?.compressionRatio)} tone={(storageSummary?.compressionRatio ?? 0) > 1 ? "good" : "neutral"} />
                    <Metric label="Active objects" value={numberValue(storageSummary?.objects)} />
                    <Metric label="Versions" value={numberValue(storageSummary?.versions)} />
                    <Metric label="Raw docs" value={formatBytes(storageSummary?.rawBytes)} />
                    <Metric label="Logical docs" value={formatBytes(storageSummary?.storedBytes)} />
                    <Metric label="Observations" value={numberValue(storageSummary?.observations)} />
                    <Metric label="Facts" value={numberValue(storageSummary?.facts)} />
                    <Metric label="Edges" value={numberValue(storageSummary?.edges)} />
                  </div>
                  <StorageWarnings warnings={storage?.warnings ?? []} />
                </>
              )}
            </Panel>

            <Panel title="Object distribution" description="Top resources by logical retained document bytes and version count.">
              <ObjectKindTable rows={storage?.objectKinds ?? []} loading={storageStats.isPending} error={storageStats.isError} />
            </Panel>

            <Panel title="Table footprint" description="Rows and physical bytes when the backend exposes them.">
              <TableFootprintTable rows={storage?.tables ?? []} loading={storageStats.isPending} error={storageStats.isError} />
            </Panel>
          </div>

          <aside className="space-y-4">
            <Panel title="Agent runs" description={agentRuns.data ? "Server-persisted run summary" : "Browser projection until server run summary is reachable."}>
              <div className="grid grid-cols-2 gap-3">
                <Metric label="Active" value={String(activeRuns)} tone={activeRuns > 0 ? "good" : "neutral"} />
                <Metric label="Completed" value={String(displayRunSummary.completed)} />
                <Metric label="Failed" value={String(displayRunSummary.failed)} tone={displayRunSummary.failed > 0 ? "bad" : "neutral"} />
                <Metric label="Cancelled" value={String(displayRunSummary.cancelled)} />
              </div>
            </Panel>

            <Panel title="Storage backend" description={info?.checkedAt ? `server info ${formatDate(info.checkedAt)}` : "server info plus /api/v1/schema"}>
              <div className="space-y-3 text-sm">
                <KeyValue icon={HardDrive} label="Backend" value={storageBackendLabel(storage, info, schema.data, schema.isError)} />
                <KeyValue icon={Database} label="Target" value={storageTargetLabel(info)} />
                <KeyValue icon={Boxes} label="Schema" value={inferSchemaSurface(schema.data, schema.isPending, schema.isError)} />
                <KeyValue icon={ShieldCheck} label="Provider/model" value={providerModelLabel(info)} />
                <KeyValue icon={ShieldCheck} label="Provider key" value={providerKeyLabel(info)} />
              </div>
            </Panel>

            <Panel title="Collector coverage" description={health?.checkedAt ? `checked ${formatDate(health.checkedAt)}` : "compact /api/v1/health summary"}>
              <div className="grid grid-cols-2 gap-3">
                <Metric label="Resources" value={numberValue(summary?.resources)} />
                <Metric label="Healthy" value={numberValue(summary?.healthy)} tone="good" />
                <Metric label="Issues" value={numberValue(collectorIssueCount(summary))} tone={collectorIssueCount(summary) > 0 ? "warn" : "neutral"} />
                <Metric label="Complete" value={summary?.complete ? "yes" : "no"} tone={summary?.complete ? "good" : "warn"} />
              </div>
              <CollectorIssues resources={resources} loading={resourceHealth.isPending} error={resourceHealth.isError} />
            </Panel>

            <Panel title="Endpoint links" description="Open the underlying server surfaces directly.">
              <div className="grid gap-2">
                <EndpointLink href="/api/v1/storage/stats" label="/api/v1/storage/stats" />
                <EndpointLink href="/api/v1/health?limit=500" label="/api/v1/health" />
                <EndpointLink href="/api/v1/schema" label="/api/v1/schema" />
                <EndpointLink href="/metrics" label="/metrics" />
              </div>
            </Panel>
          </aside>
        </section>
      </div>
    </main>
  )
}

type Tone = "good" | "warn" | "bad" | "neutral"

function ComponentStatus({
  icon: Icon,
  label,
  detail,
  tone,
}: {
  icon: ComponentType<{ className?: string }>
  label: string
  detail: string
  tone: Tone
}) {
  return (
    <div className="rounded-md border border-border bg-card p-4 shadow-sm">
      <div className="flex items-start justify-between gap-3">
        <div>
          <div className="text-xs font-medium uppercase tracking-normal text-muted-foreground">{label}</div>
          <div className="mt-2 text-sm font-semibold text-foreground">{detail}</div>
        </div>
        <span className={cn("flex size-10 items-center justify-center rounded-md border", toneClass(tone))}>
          <Icon className="size-4" />
        </span>
      </div>
    </div>
  )
}

function Panel({ title, description, children }: { title: string; description: string; children: ReactNode }) {
  return (
    <section className="rounded-md border border-border bg-card p-4 shadow-sm">
      <div className="mb-4">
        <h2 className="text-sm font-semibold">{title}</h2>
        <p className="mt-1 text-xs text-muted-foreground">{description}</p>
      </div>
      {children}
    </section>
  )
}

function Metric({ label, value, tone = "neutral" }: { label: string; value: string; tone?: Tone }) {
  return (
    <div className="rounded-md border border-border bg-background px-3 py-3">
      <div className="text-xs text-muted-foreground">{label}</div>
      <div className={cn("mt-2 text-2xl font-semibold tabular-nums", textToneClass(tone))}>{value}</div>
    </div>
  )
}

function KeyValue({
  icon: Icon,
  label,
  value,
}: {
  icon: ComponentType<{ className?: string }>
  label: string
  value: string
}) {
  return (
    <div className="flex min-h-11 items-center gap-3 rounded-md border border-border bg-background px-3 py-2">
      <Icon className="size-4 shrink-0 text-muted-foreground" />
      <div className="min-w-0">
        <div className="text-xs text-muted-foreground">{label}</div>
        <div className="truncate font-medium text-foreground">{value}</div>
      </div>
    </div>
  )
}

function EndpointLink({ href, label }: { href: string; label: string }) {
  return (
    <Button variant="outline" size="sm" asChild>
      <a href={href} target="_blank" rel="noreferrer">
        <ExternalLink className="size-3.5" aria-hidden="true" />
        {label}
      </a>
    </Button>
  )
}

function StorageWarnings({ warnings }: { warnings: string[] }) {
  if (warnings.length === 0) return null
  return (
    <ul className="mt-3 space-y-1 text-xs text-muted-foreground">
      {warnings.slice(0, 3).map((warning) => (
        <li key={warning}>{warning}</li>
      ))}
    </ul>
  )
}

function ObjectKindTable({
  rows,
  loading,
  error,
}: {
  rows: StorageObjectKindStatDTO[]
  loading: boolean
  error: boolean
}) {
  if (loading) return <p className="text-sm text-muted-foreground">Loading storage distribution...</p>
  if (error) return <p className="text-sm text-destructive">Object distribution is unavailable.</p>
  if (rows.length === 0) return <p className="text-sm text-muted-foreground">No retained object versions returned.</p>
  return (
    <div className="overflow-hidden rounded-md border border-border">
      <div className="grid grid-cols-[minmax(0,1.5fr)_5rem_5rem_7rem] gap-3 bg-muted px-3 py-2 text-xs font-medium text-muted-foreground">
        <div>Resource</div>
        <div className="text-right">Objects</div>
        <div className="text-right">Versions</div>
        <div className="text-right">Doc bytes</div>
      </div>
      {rows.slice(0, 10).map((row) => (
        <div key={objectKindKey(row)} className="grid grid-cols-[minmax(0,1.5fr)_5rem_5rem_7rem] gap-3 border-t border-border bg-background px-3 py-2 text-sm">
          <div className="min-w-0">
            <div className="truncate font-medium">{objectKindLabel(row)}</div>
            <div className="truncate text-xs text-muted-foreground">{row.group || "core"}/{row.version || "unknown"}</div>
          </div>
          <div className="text-right tabular-nums text-muted-foreground">{numberValue(row.objects)}</div>
          <div className="text-right tabular-nums text-muted-foreground">{numberValue(row.versions)}</div>
          <div className="text-right tabular-nums text-muted-foreground">{formatBytes(row.storedBytes)}</div>
        </div>
      ))}
    </div>
  )
}

function TableFootprintTable({
  rows,
  loading,
  error,
}: {
  rows: StorageTableStatDTO[]
  loading: boolean
  error: boolean
}) {
  if (loading) return <p className="text-sm text-muted-foreground">Loading table footprint...</p>
  if (error) return <p className="text-sm text-destructive">Table footprint is unavailable.</p>
  if (rows.length === 0) return <p className="text-sm text-muted-foreground">This backend did not return table-level footprint data.</p>
  return (
    <div className="overflow-hidden rounded-md border border-border">
      <div className="grid grid-cols-[minmax(0,1fr)_6rem_7rem_7rem] gap-3 bg-muted px-3 py-2 text-xs font-medium text-muted-foreground">
        <div>Table</div>
        <div className="text-right">Rows</div>
        <div className="text-right">Size</div>
        <div className="text-right">Ratio</div>
      </div>
      {rows.slice(0, 12).map((row) => (
        <div key={row.name} className="grid grid-cols-[minmax(0,1fr)_6rem_7rem_7rem] gap-3 border-t border-border bg-background px-3 py-2 text-sm">
          <div className="truncate font-medium">{row.name}</div>
          <div className="text-right tabular-nums text-muted-foreground">{numberValue(row.rows)}</div>
          <div className="text-right tabular-nums text-muted-foreground">{formatBytes(tableSizeBytes(row))}</div>
          <div className="text-right tabular-nums text-muted-foreground">{formatRatio(tableCompressionRatio(row))}</div>
        </div>
      ))}
    </div>
  )
}

function CollectorIssues({
  resources,
  loading,
  error,
}: {
  resources: ResourceHealthRecordDTO[]
  loading: boolean
  error: boolean
}) {
  const rows = priorityResources(resources).filter((resource) => resource.error || resource.stale || resource.status === "not_started" || resource.status === "retrying").slice(0, 4)
  if (loading) return <p className="mt-3 text-sm text-muted-foreground">Checking collector coverage...</p>
  if (error) return <p className="mt-3 text-sm text-destructive">Collector coverage is unavailable from this origin.</p>
  if (rows.length === 0) return <p className="mt-3 text-sm text-muted-foreground">No collector issues reported.</p>
  return (
    <div className="mt-3 space-y-2">
      {rows.map((resource) => (
        <div key={resourceKey(resource)} className="flex items-center justify-between gap-3 rounded-md border border-border bg-background px-3 py-2 text-sm">
          <div className="min-w-0">
            <div className="truncate font-medium">{resourceLabel(resource)}</div>
            <div className="truncate text-xs text-muted-foreground">{resource.error || resource.status}</div>
          </div>
          <span className={statusDot(resource.status, resource.stale, Boolean(resource.error))} aria-hidden="true" />
        </div>
      ))}
    </div>
  )
}

function countRuns(runs: Array<{ status: AgentRunStatus }>): AgentRunSummaryDTO {
  return runs.reduce<AgentRunSummaryDTO>((counts, run) => {
    counts[run.status] += 1
    counts.total += 1
    return counts
  }, { queued: 0, running: 0, completed: 0, failed: 0, cancelled: 0, total: 0 })
}

function runSummary(server: AgentRunSummaryDTO | undefined, fallback: AgentRunSummaryDTO): AgentRunSummaryDTO {
  return server ?? fallback
}

function queryDetail(isPending: boolean, isError: boolean) {
  if (isPending) return "checking"
  if (isError) return "unavailable"
  return "ok"
}

function apiDetail(info: ServerInfoDTO | undefined, ok: boolean | undefined, isPending: boolean, isError: boolean) {
  if (ok) return componentDetail(info?.components.api, "healthz ok")
  return queryDetail(isPending, isError)
}

function componentDetail(component: ServerInfoDTO["components"][string] | undefined, fallback: string) {
  if (!component) return fallback
  if (!component.enabled) return "disabled"
  return component.listen || component.url || "enabled"
}

function componentTone(component: ServerInfoDTO["components"][string] | undefined, fallbackGood = false): Tone {
  if (!component) return fallbackGood ? "good" : "neutral"
  return component.enabled ? "good" : "neutral"
}

function watcherDetail(health: ResourceHealthReportDTO | undefined, info: ServerInfoDTO | undefined) {
  const watch = info?.components.watch
  if (watch && !watch.enabled) return "disabled"
  if (!health) return watch?.enabled ? "checking coverage" : "checking coverage"
  const summary = health.summary
  if ((summary.resources ?? 0) === 0) return "no resources observed"
  if ((summary.errors ?? 0) > 0) return `${summary.errors} error streams`
  if ((summary.stale ?? 0) > 0) return `${summary.stale} stale streams`
  return summary.complete ? "coverage complete" : "coverage partial"
}

function watcherTone(health: ResourceHealthReportDTO | undefined, info: ServerInfoDTO | undefined): Tone {
  const watch = info?.components.watch
  if (watch && !watch.enabled) return "neutral"
  if (!health) return "neutral"
  const summary = health.summary
  if ((summary.errors ?? 0) > 0) return "bad"
  if ((summary.stale ?? 0) > 0 || !summary.complete) return "warn"
  return "good"
}

function metricsDetail(
  data: { ok: boolean; status: number } | undefined,
  isPending: boolean,
  isError: boolean,
  info: ServerInfoDTO | undefined,
) {
  const component = info?.components.metrics
  if (component && !component.enabled) return "disabled"
  if (component?.listen && !data?.ok) return `${component.listen} configured`
  if (isPending) return "probing /metrics"
  if (isError) return "probe failed"
  if (!data) return "not checked"
  return data.ok ? "prometheus endpoint" : `not on this origin (${data.status})`
}

function metricsTone(
  data: { ok: boolean } | undefined,
  isError: boolean,
  info: ServerInfoDTO | undefined,
): Tone {
  const component = info?.components.metrics
  if (component && !component.enabled) return "neutral"
  if (data?.ok || component?.enabled) return "good"
  return isError ? "bad" : "neutral"
}

function numberValue(value?: number) {
  return String(value ?? 0)
}

function primaryStorageBytes(summary: StorageStatsDTO["summary"] | undefined) {
  if (!summary) return 0
  return summary.bytesOnDisk || summary.databaseBytes || summary.storedBytes || summary.rawBytes
}

function tableSizeBytes(row: StorageTableStatDTO) {
  return row.bytesOnDisk || row.bytes || row.compressedBytes || row.uncompressedBytes
}

function tableCompressionRatio(row: StorageTableStatDTO) {
  return ratioValue(row.uncompressedBytes, row.compressedBytes)
}

function ratioValue(numerator?: number, denominator?: number) {
  if (!denominator || denominator <= 0) return 0
  return (numerator ?? 0) / denominator
}

function storageBackendLabel(
  storage: StorageStatsDTO | undefined,
  info: ServerInfoDTO | undefined,
  schema: unknown,
  schemaError: boolean,
) {
  if (storage?.backend) return storage.backend
  return storageDriverLabel(info, schema, schemaError)
}

function collectorIssueCount(summary: ResourceHealthReportDTO["summary"] | undefined) {
  if (!summary) return 0
  return (summary.errors ?? 0) + (summary.stale ?? 0) + (summary.notStarted ?? 0)
}

function objectKindKey(row: StorageObjectKindStatDTO) {
  return [row.group, row.version, row.resource, row.kind].filter(Boolean).join("/")
}

function objectKindLabel(row: StorageObjectKindStatDTO) {
  if (row.resource && row.kind && row.resource !== row.kind) return `${row.resource} (${row.kind})`
  return row.resource || row.kind
}

function formatBytes(value?: number) {
  const bytes = value ?? 0
  if (bytes <= 0) return "0 B"
  const units = ["B", "KiB", "MiB", "GiB", "TiB"]
  let size = bytes
  let unit = 0
  while (size >= 1024 && unit < units.length - 1) {
    size /= 1024
    unit += 1
  }
  const digits = size >= 10 || unit === 0 ? 0 : 1
  return `${size.toFixed(digits)} ${units[unit]}`
}

function formatRatio(value?: number) {
  if (!value || value <= 0) return "-"
  return `${value.toFixed(value >= 10 ? 1 : 2)}x`
}

function storageDriverLabel(info: ServerInfoDTO | undefined, schema: unknown, schemaError: boolean) {
  if (info?.storage.driver) return info.storage.driver
  if (schemaError) return "unavailable"
  if (!schema) return "checking"
  const text = JSON.stringify(schema).toLowerCase()
  if (text.includes("clickhouse")) return "ClickHouse-compatible"
  if (text.includes("sqlite")) return "SQLite"
  return "configured server store"
}

function storageTargetLabel(info: ServerInfoDTO | undefined) {
  return info?.storage.target || "unavailable"
}

function inferSchemaSurface(schema: unknown, isPending: boolean, isError: boolean) {
  if (isPending) return "checking"
  if (isError) return "unavailable"
  if (!schema || typeof schema !== "object") return "available"
  const tableCount = countNamedArray(schema, "tables")
  const recipeCount = countNamedArray(schema, "recipes")
  if (tableCount > 0 && recipeCount > 0) return `${tableCount} tables, ${recipeCount} recipes`
  if (tableCount > 0) return `${tableCount} tables`
  return "available"
}

function countNamedArray(value: unknown, name: string): number {
  if (!value || typeof value !== "object") return 0
  const record = value as Record<string, unknown>
  return Array.isArray(record[name]) ? record[name].length : 0
}

function providerModelLabel(info: ServerInfoDTO | undefined) {
  const provider = info?.chat.provider || "server default"
  const model = info?.chat.model || "model from server config"
  return `${provider} / ${model}`
}

function providerKeyLabel(info: ServerInfoDTO | undefined) {
  if (!info) return "unavailable"
  if (!info.chat.apiKeyEnv) return "not configured"
  return info.chat.apiKeyConfigured ? `set via ${info.chat.apiKeyEnv}` : `missing ${info.chat.apiKeyEnv}`
}

function priorityResources(resources: ResourceHealthRecordDTO[]) {
  return [...resources].sort((a, b) => resourcePriority(b) - resourcePriority(a))
}

function resourcePriority(resource: ResourceHealthRecordDTO) {
  if (resource.error) return 5
  if (resource.stale) return 4
  if (resource.status === "not_started") return 3
  if (resource.status === "queued" || resource.status === "retrying") return 2
  return 1
}

function resourceKey(resource: ResourceHealthRecordDTO) {
  return [resource.clusterId, resource.group, resource.version, resource.resource, resource.namespace].filter(Boolean).join("/")
}

function resourceLabel(resource: ResourceHealthRecordDTO) {
  const group = resource.group ? `.${resource.group}` : ""
  const scope = resource.namespace ? `${resource.namespace}/` : ""
  return `${scope}${resource.resource}${group}`
}

function formatDate(value: string) {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return date.toLocaleString()
}

function toneClass(tone: Tone) {
  switch (tone) {
    case "good":
      return "border-accent/30 bg-accent/10 text-accent"
    case "warn":
      return "border-[color:var(--nw-warn)]/30 bg-[color:var(--nw-warn)]/10 text-[color:var(--nw-warn)]"
    case "bad":
      return "border-destructive/30 bg-destructive/10 text-destructive"
    default:
      return "border-border bg-muted text-muted-foreground"
  }
}

function textToneClass(tone: Tone) {
  switch (tone) {
    case "good":
      return "text-accent"
    case "warn":
      return "text-[color:var(--nw-warn)]"
    case "bad":
      return "text-destructive"
    default:
      return "text-foreground"
  }
}

function statusDot(status: string, stale?: boolean, error?: boolean) {
  if (error) return "size-2 rounded-full bg-destructive"
  if (stale) return "size-2 rounded-full bg-[color:var(--nw-warn)]"
  if (status === "watching" || status === "listed") return "size-2 rounded-full bg-accent"
  return "size-2 rounded-full bg-muted-foreground"
}
