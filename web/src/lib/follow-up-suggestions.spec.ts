import { expect, test } from "@playwright/test"

import { followUpSuggestionsForRun, followUpSuggestionsFromEvents } from "@/lib/follow-up-suggestions"
import type { AgentRun, AgentRunEvent } from "@/lib/agent-store"

test("followUpSuggestionsForRun returns no suggestions for non-completed runs", () => {
  expect(followUpSuggestionsForRun({ run: runFixture({ status: "running" }) })).toEqual([])
})

test("followUpSuggestionsForRun returns no suggestions without follow-up events", () => {
  const suggestions = followUpSuggestionsForRun({
    run: runFixture({
      input: "why did default/api OOM?",
      finalAnswer: "The pod was OOMKilled and restarted twice.",
    }),
  })

  expect(suggestions).toEqual([])
})

test("followUpSuggestionsForRun prefers model suggestions from run events", () => {
  const suggestions = followUpSuggestionsForRun({
    run: runFixture({
      input: "why did default/api OOM?",
      finalAnswer: "The pod was OOMKilled and restarted twice.",
    }),
    events: [{
      id: "evt_followup",
      runId: "run_1",
      type: "followup.suggestions",
      createdAt: "2026-05-27T00:00:01Z",
      data: {
        suggestions: [
          "Check default/api-0 requests and limits",
          "Compare default/api-0 restarts in the last hour",
        ],
      },
    }],
  })

  expect(suggestions).toEqual([
    "Check default/api-0 requests and limits",
    "Compare default/api-0 restarts in the last hour",
  ])
})

test("followUpSuggestionsFromEvents deduplicates and bounds suggestions", () => {
  const suggestions = followUpSuggestionsFromEvents([
    followUpEvent(["A", "B", "A", "C", "D", "E"]),
  ])

  expect(suggestions).toEqual(["A", "B", "C", "D"])
})

function runFixture(overrides: Partial<AgentRun> = {}): AgentRun {
  return {
    id: "run_1",
    sessionId: "sess_1",
    status: "completed",
    input: "is the api service healthy?",
    createdAt: "2026-05-27T00:00:00Z",
    updatedAt: "2026-05-27T00:00:00Z",
    eventIds: [],
    artifactIds: [],
    citationIds: [],
    ...overrides,
  }
}

function followUpEvent(suggestions: string[]): AgentRunEvent {
  return {
    id: "evt_followup",
    runId: "run_1",
    type: "followup.suggestions",
    createdAt: "2026-05-27T00:00:01Z",
    data: { suggestions },
  }
}
