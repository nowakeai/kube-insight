import { ExternalLink } from "lucide-react"
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
  const selectArtifact = useAgentProjectionStore((state) => state.selectArtifact)
  const onClick = () => {
    const citation = citations[citationId]
    if (citation?.artifactId) selectArtifact(citation.artifactId)
    document.getElementById("evidence")?.scrollIntoView({ behavior: "smooth", block: "start" })
  }
  return (
    <button
      type="button"
      className="inline-flex min-h-11 items-center gap-1 rounded-md border border-border bg-muted px-3 py-2 text-[0.85em] font-medium text-foreground transition hover:border-primary/40"
      onClick={onClick}
    >
      {children}
      <ExternalLink className="size-3" aria-hidden="true" />
    </button>
  )
}
