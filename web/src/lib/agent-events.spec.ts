import { expect, test } from "@playwright/test"

import { streamAgentRunEvents } from "./agent-events"

test("streamAgentRunEvents deduplicates by event id instead of sequence", async () => {
  const sentAt = "2026-05-28T00:00:00.000Z"
  const frames = [
    sse({ id: "event_a", runId: "run_1", sequence: 1, type: "run.started", createdAt: sentAt, data: { runId: "run_1", status: "running" } }),
    sse({ id: "event_b", runId: "run_1", sequence: 1, type: "message.delta", createdAt: sentAt, data: { role: "assistant", delta: "ok" } }),
    sse({ id: "event_b", runId: "run_1", sequence: 1, type: "message.delta", createdAt: sentAt, data: { role: "assistant", delta: "ok" } }),
  ].join("")
  const events: string[] = []
  const subscription = streamAgentRunEvents({
    runId: "run_1",
    baseURL: "http://127.0.0.1:8090",
    fetcher: async () => new Response(frames, { status: 200 }),
    onEvent: (event) => events.push(event.id),
    retry: { maxAttempts: 1 },
  })

  await subscription.closed

  expect(events).toEqual(["event_a", "event_b"])
})

function sse(event: Record<string, unknown>) {
  return `event: ${event.type}\ndata: ${JSON.stringify(event)}\n\n`
}
