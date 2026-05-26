import { expect, test } from "@playwright/test"

import { displayRunIdsForRetryBranches, parentRunId, retryOfRunId } from "./agent-retry-branches"

test("retry metadata is detected from run metadata", () => {
	expect(retryOfRunId({ id: "run_retry", metadata: { retryOfRunId: "run_original" } })).toBe("run_original")
	expect(retryOfRunId({ id: "run_plain", metadata: {} })).toBeUndefined()
})

test("child run metadata is detected from run metadata", () => {
	expect(parentRunId({ id: "run_child", metadata: { parentRunId: "run_parent" } })).toBe("run_parent")
	expect(parentRunId({ id: "run_plain", metadata: {} })).toBeUndefined()
})

test("retry replaces the retried response instead of appending", () => {
  const runs = {
    run_1: { id: "run_1" },
    run_2: { id: "run_2", metadata: { retryOfRunId: "run_1" } },
  }

  expect(displayRunIdsForRetryBranches(["run_1", "run_2"], runs)).toEqual(["run_2"])
})

test("retry replaces original even when hydrated before the original run", () => {
  const runs = {
    run_1: { id: "run_1", createdAt: "2026-05-25T12:50:16.000Z" },
    run_2: { id: "run_2", createdAt: "2026-05-25T12:51:26.000Z", metadata: { retryOfRunId: "run_1" } },
  }

  expect(displayRunIdsForRetryBranches(["run_2", "run_1"], runs)).toEqual(["run_2"])
})

test("retry rewind remains chronological when server returns newest runs first", () => {
  const runs = {
    intro: { id: "intro", createdAt: "2026-05-25T12:49:00.000Z" },
    run_1: { id: "run_1", createdAt: "2026-05-25T12:50:00.000Z" },
    run_2: { id: "run_2", createdAt: "2026-05-25T12:50:30.000Z" },
    retry_1: { id: "retry_1", createdAt: "2026-05-25T12:51:00.000Z", metadata: { retryOfRunId: "run_1" } },
  }

  expect(displayRunIdsForRetryBranches(["retry_1", "run_2", "run_1", "intro"], runs)).toEqual(["intro", "retry_1"])
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


test("retry root metadata replaces the original after the direct parent was pruned", () => {
  const runs = {
    run_1: { id: "run_1" },
    retry_2: { id: "retry_2", metadata: { retryOfRunId: "retry_1", retryRootRunId: "run_1" } },
  }

  expect(displayRunIdsForRetryBranches(["run_1", "retry_2"], runs)).toEqual(["retry_2"])
})

test("retry with a pruned parent does not append to stale visible turns", () => {
  const runs = {
    run_1: { id: "run_1" },
    retry_2: { id: "retry_2", metadata: { retryOfRunId: "retry_1" } },
  }

  expect(displayRunIdsForRetryBranches(["run_1", "retry_2"], runs)).toEqual(["retry_2"])
})

test("retry chains keep only the latest attempt", () => {
	const runs = {
		run_1: { id: "run_1" },
		retry_1: { id: "retry_1", metadata: { retryOfRunId: "run_1" } },
    retry_2: { id: "retry_2", metadata: { retryOfRunId: "retry_1" } },
  }

	expect(displayRunIdsForRetryBranches(["run_1", "retry_1", "retry_2"], runs)).toEqual(["retry_2"])
})

test("child runs are hidden from the top-level session branch", () => {
	const runs = {
		run_parent: { id: "run_parent" },
		run_child: { id: "run_child", metadata: { parentRunId: "run_parent", parentToolCallId: "call_1" } },
		run_followup: { id: "run_followup" },
	}

	expect(displayRunIdsForRetryBranches(["run_parent", "run_child", "run_followup"], runs)).toEqual(["run_parent", "run_followup"])
})
