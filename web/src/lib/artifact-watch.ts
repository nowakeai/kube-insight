import { agentRequestJSON, type AgentAPIOptions } from "@/lib/agent-api"
import type { AgentArtifact } from "@/lib/agent-store"

export type ArtifactWatchTarget = {
  clusterId?: string
  kind: string
  namespace?: string
  name: string
}

export type ArtifactRefreshResult = {
  title?: string
  data: unknown
}

export function watchableArtifactTarget(artifact: AgentArtifact): ArtifactWatchTarget | undefined {
  if (!isWatchableArtifactKind(artifact.kind)) return undefined
  const payload = asRecord(artifact.data)
  if (artifact.kind === "k8s.topology") {
    return targetFromTopology(payload)
  }
  return targetFromPayload(payload)
}

export function isWatchableArtifactKind(kind: AgentArtifact["kind"]) {
  return kind === "k8s.resource" || kind === "k8s.history" || kind === "k8s.topology"
}

export async function refreshArtifact(artifact: AgentArtifact, options?: AgentAPIOptions): Promise<ArtifactRefreshResult> {
  const target = watchableArtifactTarget(artifact)
  if (!target) throw new Error("Artifact is not watchable.")
  if (artifact.kind === "k8s.topology") return refreshTopologyArtifact(target, options)
  if (artifact.kind === "k8s.history") return refreshHistoryArtifact(target, artifact.data, options)
  return refreshResourceArtifact(target, options)
}

async function refreshResourceArtifact(target: ArtifactWatchTarget, options?: AgentAPIOptions): Promise<ArtifactRefreshResult> {
  const history = await getObjectHistory(target, { maxVersions: 1, maxObservations: 5, includeDocs: true, diffs: false }, options)
  const record = asRecord(history)
  const versions = asArray(record?.versions) ?? []
  const latest = asRecord(versions[0])
  const document = latest?.document ?? latest?.object ?? latest?.resource ?? latest?.json ?? latest
  return {
    title: `Resource: ${targetLabel(target)}`,
    data: {
      identity: record?.object ?? target,
      object: document,
      history,
      refreshedAt: new Date().toISOString(),
    },
  }
}

async function refreshHistoryArtifact(target: ArtifactWatchTarget, previousData: unknown, options?: AgentAPIOptions): Promise<ArtifactRefreshResult> {
  const previousVersions = asArray(asRecord(previousData)?.versions)
  const maxVersions = Math.max(previousVersions?.length ?? 0, 5)
  const history = await getObjectHistory(target, { maxVersions, maxObservations: 20, includeDocs: true, diffs: true }, options)
  const record = asRecord(history)
  return {
    title: `History: ${targetLabel(target)}`,
    data: {
      identity: record?.object ?? target,
      title: targetLabel(target),
      versions: record?.versions ?? [],
      observations: record?.observations ?? [],
      versionDiffs: record?.versionDiffs ?? [],
      summary: record?.summary,
      refreshedAt: new Date().toISOString(),
    },
  }
}

async function refreshTopologyArtifact(target: ArtifactWatchTarget, options?: AgentAPIOptions): Promise<ArtifactRefreshResult> {
  const graph = await agentRequestJSON<unknown>(`/api/v1/topology?${targetSearchParams(target)}`, options)
  return {
    title: `Topology: ${targetLabel(target)}`,
    data: topologyArtifactData(graph, target),
  }
}

async function getObjectHistory(
  target: ArtifactWatchTarget,
  params: { maxVersions: number; maxObservations: number; includeDocs: boolean; diffs: boolean },
  options?: AgentAPIOptions,
) {
  const search = new URLSearchParams(targetParams(target))
  search.set("maxVersions", String(params.maxVersions))
  search.set("maxObservations", String(params.maxObservations))
  search.set("includeDocs", String(params.includeDocs))
  search.set("diffs", String(params.diffs))
  return agentRequestJSON<unknown>(`/api/v1/history?${search.toString()}`, options)
}

function targetFromPayload(payload: Record<string, unknown> | undefined): ArtifactWatchTarget | undefined {
  const identity = asRecord(payload?.identity)
  const object = asRecord(payload?.object) ?? asRecord(payload?.resource)
  const metadata = asRecord(object?.metadata)
  return targetFromParts({
    clusterId: textValue(identity?.clusterId ?? identity?.cluster ?? payload?.clusterId ?? payload?.cluster),
    kind: textValue(identity?.kind ?? payload?.kind ?? object?.kind),
    namespace: textValue(identity?.namespace ?? payload?.namespace ?? metadata?.namespace),
    name: textValue(identity?.name ?? payload?.name ?? metadata?.name),
  })
}

function targetFromTopology(payload: Record<string, unknown> | undefined): ArtifactWatchTarget | undefined {
  const root = asRecord(payload?.root) ?? asRecord(asRecord(payload?.graph)?.root)
  const rootTarget = targetFromObjectRecord(root)
  if (rootTarget) return rootTarget
  const firstNode = asRecord((asArray(payload?.nodes) ?? asArray(asRecord(payload?.graph)?.nodes) ?? [])[0])
  const firstNodeTarget = targetFromNodeId(textValue(firstNode?.id)) ?? targetFromObjectRecord(firstNode)
  return firstNodeTarget
}

function targetFromObjectRecord(record: Record<string, unknown> | undefined): ArtifactWatchTarget | undefined {
  const identity = asRecord(record?.identity)
  const source = identity ?? record
  return targetFromParts({
    clusterId: textValue(source?.clusterId ?? source?.cluster),
    kind: textValue(source?.kind),
    namespace: textValue(source?.namespace),
    name: textValue(source?.name),
  })
}

function targetFromNodeId(id: string | undefined): ArtifactWatchTarget | undefined {
  if (!id) return undefined
  const parts = id.split("/").filter(Boolean)
  if (parts.length >= 4) {
    return targetFromParts({
      clusterId: parts[0],
      kind: parts[1],
      namespace: parts.length === 4 ? parts[2] : parts.slice(2, -1).join("/"),
      name: parts.at(-1),
    })
  }
  if (parts.length === 3) {
    return targetFromParts({ kind: parts[0], namespace: parts[1], name: parts[2] })
  }
  if (parts.length === 2) {
    return targetFromParts({ kind: parts[0], name: parts[1] })
  }
  return undefined
}

function targetFromParts(parts: { clusterId?: string; kind?: string; namespace?: string; name?: string }): ArtifactWatchTarget | undefined {
  if (!parts.kind || !parts.name) return undefined
  return {
    clusterId: parts.clusterId,
    kind: parts.kind,
    namespace: parts.namespace,
    name: parts.name,
  }
}

function topologyArtifactData(graph: unknown, fallbackTarget: ArtifactWatchTarget) {
  const record = asRecord(graph)
  const root = asRecord(record?.root)
  const rawNodes = asArray(record?.nodes) ?? []
  const rawEdges = asArray(record?.edges) ?? []
  const nodes = [root, ...rawNodes.map(asRecord)]
    .filter((node): node is Record<string, unknown> => Boolean(node))
    .map(topologyNode)
    .filter((node): node is Record<string, unknown> => Boolean(node))
  const uniqueNodes = uniqueBy(nodes, (node) => textValue(node.id) ?? "")
  const edges = rawEdges.map(topologyEdge).filter((edge): edge is Record<string, unknown> => Boolean(edge))
  return {
    title: `Topology: ${targetLabel(fallbackTarget)}`,
    root: record?.root ?? fallbackTarget,
    nodes: uniqueNodes,
    edges,
    summary: record?.summary,
    refreshedAt: new Date().toISOString(),
  }
}

function topologyNode(value: Record<string, unknown> | undefined): Record<string, unknown> | undefined {
  if (!value) return undefined
  const target = targetFromObjectRecord(value)
  const id = target ? objectId(target) : textValue(value.id)
  if (!id) return undefined
  return {
    id,
    label: target ? targetLabel(target) : textValue(value.label) ?? id,
    clusterId: target?.clusterId,
    kind: target?.kind ?? textValue(value.kind),
    namespace: target?.namespace ?? textValue(value.namespace),
    name: target?.name ?? textValue(value.name),
  }
}

function topologyEdge(value: unknown): Record<string, unknown> | undefined {
  const edge = asRecord(value)
  if (!edge) return undefined
  const source = objectIdFromRecord(asRecord(edge.source) ?? asRecord(edge.src))
  const target = objectIdFromRecord(asRecord(edge.target) ?? asRecord(edge.dst))
  if (!source || !target) return undefined
  const label = textValue(edge.type ?? edge.label)
  return { id: `${source}->${target}:${label ?? ""}`, source, target, label }
}

function objectIdFromRecord(record: Record<string, unknown> | undefined) {
  const target = targetFromObjectRecord(record)
  return target ? objectId(target) : undefined
}

function objectId(target: ArtifactWatchTarget) {
  return [target.clusterId, target.kind, target.namespace, target.name].filter(Boolean).join("/")
}

function targetLabel(target: ArtifactWatchTarget) {
  return [target.clusterId, target.kind, target.namespace, target.name].filter(Boolean).join("/")
}

function targetSearchParams(target: ArtifactWatchTarget) {
  return new URLSearchParams(targetParams(target)).toString()
}

function targetParams(target: ArtifactWatchTarget) {
  const params: Record<string, string> = {
    kind: target.kind,
    name: target.name,
  }
  if (target.clusterId) params.cluster = target.clusterId
  if (target.namespace) params.namespace = target.namespace
  return params
}

function uniqueBy<T>(values: T[], key: (value: T) => string) {
  const seen = new Set<string>()
  const out: T[] = []
  for (const value of values) {
    const id = key(value)
    if (!id || seen.has(id)) continue
    seen.add(id)
    out.push(value)
  }
  return out
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
