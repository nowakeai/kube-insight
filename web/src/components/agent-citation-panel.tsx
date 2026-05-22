import { ExternalLink } from "lucide-react"

import { useAgentProjectionStore, type AgentRunEvent } from "@/lib/agent-store"

export function CitationPanel({
  events,
  selectedArtifactId,
}: {
  events: AgentRunEvent[]
  selectedArtifactId?: string
}) {
  const citations = events
    .filter((event) => event.type === "citation.created")
    .map((event) => citationEventData(event.data))
    .filter((citation): citation is AgentCitationView => Boolean(citation?.id))
  if (citations.length === 0) return null
  return (
    <div id="evidence" className="mb-4 rounded-md border border-border bg-background p-3">
      <div className="mb-2 text-xs font-medium text-muted-foreground">Evidence</div>
      <div className="flex flex-wrap gap-2">
        {citations.map((citation) => (
          <CitationChip
            key={citation.id}
            citation={citation}
            selected={Boolean(citation.artifactId && citation.artifactId === selectedArtifactId)}
          />
        ))}
      </div>
    </div>
  )
}

function CitationChip({ citation, selected }: { citation: AgentCitationView; selected: boolean }) {
  const selectArtifact = useAgentProjectionStore((state) => state.selectArtifact)
  const onClick = () => {
    if (citation.artifactId) selectArtifact(citation.artifactId)
    document.getElementById("evidence")?.scrollIntoView({ behavior: "smooth", block: "start" })
  }
  return (
    <button
      type="button"
      className={
        selected
          ? "inline-flex min-h-11 items-center gap-1 rounded-md border border-primary bg-muted px-3 py-2 text-left text-xs text-foreground"
          : "inline-flex min-h-11 items-center gap-1 rounded-md border border-border bg-card px-3 py-2 text-left text-xs text-muted-foreground transition hover:border-primary/40 hover:text-foreground"
      }
      onClick={onClick}
    >
      <ExternalLink className="size-3" aria-hidden="true" />
      <span>{citation.text || citation.id}</span>
    </button>
  )
}

type AgentCitationView = {
  id: string
  artifactId?: string
  text?: string
  target?: unknown
}

function citationEventData(value: unknown): AgentCitationView | undefined {
  if (!value || typeof value !== "object") return undefined
  const wrappedCitation = (value as { citation?: unknown }).citation
  const citation = wrappedCitation && typeof wrappedCitation === "object" ? wrappedCitation : value
  if (!hasCitationId(citation)) return undefined
  return citation
}

function hasCitationId(value: unknown): value is AgentCitationView {
  return Boolean(value && typeof value === "object" && typeof (value as { id?: unknown }).id === "string")
}
