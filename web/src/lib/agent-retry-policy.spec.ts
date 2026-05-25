import { expect, test } from "@playwright/test"

import { AgentAPIError } from "./agent-api"
import { retryErrorMessage, retryReplacementMetadata, shouldCreateReplacementRunForRetryError } from "./agent-retry-policy"

test("missing original run can be retried as a replacement branch", () => {
  expect(shouldCreateReplacementRunForRetryError(new AgentAPIError(404, "missing"))).toBe(true)
  expect(retryReplacementMetadata("run_original")).toEqual({
    retryOfRunId: "run_original",
    retryFallback: "missing-original-run",
  })
})

test("non-404 retry failures must not fall back to a normal append", () => {
  expect(shouldCreateReplacementRunForRetryError(new AgentAPIError(409, "running"))).toBe(false)
  expect(shouldCreateReplacementRunForRetryError(new Error("network failed"))).toBe(false)
  expect(retryErrorMessage(new Error("network failed"))).toBe("network failed")
})
