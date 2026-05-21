import { useQuery } from "@tanstack/react-query"
import { useMemo, type ComponentType, type ReactNode } from "react"
import { Activity, ArrowLeft, Database, ExternalLink, Gauge, HardDrive, Radio, RotateCw, Server, ShieldCheck } from "lucide-react"

import { Button } from "@/components/ui/button"
import {
  dashboardQueryKeys,
  getAgentRunList,
  getHealthz,
  getMetricsProbe,
  getResourceHealth,
  getSchema,
  getServerInfo,
  type ResourceHealthRecordDTO,
  type ResourceHealthReportDTO,
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
            <Button type="button" size="sm" variant="outline" onClick={() => void Promise.all([healthz.refetch(), resourceHealth.refetch(), schema.refetch(), serverInfo.refetch(), agentRuns.refetch(), metrics.refetch()])}>
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

        <section className="grid gap-4 lg:grid-cols-[minmax(0,1.5fr)_minmax(20rem,0.8fr)]">
          <div className="space-y-4">
            <Panel title="Collector coverage" description={health?.checkedAt ? `checked ${formatDate(health.checkedAt)}` : "from /api/v1/health"}>
              <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
                <Metric label="Resources" value={numberValue(summary?.resources)} />
                <Metric label="Healthy" value={numberValue(summary?.healthy)} tone="good" />
                <Metric label="Queued" value={numberValue(summary?.queued)} />
                <Metric label="Errors" value={numberValue(summary?.errors)} tone={(summary?.errors ?? 0) > 0 ? "bad" : "neutral"} />
                <Metric label="Stale" value={numberValue(summary?.stale)} tone={(summary?.stale ?? 0) > 0 ? "warn" : "neutral"} />
                <Metric label="Not started" value={numberValue(summary?.notStarted)} />
                <Metric label="Skipped" value={numberValue(summary?.skipped)} />
                <Metric label="Complete" value={summary?.complete ? "yes" : "no"} tone={summary?.complete ? "good" : "warn"} />
              </div>
              <ResourceTable resources={resources} loading={resourceHealth.isPending} error={resourceHealth.isError} />
            </Panel>

            <Panel title="Endpoint links" description="Open the underlying server surfaces directly.">
              <div className="grid gap-2 sm:grid-cols-2">
                <EndpointLink href="/healthz" label="/healthz" />
                <EndpointLink href="/api/v1/health?limit=500" label="/api/v1/health" />
                <EndpointLink href="/api/v1/schema" label="/api/v1/schema" />
                <EndpointLink href="/metrics" label="/metrics" />
              </div>
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

            <Panel title="Storage and schema" description={info?.checkedAt ? `server info ${formatDate(info.checkedAt)}` : "server info plus /api/v1/schema"}>
              <div className="space-y-3 text-sm">
                <KeyValue icon={HardDrive} label="Storage driver" value={storageDriverLabel(info, schema.data, schema.isError)} />
                <KeyValue icon={Database} label="Storage target" value={storageTargetLabel(info)} />
                <KeyValue icon={Database} label="Schema surface" value={inferSchemaSurface(schema.data, schema.isPending, schema.isError)} />
                <KeyValue icon={ShieldCheck} label="Provider/model" value={providerModelLabel(info)} />
                <KeyValue icon={ShieldCheck} label="Provider key" value={providerKeyLabel(info)} />
              </div>
            </Panel>

            <Panel title="Component notes" description="Unavailable means the current server does not expose that surface on this origin.">
              <ul className="space-y-2 text-sm text-muted-foreground">
                <li>Component listen addresses come from `/api/v1/server/info` when available.</li>
                <li>Metrics may use a separate listener; `/metrics` is still probed directly.</li>
                <li>Watcher health combines configured service state and collector coverage.</li>
              </ul>
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

function ResourceTable({
  resources,
  loading,
  error,
}: {
  resources: ResourceHealthRecordDTO[]
  loading: boolean
  error: boolean
}) {
  const rows = priorityResources(resources).slice(0, 8)
  if (loading) return <p className="mt-4 text-sm text-muted-foreground">Loading collector coverage...</p>
  if (error) return <p className="mt-4 text-sm text-destructive">Collector coverage is unavailable from this origin.</p>
  if (rows.length === 0) return <p className="mt-4 text-sm text-muted-foreground">No resource coverage records returned.</p>
  return (
    <div className="mt-4 overflow-hidden rounded-md border border-border">
      <div className="grid grid-cols-[minmax(0,1.2fr)_7rem_5rem] gap-3 bg-muted px-3 py-2 text-xs font-medium text-muted-foreground sm:grid-cols-[minmax(0,1.3fr)_8rem_6rem_6rem]">
        <div>Resource</div>
        <div>Status</div>
        <div className="text-right">Objects</div>
        <div className="hidden text-right sm:block">Age</div>
      </div>
      {rows.map((resource) => (
        <div key={resourceKey(resource)} className="grid grid-cols-[minmax(0,1.2fr)_7rem_5rem] gap-3 border-t border-border bg-background px-3 py-2 text-sm sm:grid-cols-[minmax(0,1.3fr)_8rem_6rem_6rem]">
          <div className="min-w-0">
            <div className="truncate font-medium">{resourceLabel(resource)}</div>
            <div className="truncate text-xs text-muted-foreground">{resource.clusterId || "default cluster"}</div>
          </div>
          <div className="flex items-center gap-2">
            <span className={statusDot(resource.status, resource.stale, Boolean(resource.error))} aria-hidden="true" />
            <span className="truncate text-xs capitalize text-muted-foreground">{resource.status}</span>
          </div>
          <div className="text-right tabular-nums text-muted-foreground">{resource.latestObjects ?? 0}</div>
          <div className="hidden text-right text-muted-foreground sm:block">{formatAge(resource.ageSeconds)}</div>
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

function formatAge(value?: number) {
  if (!value || value < 0) return "-"
  if (value < 60) return `${value}s`
  if (value < 3600) return `${Math.round(value / 60)}m`
  if (value < 86400) return `${Math.round(value / 3600)}h`
  return `${Math.round(value / 86400)}d`
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
