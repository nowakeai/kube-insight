# External Agent Recipes

These recipes are minimum evidence plans for generic agents using kube-insight
without the built-in agent. Replace timestamps, cluster IDs, namespace/name, and
database paths with values from the user request, client context, and active
schema. If MCP is unavailable, use equivalent CLI commands.

For HTTP-only dev or external deployments, prefer these stable API paths:
`GET /api/v1/server/info`, `GET /api/v1/health?detail=full&limit=500`,
`GET /api/v1/schema`, and `POST /api/v1/sql`. If server info reports MCP is
disabled, continue with HTTP rather than probing `/mcp` variants.
If the API is reachable only inside the cluster, use normal cluster access such
as `kubectl port-forward`; kubectl can also validate live current state. Keep
kube-insight as the source for retained history, coverage, and historical
aggregation claims.

Always start with coverage for current-state, absence, ranking, historical, or
fuzzy-cluster questions. If a user supplied a cluster fragment such as `gcp2`,
resolve it from health/coverage before using `cluster_id` in SQL.

## Fuzzy Cluster Node Inventory And Capacity

User intent: "看看 gcp2 半天内集群的节点是否有变化？一共有多少节点，有哪些类型？总的 CPU 内存量是多少"

Plan:

1. Convert "half day" to exact UTC bounds from client time.
2. Call `kube_insight_health` with no cluster filter. Resolve the fragment
   against available `cluster_id`, display name, and context.
3. Call `kube_insight_schema`.
4. Export bounded rows for:
   - latest non-deleted Node snapshot for current count/capacity;
   - recent Node ADDED/DELETED lifecycle rows in the requested window;
   - start-of-window and end-of-window latest non-deleted Node snapshots when
     the user asks whether the cluster changed;
   - optional fallback by affected node names if lifecycle labels are missing.
5. Parse CPU/memory quantity strings and group by instance type with the
   strongest available local tool, such as Python, Bash+jq, DuckDB, or
   kube-insight's built-in `kube_insight_js` fallback.

Do not query `cluster_id = 'gcp2'` unless health proves that exact ID exists.
Do not infer CPU/memory from node names or instance types. Use
`status.capacity` and `status.allocatable` from Node docs.
Report churn separately from net change: lifecycle rows prove replacement or
flapping, while start/end snapshots prove net node count, name, UID, and
capacity changes.

See `query-patterns.md` sections `Node Capacity And Allocatable` and
`Quantity Parsing Outside SQL`.

## Namespace Resource Ranking

User intent: "帮我看看哪个 namespace pod 资源占用最高"

Plan:

1. `kube_insight_health` to identify dataful clusters and Pod coverage.
2. `kube_insight_schema`.
3. Use schema recipe `pod_resource_rows_for_js` or equivalent scoped raw-doc
   SQL from latest non-deleted Pod snapshots. Despite the recipe name, external
   agents may export these rows and process them with Python, Bash+jq, DuckDB,
   or another local tool.
4. Parse request/limit quantities per container, sum by namespace, sort top
   rows, and return totals plus proof row counts.

If no cluster is specified, return per-cluster rankings or clearly labeled
global rankings. This is allocation/configuration, not actual usage; say so.

## Namespace Resource Delta

User intent: "帮我看看过去几天哪个 namespace 资源占用有较大变化"

Plan:

1. Convert "past few days" into absolute UTC bounds.
2. `kube_insight_health` and `kube_insight_schema`.
3. Profile Pod observation coverage per dataful cluster.
4. Choose start/end snapshot windows from observed data, not a fixed
   one-hour guess.
5. Use schema recipe `namespace_resource_delta_for_js` when present, or two
   latest-per-Pod snapshot queries for start/end windows. External agents may
   export the rows and process them with their normal data tools.
6. Compute start/end CPU and memory requests/limits, absolute deltas, percent
   deltas, new/removed namespace classification, and top changes.

Do not pivot to changes volume or Events unless the user asks for churn rather
than allocation/configuration.

## Pod Count Peak

User intent: "帮我看看过去一周 pod 数量最多的时间点是什么时候"

Plan:

1. Convert the week to UTC bounds.
2. `kube_insight_health` and `kube_insight_schema`.
3. Export retained Pod observation rows and run a state sweep in Python, JS,
   DuckDB, or any other reliable local processor:
   - baseline active Pods before the window from each Pod UID's latest prior
     observation;
   - ADDED/MODIFIED/DELETED rows in the window;
   - `ADDED` and `MODIFIED` mean the UID exists after that timestamp;
   - `DELETED` means the UID no longer exists after that timestamp;
   - bucket or exact-event peak calculation.
4. Return the peak timestamp/bucket, count, cluster/namespace grouping, and
   truncation/coverage caveats.

Do not use `countDistinct(uid)` over raw observations as the peak. Observation
frequency is not object count.
Use a pure ADDED/DELETED lifecycle sweep only after confirming lifecycle
coverage is complete for the window. In real retained data, current Pods may
only have MODIFIED rows inside the window, and lifecycle-only sweeps can
severely undercount.

## PVC Storage Resize Deltas

User intent: "最近一周哪些 PVC 扩容了？从多少到多少？"

Plan:

1. Convert time range to UTC bounds.
2. `kube_insight_health` and `kube_insight_schema`.
3. Use `pvc_resize_candidates_for_js` when present, or a focused
   facts/changes candidate query.
4. Use `pvc_storage_history_for_js` or scoped PVC observation rows for candidate
   names.
5. Collapse adjacent equal values, pair request/capacity transitions, and
   return `before`, `after`, `delta`, `first_seen`, and `last_seen`.

Stop after compact before/after records exist. Do not stream all PVC history
back into the model unless raw proof is requested.

## Scheduling Failures And Pending Pods

User intent: "最近有哪些 Pending/调度失败的 Pod？最常见原因是什么？"

Plan:

1. Convert the window to UTC and resolve the cluster from health.
2. Check Pod coverage and schema.
3. Use facts to find candidates with `pod_status.phase=Pending` and
   `status_condition.PodScheduled.reason=Unschedulable`.
4. Fetch retained Pod status docs for those candidates and parse the
   `PodScheduled` condition `message`.
5. Count reasons by object, not only by observation row: affinity/selector
   mismatch, untolerated taints, insufficient CPU, insufficient memory, node
   unschedulable, and preemption notes.
6. For current Pending Pods, collapse to latest non-deleted Pod snapshots and
   separate `PodScheduled=True` initialization/image-pull cases from real
   `Unschedulable` cases.

Do not answer "调度失败" from `Pending` alone. Many Pods are briefly Pending
while init containers run or images pull. Events may be useful when covered, but
retained Pod conditions are enough to prove historical scheduler messages when
Events are not collected.

## Ready Became False

User intent: "过去几天 Pod 或 Node Ready condition 有没有变成 False？"

Plan:

1. Resolve time, cluster, and coverage for Pod/Node streams.
2. Export ordered retained observations containing `status.conditions`.
3. Extract the `Ready` condition and compute previous status with a window
   function or external processor keyed by kind/namespace/name/uid.
4. Count only rows where current `Ready=False` and previous status exists and
   was not `False`.
5. Report "initially observed as False" separately; that proves observed state,
   not a transition.

Do not rely on `changes.old_scalar` alone when old values are empty. Avoid
exporting huge raw condition pages into the model; aggregate transitions in SQL,
Python, DuckDB, or JS and return compact proof rows.

## Services Without Ready Endpoints

User intent: "当前有哪些 Service 没有 ready endpoints？"

Plan:

1. Resolve cluster and check fresh Service, EndpointSlice, and Pod coverage.
2. Collapse Service observations to latest non-deleted snapshots.
3. Collapse EndpointSlice observations to latest non-deleted snapshots and
   aggregate ready endpoints by Service name/namespace.
4. Treat `ExternalName` Services separately because they do not normally have
   EndpointSlices.
5. Optionally use `kubectl get endpointslice/service` as live validation, but
   cite kube-insight snapshots for the retained evidence path.

Avoid showing empty 0-row evidence as a finding. A Service with no selector or
an ExternalName Service needs a different interpretation from a selector-backed
Service with zero ready endpoints.

## Pod Lifecycle Churn

User intent: "哪些 namespace Pod 创建/删除最频繁？是否主要来自 CronJob 或短生命周期任务？"

Plan:

1. Resolve cluster and check Pod, Job, and CronJob coverage.
2. Export Pod `ADDED`/`DELETED` lifecycle rows for the window.
3. Parse Pod ownerReferences. For Job-owned Pods, join the Job's ownerReference
   to identify CronJobs.
4. Compute namespace create/delete counts, owner-kind attribution, top owners,
   and lifetime from Pod creation timestamp to deletion observation.
5. Report CronJob, DaemonSet, ReplicaSet, StatefulSet, and unowned shares
   separately.

Do not assume high churn is CronJob-driven. DaemonSet replacement during node
churn can dominate lifecycle volume, while CronJobs may dominate specific
namespaces.

## OOM Existence

User intent: "Was there an OOM recently?" or a follow-up such as "what about the
last hour?"

MCP plan:

```json
[
  {"tool": "kube_insight_health", "arguments": {}},
  {
    "tool": "kube_insight_search",
    "arguments": {
      "query": "OOMKilled",
      "kind": "Pod",
      "from": "2026-05-26T14:00:00Z",
      "to": "2026-05-26T15:00:00Z",
      "includeBundles": true,
      "limit": 5
    }
  }
]
```

Stop after the search. If it returns matches, answer with Pod identity,
timestamps, fact/change evidence, and coverage caveats. If it returns zero and
coverage is incomplete, say kube-insight cannot prove absence for uncovered
streams.

CLI equivalent:

```bash
./bin/kube-insight db resources health --db kubeinsight.db --errors-only
./bin/kube-insight query search OOMKilled --db kubeinsight.db \
  --kind Pod \
  --from 2026-05-26T14:00:00Z \
  --to 2026-05-26T15:00:00Z \
  --include-bundles \
  --limit 5
```

## OOM Or Restart Ranking

User intent: "哪个 namespace/Pod OOM 或 restart 最多？"

Plan:

1. `kube_insight_health`.
2. `kube_insight_schema`.
3. For OOM, inspect retained Pod `status.containerStatuses[]` and filter
   `state.terminated.reason` or `lastState.terminated.reason` by termination
   `finishedAt` inside the user window. A `lastState` observed in the window may
   describe an old termination.
4. For restarts, compare per-container `restartCount` min/max within the window
   and aggregate the positive deltas. Do not mix containers in a Pod-level
   min/max.
5. Return top namespaces/Pods with first/last seen, affected Pods/containers,
   restart deltas, and proof rows.

If direct fact rows disagree with retained Pod status, prefer retained
`containerStatuses[]` proof because it carries container name, restart count,
and termination timestamps needed for a bounded answer.

Do not run many synonym searches. Use schema/fact discovery when key names are
uncertain.

## Exact Recent Change

User intent: "What changed for this Deployment in the last 24 hours?"

MCP plan:

```json
[
  {"tool": "kube_insight_health", "arguments": {}},
  {"tool": "kube_insight_schema", "arguments": {}},
  {
    "tool": "kube_insight_sql",
    "arguments": {
      "maxRows": 50,
      "sql": "<one recent-change rollup SQL query shaped like query-patterns.md>"
    }
  }
]
```

After this rollup returns rows, answer only the observed changes and coverage.
Do not add Pods, Events, topology, OOM, or root-cause checks unless asked.

## Allocation Or Requests/Limits

User intent: "Show resource allocation, not actual usage."

Plan:

1. `kube_insight_health`.
2. `kube_insight_schema`.
3. Use schema recipe `container_resource_allocation_rollup` when present, after
   applying explicit cluster/namespace filters.
4. If facts do not carry request/limit keys, run one scoped raw-document profile
   and one proof query.

If the user did not scope one exact cluster, keep `cluster_id` in rows and do
not select only the first cluster from health output.

CLI equivalent:

```bash
./bin/kube-insight query schema --db kubeinsight.db
./bin/kube-insight query sql --db kubeinsight.db --max-rows 100 --sql \
  '<schema-provided container_resource_allocation_rollup SQL>'
```

## Exact Service Health

User intent: "Is namespace/name Service healthy? Are endpoints ready?"

MCP plan:

```json
[
  {"tool": "kube_insight_health", "arguments": {}},
  {
    "tool": "kube_insight_service_investigation",
    "arguments": {"namespace": "default", "name": "api"}
  }
]
```

This is terminal when the bundle includes Service, EndpointSlice, Pod, and Event
evidence relevant to the question. Do not add search, schema, SQL, or topology
unless that bundle is missing the requested proof.

CLI equivalent:

```bash
./bin/kube-insight db resources health --db kubeinsight.db --errors-only
./bin/kube-insight query service api --db kubeinsight.db \
  --namespace default \
  --from 2026-05-26T14:00:00Z \
  --to 2026-05-26T15:00:00Z \
  --max-evidence-objects 20 \
  --max-versions-per-object 2
```
