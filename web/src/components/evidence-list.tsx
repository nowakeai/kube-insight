import { Braces, ChevronDown, FileText, Pin, Table2 } from "lucide-react"
import { useMemo, useState } from "react"

import { MarkdownContent } from "@/components/markdown-content"
import { Button } from "@/components/ui/button"
import type { EvidenceSegment } from "@/components/agent-chat-stream-model"
import type { AgentArtifact } from "@/lib/agent-store"

type EvidenceListProps = {
  artifactsById: Record<string, AgentArtifact>
  citations: EvidenceSegment[]
  onSelectArtifact?: (artifactId?: string) => void
}

type EvidenceView = "summary" | "table" | "markdown" | "json"

type EvidenceItem = {
  artifact?: AgentArtifact
  citation: EvidenceSegment
  detailLines: string[]
  jsonValue: unknown
  markdown?: string
  summary: string
  tableRows: Record<string, unknown>[]
  title: string
}

export function EvidenceList({ artifactsById, citations, onSelectArtifact }: EvidenceListProps) {
  const [expanded, setExpanded] = useState(false)
  const items = useMemo(
    () => citations.map((citation, index) => evidenceItem(citation, artifactsById[citation.artifactId ?? ""], index)),
    [artifactsById, citations],
  )
  if (items.length === 0) return null

  return (
    <div className="mt-4 rounded-md border border-border bg-card text-sm">
      <button
        type="button"
        className="flex w-full items-center gap-2 px-3 py-2 text-left"
        onClick={() => setExpanded((value) => !value)}
        aria-expanded={expanded}
      >
        <FileText className="size-3.5 text-muted-foreground" aria-hidden="true" />
        <span className="font-medium text-foreground">Evidence</span>
        <span className="rounded-md bg-muted px-2 py-0.5 text-xs tabular-nums text-muted-foreground">{items.length}</span>
        <span className="min-w-0 truncate text-xs text-muted-foreground">{items.map((item) => item.title).slice(0, 2).join(" · ")}</span>
        <ChevronDown className={expanded ? "ml-auto size-3.5 rotate-180 text-muted-foreground transition" : "ml-auto size-3.5 text-muted-foreground transition"} aria-hidden="true" />
      </button>
      {expanded ? (
        <div className="space-y-2 border-t border-border p-2">
          {items.map((item) => (
            <EvidenceCard key={item.citation.id} item={item} onSelectArtifact={onSelectArtifact} />
          ))}
        </div>
      ) : null}
    </div>
  )
}

function EvidenceCard({ item, onSelectArtifact }: { item: EvidenceItem; onSelectArtifact?: (artifactId?: string) => void }) {
  const [expanded, setExpanded] = useState(false)
  const [view, setView] = useState<EvidenceView>(item.tableRows.length > 0 ? "table" : "summary")
  const views: EvidenceView[] = ["summary", ...(item.tableRows.length > 0 ? ["table" as const] : []), ...(item.markdown ? ["markdown" as const] : []), "json"]
  const canPin = Boolean(item.artifact?.id && onSelectArtifact)

  return (
    <div className="rounded-md border border-border bg-background">
      <div className="flex items-start gap-2 px-3 py-2">
        <button type="button" className="min-w-0 flex-1 text-left" onClick={() => setExpanded((value) => !value)} aria-expanded={expanded}>
          <div className="flex min-w-0 items-center gap-2">
            <ChevronDown className={expanded ? "size-3.5 shrink-0 rotate-180 text-muted-foreground transition" : "size-3.5 shrink-0 text-muted-foreground transition"} aria-hidden="true" />
            <span className="truncate font-medium text-foreground">{item.title}</span>
            {item.artifact?.kind ? <span className="shrink-0 rounded-md bg-muted px-1.5 py-0.5 text-[0.68rem] text-muted-foreground">{item.artifact.kind}</span> : null}
          </div>
          <div className="mt-1 line-clamp-2 pl-5 text-xs leading-5 text-muted-foreground">{item.summary}</div>
        </button>
        <Button
          type="button"
          size="icon-sm"
          variant="ghost"
          className="mt-0.5 shrink-0"
          disabled={!canPin}
          title={canPin ? "Pin evidence to dock" : "No panel artifact available"}
          aria-label="Pin evidence to dock"
          onClick={() => item.artifact?.id ? onSelectArtifact?.(item.artifact.id) : undefined}
        >
          <Pin className="size-3.5" aria-hidden="true" />
        </Button>
      </div>
      {expanded ? (
        <div className="border-t border-border px-3 py-3">
          <div className="mb-3 flex flex-wrap items-center gap-1">
            {views.map((candidate) => (
              <Button key={candidate} type="button" size="sm" variant={view === candidate ? "secondary" : "ghost"} onClick={() => setView(candidate)}>
                {viewIcon(candidate)}
                {viewLabel(candidate)}
              </Button>
            ))}
          </div>
          {view === "summary" ? <EvidenceSummary item={item} /> : null}
          {view === "table" ? <EvidenceTable rows={item.tableRows} /> : null}
          {view === "markdown" && item.markdown ? <MarkdownContent text={item.markdown} /> : null}
          {view === "json" ? <EvidenceJSON value={item.jsonValue} /> : null}
        </div>
      ) : null}
    </div>
  )
}

function EvidenceSummary({ item }: { item: EvidenceItem }) {
  return (
    <div className="space-y-2 text-sm">
      <p className="text-muted-foreground">{item.summary}</p>
      {item.detailLines.length > 0 ? (
        <dl className="grid gap-2 sm:grid-cols-2">
          {item.detailLines.slice(0, 8).map((line) => {
            const [key, ...rest] = line.split(": ")
            return (
              <div key={line} className="min-w-0 rounded-md bg-muted px-2.5 py-2">
                <dt className="truncate text-[0.68rem] uppercase text-muted-foreground">{key}</dt>
                <dd className="mt-0.5 truncate text-xs text-foreground">{rest.join(": ") || line}</dd>
              </div>
            )
          })}
        </dl>
      ) : null}
    </div>
  )
}

function EvidenceTable({ rows }: { rows: Record<string, unknown>[] }) {
  if (rows.length === 0) return <p className="text-sm text-muted-foreground">No tabular rows available for this evidence.</p>
  const columns = tableColumns(rows).slice(0, 6)
  return (
    <div className="max-h-80 overflow-auto rounded-md border border-border">
      <table className="w-full min-w-[32rem] text-left text-xs">
        <thead className="sticky top-0 bg-muted text-muted-foreground">
          <tr>{columns.map((column) => <th key={column} className="px-2 py-2 font-medium">{column}</th>)}</tr>
        </thead>
        <tbody>
          {rows.slice(0, 25).map((row, index) => (
            <tr key={index} className="border-t border-border">
              {columns.map((column) => <td key={column} className="max-w-52 truncate px-2 py-2 text-foreground">{compactValue(row[column])}</td>)}
            </tr>
          ))}
        </tbody>
      </table>
      {rows.length > 25 ? <div className="border-t border-border px-2 py-1 text-xs text-muted-foreground">Showing 25 of {rows.length} rows.</div> : null}
    </div>
  )
}

function EvidenceJSON({ value }: { value: unknown }) {
  return <pre className="max-h-96 overflow-auto rounded-md border border-border bg-muted p-3 text-xs leading-5 text-muted-foreground">{JSON.stringify(value, null, 2)}</pre>
}

function evidenceItem(citation: EvidenceSegment, artifact: AgentArtifact | undefined, index: number): EvidenceItem {
  const data = asRecord(artifact?.data)
  const markdown = markdownText(data)
  const parsedMarkdown = parseMarkdownJSON(markdown)
  const jsonValue = parsedMarkdown ?? stripCitation(data) ?? citation.target ?? {}
  const rows = evidenceRows(jsonValue, data)
  const title = readableTitle(citation, artifact, index, rows, jsonValue)
  const detailLines = evidenceDetails(jsonValue, data, rows)
  return {
    artifact,
    citation,
    detailLines,
    jsonValue,
    markdown,
    summary: readableSummary(artifact, rows, jsonValue, detailLines),
    tableRows: rows,
    title,
  }
}

function readableTitle(citation: EvidenceSegment, artifact: AgentArtifact | undefined, index: number, rows: Record<string, unknown>[], value: unknown) {
  const base = citation.text || artifact?.title || `Evidence ${index + 1}`
  if (!/^SQL evidence/i.test(base)) return base
  const columns = tableColumns(rows).slice(0, 4).join(", ")
  const query = textField(asRecord(value), "query") || textField(asRecord(value), "sql")
  if (query) return `SQL: ${truncate(query, 80)}`
  if (columns) return `SQL rows: ${columns}`
  return base
}

function readableSummary(artifact: AgentArtifact | undefined, rows: Record<string, unknown>[], value: unknown, details: string[]) {
  if (rows.length > 0) {
    const sample = rows.slice(0, 2).map((row) => rowLabel(row)).filter(Boolean).join("; ")
    const columns = tableColumns(rows).slice(0, 5).join(", ")
    return `${rows.length} row${rows.length === 1 ? "" : "s"}${columns ? ` with ${columns}` : ""}${sample ? `. Sample: ${sample}` : ""}`
  }
  if (artifact?.kind === "k8s.topology") return details.join("; ") || "Topology evidence with Kubernetes nodes and edges."
  if (artifact?.kind === "k8s.history") return details.join("; ") || "History evidence with observed resource versions."
  if (details.length > 0) return details.slice(0, 3).join("; ")
  if (typeof value === "string") return truncate(value, 160)
  return "Structured evidence captured from the agent tool output."
}

function evidenceRows(value: unknown, data: Record<string, unknown> | undefined): Record<string, unknown>[] {
  const record = asRecord(value)
  const dataRecord = asRecord(data)
  const candidates = [record?.rows, record?.items, record?.versions, record?.nodes, dataRecord?.items, dataRecord?.versions, dataRecord?.nodes]
  for (const candidate of candidates) {
    const rows = recordArray(candidate)
    if (rows.length > 0) return rows
  }
  return []
}

function evidenceDetails(value: unknown, data: Record<string, unknown> | undefined, rows: Record<string, unknown>[]) {
  const record = asRecord(value)
  const dataRecord = asRecord(data)
  const lines: string[] = []
  const add = (key: string, value: unknown) => {
    const text = compactValue(value)
    if (text) lines.push(`${key}: ${text}`)
  }
  add("Rows", rows.length || record?.rowCount)
  add("Object", identityLabel(asRecord(record?.object) ?? asRecord(dataRecord?.identity)))
  add("Nodes", recordArray(record?.nodes ?? dataRecord?.nodes).length || undefined)
  add("Edges", recordArray(record?.edges ?? dataRecord?.edges).length || undefined)
  add("Versions", recordArray(record?.versions ?? dataRecord?.versions).length || undefined)
  add("Source", textField(asRecord(asRecord(record?.citation ?? dataRecord?.citation)?.target), "source"))
  add("Columns", tableColumns(rows).slice(0, 8).join(", "))
  return lines.filter((line, index, all) => all.indexOf(line) === index)
}

function tableColumns(rows: Record<string, unknown>[]) {
  const columns: string[] = []
  for (const row of rows) {
    for (const key of Object.keys(row)) {
      if (!columns.includes(key)) columns.push(key)
    }
  }
  return columns
}

function rowLabel(row: Record<string, unknown>) {
  return identityLabel(asRecord(row.identity) ?? row) || tableColumns([row]).slice(0, 3).map((key) => `${key}=${compactValue(row[key])}`).join(", ")
}

function identityLabel(record: Record<string, unknown> | undefined) {
  if (!record) return ""
  const parts = [textField(record, "kind"), textField(record, "namespace"), textField(record, "name")].filter(Boolean)
  if (parts.length > 0) return parts.join("/")
  return textField(record, "resource") || textField(record, "uid")
}

function markdownText(record: Record<string, unknown> | undefined) {
  return typeof record?.markdown === "string" ? record.markdown : undefined
}

function parseMarkdownJSON(markdown: string | undefined) {
  if (!markdown) return undefined
  const match = markdown.match(/```json\s*([\s\S]*?)```/i)
  if (!match) return undefined
  try {
    return JSON.parse(match[1])
  } catch {
    return undefined
  }
}

function stripCitation(record: Record<string, unknown> | undefined) {
  if (!record) return undefined
  const rest = { ...record }
  delete rest.citation
  return rest
}

function recordArray(value: unknown): Record<string, unknown>[] {
  return Array.isArray(value) ? value.map(asRecord).filter((item): item is Record<string, unknown> => Boolean(item)) : []
}

function asRecord(value: unknown): Record<string, unknown> | undefined {
  return value && typeof value === "object" && !Array.isArray(value) ? value as Record<string, unknown> : undefined
}

function textField(record: Record<string, unknown> | undefined, key: string) {
  const value = record?.[key]
  return typeof value === "string" ? value : ""
}

function compactValue(value: unknown) {
  if (value === undefined || value === null || value === "") return ""
  if (typeof value === "string") return truncate(value, 96)
  if (typeof value === "number" || typeof value === "boolean") return String(value)
  return truncate(JSON.stringify(value), 96)
}

function truncate(value: string, max: number) {
  return value.length > max ? `${value.slice(0, max - 3)}...` : value
}

function viewLabel(view: EvidenceView) {
  if (view === "summary") return "Summary"
  if (view === "table") return "Table"
  if (view === "markdown") return "Markdown"
  return "JSON"
}

function viewIcon(view: EvidenceView) {
  if (view === "table") return <Table2 className="size-3.5" aria-hidden="true" />
  if (view === "json") return <Braces className="size-3.5" aria-hidden="true" />
  return <FileText className="size-3.5" aria-hidden="true" />
}
