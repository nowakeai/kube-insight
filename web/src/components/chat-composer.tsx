import { ComposerPrimitive } from "@assistant-ui/react"
import { ArrowRight, CircleStop, Search, X } from "lucide-react"
import { useEffect, useMemo, useState, type ClipboardEvent, type ReactNode } from "react"

import { Button } from "@/components/ui/button"
import type { ComposerContextBlock } from "@/lib/composer-context"

const composerPlaceholders = [
  "Ask about a Service, Pod, namespace, topology, or recent change",
  "Find restart, OOM, scheduling, or readiness evidence",
  "Compare the last hour with the current cluster state",
  "Trace a Service to EndpointSlices, Pods, Nodes, and Events",
]

const longPasteMinChars = 900
const longPasteMinLines = 10

export function ChatComposer({
  autoFocus = false,
  contextBlocks = [],
  isRunning = false,
  onCancel,
  onContextPaste,
  onRemoveContextBlock,
  status,
  variant = "thread",
}: {
  autoFocus?: boolean
  contextBlocks?: ComposerContextBlock[]
  isRunning?: boolean
  onCancel?: () => void
  onContextPaste?: (text: string) => void
  onRemoveContextBlock?: (id: string) => void
  status?: ReactNode
  variant?: "home" | "thread"
}) {
  const isHome = variant === "home"
  const [placeholderIndex, setPlaceholderIndex] = useState(0)
  const placeholder = composerPlaceholders[placeholderIndex % composerPlaceholders.length]

  useEffect(() => {
    const intervalId = window.setInterval(() => {
      setPlaceholderIndex((current) => current + 1)
    }, 4200)
    return () => window.clearInterval(intervalId)
  }, [])

  const handlePaste = (event: ClipboardEvent<HTMLTextAreaElement>) => {
    const text = event.clipboardData.getData("text")
    if (!isLongContextPaste(text)) return
    event.preventDefault()
    onContextPaste?.(text)
  }

  const inputRow = (
    <ComposerPrimitive.Root
      className={
        isHome
          ? "flex min-h-16 w-full items-end gap-2 rounded-lg border border-border bg-card p-2 shadow-md shadow-muted/40"
          : "flex min-h-14 w-full items-end gap-2 bg-card p-2"
      }
    >
      {isHome ? <Search className="mb-3 ml-2 size-5 shrink-0 text-muted-foreground" aria-hidden="true" /> : null}
      <ComposerPrimitive.Input
        autoFocus={autoFocus}
        rows={1}
        submitMode="enter"
        placeholder={placeholder}
        onPaste={handlePaste}
        className={
          isHome
            ? "max-h-44 min-h-12 flex-1 resize-none bg-transparent px-2 py-3 text-base leading-6 outline-none placeholder:text-muted-foreground"
            : "max-h-44 min-h-11 flex-1 resize-none bg-transparent px-2 py-2 text-sm leading-6 outline-none placeholder:text-muted-foreground"
        }
      />
      {isRunning ? (
        <Button type="button" size="icon" variant="secondary" aria-label="Stop run" className={isHome ? "size-11" : undefined} onClick={onCancel}>
          <CircleStop className="size-4" aria-hidden="true" />
        </Button>
      ) : (
        <ComposerPrimitive.Send asChild>
          <Button type="submit" size="icon" aria-label="Send message" className={isHome ? "size-11" : undefined}>
            <ArrowRight className="size-4" aria-hidden="true" />
          </Button>
        </ComposerPrimitive.Send>
      )}
    </ComposerPrimitive.Root>
  )

  const contextStrip = useMemo(() => (
    <ComposerContextStrip blocks={contextBlocks} onRemove={onRemoveContextBlock} />
  ), [contextBlocks, onRemoveContextBlock])

  if (isHome) {
    return (
      <div className="space-y-2">
        {contextStrip}
        {inputRow}
      </div>
    )
  }
  return (
    <div className="overflow-hidden rounded-lg border border-border bg-card shadow-sm shadow-muted/30">
      {status}
      {contextStrip}
      {inputRow}
    </div>
  )
}

function ComposerContextStrip({
  blocks,
  onRemove,
}: {
  blocks: ComposerContextBlock[]
  onRemove?: (id: string) => void
}) {
  if (blocks.length === 0) return null
  return (
    <div className="flex flex-wrap gap-1.5 border-b border-border bg-muted/30 px-2 py-2">
      {blocks.map((block) => (
        <span key={block.id} className="inline-flex max-w-full items-center gap-1.5 rounded-md border border-border bg-background px-2 py-1 text-xs text-muted-foreground">
          <span className="truncate">{block.title}</span>
          <span className="shrink-0 tabular-nums">{formatCharacterCount(block.text.length)}</span>
          {onRemove ? (
            <button type="button" className="rounded-sm p-0.5 text-muted-foreground hover:bg-muted hover:text-foreground" onClick={() => onRemove(block.id)} aria-label={`Remove ${block.title}`}>
              <X className="size-3" aria-hidden="true" />
            </button>
          ) : null}
        </span>
      ))}
    </div>
  )
}

function isLongContextPaste(text: string) {
  if (!text.trim()) return false
  return text.length >= longPasteMinChars || text.split(/\r?\n/).length >= longPasteMinLines
}

function formatCharacterCount(count: number) {
  if (count >= 1000) return `${(count / 1000).toFixed(count >= 10000 ? 0 : 1)}k chars`
  return `${count} chars`
}
