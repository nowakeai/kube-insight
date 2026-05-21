import { FileText } from "lucide-react"

import { K8sDiffArtifact } from "@/components/k8s-diff-artifact"
import { K8sHistoryArtifact } from "@/components/k8s-history-artifact"
import { K8sResourceArtifact } from "@/components/k8s-resource-artifact"
import { K8sResourceListArtifact } from "@/components/k8s-resource-list-artifact"
import { K8sTopologyArtifact } from "@/components/k8s-topology-artifact"
import { MarkdownContent } from "@/components/markdown-content"
import { Button } from "@/components/ui/button"
import { type AgentArtifact, useAgentProjectionStore } from "@/lib/agent-store"

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
    return <K8sTopologyArtifact data={artifact.data} />
  }
  if (artifact.kind === "k8s.history") {
    return <K8sHistoryArtifact data={artifact.data} />
  }
  if (artifact.kind === "k8s.diff") {
    return <K8sDiffArtifact data={artifact.data} />
  }
  return (
    <div className="rounded-md border border-dashed border-border bg-background px-3 py-6 text-center text-sm text-muted-foreground">
      Renderer for {artifact.kind} is not implemented yet.
    </div>
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
