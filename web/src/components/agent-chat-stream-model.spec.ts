import { expect, test } from "@playwright/test"

import { conversationSegments, type ToolSegment } from "./agent-chat-stream-model"

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

