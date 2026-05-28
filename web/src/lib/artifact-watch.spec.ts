import { expect, test } from "@playwright/test"

import type { AgentArtifact } from "@/lib/agent-store"
import { watchableArtifactTarget } from "@/lib/artifact-watch"

const createdAt = "2026-05-25T00:00:00.000Z"

test("extracts watch target from history artifact identity", () => {
  expect(watchableArtifactTarget(artifact("k8s.history", {
    identity: { clusterId: "gcp-2", kind: "Pod", namespace: "default", name: "api-0" },
    versions: [],
  }))).toEqual({ clusterId: "gcp-2", kind: "Pod", namespace: "default", name: "api-0" })
})

test("extracts watch target from resource artifact object metadata", () => {
  expect(watchableArtifactTarget(artifact("k8s.resource", {
    cluster: "gcp-2",
    object: {
      apiVersion: "v1",
      kind: "Service",
      metadata: { namespace: "default", name: "api" },
    },
  }))).toEqual({ clusterId: "gcp-2", kind: "Service", namespace: "default", name: "api" })
})

test("extracts watch target from topology node ids", () => {
  expect(watchableArtifactTarget(artifact("k8s.topology", {
    nodes: [
      { id: "gcp-2/Service/default/api", kind: "Service", namespace: "default", name: "api" },
    ],
    edges: [],
  }))).toEqual({ clusterId: "gcp-2", kind: "Service", namespace: "default", name: "api" })
})

test("non-resource artifacts are not watchable", () => {
  expect(watchableArtifactTarget(artifact("markdown", { markdown: "hello" }))).toBeUndefined()
})

function artifact(kind: AgentArtifact["kind"], data: unknown): AgentArtifact {
  return {
    id: `artifact_${kind}`,
    runId: "run_1",
    kind,
    title: kind,
    data,
    createdAt,
    updatedAt: createdAt,
  }
}
