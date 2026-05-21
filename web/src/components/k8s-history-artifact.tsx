import { ChevronLeft, ChevronRight } from "lucide-react"
import { useMemo, useState } from "react"

import { ProofViewer } from "@/components/proof-viewer"
import { Button } from "@/components/ui/button"

export function K8sHistoryArtifact({ data }: { data: unknown }) {
  const history = useMemo(() => historyData(data), [data])
  const [selectedIndex, setSelectedIndex] = useState(() => Math.max(history.versions.length - 1, 0))
  const boundedIndex = Math.min(selectedIndex, Math.max(history.versions.length - 1, 0))
  const selected = history.versions[boundedIndex]

  if (!selected) {
    return (
      <div className="rounded-md border border-dashed border-border bg-background px-3 py-6 text-center text-sm text-muted-foreground">
        No history versions were included in this artifact.
      </div>
    )
  }

  return (
    <div className="space-y-4">
      <section className="rounded-md border border-border bg-background px-3 py-3">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div className="min-w-0">
            <h3 className="truncate text-sm font-semibold text-foreground">{history.title}</h3>
            <p className="text-xs text-muted-foreground">{history.versions.length} retained versions</p>
          </div>
          <div className="flex items-center gap-1">
            <Button
              type="button"
              size="icon-sm"
              variant="outline"
              aria-label="Previous version"
              disabled={boundedIndex === 0}
              onClick={() => setSelectedIndex((current) => Math.max(current - 1, 0))}
            >
              <ChevronLeft className="size-4" aria-hidden="true" />
            </Button>
            <Button
              type="button"
              size="icon-sm"
              variant="outline"
              aria-label="Next version"
              disabled={boundedIndex >= history.versions.length - 1}
              onClick={() => setSelectedIndex((current) => Math.min(current + 1, history.versions.length - 1))}
            >
              <ChevronRight className="size-4" aria-hidden="true" />
            </Button>
          </div>
        </div>
        <div className="mt-3 flex gap-1 overflow-x-auto">
          {history.versions.map((version, index) => (
            <Button
              key={version.id}
              type="button"
              size="sm"
              variant={index === boundedIndex ? "secondary" : "ghost"}
              onClick={() => setSelectedIndex(index)}
            >
              {versionLabel(version, index)}
            </Button>
          ))}
        </div>
      </section>

      <section className="grid gap-px overflow-hidden rounded-md border border-border bg-border text-xs sm:grid-cols-2">
        {versionFields(selected).map(([label, value]) => (
          <div key={label} className="min-w-0 bg-card px-3 py-2">
            <div className="text-muted-foreground">{label}</div>
            <div className="mt-1 truncate font-mono text-foreground">{value}</div>
          </div>
        ))}
      </section>

      {selected.summary.length > 0 ? (
        <section>
          <h4 className="mb-2 text-xs font-medium uppercase text-muted-foreground">Summary</h4>
          <ul className="space-y-1 text-sm text-foreground">
            {selected.summary.map((line) => (
              <li key={line} className="leading-6">
                {line}
              </li>
            ))}
          </ul>
        </section>
      ) : null}

      <section>
        <h4 className="mb-2 text-xs font-medium uppercase text-muted-foreground">Retained document</h4>
        <ProofViewer value={selected.proof} language={selected.language} />
      </section>
    </div>
  )
}

type HistoryVersion = {
  id: string
  observedAt?: string
  resourceVersion?: string
  status?: string
  reason?: string
  summary: string[]
  proof: unknown
  language: "json" | "yaml"
}

type HistoryData = {
  title: string
  versions: HistoryVersion[]
}

function historyData(data: unknown): HistoryData {
  const payload = asRecord(data)
  const identity = asRecord(payload?.identity)
  const title = textValue(payload?.title) ?? [identity?.kind, identity?.namespace, identity?.name].filter(Boolean).join("/") ?? "History"
  const rawVersions = asArray(payload?.versions) ?? asArray(payload?.history) ?? []
  return {
    title,
    versions: rawVersions.map(historyVersion).filter((version): version is HistoryVersion => Boolean(version)),
  }
}

function historyVersion(value: unknown): HistoryVersion | undefined {
  const version = asRecord(value)
  if (!version) return undefined
  const proof = version.document ?? version.object ?? version.resource ?? version.json ?? version.yaml ?? version
  return {
    id: textValue(version.id ?? version.versionId ?? version.resourceVersion) ?? crypto.randomUUID(),
    observedAt: textValue(version.observedAt ?? version.timestamp ?? version.time),
    resourceVersion: textValue(version.resourceVersion),
    status: textValue(version.status ?? version.phase),
    reason: textValue(version.reason),
    summary: summaryLines(version),
    proof,
    language: typeof version.yaml === "string" ? "yaml" : "json",
  }
}

function versionFields(version: HistoryVersion): [string, string][] {
  return [
    ["Version", version.id],
    ["Observed", version.observedAt],
    ["Resource version", version.resourceVersion],
    ["Status", version.status],
    ["Reason", version.reason],
  ].filter((item): item is [string, string] => Boolean(item[1]))
}

function versionLabel(version: HistoryVersion, index: number) {
  if (version.resourceVersion) return version.resourceVersion
  return version.id || `v${index + 1}`
}

function summaryLines(payload: Record<string, unknown>) {
  const summary = payload.summary
  if (Array.isArray(summary)) return summary.map((item) => textValue(item)).filter((item): item is string => Boolean(item))
  const text = textValue(summary)
  return text ? [text] : []
}

function asRecord(value: unknown): Record<string, unknown> | undefined {
  if (!value || typeof value !== "object" || Array.isArray(value)) return undefined
  return value as Record<string, unknown>
}

function asArray(value: unknown): unknown[] | undefined {
  return Array.isArray(value) ? value : undefined
}

function textValue(value: unknown): string | undefined {
  if (typeof value === "string") return value
  if (typeof value === "number" || typeof value === "boolean") return String(value)
  return undefined
}
