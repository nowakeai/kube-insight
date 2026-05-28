import { expect, test } from "@playwright/test"

import type { EvidenceSegment } from "@/components/agent-chat-stream-model"
import type { AgentArtifact } from "@/lib/agent-store"
import { isVisibleEvidenceCitation } from "@/lib/evidence-visibility"

test("isVisibleEvidenceCitation hides zero-row SQL evidence cards", () => {
  expect(isVisibleEvidenceCitation(
    citation("citation_empty", "artifact_empty", "SQL evidence (0 rows)", { type: "sql_row", source: "kube_insight_sql", rowCount: 0 }),
    markdownArtifact("artifact_empty", "SQL evidence (0 rows)", "### SQL evidence (0 rows)\n\nNo rows returned."),
  )).toBe(false)

  expect(isVisibleEvidenceCitation(
    citation("citation_rows", "artifact_rows", "SQL evidence (2 rows)", { type: "sql_row", source: "kube_insight_sql", rowCount: 2 }),
    markdownArtifact("artifact_rows", "SQL evidence (2 rows)", [
      "### SQL evidence (2 rows)",
      "",
      "```json",
      JSON.stringify({ rowCount: 2, rows: [{ name: "node-a" }, { name: "node-b" }] }),
      "```",
    ].join("\n")),
  )).toBe(true)
})

function citation(id: string, artifactId: string, text: string, target: unknown): EvidenceSegment {
  return { type: "evidence", id, artifactId, text, target }
}

function markdownArtifact(id: string, title: string, markdown: string): AgentArtifact {
  return {
    id,
    runId: "run_1",
    kind: "markdown",
    title,
    data: { markdown },
    createdAt: "2026-05-27T00:00:00Z",
    updatedAt: "2026-05-27T00:00:00Z",
  }
}
