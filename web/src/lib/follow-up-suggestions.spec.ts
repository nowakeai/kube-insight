import { expect, test } from "@playwright/test"

import { followUpSuggestionsForRun } from "@/lib/follow-up-suggestions"
import type { AgentRun } from "@/lib/agent-store"

test("followUpSuggestionsForRun returns no suggestions for non-completed runs", () => {
  expect(followUpSuggestionsForRun({ run: runFixture({ status: "running" }) })).toEqual([])
})

test("followUpSuggestionsForRun prefers OOM and restart follow-ups when the run discussed OOM", () => {
  const suggestions = followUpSuggestionsForRun({
    run: runFixture({
      input: "why did default/api OOM?",
      finalAnswer: "The pod was OOMKilled and restarted twice.",
    }),
  })

  expect(suggestions).toContain("Check resource requests and limits for the affected Pods")
  expect(suggestions).toContain("Compare OOM and restart evidence in the last hour")
})

test("followUpSuggestionsForRun derives node follow-ups from Chinese answer text", () => {
  const suggestions = followUpSuggestionsForRun({
    run: runFixture({
      input: "看看节点容量变化",
      finalAnswer: "过去 12 小时内，集群维持了 9 个节点，未发现 CPU 或内存容量变化。",
    }),
  })

  expect(suggestions[0]).toBe("Compare node capacity and allocatable changes in the last hour")
  expect(suggestions).toContain("Show MemoryPressure and DiskPressure evidence by node")
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
