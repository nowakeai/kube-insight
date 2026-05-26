import { expect, test } from "@playwright/test"

import type { AgentRunDTO } from "./agent-schemas"
import { useAgentProjectionStore } from "./agent-store"

const createdAt = "2026-05-25T00:00:00.000Z"

test.beforeEach(() => {
  useAgentProjectionStore.getState().reset()
})

test("server retry run replaces the retried branch in session projection", () => {
  const store = useAgentProjectionStore.getState()

  store.upsertServerRun(runDTO("run_1", "question"))
  store.upsertServerRun(runDTO("run_2", "follow-up"))
  store.upsertServerRun(runDTO("run_retry", "question", { retryOfRunId: "run_1" }))

  expect(useAgentProjectionStore.getState().sessions.sess_1.runIds).toEqual(["run_retry"])
})

test("server session hydration keeps retry branch projection stable", () => {
	const store = useAgentProjectionStore.getState()

	store.upsertServerSession({
    id: "sess_1",
    title: "Retry branch",
    createdAt,
    updatedAt: createdAt,
    runs: [
      runDTO("run_1", "question"),
      runDTO("run_2", "follow-up"),
      runDTO("run_retry", "question", { retryOfRunId: "run_1" }),
    ],
  })

	expect(useAgentProjectionStore.getState().sessions.sess_1.runIds).toEqual(["run_retry"])
})

test("server session hydration keeps child runs available but out of top-level projection", () => {
	const store = useAgentProjectionStore.getState()

	store.upsertServerSession({
		id: "sess_1",
		title: "Subagent branch",
		createdAt,
		updatedAt: createdAt,
		runs: [
			runDTO("run_parent", "investigate"),
			runDTO("run_child", "child branch", { parentRunId: "run_parent", parentToolCallId: "call_1" }),
			runDTO("run_followup", "follow up"),
		],
	})

	const state = useAgentProjectionStore.getState()
	expect(state.sessions.sess_1.runIds).toEqual(["run_parent", "run_followup"])
	expect(state.runs.run_child?.metadata).toEqual({ parentRunId: "run_parent", parentToolCallId: "call_1" })
})

test("retry fallback replacement still rewinds later turns", () => {
	const store = useAgentProjectionStore.getState()

  store.upsertServerRun(runDTO("run_1", "question"))
  store.upsertServerRun(runDTO("run_2", "follow-up"))
  store.upsertServerRun(runDTO("run_retry", "question", { retryOfRunId: "run_1", retryFallback: "missing-original-run" }))

  expect(useAgentProjectionStore.getState().sessions.sess_1.runIds).toEqual(["run_retry"])
})

test("server retry with pruned parent does not append to stale session projection", () => {
  const store = useAgentProjectionStore.getState()

  store.upsertServerRun(runDTO("run_1", "question"))
  store.upsertServerRun(runDTO("run_retry", "question", { retryOfRunId: "run_pruned" }))

  expect(useAgentProjectionStore.getState().sessions.sess_1.runIds).toEqual(["run_retry"])
})

function runDTO(id: string, input: string, metadata?: unknown): AgentRunDTO {
  return {
    id,
    sessionId: "sess_1",
    status: "completed",
    input,
    createdAt,
    completedAt: createdAt,
    metadata,
  }
}
