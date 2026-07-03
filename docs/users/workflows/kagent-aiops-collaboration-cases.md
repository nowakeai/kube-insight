# kagent AIOps Collaboration Cases

These cases are designed for a kagent full-stack AIOps Agent that combines:

- kube-insight retained Kubernetes evidence;
- kagent Kubernetes, Helm, shell, and API-server tools;
- Prometheus-compatible metrics tools, including VictoriaMetrics-backed
  endpoints;
- optional Loki tools for logs.

The goal is not to make kube-insight replace metrics or logs. kube-insight adds
the Kubernetes control-plane memory that those sources usually do not retain:
object versions, deleted objects, owner relationships, Service-to-Pod topology,
GitOps/Helm object changes, and collector coverage.

## Case 1: Deploy Regression With Metric Spike

User prompt:

```text
After the last deploy, checkout latency went up. What changed and which
Kubernetes objects are implicated?
```

Use when:

- Prometheus shows a latency or error-rate increase.
- The user needs the exact Kubernetes change that preceded it.

Tool collaboration:

| step | tool source | purpose |
| --- | --- | --- |
| 1 | kube-insight health | confirm retained evidence coverage for Deployment, ReplicaSet, Pod, Service, EndpointSlice, HelmRelease/Kustomization |
| 2 | Prometheus query range | find the latency/error spike window with exact timestamps |
| 3 | kube-insight search/history/SQL/JS | list Kubernetes object changes in that window and rank by time proximity |
| 4 | Helm or GitOps tools | verify the live release/sync metadata |
| 5 | Kubernetes live tools | confirm the current rollout, Pod status, and Service endpoints |
| 6 | Loki | sample application errors around the spike |

kube-insight advantage:

- Prometheus can prove the symptom timing, but not which Kubernetes object
  changed.
- Loki can prove error messages, but logs may not include resource ownership or
  deployment metadata.
- kube-insight can show the retained change chain: HelmRelease/Kustomization ->
  Deployment -> ReplicaSet -> Pods -> EndpointSlices.

Expected answer evidence:

- metric spike start/end time;
- exact changed objects with old/new resource versions or versions;
- deploy or GitOps object that introduced the change;
- whether the Service endpoint set changed after the deploy;
- current live validation and any coverage gaps.

Pass criteria:

- The answer names at least one concrete Kubernetes object change before or at
  the metric spike.
- It does not claim causality from metrics alone.
- It cites both metric evidence and retained object-history evidence.

## Case 2: Logs Rotated But Pod Restart History Remains

User prompt:

```text
Why did api pods restart overnight? Current logs only show the latest run.
```

Use when:

- container logs rotated or the container restarted;
- current `kubectl logs` is insufficient.

Tool collaboration:

| step | tool source | purpose |
| --- | --- | --- |
| 1 | kube-insight health | verify Pod, ReplicaSet, Deployment, Node, ConfigMap, Secret metadata, and Event coverage |
| 2 | kube-insight SQL/JS | find Pod restartCount transitions, container status changes, and owner chain |
| 3 | Prometheus query range | check restart, OOM, CPU throttling, memory, and node pressure time series |
| 4 | Loki | query logs before the restart if retained |
| 5 | Kubernetes live tools | verify current Pod and Node conditions |

kube-insight advantage:

- Live Kubernetes only shows the current Pod state.
- Loki may miss pre-restart logs or only show application-level errors.
- kube-insight can retain previous Pod status snapshots and object transitions,
  including restartCount, terminated reason, image, owner, node, and config
  references.

Expected answer evidence:

- the first restart timestamp detected from retained Pod history;
- previous and current container status fields;
- related Node condition or metric evidence;
- whether logs confirm or only support the hypothesis.

Pass criteria:

- The answer can still make progress when Loki has no old logs.
- It separates retained Kubernetes status evidence from metric/log inference.

## Case 3: Service Endpoint Flapping

User prompt:

```text
Users saw intermittent 503s for service payments/api. Did endpoints change or
did the app fail while endpoints stayed stable?
```

Use when:

- metrics show 5xx or availability drops;
- it is unclear whether the issue is routing/endpoints or application behavior.

Tool collaboration:

| step | tool source | purpose |
| --- | --- | --- |
| 1 | kube-insight service investigation | retrieve Service, EndpointSlice, Pods, owners, and topology |
| 2 | Prometheus query range | check 5xx, request rate, readiness, and endpoint-related metrics |
| 3 | kube-insight history | compare EndpointSlice and Pod readiness changes across the incident window |
| 4 | Kubernetes live tools | validate current endpoint state |
| 5 | Loki | inspect application or ingress logs only for the narrowed window |

kube-insight advantage:

- Prometheus shows the symptom but often lacks exact endpoint membership
  snapshots.
- Loki logs can show 503s but not whether Kubernetes endpoints were removed.
- kube-insight can prove whether EndpointSlice membership and Pod readiness
  changed at the same time.

Expected answer evidence:

- Service selector and current/live endpoints;
- retained EndpointSlice membership changes;
- Pod readiness/status transition timeline;
- metric 5xx window and matching logs if available.

Pass criteria:

- The answer explicitly chooses one of: endpoint membership changed, endpoints
  stable but app failed, or evidence is insufficient.
- It cites retained EndpointSlice/Pod evidence, not only metrics.

## Case 4: GitOps Or Helm Drift Caused Rollout

User prompt:

```text
Something rolled out in namespace platform, but nobody remembers applying it.
Was it Helm, GitOps, or a manual kubectl change?
```

Use when:

- current workloads changed;
- source of change is unclear.

Tool collaboration:

| step | tool source | purpose |
| --- | --- | --- |
| 1 | kube-insight SQL/JS | list Deployment/StatefulSet/DaemonSet and ReplicaSet changes in the namespace |
| 2 | kube-insight history | inspect HelmRelease, Kustomization, and source repository object changes |
| 3 | Helm tools | compare live Helm release revision and values |
| 4 | Kubernetes live tools | inspect managed-by labels, annotations, and current owners |
| 5 | Prometheus | confirm whether rollout changed service-level metrics |

kube-insight advantage:

- Helm tools show current release state, not necessarily all Kubernetes object
  transitions.
- GitOps tools show controller state, but may not retain older object snapshots.
- kube-insight can correlate workload changes with GitOps/Helm CR changes and
  retained Kubernetes annotations.

Expected answer evidence:

- changed workload objects and timestamps;
- matching HelmRelease/Kustomization/source changes if present;
- annotations such as Helm revision or GitOps reconciliation metadata;
- current Helm/GitOps live validation.

Pass criteria:

- The answer distinguishes Helm/GitOps-originated rollout from manual change
  based on evidence.
- It includes a confidence level when annotations or history are incomplete.

## Case 5: Alert Fired, Current State Looks Healthy

User prompt:

```text
Prometheus alerted on node pressure 40 minutes ago, but everything looks fine
now. What actually happened?
```

Use when:

- a transient alert resolved before humans started investigating;
- current Kubernetes state is clean.

Tool collaboration:

| step | tool source | purpose |
| --- | --- | --- |
| 1 | Prometheus query range | reconstruct the alert window and affected node/pod labels |
| 2 | kube-insight SQL/JS | find retained Node condition, Pod scheduling, eviction, and restart changes around that window |
| 3 | Kubernetes live tools | confirm the node and pods recovered |
| 4 | Loki | inspect kubelet/application logs if retained |

kube-insight advantage:

- Prometheus keeps numeric symptoms but not Kubernetes object versions.
- Live Kubernetes no longer shows the transient condition.
- kube-insight can retain the object timeline that explains a resolved alert.

Expected answer evidence:

- alert metric window and affected labels;
- Node condition or Pod transition retained by kube-insight;
- current live recovery validation;
- whether any deleted/evicted Pods were involved.

Pass criteria:

- The answer does not stop at "current state is healthy."
- It reconstructs the incident from retained history and metrics.

## Case 6: Deleted Resource Still Matters

User prompt:

```text
A job failed and was cleaned up by TTL. Can you still tell what happened?
```

Use when:

- failed Jobs/Pods were deleted;
- Prometheus/Loki may still have partial labels or logs.

Tool collaboration:

| step | tool source | purpose |
| --- | --- | --- |
| 1 | kube-insight search | find deleted Job/Pod objects by name pattern, namespace, owner, or failure text |
| 2 | kube-insight history | inspect retained deleted object versions and terminal status |
| 3 | Loki | query logs for the deleted Pod name in the bounded time window |
| 4 | Prometheus | check job duration, container exit, or restart metrics if labels remain |
| 5 | Kubernetes live tools | confirm the object is no longer present |

kube-insight advantage:

- Live Kubernetes cannot inspect deleted objects.
- Prometheus labels may no longer be enough to reconstruct owner and spec.
- kube-insight retains deleted-object evidence and the Kubernetes spec/status
  that produced the failure.

Expected answer evidence:

- deleted object identity, UID, owner, and terminal status;
- retained spec fields such as image, command, resource limits, and env names
  without exposing secret values;
- logs/metrics if still available;
- current live absence confirmation.

Pass criteria:

- The answer can still identify deleted Kubernetes objects.
- It avoids exposing secret values from retained specs or logs.

## Case 7: Ingress Or Gateway 5xx Investigation

User prompt:

```text
Gateway 5xx increased for api.example.com. Is this Gateway/Route config,
backend endpoints, or application errors?
```

Use when:

- traffic-level metrics or logs show 5xx;
- ingress/gateway config may have changed.

Tool collaboration:

| step | tool source | purpose |
| --- | --- | --- |
| 1 | Prometheus query range | locate 5xx window and affected gateway/route/service labels |
| 2 | kube-insight topology/search | map Gateway/HTTPRoute/Ingress to Service, EndpointSlice, Pods, and owners |
| 3 | kube-insight history | find Gateway/HTTPRoute/Ingress/Service/EndpointSlice changes around the window |
| 4 | Loki | query gateway/controller/application logs for matching errors |
| 5 | Kubernetes live tools | confirm current route and backend state |

kube-insight advantage:

- Metrics show which route is failing, but not the retained config diff.
- Logs can show proxy errors, but not the full Kubernetes relationship graph.
- kube-insight can connect route config changes to backend object changes.

Expected answer evidence:

- affected route/host/service and time window;
- retained config or endpoint changes;
- log samples for proxy/application error class;
- current live state and remediation target.

Pass criteria:

- The answer identifies the likely layer: route config, backend endpoint,
  application, or unknown.
- It includes the Kubernetes object path from edge to backend.

## Case 8: Agent Toolchain Or MCP Integration Regression

User prompt:

```text
kagent stopped using kube-insight tools after the last change. What changed?
```

Use when:

- a kagent Agent no longer discovers or calls kube-insight tools;
- the integration itself is the operational target.

Tool collaboration:

| step | tool source | purpose |
| --- | --- | --- |
| 1 | kube-insight search/history | inspect retained RemoteMCPServer, Agent, ModelConfig, and Secret metadata changes |
| 2 | Kubernetes live tools | check current CR status and kagent controller events |
| 3 | Loki | query kagent controller/agent logs if installed |
| 4 | Prometheus | check kagent/kube-insight availability metrics if exposed |

kube-insight advantage:

- kagent live CR status shows current failure, but not necessarily the previous
  working configuration.
- kube-insight can compare previous and current Agent/RemoteMCPServer specs and
  show exactly which toolNames, URLs, prompts, or references changed.

Expected answer evidence:

- changed kagent CR fields;
- discovered tool list before/after if retained;
- current controller status/log error;
- safe fix suggestion without exposing provider secrets.

Pass criteria:

- The answer identifies a concrete config drift or proves no relevant CR change
  was retained.
- It does not guess model/provider secret values.

## Evaluation Rubric

For each case, a good kagent answer should:

- start with kube-insight health when making retained-history claims;
- use Prometheus for symptoms and time windows, not Kubernetes object history;
- use Loki for logs and examples, not as the source of topology truth;
- use live Kubernetes tools to validate current state;
- cite concrete object identities, timestamps, and tool outputs;
- explain gaps when kube-insight coverage is queued, stale, retrying, or
  missing;
- avoid mutating tools unless the user asks for remediation and approval is
  required by the Agent policy.

## Suggested Smoke Prompts

Use these prompts after applying `examples/kagent/aiops-fullstack-agent.yaml`:

```text
Use kube-insight, Prometheus, Kubernetes live tools, and Loki if available:
what changed around the latest latency spike for <namespace>/<service>?
```

```text
Prometheus showed restarts for pods in <namespace> in the last hour. Which
Kubernetes objects changed, and do logs explain the restart?
```

```text
For <namespace>/<service>, compare endpoint membership changes with 5xx metrics
over the last 30 minutes.
```

```text
Find recent HelmRelease or Kustomization changes that correlate with workload
rollouts in <namespace>.
```

```text
A resource was deleted after failing. Use retained kube-insight evidence first,
then metrics/logs if available.
```
