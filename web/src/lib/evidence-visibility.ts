import type { EvidenceSegment } from "@/components/agent-chat-stream-model"
import type { AgentArtifact } from "@/lib/agent-store"

export function isVisibleEvidenceCitation(citation: EvidenceSegment, artifact: AgentArtifact | undefined) {
  const data = asRecord(artifact?.data)
  if (!artifact) return hasUsefulTarget(citation.target)
  const markdown = markdownText(data)
  const parsedMarkdown = parseMarkdownJSON(markdown)
  const jsonValue = parsedMarkdown ?? stripCitation(data) ?? citation.target ?? {}
  const rows = evidenceRows(jsonValue, data)
  if (rows.length > 0) return true
  const rowCount = numericField(asRecord(jsonValue), "rowCount") ?? numericField(asRecord(citation.target), "rowCount")
  if (rowCount === 0) return false
  if (markdown && isEmptyEvidenceMarkdown(markdown)) return false
  return hasUsefulValue(jsonValue)
}

function isEmptyEvidenceMarkdown(markdown: string) {
  const normalized = markdown.replace(/\s+/g, " ").trim().toLowerCase()
  return normalized === "" || normalized.includes("no rows returned") || normalized.includes("0 rows")
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

function hasUsefulTarget(value: unknown) {
  const record = asRecord(value)
  if (!record) return false
  const rowCount = numericField(record, "rowCount")
  if (rowCount === 0) return false
  return Object.keys(record).some((key) => !["type", "source", "rowCount"].includes(key) && compactValue(record[key]) !== "")
}

function hasUsefulValue(value: unknown) {
  const record = asRecord(value)
  if (!record) return Boolean(compactValue(value))
  const keys = Object.keys(record).filter((key) => key !== "citation")
  if (keys.length === 0) return false
  if (numericField(record, "rowCount") === 0 && recordArray(record.rows).length === 0) return false
  return keys.some((key) => {
    const item = record[key]
    if (Array.isArray(item)) return item.length > 0
    return compactValue(item) !== ""
  })
}

function recordArray(value: unknown): Record<string, unknown>[] {
  return Array.isArray(value) ? value.map(asRecord).filter((item): item is Record<string, unknown> => Boolean(item)) : []
}

function asRecord(value: unknown): Record<string, unknown> | undefined {
  return value && typeof value === "object" && !Array.isArray(value) ? value as Record<string, unknown> : undefined
}

function numericField(record: Record<string, unknown> | undefined, key: string) {
  const value = record?.[key]
  const numberValue = typeof value === "number" ? value : typeof value === "string" && value.trim() !== "" ? Number(value) : NaN
  return Number.isFinite(numberValue) ? numberValue : undefined
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
