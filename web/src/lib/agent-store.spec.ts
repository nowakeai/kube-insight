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

test("batch server session hydration preserves list order and run projections", () => {
  const store = useAgentProjectionStore.getState()

  store.upsertServerSessions([
    {
      id: "sess_old",
      title: "Old",
      createdAt,
      updatedAt: createdAt,
      runCount: 3,
      latestRun: { ...runDTO("run_old", "old", undefined, "sess_old"), finalAnswer: "old answer" },
    },
    {
      id: "sess_new",
      title: "New",
      createdAt,
      updatedAt: createdAt,
      runCount: 1,
      latestRun: runDTO("run_new", "new", undefined, "sess_new"),
    },
  ], { activate: false })

  const state = useAgentProjectionStore.getState()
  expect(state.sessionOrder).toEqual(["sess_new", "sess_old"])
  expect(state.activeSessionId).toBeUndefined()
  expect(state.sessions.sess_old.runIds).toEqual(["run_old"])
  expect(state.sessions.sess_new.runIds).toEqual(["run_new"])
  expect(state.sessions.sess_old.runCount).toBe(3)
  expect(state.sessions.sess_new.runCount).toBe(1)
  expect(state.runs.run_old.input).toBe("old")
  expect(state.runs.run_old.finalAnswer).toBe("old answer")
  expect(state.runs.run_new.input).toBe("new")
})

test("removeSession clears runs, events, artifacts, citations, and active selection", () => {
  const store = useAgentProjectionStore.getState()

  store.upsertServerRun(runDTO("run_1", "question"))
  store.applyServerEvent({
    id: "evt_artifact",
    runId: "run_1",
    sequence: 1,
    type: "artifact.created",
    createdAt,
    data: { artifact: { id: "artifact_1", kind: "markdown", title: "Proof" } },
  })
  store.applyServerEvent({
    id: "evt_citation",
    runId: "run_1",
    sequence: 2,
    type: "citation.created",
    createdAt,
    data: { citation: { id: "citation_1", artifactId: "artifact_1" } },
  })
  store.selectArtifact("artifact_1")
  store.removeSession("sess_1")

  const state = useAgentProjectionStore.getState()
  expect(state.activeSessionId).toBeUndefined()
  expect(state.selectedArtifactId).toBeUndefined()
  expect(state.sessionOrder).toEqual([])
  expect(state.sessions.sess_1).toBeUndefined()
  expect(state.runs.run_1).toBeUndefined()
  expect(state.events.evt_artifact).toBeUndefined()
  expect(state.artifacts.artifact_1).toBeUndefined()
  expect(state.citations.citation_1).toBeUndefined()
  expect(state.panelWorkspaces.sess_1).toBeUndefined()
})

test("batch server event replay hydrates run evidence without duplicating events", () => {
  const store = useAgentProjectionStore.getState()

  store.upsertServerRun(runDTO("run_1", "question"))
  const events = [
    {
      id: "evt_artifact",
      runId: "run_1",
      sequence: 1,
      type: "artifact.created",
      createdAt,
      data: { artifact: { id: "artifact_1", kind: "markdown", title: "Proof", data: { ok: true } } },
    },
    {
      id: "evt_citation",
      runId: "run_1",
      sequence: 2,
      type: "citation.created",
      createdAt,
      data: { citation: { id: "citation_1", artifactId: "artifact_1", text: "source" } },
    },
    {
      id: "evt_answer",
      runId: "run_1",
      sequence: 3,
      type: "answer.final",
      createdAt,
      data: { content: "done" },
    },
  ] as const

  store.applyServerEvents([...events])
  store.applyServerEvents([...events])

  const state = useAgentProjectionStore.getState()
  expect(state.runs.run_1.eventIds).toEqual(["evt_artifact", "evt_citation", "evt_answer"])
  expect(state.runs.run_1.artifactIds).toEqual(["artifact_1"])
  expect(state.runs.run_1.citationIds).toEqual(["citation_1"])
  expect(state.runs.run_1.status).toBe("completed")
  expect(state.runs.run_1.finalAnswer).toBe("done")
  expect(state.artifacts.artifact_1.data).toEqual({ ok: true })
  expect(state.citations.citation_1.text).toBe("source")
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

test("panel watch interval is stored per session", () => {
  const store = useAgentProjectionStore.getState()

  store.setPanelWatchIntervalSeconds("sess_1", 15)
  expect(useAgentProjectionStore.getState().panelWorkspaces.sess_1.watchIntervalSeconds).toBe(15)

  store.setPanelWatchIntervalSeconds("sess_1", undefined)
  expect(useAgentProjectionStore.getState().panelWorkspaces.sess_1.watchIntervalSeconds).toBeUndefined()
})

test("updateArtifact refreshes panel data without appending another artifact event", () => {
  const store = useAgentProjectionStore.getState()

  store.upsertServerRun(runDTO("run_1", "question"))
  store.upsertArtifact("run_1", { id: "artifact_1", kind: "k8s.history", title: "Before", data: { versions: [] } })
  const beforeEvents = useAgentProjectionStore.getState().runs.run_1.eventIds.length

  store.updateArtifact("artifact_1", { title: "After", data: { versions: [{ id: "v1" }] } })

  const state = useAgentProjectionStore.getState()
  expect(state.artifacts.artifact_1.title).toBe("After")
  expect(state.artifacts.artifact_1.data).toEqual({ versions: [{ id: "v1" }] })
  expect(state.runs.run_1.eventIds.length).toBe(beforeEvents)
})

function runDTO(id: string, input: string, metadata?: unknown, sessionId = "sess_1"): AgentRunDTO {
  return {
    id,
    sessionId,
    status: "completed",
    input,
    createdAt,
    completedAt: createdAt,
    metadata,
  }
}
