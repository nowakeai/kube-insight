export function demoAgentAnswer(question: string) {
  return [
    `I received: **${escapeMarkdown(question)}**`,
    "",
    "The backend agent API is not wired into this screen yet. The next implementation step is to replace this local demo adapter with session creation, run start, and SSE event projection.",
    "",
    "### Demo findings",
    "| Signal | Status | Evidence |",
    "| --- | --- | --- |",
    "| Agent loop | Pending API wiring | Local demo adapter |",
    "| Markdown renderer | Ready | GFM table and fenced code block |",
    "",
    "```yaml",
    "kind: DemoInvestigation",
    `query: ${JSON.stringify(question)}`,
    "status: rendered",
    "```",
    "",
    "> Rich answers should stay compact, scannable, and tied to evidence.",
    "",
    "### Evidence",
    "- Demo runtime: [citation: demo runtime](#citation:citation_demo_runtime).",
    "- Renderer: [react-markdown](https://github.com/remarkjs/react-markdown) with GFM enabled.",
  ].join("\n")
}

export function demoK8sResourceArtifact(question: string) {
  return {
    identity: {
      cluster: "local-demo",
      apiVersion: "v1",
      kind: "Pod",
      namespace: "default",
      name: "api-0",
      uid: "demo-pod-api-0",
      resourceVersion: "48291",
    },
    status: {
      phase: "Running",
      ready: "True",
      reason: "ContainersReady",
      message: "Demo projection rendered from local UI state.",
    },
    summary: [
      `Question: ${question}`,
      "Pod api-0 is running and all demo containers are ready.",
      "This renderer accepts typed identity, status, labels, annotations, conditions, and retained object proof.",
    ],
    labels: {
      app: "api",
      "pod-template-hash": "demo",
      "kube-insight.io/source": "demo",
    },
    annotations: {
      "kube-insight.io/artifact-kind": "k8s.resource",
      "kube-insight.io/proof": "retained-object",
    },
    object: {
      apiVersion: "v1",
      kind: "Pod",
      metadata: {
        namespace: "default",
        name: "api-0",
        uid: "demo-pod-api-0",
        resourceVersion: "48291",
        labels: { app: "api", "pod-template-hash": "demo" },
      },
      status: {
        phase: "Running",
        podIP: "10.42.0.18",
        conditions: [
          { type: "PodScheduled", status: "True", reason: "Scheduler", message: "Assigned to demo-node-1" },
          { type: "Ready", status: "True", reason: "ContainersReady", message: "All containers are ready" },
        ],
      },
    },
  }
}

export function demoK8sResourceListArtifact(question: string) {
  return {
    title: "Candidates",
    groups: [
      {
        title: "Likely related",
        items: [
          {
            id: "candidate-pod-api-0",
            identity: { apiVersion: "v1", kind: "Pod", namespace: "default", name: "api-0" },
            status: { phase: "Running", ready: "True" },
            score: 0.98,
            reason: "Exact workload match for the demo query.",
            summary: [`Question: ${question}`, "Running pod with retained status proof."],
            labels: { app: "api", "pod-template-hash": "demo" },
          },
          {
            id: "candidate-service-api",
            identity: { apiVersion: "v1", kind: "Service", namespace: "default", name: "api" },
            status: { phase: "Active" },
            score: 0.86,
            reason: "Service selector matches app=api.",
            summary: ["Routes traffic to demo api pods."],
            labels: { app: "api" },
          },
        ],
      },
      {
        title: "Needs attention",
        items: [
          {
            id: "candidate-pod-worker-0",
            identity: { apiVersion: "v1", kind: "Pod", namespace: "default", name: "worker-0" },
            status: { phase: "Running", ready: "False", reason: "ContainersNotReady" },
            score: 0.63,
            reason: "Shares namespace and recent readiness signal.",
            summary: ["Readiness is false in the demo candidate set."],
            labels: { app: "worker" },
          },
        ],
      },
    ],
  }
}

export function demoK8sHistoryArtifact(question: string) {
  return {
    title: "Pod default/api-0 history",
    identity: { apiVersion: "v1", kind: "Pod", namespace: "default", name: "api-0" },
    versions: [
      {
        id: "version-48270",
        observedAt: "2026-05-21T01:40:00Z",
        resourceVersion: "48270",
        status: "Pending",
        reason: "Scheduled",
        summary: ["Pod was scheduled and waiting for container readiness.", `Question context: ${question}`],
        document: {
          apiVersion: "v1",
          kind: "Pod",
          metadata: { namespace: "default", name: "api-0", resourceVersion: "48270" },
          status: { phase: "Pending", conditions: [{ type: "Ready", status: "False" }] },
        },
      },
      {
        id: "version-48291",
        observedAt: "2026-05-21T01:42:00Z",
        resourceVersion: "48291",
        status: "Running",
        reason: "ContainersReady",
        summary: ["Pod reached Ready=True and all demo containers are ready."],
        document: {
          apiVersion: "v1",
          kind: "Pod",
          metadata: { namespace: "default", name: "api-0", resourceVersion: "48291" },
          status: { phase: "Running", conditions: [{ type: "Ready", status: "True" }] },
        },
      },
    ],
  }
}

export function demoK8sTopologyArtifact(question: string) {
  return {
    title: "Service default/api topology",
    query: question,
    nodes: [
      { id: "svc-default-api", kind: "Service", namespace: "default", name: "api", status: "Active" },
      { id: "eps-default-api", kind: "EndpointSlice", namespace: "default", name: "api-abc12", status: "Ready 2" },
      { id: "pod-default-api-0", kind: "Pod", namespace: "default", name: "api-0", status: "Ready True" },
      { id: "pod-default-worker-0", kind: "Pod", namespace: "default", name: "worker-0", status: "Ready False" },
      { id: "node-demo-1", kind: "Node", name: "demo-node-1", status: "Ready" },
      { id: "event-worker-unready", kind: "Event", namespace: "default", name: "worker-0-unready", status: "Warning" },
    ],
    edges: [
      { id: "svc-eps", source: "svc-default-api", target: "eps-default-api", label: "selects" },
      { id: "eps-api", source: "eps-default-api", target: "pod-default-api-0", label: "endpoint" },
      { id: "eps-worker", source: "eps-default-api", target: "pod-default-worker-0", label: "endpoint" },
      { id: "api-node", source: "pod-default-api-0", target: "node-demo-1", label: "scheduled" },
      { id: "worker-node", source: "pod-default-worker-0", target: "node-demo-1", label: "scheduled" },
      { id: "worker-event", source: "pod-default-worker-0", target: "event-worker-unready", label: "emits" },
    ],
  }
}

const markdownEscapeChars = new Set(["\\", "`", "*", "_", "{", "}", "[", "]", "(", ")", "#", "+", "-", ".", "!", "|", ">"])

function escapeMarkdown(value: string) {
  return Array.from(value, (char) =>
    markdownEscapeChars.has(char) ? `\\${char}` : char,
  ).join("")
}
