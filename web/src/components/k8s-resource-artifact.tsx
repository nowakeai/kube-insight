import { AlertTriangle, CheckCircle2, CircleDashed, CopyCheck, FileCode2 } from "lucide-react"

import { ProofViewer } from "@/components/proof-viewer"

export function K8sResourceArtifact({ data }: { data: unknown }) {
  const payload = asRecord(data)
  const object = asRecord(payload?.object) ?? asRecord(payload?.resource)
  const metadata = asRecord(object?.metadata)
  const identity = resourceIdentity(payload, object, metadata)
  const labels = recordEntries(asRecord(payload?.labels) ?? asRecord(metadata?.labels))
  const annotations = recordEntries(asRecord(payload?.annotations) ?? asRecord(metadata?.annotations))
  const conditions = resourceConditions(payload, object)
  const status = resourceStatus(payload, object)
  const summary = resourceSummary(payload)
  const proof = payload?.yaml ?? payload?.json ?? payload?.object ?? payload?.resource ?? data

  return (
    <div className="space-y-4">
      <section className="rounded-md border border-border bg-background">
        <div className="flex flex-wrap items-start justify-between gap-3 border-b border-border px-3 py-3">
          <div className="min-w-0">
            <div className="flex flex-wrap items-center gap-2">
              <span className="rounded-md bg-primary/10 px-2 py-1 text-xs font-medium text-primary">
                {identity.kind || "Resource"}
              </span>
              {identity.namespace ? (
                <span className="text-xs text-muted-foreground">{identity.namespace}</span>
              ) : null}
            </div>
            <h3 className="mt-2 truncate text-base font-semibold text-foreground">{identity.name || "Unnamed object"}</h3>
          </div>
          <StatusBadge status={status} />
        </div>
        <dl className="grid gap-px bg-border text-xs sm:grid-cols-2">
          {identityFields(identity).map(([label, value]) => (
            <div key={label} className="min-w-0 bg-card px-3 py-2">
              <dt className="text-muted-foreground">{label}</dt>
              <dd className="mt-1 truncate font-mono text-foreground">{value}</dd>
            </div>
          ))}
        </dl>
      </section>

      {summary.length > 0 ? (
        <section>
          <h4 className="mb-2 text-xs font-medium uppercase text-muted-foreground">Summary</h4>
          <ul className="space-y-1 text-sm text-foreground">
            {summary.map((item) => (
              <li key={item} className="leading-6">
                {item}
              </li>
            ))}
          </ul>
        </section>
      ) : null}

      {conditions.length > 0 ? (
        <section>
          <h4 className="mb-2 text-xs font-medium uppercase text-muted-foreground">Conditions</h4>
          <div className="overflow-x-auto rounded-md border border-border">
            <table className="w-full min-w-[34rem] border-collapse text-left text-xs">
              <thead className="bg-muted text-muted-foreground">
                <tr>
                  <th className="px-3 py-2 font-medium">Type</th>
                  <th className="px-3 py-2 font-medium">Status</th>
                  <th className="px-3 py-2 font-medium">Reason</th>
                  <th className="px-3 py-2 font-medium">Message</th>
                </tr>
              </thead>
              <tbody>
                {conditions.map((condition) => (
                  <tr key={`${condition.type}-${condition.status}`} className="border-t border-border">
                    <td className="px-3 py-2 font-medium text-foreground">{condition.type}</td>
                    <td className="px-3 py-2">
                      <ConditionStatus value={condition.status} />
                    </td>
                    <td className="px-3 py-2 text-muted-foreground">{condition.reason}</td>
                    <td className="px-3 py-2 text-muted-foreground">{condition.message}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </section>
      ) : null}

      <KeyValueSection title="Labels" entries={labels} />
      <KeyValueSection title="Annotations" entries={annotations} />

      <section>
        <h4 className="mb-2 flex items-center gap-1.5 text-xs font-medium uppercase text-muted-foreground">
          <FileCode2 className="size-3.5" aria-hidden="true" />
          Proof
        </h4>
<ProofViewer value={proof} language="yaml" />
      </section>
    </div>
  )
}

type ResourceIdentity = {
  cluster?: string
  apiVersion?: string
  kind?: string
  namespace?: string
  name?: string
  uid?: string
  resourceVersion?: string
}

type ResourceStatus = {
  phase?: string
  ready?: string
  reason?: string
  message?: string
}

type ResourceCondition = {
  type: string
  status: string
  reason: string
  message: string
}

function StatusBadge({ status }: { status: ResourceStatus }) {
  const ready = status.ready?.toLowerCase()
  const phase = status.phase?.toLowerCase()
  const failed = phase === "failed" || ready === "false"
  const ok = ready === "true" || phase === "running" || phase === "active" || phase === "bound"
  const Icon = failed ? AlertTriangle : ok ? CheckCircle2 : CircleDashed
  const text = status.ready ? `Ready ${status.ready}` : status.phase || status.reason || "Status unknown"
  return (
    <div className="flex max-w-full items-center gap-2 rounded-md border border-border bg-card px-2.5 py-1.5 text-xs">
      <Icon className={failed ? "size-3.5 text-destructive" : ok ? "size-3.5 text-accent" : "size-3.5 text-muted-foreground"} aria-hidden="true" />
      <span className="truncate font-medium text-foreground">{text}</span>
    </div>
  )
}

function ConditionStatus({ value }: { value: string }) {
  const normalized = value.toLowerCase()
  const className =
    normalized === "true"
      ? "bg-accent/10 text-accent"
      : normalized === "false"
        ? "bg-destructive/10 text-destructive"
        : "bg-muted text-muted-foreground"
  return <span className={`rounded-md px-2 py-1 font-medium ${className}`}>{value || "Unknown"}</span>
}

function KeyValueSection({ title, entries }: { title: string; entries: [string, string][] }) {
  if (entries.length === 0) return null
  return (
    <section>
      <h4 className="mb-2 flex items-center gap-1.5 text-xs font-medium uppercase text-muted-foreground">
        <CopyCheck className="size-3.5" aria-hidden="true" />
        {title}
      </h4>
      <div className="grid gap-1 sm:grid-cols-2">
        {entries.slice(0, 12).map(([key, value]) => (
          <div key={key} className="min-w-0 rounded-md border border-border bg-background px-2.5 py-2">
            <div className="truncate font-mono text-xs text-foreground">{key}</div>
            <div className="mt-1 truncate text-xs text-muted-foreground">{value}</div>
          </div>
        ))}
      </div>
      {entries.length > 12 ? <p className="mt-2 text-xs text-muted-foreground">+{entries.length - 12} more</p> : null}
    </section>
  )
}

function resourceIdentity(payload: Record<string, unknown> | undefined, object: Record<string, unknown> | undefined, metadata: Record<string, unknown> | undefined): ResourceIdentity {
  const identity = asRecord(payload?.identity)
  return {
    cluster: textValue(identity?.cluster ?? payload?.cluster),
    apiVersion: textValue(identity?.apiVersion ?? payload?.apiVersion ?? object?.apiVersion),
    kind: textValue(identity?.kind ?? payload?.kind ?? object?.kind),
    namespace: textValue(identity?.namespace ?? payload?.namespace ?? metadata?.namespace),
    name: textValue(identity?.name ?? payload?.name ?? metadata?.name),
    uid: textValue(identity?.uid ?? payload?.uid ?? metadata?.uid),
    resourceVersion: textValue(identity?.resourceVersion ?? payload?.resourceVersion ?? metadata?.resourceVersion),
  }
}

function resourceStatus(payload: Record<string, unknown> | undefined, object: Record<string, unknown> | undefined): ResourceStatus {
  const payloadStatus = asRecord(payload?.status)
  const objectStatus = asRecord(object?.status)
  return {
    phase: textValue(payloadStatus?.phase ?? payload?.phase ?? objectStatus?.phase),
    ready: textValue(payloadStatus?.ready ?? payload?.ready),
    reason: textValue(payloadStatus?.reason ?? payload?.reason),
    message: textValue(payloadStatus?.message ?? payload?.message),
  }
}

function resourceConditions(payload: Record<string, unknown> | undefined, object: Record<string, unknown> | undefined): ResourceCondition[] {
  const payloadStatus = asRecord(payload?.status)
  const objectStatus = asRecord(object?.status)
  const raw = asArray(payload?.conditions) ?? asArray(payloadStatus?.conditions) ?? asArray(objectStatus?.conditions) ?? []
  return raw.map((item) => asRecord(item)).filter(Boolean).map((condition) => ({
    type: textValue(condition?.type) ?? "",
    status: textValue(condition?.status) ?? "",
    reason: textValue(condition?.reason) ?? "",
    message: textValue(condition?.message) ?? "",
  })).filter((condition) => condition.type || condition.status || condition.reason || condition.message)
}

function resourceSummary(payload: Record<string, unknown> | undefined) {
  const summary = payload?.summary
  if (Array.isArray(summary)) return summary.map((item) => textValue(item)).filter((item): item is string => Boolean(item))
  const text = textValue(summary)
  return text ? [text] : []
}

function identityFields(identity: ResourceIdentity): [string, string][] {
  return [
    ["Cluster", identity.cluster],
    ["API version", identity.apiVersion],
    ["Namespace", identity.namespace],
    ["Name", identity.name],
    ["UID", identity.uid],
    ["Resource version", identity.resourceVersion],
  ].filter((item): item is [string, string] => Boolean(item[1]))
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
