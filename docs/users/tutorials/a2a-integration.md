# A2A Integration Tutorial

kube-insight can expose a small A2A-compatible HTTP surface so another agent
orchestrator can delegate a Kubernetes investigation to kube-insight as a whole
task.

Use this path when the caller wants an investigation answer, not direct tool
calls. Use MCP when the caller wants to choose specific kube-insight tools such
as health, SQL, search, history, topology, or service investigation.

Current A2A support includes:

- Agent Card discovery at `/.well-known/agent-card.json`;
- non-streaming message send at `POST /message:send`;
- streaming message send at `POST /message:stream`;
- task status and result lookup at `GET /tasks/{task_id}`;
- task subscription at `GET /tasks/{task_id}:subscribe`;
- task list at `GET /tasks`;
- best-effort task cancellation at `POST /tasks/{task_id}:cancel`.

Final answers are returned as text artifacts. kube-insight artifact and citation
events are also projected into A2A artifacts with kube-insight metadata.

## Prerequisites

- kube-insight API or app mode is running.
- The server has a configured chat provider if you want A2A tasks to execute
  investigations. Without an agent runner, kube-insight returns a failed task
  that explains the missing runtime.
- kube-insight has collected Kubernetes history for the namespaces and
  resources you want to investigate.

For storage choices, retention, and service exposure, use the
[Helm Chart](../../../charts/kube-insight/README.md) as the source of truth.
SQLite is only for tests, demos, and temporary runs.

## Install With Helm

For an A2A environment with embedded storage, use the default chDB chart
install:

```bash
helm install kube-insight oci://ghcr.io/nowakeai/charts/kube-insight \
  --namespace kube-insight \
  --create-namespace \
  --version <CHART_VERSION>
```

Agent Card discovery works without an LLM runtime, but investigation tasks need
the server-side chat provider enabled. After installing kube-insight with any
storage mode, store provider credentials in a Secret and enable chat:

```bash
kubectl -n kube-insight create secret generic kube-insight-chat \
  --from-literal=apiKey='<API_KEY>' \
  --from-literal=baseUrl='<OPENAI_COMPATIBLE_BASE_URL>'

helm upgrade kube-insight oci://ghcr.io/nowakeai/charts/kube-insight \
  --namespace kube-insight \
  --reuse-values \
  --version <CHART_VERSION> \
  --set server.chat.enabled=true \
  --set server.chat.existingSecret=kube-insight-chat \
  --set server.chat.provider=openai-compatible \
  --set server.chat.model='<MODEL_NAME>'
```

For providers that do not need a custom base URL, omit `baseUrl` from the
Secret and keep the default optional `baseUrl` reference.

Forward the service for local testing:

```bash
kubectl -n kube-insight port-forward svc/kube-insight 8090:8090
```

## Discover The Agent Card

```bash
curl -s http://127.0.0.1:8090/.well-known/agent-card.json
```

The card should identify `kube-insight`, advertise the HTTP+JSON A2A surface,
and include a Kubernetes retained-evidence investigation skill.

## Send An Investigation Task

```bash
curl -s http://127.0.0.1:8090/message:send \
  -H 'Content-Type: application/json' \
  -d '{
    "message": {
      "messageId": "msg-api-restarts",
      "role": "user",
      "parts": [
        {
          "kind": "text",
          "text": "Investigate why pods in namespace default restarted in the last hour. Use retained Kubernetes evidence and include exact object names and timestamps."
        }
      ]
    },
    "configuration": {
      "historyLength": 2
    }
  }'
```

The response contains a task. A completed task includes the final answer as a
text artifact. A queued or working task can be queried with its task ID.

## Query A Task

```bash
curl -s http://127.0.0.1:8090/tasks/<TASK_ID>
```

Task states map to kube-insight agent run lifecycle:

| A2A task state | kube-insight run status |
| --- | --- |
| `submitted` | `queued` |
| `working` | `running` |
| `completed` | `completed` |
| `failed` | `failed` |
| `canceled` | `cancelled` |

Use `historyLength=0` when the caller only wants status and artifacts:

```bash
curl -s 'http://127.0.0.1:8090/tasks/<TASK_ID>?historyLength=0'
```

## Stream A Task

Use `message:stream` when the caller wants task snapshots as the underlying
kube-insight agent run progresses:

```bash
curl -N http://127.0.0.1:8090/message:stream \
  -H 'Content-Type: application/json' \
  -d '{
    "message": {
      "messageId": "msg-stream-api-restarts",
      "role": "user",
      "parts": [
        {
          "kind": "text",
          "text": "Stream an investigation for recent pod restarts in namespace default."
        }
      ]
    },
    "configuration": {
      "historyLength": 2
    }
  }'
```

The response is Server-Sent Events. Each `event: task` contains a task snapshot.
The terminal snapshot includes `"final": true`.

To subscribe to an existing task:

```bash
curl -N http://127.0.0.1:8090/tasks/<TASK_ID>:subscribe
```

## Continue A Context

The returned `contextId` is the kube-insight agent session ID. Send it back to
continue the same investigation context:

```bash
curl -s http://127.0.0.1:8090/message:send \
  -H 'Content-Type: application/json' \
  -d '{
    "message": {
      "contextId": "<CONTEXT_ID>",
      "role": "user",
      "parts": [
        {
          "kind": "text",
          "text": "Narrow that to pods owned by the checkout deployment."
        }
      ]
    }
  }'
```

## Cancel A Task

```bash
curl -s -X POST http://127.0.0.1:8090/tasks/<TASK_ID>:cancel
```

Cancellation is best-effort. If the underlying run has already reached a
terminal state, kube-insight returns the existing terminal task.

## Current Limits

- A2A currently delegates to kube-insight's built-in agent runner. Configure the
  server chat provider before using A2A for real investigations.
- `message:send` waits for terminal completion for a bounded interval. If the
  investigation is still running, the response returns the current working task;
  callers should poll `GET /tasks/{task_id}`.
- Streaming is exposed with Server-Sent Events. Push notifications are not
  exposed yet.
- kagent integration should use the MCP tutorial unless a specific kagent A2A
  consumption path has been validated for the target version.
