import type { ReactNode } from "react"
import ReactMarkdown from "react-markdown"
import remarkGfm from "remark-gfm"

import { useAgentProjectionStore } from "@/lib/agent-store"

export function MarkdownContent({ text }: { text: string }) {
  return (
    <ReactMarkdown
      remarkPlugins={[remarkGfm]}
      components={{
        h1: ({ children }) => <h1 className="mb-3 text-xl font-semibold leading-7 last:mb-0">{children}</h1>,
        h2: ({ children }) => <h2 className="mb-3 mt-5 text-lg font-semibold leading-7 first:mt-0 last:mb-0">{children}</h2>,
        h3: ({ children }) => <h3 className="mb-2 mt-4 text-base font-semibold leading-6 first:mt-0 last:mb-0">{children}</h3>,
        p: ({ children }) => <p className="mb-3 leading-6 last:mb-0">{children}</p>,
        a: ({ children, href }) => {
          if (href?.startsWith("#citation:")) {
            return <CitationJump href={href}>{children}</CitationJump>
          }
          return (
            <a className="font-medium text-primary underline underline-offset-4" href={href} rel="noreferrer" target="_blank">
              {children}
            </a>
          )
        },
        blockquote: ({ children }) => (
          <blockquote className="mb-3 border-l-2 border-border pl-3 text-muted-foreground last:mb-0">
            {children}
          </blockquote>
        ),
        ul: ({ children }) => <ul className="mb-3 list-disc space-y-1 pl-5 last:mb-0">{children}</ul>,
        ol: ({ children }) => <ol className="mb-3 list-decimal space-y-1 pl-5 last:mb-0">{children}</ol>,
        li: ({ children }) => <li className="leading-6">{children}</li>,
        table: ({ children }) => (
          <div className="mb-3 max-w-full overflow-x-auto rounded-md border border-border last:mb-0">
            <table className="w-full min-w-[32rem] border-collapse text-left text-xs">{children}</table>
          </div>
        ),
        thead: ({ children }) => <thead className="bg-muted text-muted-foreground">{children}</thead>,
        th: ({ children }) => <th className="border-b border-border px-3 py-2 font-medium">{children}</th>,
        td: ({ children }) => <td className="border-t border-border px-3 py-2 align-top">{children}</td>,
        hr: () => <hr className="my-4 border-border" />,
        pre: ({ children }) => (
          <pre className="mb-3 max-w-full overflow-x-auto rounded-md border border-border bg-muted p-3 last:mb-0">
            {children}
          </pre>
        ),
        code: ({ children, className }) => {
          const isBlock = className?.startsWith("language-")
          return (
            <code
              className={
                isBlock
                  ? "font-mono text-xs leading-5 text-foreground"
                  : "rounded bg-muted px-1.5 py-0.5 font-mono text-[0.85em] text-foreground"
              }
            >
              {children}
            </code>
          )
        },
      }}
    >
      {text}
    </ReactMarkdown>
  )
}

function CitationJump({ href, children }: { href: string; children: ReactNode }) {
  const citationId = decodeURIComponent(href.replace("#citation:", ""))
  const citations = useAgentProjectionStore((state) => state.citations)
  const artifacts = useAgentProjectionStore((state) => state.artifacts)
  const selectArtifact = useAgentProjectionStore((state) => state.selectArtifact)
  const citation = citations[citationId]
  const artifact = citation?.artifactId ? artifacts[citation.artifactId] : undefined
  const marker = citationMarker(children)
  const childText = reactText(children).trim()
  const label = citationLabel(/^E\d+$/i.test(childText) ? citation?.text || artifact?.title || childText : childText || citation?.text || artifact?.title || "Evidence")
  const onClick = () => {
    if (citation?.artifactId) selectArtifact(citation.artifactId)
    window.dispatchEvent(new CustomEvent("kube-insight:evidence-jump", { detail: { citationId } }))
    const target = document.getElementById(`evidence-${citationId}`) ?? document.getElementById("evidence")
    target?.scrollIntoView({ behavior: "smooth", block: "start" })
  }
  return (
    <button
      type="button"
      className="mx-1 inline-flex max-w-[14rem] items-center rounded-full bg-muted px-2.5 py-1 align-[0.08em] text-[0.78em] font-medium leading-none text-muted-foreground shadow-none ring-1 ring-border/40 transition hover:bg-muted/80 hover:text-foreground hover:ring-border focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
      onClick={onClick}
      title={marker ? `${marker}: ${label}` : `Evidence: ${label}`}
      aria-label={marker ? `Jump to evidence ${marker}: ${label}` : `Jump to evidence: ${label}`}
    >
      <span className="truncate">{label}</span>
    </button>
  )
}

function citationMarker(children: ReactNode) {
  const text = reactText(children).trim()
  return /^E\d+$/i.test(text) ? text.toUpperCase() : ""
}

function citationLabel(value: string) {
  const cleaned = value.replace(/\s+/g, " ").trim()
  if (!cleaned) return "Evidence"
  return cleaned.length > 28 ? `${cleaned.slice(0, 25).trimEnd()}...` : cleaned
}

function reactText(node: ReactNode): string {
  if (typeof node === "string" || typeof node === "number") return String(node)
  if (Array.isArray(node)) return node.map(reactText).join("")
  return ""
}
