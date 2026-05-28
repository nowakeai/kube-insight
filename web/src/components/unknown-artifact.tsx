import { FileQuestion } from "lucide-react"

import { ProofViewer } from "@/components/proof-viewer"

export function UnknownArtifact({ kind, data }: { kind: string; data: unknown }) {
  return (
    <div className="space-y-3">
      <div className="rounded-md border border-dashed border-border bg-background px-3 py-3">
        <div className="flex items-center gap-2 text-sm font-medium text-foreground">
          <FileQuestion className="size-4 text-muted-foreground" aria-hidden="true" />
          Renderer not implemented
        </div>
        <p className="mt-1 text-xs text-muted-foreground">
          This artifact kind is not specialized yet. Raw payload is shown for inspection.
        </p>
        <div className="mt-2 rounded-md bg-muted px-2 py-1 font-mono text-xs text-muted-foreground">{kind}</div>
      </div>
      {data !== undefined ? <ProofViewer value={data} language={typeof data === "string" ? "yaml" : "json"} /> : null}
    </div>
  )
}
