import {
  AssistantRuntimeProvider,
  ComposerPrimitive,
  MessagePrimitive,
  ThreadPrimitive,
  useExternalStoreRuntime,
  type AppendMessage,
  type ThreadMessage,
} from "@assistant-ui/react"
import { ArrowUp, Bot, CircleStop, Server, Sparkles, UserRound } from "lucide-react"
import { useCallback, useState } from "react"
import ReactMarkdown from "react-markdown"
import remarkGfm from "remark-gfm"

import { Button } from "@/components/ui/button"

const starterPrompts = [
  "Is the API service healthy right now?",
  "Show recent changes for default/api",
  "Find pods with restart evidence",
]

export function AgentChat() {
  const [messages, setMessages] = useState<ThreadMessage[]>([])
  const [isRunning, setIsRunning] = useState(false)

  const onNew = useCallback(async (message: AppendMessage) => {
    const text = appendMessageText(message)
    if (!text) return

    const assistantID = newMessageID("assistant")
    setIsRunning(true)
    setMessages((current) => [
      ...current,
      userMessage(text),
      assistantMessage(assistantID, "", "running"),
    ])

    await delay(350)

    setMessages((current) =>
      current.map((item) =>
        item.id === assistantID
          ? assistantMessage(
              assistantID,
              demoAgentAnswer(text),
              "complete",
            )
          : item,
      ),
    )
    setIsRunning(false)
  }, [])

  const onCancel = useCallback(async () => {
    setIsRunning(false)
    setMessages((current) =>
      current.map((item) =>
        item.role === "assistant" && item.status?.type === "running"
          ? assistantMessage(item.id, "Run cancelled.", "cancelled")
          : item,
      ),
    )
  }, [])

  const runtime = useExternalStoreRuntime({
    messages,
    isRunning,
    onNew,
    onCancel,
    unstable_capabilities: { copy: true },
  })

  return (
    <AssistantRuntimeProvider runtime={runtime}>
      <main className="min-h-svh bg-background text-foreground">
        <div className="mx-auto flex min-h-svh w-full max-w-5xl flex-col px-4 py-4 sm:px-6 lg:px-8">
          <header className="flex h-12 items-center justify-between border-b border-border/80">
            <div className="flex items-center gap-2 text-sm font-medium">
              <span className="flex size-7 items-center justify-center rounded-md bg-primary text-primary-foreground">
                <Sparkles className="size-4" aria-hidden="true" />
              </span>
              <span>kube-insight</span>
            </div>
            <div className="flex items-center gap-2 text-xs text-muted-foreground">
              <Server className="size-4" aria-hidden="true" />
              <span>local agent</span>
            </div>
          </header>

          <ThreadPrimitive.Root className="flex min-h-0 flex-1 flex-col">
            <ThreadPrimitive.Viewport className="flex min-h-0 flex-1 flex-col overflow-y-auto scroll-smooth py-6">
              <ThreadPrimitive.Empty>
                <div className="mx-auto flex w-full max-w-3xl flex-1 flex-col justify-center gap-6 pb-12 pt-8">
                  <div className="space-y-3">
                    <h1 className="text-3xl font-semibold tracking-normal text-foreground sm:text-4xl">
                      Ask kube-insight
                    </h1>
                    <p className="max-w-2xl text-sm leading-6 text-muted-foreground sm:text-base">
                      Query Kubernetes state, history, topology, and evidence from the local server.
                    </p>
                  </div>
                  <ChatComposer autoFocus />
                  <div className="flex flex-wrap gap-2">
                    {starterPrompts.map((prompt) => (
                      <ThreadPrimitive.Suggestion
                        key={prompt}
                        prompt={prompt}
                        method="replace"
                        autoSend={false}
                        className="rounded-md border border-border bg-background px-3 py-2 text-left text-sm text-muted-foreground transition hover:bg-muted hover:text-foreground"
                      >
                        {prompt}
                      </ThreadPrimitive.Suggestion>
                    ))}
                  </div>
                </div>
              </ThreadPrimitive.Empty>

              <ThreadPrimitive.Messages components={{ Message: ChatMessage }} />

              <ThreadPrimitive.If empty={false}>
                <ThreadPrimitive.ViewportFooter className="sticky bottom-0 mt-6 bg-background/95 py-4 backdrop-blur">
                  <ChatComposer />
                </ThreadPrimitive.ViewportFooter>
              </ThreadPrimitive.If>
            </ThreadPrimitive.Viewport>
          </ThreadPrimitive.Root>
        </div>
      </main>
    </AssistantRuntimeProvider>
  )
}

function ChatMessage() {
  return (
    <MessagePrimitive.Root className="w-full py-4">
      <MessagePrimitive.If assistant>
        <div className="grid w-full grid-cols-[2rem_minmax(0,1fr)] gap-3">
          <div className="flex size-8 items-center justify-center rounded-md border border-border bg-muted text-muted-foreground">
            <Bot className="size-4" aria-hidden="true" />
          </div>
          <div className="min-w-0 rounded-md border border-border bg-card px-4 py-3 text-sm shadow-sm">
            <MessagePrimitive.Content
              components={{
                Text: MarkdownText,
                Empty: RunningMessage,
                tools: { Fallback: ToolCallPart },
              }}
            />
          </div>
        </div>
      </MessagePrimitive.If>

      <MessagePrimitive.If user>
        <div className="ml-auto grid max-w-[80%] grid-cols-[minmax(0,1fr)_2rem] gap-3 sm:max-w-[70%]">
          <div className="min-w-0 rounded-md bg-primary px-4 py-3 text-sm text-primary-foreground">
            <MessagePrimitive.Content components={{ Text: PlainText }} />
          </div>
          <div className="flex size-8 items-center justify-center rounded-md border border-border bg-background text-muted-foreground">
            <UserRound className="size-4" aria-hidden="true" />
          </div>
        </div>
      </MessagePrimitive.If>
    </MessagePrimitive.Root>
  )
}

function ChatComposer({ autoFocus = false }: { autoFocus?: boolean }) {
  return (
    <ComposerPrimitive.Root className="flex min-h-14 w-full items-end gap-2 rounded-lg border border-border bg-card p-2 shadow-sm">
      <ComposerPrimitive.Input
        autoFocus={autoFocus}
        rows={1}
        submitMode="enter"
        placeholder="Ask about a Service, Pod, namespace, topology, or recent change"
        className="max-h-44 min-h-10 flex-1 resize-none bg-transparent px-2 py-2 text-sm leading-6 outline-none placeholder:text-muted-foreground"
      />
      <ThreadPrimitive.If running={false}>
        <ComposerPrimitive.Send asChild>
          <Button type="submit" size="icon" aria-label="Send message">
            <ArrowUp className="size-4" aria-hidden="true" />
          </Button>
        </ComposerPrimitive.Send>
      </ThreadPrimitive.If>
      <ThreadPrimitive.If running>
        <ComposerPrimitive.Cancel asChild>
          <Button type="button" size="icon" variant="secondary" aria-label="Cancel run">
            <CircleStop className="size-4" aria-hidden="true" />
          </Button>
        </ComposerPrimitive.Cancel>
      </ThreadPrimitive.If>
    </ComposerPrimitive.Root>
  )
}

function MarkdownText({ text }: { text: string }) {
  return (
    <ReactMarkdown
      remarkPlugins={[remarkGfm]}
      components={{
        p: ({ children }) => <p className="mb-3 last:mb-0">{children}</p>,
        ul: ({ children }) => <ul className="mb-3 list-disc pl-5 last:mb-0">{children}</ul>,
        ol: ({ children }) => <ol className="mb-3 list-decimal pl-5 last:mb-0">{children}</ol>,
        code: ({ children }) => (
          <code className="rounded bg-muted px-1.5 py-0.5 font-mono text-[0.85em] text-foreground">
            {children}
          </code>
        ),
      }}
    >
      {text}
    </ReactMarkdown>
  )
}

function PlainText({ text }: { text: string }) {
  return <p className="whitespace-pre-wrap leading-6">{text}</p>
}

function RunningMessage() {
  return <p className="text-sm text-muted-foreground">Working...</p>
}

function ToolCallPart({ toolName }: { toolName: string }) {
  return (
    <div className="rounded-md border border-border bg-muted px-3 py-2 text-xs text-muted-foreground">
      {toolName}
    </div>
  )
}

function appendMessageText(message: AppendMessage) {
  return message.content
    .filter((part) => part.type === "text")
    .map((part) => part.text)
    .join("\n")
    .trim()
}

function userMessage(text: string): ThreadMessage {
  return {
    id: newMessageID("user"),
    role: "user",
    createdAt: new Date(),
    content: [{ type: "text", text }],
    attachments: [],
    metadata: { custom: {} },
  }
}

function assistantMessage(
  id: string,
  text: string,
  status: "running" | "complete" | "cancelled",
): ThreadMessage {
  return {
    id,
    role: "assistant",
    createdAt: new Date(),
    content: text ? [{ type: "text", text }] : [],
    status:
      status === "complete"
        ? { type: "complete", reason: "stop" }
        : status === "cancelled"
          ? { type: "incomplete", reason: "cancelled" }
          : { type: "running" },
    metadata: {
      unstable_state: null,
      unstable_annotations: [],
      unstable_data: [],
      steps: [],
      custom: {},
    },
  }
}

function demoAgentAnswer(question: string) {
  return [
    `I received: **${escapeMarkdown(question)}**`,
    "",
    "The backend agent API is not wired into this screen yet. The next implementation step is to replace this local demo adapter with session creation, run start, and SSE event projection.",
    "",
    "### Evidence",
    "- Demo runtime: local assistant-ui ExternalStoreRuntime.",
  ].join("\n")
}

function newMessageID(prefix: string) {
  if (globalThis.crypto?.randomUUID) return `${prefix}_${globalThis.crypto.randomUUID()}`
  return `${prefix}_${Date.now()}_${Math.random().toString(36).slice(2)}`
}

function delay(ms: number) {
  return new Promise((resolve) => window.setTimeout(resolve, ms))
}

const markdownEscapeChars = new Set(["\\", "`", "*", "_", "{", "}", "[", "]", "(", ")", "#", "+", "-", ".", "!", "|", ">"])

function escapeMarkdown(value: string) {
  return Array.from(value, (char) =>
    markdownEscapeChars.has(char) ? `\\${char}` : char,
  ).join("")
}
