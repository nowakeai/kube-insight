import { AlertCircle, CheckCircle2, ListTree } from "lucide-react"

export function K8sResourceListArtifact({ data }: { data: unknown }) {
  const groups = resourceGroups(data)
  const count = groups.reduce((total, group) => total + group.items.length, 0)

  if (count === 0) {
    return (
      <div className="rounded-md border border-dashed border-border bg-background px-3 py-6 text-center text-sm text-muted-foreground">
        No Kubernetes resource candidates were included in this artifact.
      </div>
    )
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between gap-3 rounded-md border border-border bg-background px-3 py-3">
        <div className="flex min-w-0 items-center gap-2">
          <ListTree className="size-4 text-muted-foreground" aria-hidden="true" />
          <div className="min-w-0">
            <h3 className="truncate text-sm font-semibold text-foreground">Resource candidates</h3>
            <p className="text-xs text-muted-foreground">{count} objects</p>
          </div>
        </div>
      </div>

      {groups.map((group) => (
        <section key={group.title} className="space-y-2">
          <div className="flex items-center justify-between gap-3">
            <h4 className="text-xs font-medium uppercase text-muted-foreground">{group.title}</h4>
            <span className="text-xs text-muted-foreground">{group.items.length}</span>
          </div>
          <div className="space-y-2">
            {group.items.map((item, index) => (
              <ResourceCandidateRow key={item.key} item={item} rank={index + 1} />
            ))}
          </div>
        </section>
      ))}
    </div>
  )
}

type ResourceListGroup = {
  title: string
  items: ResourceCandidate[]
}

type ResourceCandidate = {
  key: string
  identity: ResourceIdentity
  status: ResourceStatus
  summary: string[]
  reason?: string
  score?: string
  labels: [string, string][]
}

type ResourceIdentity = {
  apiVersion?: string
  kind?: string
  namespace?: string
  name?: string
}

type ResourceStatus = {
  phase?: string
  ready?: string
  reason?: string
}

function ResourceCandidateRow({ item, rank }: { item: ResourceCandidate; rank: number }) {
  const warning = item.status.ready?.toLowerCase() === "false" || item.status.phase?.toLowerCase() === "failed"
  const Icon = warning ? AlertCircle : CheckCircle2
  return (
    <article className="rounded-md border border-border bg-card px-3 py-3">
      <div className="flex items-start gap-3">
        <div className="flex size-7 shrink-0 items-center justify-center rounded-md bg-muted text-xs font-medium text-muted-foreground">
          {rank}
        </div>
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <span className="rounded-md bg-primary/10 px-2 py-1 text-xs font-medium text-primary">
              {item.identity.kind || "Resource"}
            </span>
            {item.identity.namespace ? (
              <span className="text-xs text-muted-foreground">{item.identity.namespace}</span>
            ) : null}
            {item.score ? <span className="text-xs text-muted-foreground">score {item.score}</span> : null}
          </div>
          <h5 className="mt-2 truncate text-sm font-semibold text-foreground">{item.identity.name || "Unnamed object"}</h5>
          {item.reason ? <p className="mt-1 text-xs text-muted-foreground">{item.reason}</p> : null}
          {item.summary.length > 0 ? (
            <ul className="mt-2 space-y-1 text-xs text-muted-foreground">
              {item.summary.slice(0, 3).map((line) => (
                <li key={line} className="leading-5">
                  {line}
                </li>
              ))}
            </ul>
          ) : null}
          {item.labels.length > 0 ? (
            <div className="mt-2 flex flex-wrap gap-1">
              {item.labels.slice(0, 4).map(([key, value]) => (
                <span key={key} className="max-w-full truncate rounded-md bg-muted px-2 py-1 font-mono text-[0.7rem] text-muted-foreground">
                  {key}={value}
                </span>
              ))}
            </div>
          ) : null}
        </div>
        <div className="flex shrink-0 items-center gap-1 rounded-md border border-border bg-background px-2 py-1 text-xs">
          <Icon className={warning ? "size-3.5 text-destructive" : "size-3.5 text-accent"} aria-hidden="true" />
          <span className="font-medium text-foreground">{statusLabel(item.status)}</span>
        </div>
      </div>
    </article>
  )
}

function resourceGroups(data: unknown): ResourceListGroup[] {
  const payload = asRecord(data)
  const explicitGroups = asArray(payload?.groups)
  if (explicitGroups) {
    return explicitGroups
      .map((group, index) => {
        const record = asRecord(group)
        const items = asArray(record?.items) ?? []
        return {
          title: textValue(record?.title ?? record?.name) ?? `Group ${index + 1}`,
          items: items.map((item, itemIndex) => resourceCandidate(item, `${index}-${itemIndex}`)),
        }
      })
      .filter((group) => group.items.length > 0)
  }

  const items = asArray(data) ?? asArray(payload?.items) ?? asArray(payload?.resources) ?? []
  return [{ title: textValue(payload?.title) ?? "Candidates", items: items.map((item, index) => resourceCandidate(item, String(index))) }]
}

function resourceCandidate(value: unknown, fallbackKey: string): ResourceCandidate {
  const item = asRecord(value)
  const object = asRecord(item?.object) ?? asRecord(item?.resource)
  const metadata = asRecord(object?.metadata)
  const identity = resourceIdentity(item, object, metadata)
  return {
    key: textValue(item?.id ?? item?.key) ?? `${identity.kind}-${identity.namespace}-${identity.name}-${fallbackKey}`,
    identity,
    status: resourceStatus(item, object),
    summary: summaryLines(item),
    reason: textValue(item?.reason ?? item?.matchReason),
    score: textValue(item?.score ?? item?.rankScore),
    labels: recordEntries(asRecord(item?.labels) ?? asRecord(metadata?.labels)),
  }
}

function resourceIdentity(
  payload: Record<string, unknown> | undefined,
  object: Record<string, unknown> | undefined,
  metadata: Record<string, unknown> | undefined,
): ResourceIdentity {
  const identity = asRecord(payload?.identity)
  return {
    apiVersion: textValue(identity?.apiVersion ?? payload?.apiVersion ?? object?.apiVersion),
    kind: textValue(identity?.kind ?? payload?.kind ?? object?.kind),
    namespace: textValue(identity?.namespace ?? payload?.namespace ?? metadata?.namespace),
    name: textValue(identity?.name ?? payload?.name ?? metadata?.name),
  }
}

function resourceStatus(payload: Record<string, unknown> | undefined, object: Record<string, unknown> | undefined): ResourceStatus {
  const payloadStatus = asRecord(payload?.status)
  const objectStatus = asRecord(object?.status)
  return {
    phase: textValue(payloadStatus?.phase ?? payload?.phase ?? objectStatus?.phase),
    ready: textValue(payloadStatus?.ready ?? payload?.ready),
    reason: textValue(payloadStatus?.reason ?? payload?.reason),
  }
}

function summaryLines(payload: Record<string, unknown> | undefined) {
  const summary = payload?.summary
  if (Array.isArray(summary)) return summary.map((item) => textValue(item)).filter((item): item is string => Boolean(item))
  const text = textValue(summary)
  return text ? [text] : []
}

function statusLabel(status: ResourceStatus) {
  if (status.ready) return `Ready ${status.ready}`
  return status.phase ?? status.reason ?? "Unknown"
}

function recordEntries(value: Record<string, unknown> | undefined): [string, string][] {
  if (!value) return []
  return Object.entries(value)
    .map(([key, item]) => [key, textValue(item) ?? JSON.stringify(item)] as [string, string])
    .filter(([, item]) => Boolean(item))
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
