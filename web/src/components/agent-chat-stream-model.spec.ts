import { expect, test } from "@playwright/test"

import type { AgentArtifact, AgentRun } from "@/lib/agent-store"

import { conversationSegments, runInlineArtifacts, toolSegmentDetail, type ToolSegment } from "./agent-chat-stream-model"

const createdAt = "2026-05-25T00:00:00.000Z"

test("tool artifact child run ids are projected onto the tool segment", () => {
  const segments = conversationSegments(
    {
      id: "run_parent",
      sessionId: "sess_1",
      status: "completed",
      input: "investigate",
      createdAt,
      updatedAt: createdAt,
      eventIds: [],
      artifactIds: [],
      citationIds: [],
    },
    [
      {
        id: "evt_artifact",
        runId: "run_parent",
        type: "artifact.created",
        createdAt,
        data: {
          artifact: {
            id: "artifact_tool",
            kind: "tool_call",
            data: {
              toolCallId: "call_parallel",
              output: JSON.stringify({
                branches: [
                  { childRunId: "run_child_a" },
                  { childRunId: "run_child_b" },
                ],
              }),
            },
          },
        },
      },
      {
        id: "evt_tool",
        runId: "run_parent",
        type: "tool.completed",
        createdAt,
        data: {
          toolCallId: "call_parallel",
          name: "parallel_investigation",
          status: "completed",
          outputSummary: "parallel investigation completed 2 branch(es), failed 0 branch(es)",
        },
      },
    ],
    "completed",
    Date.parse(createdAt),
  )

  const tool = segments.find((segment): segment is ToolSegment => segment.type === "tool")
  expect(tool?.childRunIds).toEqual(["run_child_a", "run_child_b"])
})

test("conversationSegments uses run finalAnswer when events are not hydrated", () => {
  const segments = conversationSegments(
    {
      id: "run_1",
      sessionId: "sess_1",
      status: "completed",
      input: "question",
      finalAnswer: "answer from summary",
      createdAt,
      updatedAt: createdAt,
      eventIds: [],
      artifactIds: [],
      citationIds: [],
    },
    [],
    "completed",
    Date.parse(createdAt),
  )

  expect(segments).toContainEqual({
    type: "assistant",
    id: "assistant_final_run_1",
    messageId: "final_run_1",
    content: "answer from summary",
    final: true,
  })
})

test("tool segment detail keeps child run payloads concise", () => {
  const detail = toolSegmentDetail({
    type: "tool",
    id: "tool_call_parallel",
    toolCallId: "call_parallel",
    name: "parallel_investigation",
    status: "completed",
    childRunIds: ["run_child_a"],
    childRuns: [{
      id: "run_child_a",
      sessionId: "sess_1",
      status: "completed",
      branchName: "oom_restarts",
      input: "large prompt body",
      finalAnswer: "large final answer body",
      eventCount: 12,
      artifactCount: 3,
    }],
  })

  expect(detail).toContain("\"branchName\": \"oom_restarts\"")
  expect(detail).toContain("\"hasFinalAnswer\": true")
  expect(detail).not.toContain("large prompt body")
  expect(detail).not.toContain("large final answer body")
})

test("runInlineArtifacts keeps dockable artifacts and drops audit-only artifacts", () => {
  const run: AgentRun = {
    id: "run_1",
    sessionId: "sess_1",
    status: "completed",
    input: "show proof",
    createdAt,
    updatedAt: createdAt,
    eventIds: [],
    artifactIds: ["artifact_tool", "artifact_markdown", "artifact_resource", "artifact_topology"],
    citationIds: [],
  }
  const artifactsById: Record<string, AgentArtifact> = {
    artifact_tool: artifact("artifact_tool", "tool_call"),
    artifact_markdown: artifact("artifact_markdown", "markdown"),
    artifact_resource: artifact("artifact_resource", "k8s.resource"),
    artifact_topology: artifact("artifact_topology", "k8s.topology"),
  }

  expect(runInlineArtifacts(run, artifactsById).map((item) => item.id)).toEqual([
    "artifact_markdown",
    "artifact_resource",
    "artifact_topology",
  ])
  expect(runInlineArtifacts(run, artifactsById, { limit: 2 }).map((item) => item.id)).toEqual([
    "artifact_resource",
    "artifact_topology",
  ])
  expect(runInlineArtifacts(run, artifactsById, { excludeArtifactIds: ["artifact_resource"] }).map((item) => item.id)).toEqual([
    "artifact_markdown",
    "artifact_topology",
  ])
})

function artifact(id: string, kind: AgentArtifact["kind"]): AgentArtifact {
  return {
    id,
    runId: "run_1",
    kind,
    title: id,
    createdAt,
    updatedAt: createdAt,
  }
}
