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

const markdownEscapeChars = new Set(["\\", "`", "*", "_", "{", "}", "[", "]", "(", ")", "#", "+", "-", ".", "!", "|", ">"])

function escapeMarkdown(value: string) {
  return Array.from(value, (char) =>
    markdownEscapeChars.has(char) ? `\\${char}` : char,
  ).join("")
}
