import { expect, test } from "@playwright/test"

import { displayRunIdsForRetryBranches, retryOfRunId } from "./agent-retry-branches"

test("retry metadata is detected from run metadata", () => {
  expect(retryOfRunId({ id: "run_retry", metadata: { retryOfRunId: "run_original" } })).toBe("run_original")
  expect(retryOfRunId({ id: "run_plain", metadata: {} })).toBeUndefined()
})

test("retry replaces the retried response instead of appending", () => {
  const runs = {
    run_1: { id: "run_1" },
    run_2: { id: "run_2", metadata: { retryOfRunId: "run_1" } },
  }

  expect(displayRunIdsForRetryBranches(["run_1", "run_2"], runs)).toEqual(["run_2"])
})

test("retry rewinds later conversation turns in the same branch", () => {
  const runs = {
    intro: { id: "intro" },
    run_1: { id: "run_1" },
    run_2: { id: "run_2" },
    retry_1: { id: "retry_1", metadata: { retryOfRunId: "run_1" } },
  }

  expect(displayRunIdsForRetryBranches(["intro", "run_1", "run_2", "retry_1"], runs)).toEqual(["intro", "retry_1"])
})

test("retry chains keep only the latest attempt", () => {
  const runs = {
    run_1: { id: "run_1" },
    retry_1: { id: "retry_1", metadata: { retryOfRunId: "run_1" } },
    retry_2: { id: "retry_2", metadata: { retryOfRunId: "retry_1" } },
  }

  expect(displayRunIdsForRetryBranches(["run_1", "retry_1", "retry_2"], runs)).toEqual(["retry_2"])
})
