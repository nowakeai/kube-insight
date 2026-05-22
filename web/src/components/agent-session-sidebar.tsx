import { MessageSquareText, Plus } from "lucide-react"

import { Button } from "@/components/ui/button"
import type { AgentRun, AgentSession } from "@/lib/agent-store"

export function SessionSidebar({
  activeSessionId,
  disabled,
  onNew,
  onSelect,
  runs,
  sessionOrder,
  sessions,
}: {
  activeSessionId?: string
  disabled: boolean
  onNew: () => void
  onSelect: (sessionId: string) => void
  runs: Record<string, AgentRun>
  sessionOrder: string[]
  sessions: Record<string, AgentSession>
}) {
  const visibleSessions = sessionOrder
    .map((sessionId) => sessions[sessionId])
    .filter((session): session is AgentSession => Boolean(session))

  return (
    <aside className="hidden min-h-0 border-r border-border/80 bg-card/60 lg:flex lg:flex-col" aria-label="Sessions">
      <div className="border-b border-border/80 p-4">
        <Button type="button" variant="outline" className="w-full justify-start" onClick={onNew} disabled={disabled}>
          <Plus className="size-4" aria-hidden="true" />
          New thread
        </Button>
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto p-3">
        {visibleSessions.length === 0 ? (
          <div className="rounded-md border border-dashed border-border bg-background px-3 py-6 text-sm text-muted-foreground">
            Sessions will appear here after the first run.
          </div>
        ) : (
          <div className="flex flex-col gap-1">
            {visibleSessions.map((session) => {
              const latestRunId = session.runIds.at(-1)
              const latestRun = latestRunId ? runs[latestRunId] : undefined
              const selected = session.id === activeSessionId
              return (
                <button
                  key={session.id}
                  type="button"
                  className={
                    selected
                      ? "rounded-md border border-border bg-background px-3 py-2 text-left shadow-sm"
                      : "rounded-md border border-transparent px-3 py-2 text-left text-muted-foreground transition hover:border-border hover:bg-background hover:text-foreground"
                  }
                  onClick={() => onSelect(session.id)}
                >
                  <div className="flex items-center gap-2 text-sm font-medium">
                    <MessageSquareText className="size-3.5 shrink-0" aria-hidden="true" />
                    <span className="truncate">{session.title || "New investigation"}</span>
                  </div>
                  <div className="mt-1 flex items-center justify-between gap-2 text-[0.7rem] text-muted-foreground">
                    <span>{latestRun ? runStatusLabel(latestRun.status) : "No runs"}</span>
                    <span>{session.runIds.length} run{session.runIds.length === 1 ? "" : "s"}</span>
                  </div>
                </button>
              )
            })}
          </div>
        )}
      </div>
    </aside>
  )
}

function runStatusLabel(status: string) {
  if (status === "queued") return "queued"
  if (status === "running") return "running"
  if (status === "completed") return "completed"
  if (status === "failed") return "failed"
  if (status === "cancelled") return "cancelled"
  if (status === "started") return "started"
  return status
}
