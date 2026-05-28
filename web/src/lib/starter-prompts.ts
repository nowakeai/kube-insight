const starterPromptPool = [
  "Is the API service healthy right now?",
  "Show recent changes for default/api in the last hour",
  "Find pods with restart or OOMKilled evidence",
  "Map topology for namespace default",
  "Which workloads have readiness or liveness probe failures?",
  "Find Services with no ready backend Pods",
  "Show scheduling failures from the last 30 minutes",
  "Compare current Pod status with retained history",
  "List recent config changes that may affect default/api",
  "Trace default/api from Service to EndpointSlices, Pods, Nodes, and Events",
  "Which namespaces have the most unstable resources?",
  "Find image pull failures and affected workloads",
]

export function randomStarterPrompts(count = 4, random = Math.random) {
  return shuffle(starterPromptPool, random).slice(0, Math.max(0, count))
}

function shuffle(values: string[], random: () => number) {
  const out = [...values]
  for (let index = out.length - 1; index > 0; index -= 1) {
    const swapIndex = Math.floor(random() * (index + 1))
    const value = out[index]
    out[index] = out[swapIndex]
    out[swapIndex] = value
  }
  return out
}
