import { ArrowRight } from "lucide-react"

import { ProofViewer } from "@/components/proof-viewer"

export function K8sDiffArtifact({ data }: { data: unknown }) {
  const diff = diffData(data)

  return (
    <div className="space-y-4">
      <section className="rounded-md border border-border bg-background px-3 py-3">
        <div className="flex flex-wrap items-center gap-2">
          <VersionBadge label="Before" value={diff.beforeLabel} />
          <ArrowRight className="size-4 text-muted-foreground" aria-hidden="true" />
          <VersionBadge label="After" value={diff.afterLabel} />
        </div>
        {diff.title ? <h3 className="mt-3 text-sm font-semibold text-foreground">{diff.title}</h3> : null}
      </section>

      {diff.changes.length > 0 ? (
        <section>
          <h4 className="mb-2 text-xs font-medium uppercase text-muted-foreground">Changes</h4>
          <div className="space-y-1">
            {diff.changes.map((change) => (
              <div key={change.key} className="rounded-md border border-border bg-card px-3 py-2 text-sm">
                <div className="font-mono text-xs text-foreground">{change.path}</div>
                <div className="mt-1 text-xs text-muted-foreground">{change.summary}</div>
              </div>
            ))}
          </div>
        </section>
      ) : null}

      <section className="grid gap-3 lg:grid-cols-2">
        <div className="min-w-0">
          <h4 className="mb-2 text-xs font-medium uppercase text-muted-foreground">Before</h4>
          <ProofViewer value={diff.before} language={diff.language} />
        </div>
        <div className="min-w-0">
          <h4 className="mb-2 text-xs font-medium uppercase text-muted-foreground">After</h4>
          <ProofViewer value={diff.after} language={diff.language} />
        </div>
      </section>
    </div>
  )
}

type DiffData = {
  title?: string
  beforeLabel: string
  afterLabel: string
  before: unknown
  after: unknown
  language: "json" | "yaml"
  changes: Array<{ key: string; path: string; summary: string }>
}

function VersionBadge({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-md border border-border bg-card px-2.5 py-1.5 text-xs">
      <span className="text-muted-foreground">{label}</span>
      <span className="ml-2 font-mono text-foreground">{value}</span>
    </div>
  )
}

function diffData(data: unknown): DiffData {
  const payload = asRecord(data)
  const before = payload?.before ?? payload?.from ?? payload?.old ?? {}
  const after = payload?.after ?? payload?.to ?? payload?.new ?? {}
  return {
    title: textValue(payload?.title),
    beforeLabel: textValue(payload?.beforeLabel ?? payload?.fromVersion ?? asRecord(payload?.before)?.resourceVersion) ?? "before",
    afterLabel: textValue(payload?.afterLabel ?? payload?.toVersion ?? asRecord(payload?.after)?.resourceVersion) ?? "after",
    before,
    after,
    language: typeof before === "string" || typeof after === "string" ? "yaml" : "json",
    changes: changeEntries(payload?.changes),
  }
}

function changeEntries(value: unknown) {
  const changes = asArray(value) ?? []
  return changes.map((item, index) => {
    const change = asRecord(item)
    const path = textValue(change?.path ?? change?.field) ?? `change ${index + 1}`
    const summary = textValue(change?.summary ?? change?.message ?? change?.op) ?? "Changed"
    return { key: `${path}-${index}`, path, summary }
  })
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
