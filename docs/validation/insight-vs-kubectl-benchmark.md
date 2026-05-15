# kube-insight vs direct kubectl for agent investigations

Last updated: 2026-05-15.

This report compares two ways an agent can investigate Kubernetes state:

- direct `kubectl` access to the live apiserver,
- kube-insight queries over retained, sanitized facts, edges, observations, and
  versions.

The point is not that every kube-insight query is universally faster than every
`kubectl` command. The point is that many agent investigations need repeated
filtering, joins, and historical context. kube-insight moves those operations to
a pre-extracted evidence layer, so the agent can query a smaller and safer
surface.

## Executive Summary

Before the timed comparison, kube-insight ran a bounded watcher refresh against
the same cluster context used by `kubectl`. The timed query phase did not
include watcher startup/list time; in service deployments the watcher should run
continuously. The timed kube-insight queries completed in **24-215 ms**.
Comparable direct `kubectl` operations took **3,104-5,745 ms** because they had
to ask the live apiserver for current Events or broad resource lists.

| Scenario | kube-insight | kubectl | Speedup |
| --- | ---: | ---: | ---: |
| Retained PolicyViolation Event count | 215 ms | 3,214 ms | 14.9x |
| Event to affected resource investigation | 26 ms | 3,307 ms | 127.2x |
| Event message keyword search | 24 ms | 3,794 ms | 158.1x |
| Service topology candidate list | 32 ms | 3,104 ms | 97.0x |
| Workload inventory for scope selection | 26 ms | 5,745 ms | 221.0x |

Simple latency chart, lower is better:

```text
Retained PolicyViolation Event count
  kube-insight  215 ms | █
  kubectl      3214 ms | █████████████

Event to affected resource investigation
  kube-insight   26 ms | █
  kubectl      3307 ms | █████████████

Event message keyword search
  kube-insight   24 ms | █
  kubectl      3794 ms | ███████████████

Service topology candidate list
  kube-insight   32 ms | █
  kubectl      3104 ms | ████████████

Workload inventory for scope selection
  kube-insight   26 ms | █
  kubectl      5745 ms | ███████████████████████
```

Each bar block is about 250 ms, with one block used as the minimum visible bar.

## Why This Matters for Agents

Direct `kubectl` is excellent for current-state checks, but it is a broad live
cluster interface. An agent using only `kubectl` must repeatedly:

- list live objects from the apiserver,
- parse raw Kubernetes payloads,
- reconstruct joins such as Event -> involved object or Service ->
  EndpointSlice -> Pod,
- handle missing history when Events have expired,
- receive whatever raw fields the live API returns.

kube-insight changes the agent workflow:

- filters run before storage, so sensitive fields can be removed or redacted
  before the agent queries them,
- destructive filter decisions are auditable,
- facts and edges are pre-extracted for common troubleshooting paths,
- retained versions and observations provide proof after live state has moved
  on,
- SQL/MCP read surfaces are intentionally read-only,
- service mode is designed to support Kubernetes authorization-aware query
  boundaries.

## Reproduce

For current-state comparisons, refresh kube-insight from the same cluster
context immediately before the timed benchmark. For a one-shot validation run, a
bounded watcher is enough to update the local evidence database and record
collector coverage:

```bash
make build
./bin/kube-insight watch \
  --db kubeinsight.db \
  --client-go \
  --context <kubectl-context> \
  --timeout 90s

./bin/kube-insight db resources health --db kubeinsight.db --stale-after 10m
```

For production comparisons, keep `watch` running continuously and check
collector health before trusting absence claims.

Then run the multi-scenario helper:

```bash
./scripts/benchmark-agent-vs-kubectl.sh \
  kubeinsight.db \
  <kubectl-context> \
  testdata/generated/agent-vs-kubectl-latest
```

The script writes:

- `summary.tsv`: one row per scenario,
- one kube-insight output file per scenario,
- one kubectl output file per scenario.

Useful environment variables:

- `INSIGHT_CLUSTER_ID`: numeric `clusters.id` in the local kube-insight DB.
- `INSIGHT_EVENT_KEYWORD`: Event message keyword used by the message-search
  scenario.
- `KUBE_INSIGHT_BIN`: path to the kube-insight binary.
- `KUBECTL_BIN`: path to kubectl.
- `GCLOUD_SDK_BIN`: optional path containing the GKE auth plugin.

## Freshness Check

This run used a bounded watcher refresh before the timed benchmark:

| Watch refresh result | Value |
| --- | ---: |
| Discovered resources | 241 |
| List/watch errors | 0 |
| Listed objects | 4,226 |
| Watch events during refresh | 43 |
| Stored observations | 4,764 |

The post-refresh health check reported 242 resource rows, 199 healthy, 42 queued,
0 errors, and 1 stale resource under a 10-minute staleness threshold. The core
resources used by these scenarios, including Events, Pods, Services,
EndpointSlices, and common workload controllers, were refreshed during the run.

## Dataset

The benchmark used a local kube-insight SQLite evidence database captured from a
sanitized GKE workload cluster and the current live apiserver for the same
cluster context.

| Data | Rows |
| --- | ---: |
| Objects | 34,241 |
| Versions | 42,101 |
| Facts | 265,711 |
| Edges | 97,670 |
| Changes | 45,365 |
| Filter decisions | 596 |
| Filter decision rollups | 865 |

Event facts:

| Fact | Rows |
| --- | ---: |
| `k8s_event.type` | 28,451 |
| `k8s_event.reason` | 28,451 |
| `k8s_event.message_preview` | 28,449 |
| `k8s_event.message_fingerprint` | 28,449 |
| `k8s_event.reporting_controller` | 27,513 |
| `k8s_event.reporting_instance` | 25,643 |
| `k8s_event.action` | 22,470 |

For retained `PolicyViolation` Warning Events, kube-insight had **22,470**
rows covering `2026-05-14 16:31:19` to `2026-05-15 04:17:52`. The live
apiserver returned **1,938** current `PolicyViolation` Warning Events at the
time of the run. This difference is expected: kube-insight is retaining
historical evidence while `kubectl` sees current apiserver state.

## Scenarios

### 1. Retained PolicyViolation Event count

Agent question:

> How many policy-admission Warning Events do we have, and what retained time
> window do they cover?

kube-insight operation:

- run one read-only SQL query over retained Event facts,
- return count plus first/latest retained timestamps.

kubectl operation:

- list current `events.events.k8s.io` from all namespaces,
- filter by `type=Warning,reason=PolicyViolation`,
- count returned names.

Result: **215 ms** with kube-insight, **3,214 ms** with `kubectl`.

### 2. Event to affected resource investigation

Agent question:

> Which Warning Events matter, and which Kubernetes objects were affected?

kube-insight operation:

- query retained Event reason facts,
- join Event objects through pre-extracted `object_edges`,
- return affected resource kind, namespace, name, and message preview.

kubectl operation:

- fetch current `PolicyViolation` Warning Events as JSON from the apiserver,
- leave object relationship extraction to the caller.

Result: **26 ms** with kube-insight, **3,307 ms** with `kubectl`.

### 3. Event message keyword search

Agent question:

> Which warning Event messages mention a specific policy or failure keyword?

kube-insight operation:

- search retained `k8s_event.message_preview` facts scoped by cluster,
- return recent matching Event rows.

kubectl operation:

- fetch current Warning Events as JSON,
- scan the payload text for the keyword.

Result: **24 ms** with kube-insight, **3,794 ms** with `kubectl`.

### 4. Service topology candidate list

Agent question:

> Which Services have EndpointSlices and Pod fan-out worth investigating?

kube-insight operation:

- use pre-extracted topology edges,
- aggregate Service -> EndpointSlice -> Pod relationships in one SQL query.

kubectl operation:

- fetch Services, EndpointSlices, and Pods from the live apiserver,
- leave relationship joins to the caller.

Result: **32 ms** with kube-insight, **3,104 ms** with `kubectl`.

### 5. Workload inventory for scope selection

Agent question:

> What is the workload/routing object scope before narrowing the incident?

kube-insight operation:

- count common workload and routing kinds from `latest_index`.

kubectl operation:

- list Pods, Services, Deployments, ReplicaSets, StatefulSets, DaemonSets, Jobs,
  CronJobs, and EndpointSlices across all namespaces.

Result: **26 ms** with kube-insight, **5,745 ms** with `kubectl`.

## Interpretation

The fastest kube-insight cases are not magic database tricks. They are the
result of doing the expensive investigation work once during ingestion:

- Kubernetes discovery maps resources into stable kinds and scopes.
- Filters normalize, redact, or discard fields before storage.
- Facts turn nested JSON status and Event data into indexed rows.
- Edges turn object relationships into joinable topology.
- Observations preserve when kube-insight saw an object even when content did
  not change.
- Versions preserve proof for final claims.

For agents, this means fewer live API calls, fewer raw payloads, and fewer
prompt-side joins. `kubectl` remains useful as a current-state comparison, but
it should not be the only investigation interface when the question needs
history, topology, or sanitized evidence.

## Guardrails

These numbers are point-in-time results from one sanitized workload cluster.
They should be used as validation evidence for the product shape, not as a
universal performance guarantee.

The scenarios are also not always semantically identical:

- kube-insight answers from retained evidence and can include history no longer
  visible to the apiserver.
- kubectl answers from current live state and may need additional client-side
  parsing or joins to reach the same final answer.

That semantic difference is part of the product value: kube-insight gives
agents a purpose-built evidence layer instead of asking them to reconstruct
history and relationships from raw live API calls.
