import { FileText, PanelRightClose, PanelRightOpen, RefreshCw, X } from "lucide-react"
import { lazy, Suspense, useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react"

import { K8sResourceArtifact } from "@/components/k8s-resource-artifact"
import { K8sResourceListArtifact } from "@/components/k8s-resource-list-artifact"
import { MarkdownContent } from "@/components/markdown-content"
import { Button } from "@/components/ui/button"
import { type AgentArtifact, useAgentProjectionStore } from "@/lib/agent-store"
import { isWatchableArtifactKind, refreshArtifact, watchableArtifactTarget } from "@/lib/artifact-watch"


const LazyK8sDiffArtifact = lazy(() =>
  import("@/components/k8s-diff-artifact").then((module) => ({ default: module.K8sDiffArtifact })),
)
const LazyK8sHistoryArtifact = lazy(() =>
  import("@/components/k8s-history-artifact").then((module) => ({ default: module.K8sHistoryArtifact })),
)
const LazyK8sTopologyArtifact = lazy(() =>
  import("@/components/k8s-topology-artifact").then((module) => ({ default: module.K8sTopologyArtifact })),
)
const LazyUnknownArtifact = lazy(() =>
  import("@/components/unknown-artifact").then((module) => ({ default: module.UnknownArtifact })),
)


export function ArtifactDock({
  artifacts,
  sessionId,
  selectedArtifactId,
  collapsed,
  watchIntervalSeconds,
  onCollapsedChange,
  onCloseArtifact,
  onUpdateArtifact,
  onWatchIntervalChange,
}: {
  artifacts: AgentArtifact[]
  sessionId?: string
  selectedArtifactId?: string
  collapsed: boolean
  watchIntervalSeconds?: number
  onCollapsedChange: (collapsed: boolean) => void
  onCloseArtifact: (artifactId: string) => void
  onUpdateArtifact: (artifactId: string, input: { title?: string; data?: unknown }) => void
  onWatchIntervalChange: (sessionId: string, seconds?: number) => void
}) {
  const [documentVisible, setDocumentVisible] = useState(() => isDocumentVisible())
  const [watchStates, setWatchStates] = useState<Record<string, ArtifactWatchState>>({})
  const watchableArtifacts = useMemo(
    () => artifacts.filter((artifact) => isWatchableArtifactKind(artifact.kind) && watchableArtifactTarget(artifact)),
    [artifacts],
  )
  const watchableArtifactsRef = useRef(watchableArtifacts)
  const watchEnabled = Boolean(watchIntervalSeconds && watchIntervalSeconds > 0)
  const watchPaused = watchEnabled && (collapsed || !documentVisible)
  const canWatch = Boolean(sessionId && watchableArtifacts.length > 0)
  const refreshWatchableArtifacts = useCallback(async (signal?: AbortSignal) => {
    for (const artifact of watchableArtifactsRef.current) {
      if (signal?.aborted) return
      setWatchStates((current) => ({
        ...current,
        [artifact.id]: { status: "refreshing", refreshedAt: current[artifact.id]?.refreshedAt },
      }))
      try {
        const refreshed = await refreshArtifact(artifact, { signal })
        if (signal?.aborted) return
        onUpdateArtifact(artifact.id, refreshed)
        setWatchStates((current) => ({
          ...current,
          [artifact.id]: { status: "ok", refreshedAt: new Date().toISOString() },
        }))
      } catch (error) {
        if (signal?.aborted) return
        setWatchStates((current) => ({
          ...current,
          [artifact.id]: {
            status: "error",
            error: error instanceof Error ? error.message : "Refresh failed",
            refreshedAt: current[artifact.id]?.refreshedAt,
          },
        }))
      }
    }
  }, [onUpdateArtifact])

  useEffect(() => {
    watchableArtifactsRef.current = watchableArtifacts
  }, [watchableArtifacts])

  useEffect(() => {
    if (typeof document === "undefined") return undefined
    const onVisibilityChange = () => setDocumentVisible(isDocumentVisible())
    document.addEventListener("visibilitychange", onVisibilityChange)
    return () => document.removeEventListener("visibilitychange", onVisibilityChange)
  }, [])

  useEffect(() => {
    if (!watchEnabled || !canWatch || watchPaused) return undefined
    let activeController: AbortController | undefined
    const run = () => {
      activeController?.abort()
      activeController = new AbortController()
      void refreshWatchableArtifacts(activeController.signal)
    }
    run()
    const intervalId = window.setInterval(run, watchIntervalSeconds! * 1000)
    return () => {
      window.clearInterval(intervalId)
      activeController?.abort()
    }
  }, [canWatch, refreshWatchableArtifacts, watchEnabled, watchIntervalSeconds, watchPaused])

  const handleWatchToggle = useCallback(() => {
    if (!sessionId) return
    onWatchIntervalChange(sessionId, watchEnabled ? undefined : watchIntervalSeconds ?? 15)
  }, [onWatchIntervalChange, sessionId, watchEnabled, watchIntervalSeconds])

  const handleIntervalChange = useCallback((value: string) => {
    if (!sessionId) return
    const seconds = Number(value)
    onWatchIntervalChange(sessionId, Number.isFinite(seconds) && seconds > 0 ? seconds : undefined)
  }, [onWatchIntervalChange, sessionId])

  const handleRefreshNow = useCallback(() => {
    void refreshWatchableArtifacts()
  }, [refreshWatchableArtifacts])

  if (collapsed) {
    return (
      <aside className="pointer-events-none absolute right-3 top-4 z-20 hidden lg:block" aria-label="Panel dock collapsed">
        <Button
          type="button"
          size="icon"
          variant="outline"
          className="pointer-events-auto size-10 bg-background/95 shadow-md backdrop-blur"
          onClick={() => onCollapsedChange(false)}
          aria-label="Expand panel dock"
          title="Expand panel dock"
        >
          <PanelRightOpen className="size-4" aria-hidden="true" />
          <span className="sr-only">Panel dock</span>
          {artifacts.length > 0 ? <span className="absolute -left-1 -top-1 rounded-full bg-primary px-1.5 py-0.5 text-[0.65rem] leading-none text-primary-foreground tabular-nums">{artifacts.length}</span> : null}
        </Button>
      </aside>
    )
  }

  return (
    <aside className="hidden h-full min-h-0 flex-col border-l border-border bg-card lg:flex" aria-label="Panel dock">
      <div className="flex min-h-0 flex-1 flex-col">
        <div className="flex h-14 shrink-0 items-center justify-between gap-3 border-b border-border px-4">
          <div className="flex min-w-0 items-center gap-2">
            <FileText className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
            <div className="min-w-0">
              <h2 className="truncate text-sm font-semibold text-foreground">Panel dock</h2>
              <p className="truncate text-[0.7rem] text-muted-foreground">{watchStatusText(artifacts.length, watchableArtifacts.length, watchEnabled, watchPaused, watchIntervalSeconds)}</p>
            </div>
          </div>
          <div className="flex shrink-0 items-center gap-1.5">
            <Button
              type="button"
              size="sm"
              variant={watchEnabled ? "secondary" : "outline"}
              disabled={!sessionId}
              onClick={handleWatchToggle}
            >
              Watch
            </Button>
            <select
              className="h-8 rounded-md border border-input bg-background px-2 text-xs text-foreground disabled:opacity-50"
              aria-label="Watch interval"
              disabled={!sessionId || !watchEnabled}
              value={String(watchIntervalSeconds ?? 15)}
              onChange={(event) => handleIntervalChange(event.target.value)}
            >
              <option value="5">5s</option>
              <option value="15">15s</option>
              <option value="30">30s</option>
              <option value="60">60s</option>
            </select>
            <Button type="button" size="icon-sm" variant="ghost" disabled={!canWatch} onClick={handleRefreshNow} aria-label="Refresh watchable panels">
              <RefreshCw className="size-4" aria-hidden="true" />
            </Button>
            <Button type="button" size="icon-sm" variant="ghost" onClick={() => onCollapsedChange(true)} aria-label="Collapse panel dock">
              <PanelRightClose className="size-4" aria-hidden="true" />
            </Button>
          </div>
        </div>

        <div className="min-h-0 flex-1 space-y-3 overflow-auto p-3">
          {artifacts.length === 0 ? (
            <div className="rounded-md border border-dashed border-border bg-background px-3 py-8 text-center text-sm text-muted-foreground">
              Pin resources, topology, history, or diff artifacts from the chat stream.
            </div>
          ) : (
            artifacts.map((artifact) => (
              <DockPanel
                key={artifact.id}
                artifact={artifact}
                selected={artifact.id === selectedArtifactId}
                watchState={watchStates[artifact.id]}
                watchable={Boolean(watchableArtifactTarget(artifact))}
                onClose={() => onCloseArtifact(artifact.id)}
              />
            ))
          )}
        </div>
      </div>
    </aside>
  )
}

function DockPanel({
  artifact,
  selected,
  watchState,
  watchable,
  onClose,
}: {
  artifact: AgentArtifact
  selected: boolean
  watchState?: ArtifactWatchState
  watchable: boolean
  onClose: () => void
}) {
  const selectArtifact = useAgentProjectionStore((state) => state.selectArtifact)
  return (
    <section className={selected ? "overflow-hidden rounded-md border border-primary/50 bg-background" : "overflow-hidden rounded-md border border-border bg-background"}>
      <div className="flex items-start justify-between gap-2 border-b border-border px-3 py-2">
        <button type="button" className="min-w-0 flex-1 text-left" onClick={() => selectArtifact(artifact.id)}>
          <div className="truncate text-[0.7rem] text-muted-foreground">{artifact.kind}</div>
          <div className="mt-1 truncate text-sm font-medium text-foreground">{artifact.title || "Artifact"}</div>
          <div className="mt-1 truncate text-[0.65rem] text-muted-foreground">{panelWatchStateText(watchable, watchState)}</div>
        </button>
        <Button type="button" size="icon-sm" variant="ghost" onClick={onClose} aria-label="Close artifact panel">
          <X className="size-3.5" aria-hidden="true" />
        </Button>
      </div>
      <div className="max-h-[24rem] overflow-auto px-3 py-3">
        <ArtifactBody artifact={artifact} />
      </div>
    </section>
  )
}

type ArtifactWatchState = {
  status: "refreshing" | "ok" | "error"
  refreshedAt?: string
  error?: string
}

export function ArtifactPanel({
  artifacts,
  selectedArtifactId,
}: {
  artifacts: AgentArtifact[]
  selectedArtifactId?: string
}) {
  const selectArtifact = useAgentProjectionStore((state) => state.selectArtifact)
  const activeArtifact = selectedArtifactId
    ? artifacts.find((artifact) => artifact.id === selectedArtifactId)
    : artifacts.at(-1)
  if (!activeArtifact) return null

  return (
    <aside className="mb-4 rounded-md border border-border bg-card shadow-sm" aria-label="Artifacts">
      <div className="flex items-start justify-between gap-3 border-b border-border px-3 py-3">
        <div className="min-w-0">
          <div className="flex items-center gap-2 text-xs text-muted-foreground">
            <FileText className="size-3.5" aria-hidden="true" />
            <span>{activeArtifact.kind}</span>
          </div>
          <h2 className="mt-1 truncate text-sm font-medium text-foreground">
            {activeArtifact.title || "Artifact"}
          </h2>
        </div>
        <div className="shrink-0 text-right text-[0.7rem] text-muted-foreground">
          {formatArtifactTime(activeArtifact.updatedAt)}
        </div>
      </div>

      {artifacts.length > 1 ? (
        <div className="flex gap-1 overflow-x-auto border-b border-border px-2 py-2">
          {artifacts.map((artifact) => (
            <Button
              key={artifact.id}
              type="button"
              size="sm"
              variant={artifact.id === activeArtifact.id ? "secondary" : "ghost"}
              onClick={() => selectArtifact(artifact.id)}
            >
              {artifact.title || artifact.kind}
            </Button>
          ))}
        </div>
      ) : null}

      <div className="max-h-[28rem] overflow-auto px-3 py-3">
        <ArtifactBody artifact={activeArtifact} />
      </div>
    </aside>
  )
}

function ArtifactBody({ artifact }: { artifact: AgentArtifact }) {
  if (artifact.kind === "markdown") {
    return <MarkdownContent text={markdownArtifactText(artifact.data)} />
  }
  if (artifact.kind === "k8s.resource") {
    return <K8sResourceArtifact data={artifact.data} />
  }
  if (artifact.kind === "k8s.resource_list") {
    return <K8sResourceListArtifact data={artifact.data} />
  }
  if (artifact.kind === "k8s.topology") {
    return <LazyArtifact><LazyK8sTopologyArtifact data={artifact.data} /></LazyArtifact>
  }
  if (artifact.kind === "k8s.history") {
    return <LazyArtifact><LazyK8sHistoryArtifact data={artifact.data} /></LazyArtifact>
  }
  if (artifact.kind === "k8s.diff") {
    return <LazyArtifact><LazyK8sDiffArtifact data={artifact.data} /></LazyArtifact>
  }
  return <LazyArtifact><LazyUnknownArtifact kind={artifact.kind} data={artifact.data} /></LazyArtifact>
}

function LazyArtifact({ children }: { children: ReactNode }) {
  return (
    <Suspense
      fallback={
        <div className="rounded-md border border-border bg-background px-3 py-6 text-center text-sm text-muted-foreground">
          Loading artifact renderer...
        </div>
      }
    >
      {children}
    </Suspense>
  )
}

function markdownArtifactText(data: unknown) {
  if (typeof data === "string") return data
  if (data && typeof data === "object" && typeof (data as { markdown?: unknown }).markdown === "string") {
    return (data as { markdown: string }).markdown
  }
  return ""
}

function formatArtifactTime(value: string) {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return ""
  return date.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })
}

function isDocumentVisible() {
  return typeof document === "undefined" || document.visibilityState !== "hidden"
}

function watchStatusText(
  pinnedCount: number,
  watchableCount: number,
  watchEnabled: boolean,
  watchPaused: boolean,
  intervalSeconds?: number,
) {
  if (!watchEnabled) return `${pinnedCount} pinned`
  if (watchableCount === 0) return "No watchable panels"
  if (watchPaused) return `Watch paused - ${watchableCount} panels`
  return `Watching ${watchableCount} panels - ${intervalSeconds ?? 15}s`
}

function panelWatchStateText(watchable: boolean, state?: ArtifactWatchState) {
  if (!watchable) return "Watch unavailable"
  if (!state) return "Watch ready"
  if (state.status === "refreshing") return "Refreshing..."
  if (state.status === "error") return state.error ?? "Refresh failed"
  return state.refreshedAt ? `Refreshed ${formatArtifactTime(state.refreshedAt)}` : "Refreshed"
}
