import { Background, Controls, MarkerType, MiniMap, ReactFlow, type Edge, type Node } from "@xyflow/react"
import "@xyflow/react/dist/style.css"

export function K8sTopologyArtifact({ data }: { data: unknown }) {
  const topology = topologyGraph(data)

  if (topology.nodes.length === 0) {
    return (
      <div className="rounded-md border border-dashed border-border bg-background px-3 py-6 text-center text-sm text-muted-foreground">
        No topology nodes were included in this artifact.
      </div>
    )
  }

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center justify-between gap-3 rounded-md border border-border bg-background px-3 py-3">
        <div className="min-w-0">
          <h3 className="truncate text-sm font-semibold text-foreground">{topology.title}</h3>
          <p className="text-xs text-muted-foreground">
            {topology.nodes.length} nodes, {topology.edges.length} edges
          </p>
        </div>
        <div className="flex flex-wrap gap-1">
          {topology.kinds.map((kind) => (
            <span key={kind} className="rounded-md bg-muted px-2 py-1 text-xs text-muted-foreground">
              {kind}
            </span>
          ))}
        </div>
      </div>

      <div className="h-[24rem] overflow-hidden rounded-md border border-border bg-card">
        <ReactFlow
          nodes={topology.nodes}
          edges={topology.edges}
          fitView
          fitViewOptions={{ padding: 0.2 }}
          minZoom={0.45}
          maxZoom={1.6}
          nodesDraggable={false}
          nodesConnectable={false}
          proOptions={{ hideAttribution: true }}
        >
          <Background color="var(--border)" gap={18} />
          <MiniMap pannable zoomable nodeStrokeWidth={2} />
          <Controls showInteractive={false} />
        </ReactFlow>
      </div>

      {topology.edges.length > 0 ? (
        <section>
          <h4 className="mb-2 text-xs font-medium uppercase text-muted-foreground">Edges</h4>
          <div className="space-y-1">
            {topology.edgeSummaries.slice(0, 8).map((edge) => (
              <div key={edge.key} className="rounded-md border border-border bg-background px-2.5 py-2 text-xs text-muted-foreground">
                <span className="font-mono text-foreground">{edge.source}</span>
                <span>{" -> "}</span>
                <span className="font-mono text-foreground">{edge.target}</span>
                {edge.label ? <span> · {edge.label}</span> : null}
              </div>
            ))}
          </div>
        </section>
      ) : null}
    </div>
  )
}

type TopologyNodeInput = {
  id: string
  label: string
  kind?: string
  namespace?: string
  name?: string
  status?: string
  x?: number
  y?: number
}

type TopologyEdgeInput = {
  id: string
  source: string
  target: string
  label?: string
  kind?: string
}

type TopologyGraph = {
  title: string
  nodes: Node[]
  edges: Edge[]
  kinds: string[]
  edgeSummaries: Array<{ key: string; source: string; target: string; label?: string }>
}

const kindOrder = ["Service", "EndpointSlice", "Pod", "Node", "Event"]

function topologyGraph(data: unknown): TopologyGraph {
  const payload = asRecord(data)
  const graph = asRecord(payload?.graph)
  const nodeInputs = (asArray(payload?.nodes) ?? asArray(graph?.nodes) ?? [])
    .map(topologyNodeInput)
    .filter((node): node is TopologyNodeInput => Boolean(node))
  const edgeInputs = (asArray(payload?.edges) ?? asArray(graph?.edges) ?? [])
    .map(topologyEdgeInput)
    .filter((edge): edge is TopologyEdgeInput => Boolean(edge))
  const positioned = layoutNodes(nodeInputs)
  const labelById = new Map(positioned.map((node) => [node.id, node.data.labelText]))
  return {
    title: textValue(payload?.title ?? graph?.title) ?? "Topology",
    nodes: positioned.map((node) => ({
      id: node.id,
      type: "default",
      position: { x: node.x, y: node.y },
      data: { label: nodeLabel(node), labelText: node.data.labelText },
      className: "border-border bg-card text-foreground shadow-sm",
      style: { borderRadius: 8, borderWidth: 1, width: 170 },
    })),
    edges: edgeInputs.map((edge) => ({
      id: edge.id,
      source: edge.source,
      target: edge.target,
      label: edge.label ?? edge.kind,
      markerEnd: { type: MarkerType.ArrowClosed },
      style: { stroke: "var(--primary)", strokeWidth: 1.5 },
      labelStyle: { fill: "var(--muted-foreground)", fontSize: 11 },
    })),
    kinds: unique(positioned.map((node) => node.kind).filter((kind): kind is string => Boolean(kind))),
    edgeSummaries: edgeInputs.map((edge) => ({
      key: edge.id,
      source: labelById.get(edge.source) ?? edge.source,
      target: labelById.get(edge.target) ?? edge.target,
      label: edge.label ?? edge.kind,
    })),
  }
}

function nodeLabel(node: ReturnType<typeof layoutNodes>[number]) {
  return (
    <div className="space-y-1 text-left">
      <div className="text-[0.65rem] font-medium uppercase text-muted-foreground">{node.kind ?? "Resource"}</div>
      <div className="truncate text-xs font-semibold text-foreground">{node.name ?? node.data.labelText}</div>
      {node.namespace ? <div className="truncate text-[0.65rem] text-muted-foreground">{node.namespace}</div> : null}
      {node.status ? <div className="text-[0.65rem] text-muted-foreground">{node.status}</div> : null}
    </div>
  )
}

function layoutNodes(nodes: TopologyNodeInput[]) {
  const counters = new Map<number, number>()
  return nodes.map((node, index) => {
    if (typeof node.x === "number" && typeof node.y === "number") {
      return { ...node, x: node.x, y: node.y, data: { labelText: node.label } }
    }
    const layer = node.kind ? kindLayer(node.kind) : Math.min(index, 4)
    const row = counters.get(layer) ?? 0
    counters.set(layer, row + 1)
    return {
      ...node,
      x: layer * 220,
      y: row * 105,
      data: { labelText: node.label },
    }
  })
}

function kindLayer(kind: string) {
  const index = kindOrder.indexOf(kind)
  return index >= 0 ? index : kindOrder.length
}

function topologyNodeInput(value: unknown): TopologyNodeInput | undefined {
  const node = asRecord(value)
  if (!node) return undefined
  const identity = asRecord(node.identity)
  const object = asRecord(node.object) ?? asRecord(node.resource)
  const metadata = asRecord(object?.metadata)
  const kind = textValue(identity?.kind ?? node.kind ?? object?.kind)
  const namespace = textValue(identity?.namespace ?? node.namespace ?? metadata?.namespace)
  const name = textValue(identity?.name ?? node.name ?? metadata?.name)
  const id = textValue(node.id ?? node.key) ?? [kind, namespace, name].filter(Boolean).join("/")
  if (!id) return undefined
  return {
    id,
    label: textValue(node.label) ?? [kind, namespace, name].filter(Boolean).join("/"),
    kind,
    namespace,
    name,
    status: textValue(node.phase ?? node.status) ?? textValue(asRecord(node.status)?.phase),
    x: numberValue(node.x),
    y: numberValue(node.y),
  }
}

function topologyEdgeInput(value: unknown): TopologyEdgeInput | undefined {
  const edge = asRecord(value)
  if (!edge) return undefined
  const source = textValue(edge.source ?? edge.sourceId ?? edge.from)
  const target = textValue(edge.target ?? edge.targetId ?? edge.to)
  if (!source || !target) return undefined
  return {
    id: textValue(edge.id ?? edge.key) ?? `${source}->${target}`,
    source,
    target,
    label: textValue(edge.label),
    kind: textValue(edge.kind ?? edge.type),
  }
}

function unique(values: string[]) {
  return Array.from(new Set(values))
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

function numberValue(value: unknown): number | undefined {
  return typeof value === "number" ? value : undefined
}
