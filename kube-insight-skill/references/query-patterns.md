# Query Patterns

For ClickHouse-compatible backends, prefer the schema recipes returned by
`kube_insight_schema`. These patterns show the intended shape; adapt table and
column names only after reading the active schema.

The examples intentionally avoid reusing a source column name as an aggregate
alias. In ClickHouse, `max(updated_at) as updated_at` next to
`argMax(status, updated_at)` can be rewritten as a nested aggregate. Use aliases
such as `latest_update` or `last_seen` instead.

## Fuzzy Cluster Resolution

When the user gives a cluster fragment or nickname such as `gcp2`, do not put
that value directly into `cluster_id = ...`. First call health/coverage without
a cluster filter and match the fragment against returned `cluster_id`, display
name, context name, or source labels. Only after resolution should SQL use the
stable `cluster_id`. If multiple clusters match, either return per-cluster
results or ask the user to disambiguate.

## Coverage

```sql
with latest as (
  select cluster_id, api_group, api_version, resource, kind,
         argMax(status, updated_at) as status,
         argMax(error, updated_at) as error,
         max(updated_at) as latest_update
  from ingestion_offsets
  group by cluster_id, api_group, api_version, resource, kind
)
select cluster_id, api_group, api_version, resource, kind, status, error, latest_update
from latest
where status in ('not_started','queued','retrying','list_error','watch_error')
order by latest_update desc
limit 50;
```

## Recent Fact Rollup

```sql
select cluster_id, kind, namespace, name, fact_key, fact_value,
       count() as rows,
       min(ts) as first_seen,
       max(ts) as last_seen
from facts
where ts >= toDateTime64('2026-05-25 00:00:00', 3, 'UTC')
  and kind = 'Pod'
  and fact_key = 'pod_status.last_reason'
  and fact_value = 'OOMKilled'
group by cluster_id, kind, namespace, name, fact_key, fact_value
order by rows desc, last_seen desc
limit 50;
```

## Exact Recent Changes

```sql
select cluster_id, kind, namespace, name, change_family, path, severity,
       count() as changes,
       min(ts) as first_seen,
       max(ts) as last_seen,
       any(old_scalar) as sample_old,
       any(new_scalar) as sample_new
from changes
where ts >= toDateTime64('2026-05-25 00:00:00', 3, 'UTC')
  and kind = 'Deployment'
  and namespace = 'default'
  and name = 'api'
group by cluster_id, kind, namespace, name, change_family, path, severity
order by last_seen desc, changes desc
limit 50;
```

If this answers an exact recent-change question, stop. Do not expand to Pods,
Events, topology, OOM, root cause, or spec-only follow-up unless the user asks.

## Allocation Or Requests/Limits

Use the schema recipe named `container_resource_allocation_rollup` when present.
If facts do not carry resource configuration, use one `observations.doc`
profile or recipe such as `raw_doc_field_profile`, scoped by the user's
cluster/namespace/kind filters before fetching proof rows. If the user did not
scope one exact cluster, keep `cluster_id` in rows and do not silently pick the
first cluster from health output.
Avoid repeated JSON syntax probing.

## Namespace Resource Ranking And Time Buckets

For top namespace by Pod resources, resource-change ranking, or peak Pod-count
time, export bounded row-level evidence when code-shaped aggregation is clearer
than plain SQL. External agents should use their strongest available processing
environment, such as Python, Bash+jq, DuckDB, R, notebooks, or another local
tool. kube-insight's `kube_insight_js` is a built-in fallback for MCP-only
agents, not the preferred environment when richer local tooling exists.

If the user did not scope one exact cluster, keep all dataful clusters, preserve
`cluster_id`, and return either a per-cluster ranking or an explicitly labeled
global ranking. Do not silently select the first or healthiest cluster. If only
one cluster has resource rows in the queried window, say the ranking is based on
that cluster's available rows. In final tables, show CPU in cores or millicores
and memory/storage in MiB/GiB/TiB; prefer `GiB` labels for binary Gi quantities
unless quoting raw Kubernetes strings.

When practical, include tightly related profile, proof, and export queries
together to reduce latency and context size. Separate focused SQL/export steps
are acceptable when they improve planning or keep intermediate data bounded;
avoid repeating the same reconstruction unless the first result failed, was
empty, was truncated, or missed a field the user explicitly required.
Do not run another SQL/export/tool step only to format, translate, hard-code, or
re-present rows from a successful prior result. Write the final Markdown table
directly from the existing evidence.
The `maxQueries`, `sqlAll`, `scratch.write`, and `scratch.load` details apply
only when using kube-insight's built-in JavaScript tool. If the external agent
has local files, prefer exporting rows to its filesystem and processing them
there.
When using the built-in JS fallback, usually omit `maxQueries`; the default is
the safer choice for profile plus proof scripts. If you set it explicitly, count
every `sql()` call and every `sqlAll()` item first. Use
`sqlAll([{name, sql, maxRows}, ...])` for independent reads so the interpreter
can run them concurrently inside one bounded tool call. Never name a SQL string
variable `sql`; it shadows the helper. Use `query`, `sqlText`, or a domain name
such as `lifecycleSQL`, then call `sql(query, maxRows)`.
For current-state, historical lookback, ranking, aggregation, or absence
claims, do not start directly with schema/SQL/export. Call `kube_insight_health`
first or in the same initial tool group so collector coverage and available
cluster IDs are in context before data interpretation.
When a query returns large reusable JSON inside `kube_insight_js`, write it to
session scratch with `scratch.write('/some/path.json', value, metadata)` and
return the handle plus the final top rows. Later JS steps should use
`scratch.load(path)` to load the full JSON value for in-script aggregation. Use
`scratch.read(path, options)` only for metadata, preview, or chunked content.
Do not return broad raw arrays such as `{history: history}` or `{rows: rows}`;
aggregate them, slice to proof rows, write them to local files, or return a
scratch handle.

Resource ranking needs row-level container resources from latest non-deleted Pod
snapshots. Do not multiply sample request strings from rollup rows; samples are
for display only. If a CTE defines `argMax(doc, observed_at) as latest_doc`,
filter `latest_doc` in `HAVING` or an outer SELECT, not in `WHERE`. Pod-count
peaks need object-state reconstruction per bucket from lifecycle rows plus a
baseline, not `countDistinct(uid)` over raw observations. Use an event-sweep
algorithm: sorted `+1`/`-1` lifecycle events, one cumulative pass, then
moving-pointer or binary-search bucket lookup. Avoid `buckets.map(...)` with an
inner scan over all Pods because that is `O(bucket_count*object_count)` and can
timeout in constrained JavaScript interpreters.

For namespace resource deltas, use `namespace_resource_delta_for_js` when the
schema exposes it, or use its SQL shape as an export source for external
processing. The computed result should contain start/end values, absolute
deltas, percentage deltas, new/existing namespace classification, and the final
top ranking when available. Keep this as an allocation/configuration
comparison; do not pivot to `changes` or status-event volume unless the user
asks for churn rather than resource allocation.
Query Pod coverage/min/max timestamps and choose usable start/end snapshot
windows from the observed data. Do not assume fixed one-hour or half-day
windows. Compute `startWindow` and `endWindow` variables from coverage before
constructing start/end snapshot SQL. If coverage is per cluster, choose the
dataful cluster/window before comparing snapshots.

## Scheduling And Pending Pods

Pending is broader than scheduling failure. Use facts to find candidates, then
retained Pod docs to extract `status.conditions[].message` for `PodScheduled`:

```sql
select namespace, name, object_id,
       countIf(fact_key = 'pod_status.phase' and fact_value = 'Pending') as pending_rows,
       countIf(fact_key = 'status_condition.PodScheduled.reason'
               and fact_value = 'Unschedulable') as unschedulable_rows,
       min(ts) as first_seen,
       max(ts) as last_seen
from facts
where cluster_id = 'CLUSTER_ID'
  and kind = 'Pod'
  and ts >= toDateTime64('2026-05-26 00:00:00', 3, 'UTC')
  and ts < toDateTime64('2026-05-28 00:00:00', 3, 'UTC')
  and (
    (fact_key = 'pod_status.phase' and fact_value = 'Pending')
    or (fact_key = 'status_condition.PodScheduled.reason'
        and fact_value = 'Unschedulable')
    or (fact_key = 'status_condition.PodScheduled' and fact_value = 'False')
  )
group by namespace, name, object_id
having pending_rows > 0 or unschedulable_rows > 0
order by unschedulable_rows desc, pending_rows desc, last_seen desc
limit 500;
```

For current Pending, collapse to latest non-deleted Pod snapshots and inspect
`PodScheduled`. A current Pending Pod with `PodScheduled=True` is not a
scheduler failure; report init/image-pull/container waiting reasons separately.

## Ready Condition Transitions

To prove `Ready` became `False`, compare ordered retained observations. Do not
depend on `changes.old_scalar` if it is empty or absent:

```sql
with ready as (
  select observed_at, kind, namespace, name, uid, resource_version,
         JSONExtractString(cond, 'status') as ready_status,
         JSONExtractString(cond, 'reason') as ready_reason,
         JSONExtractString(cond, 'message') as ready_message
  from (
    select observed_at, kind, namespace, name, uid, resource_version,
           arrayJoin(JSONExtractArrayRaw(doc, 'status', 'conditions')) as cond
    from observations
    where cluster_id = 'CLUSTER_ID'
      and kind in ('Pod', 'Node')
      and observed_at >= toDateTime64('2026-05-26 00:00:00', 3, 'UTC')
      and observed_at < toDateTime64('2026-05-28 00:00:00', 3, 'UTC')
      and position(doc, '"Ready"') > 0
  )
  where JSONExtractString(cond, 'type') = 'Ready'
), sequenced as (
  select *,
         lagInFrame(ready_status) over (
           partition by kind, namespace, name, uid
           order by observed_at, resource_version
           rows between unbounded preceding and current row
         ) as prev_status
  from ready
)
select kind, ready_reason,
       count() as transitions,
       countDistinct(uid) as objects,
       min(observed_at) as first_seen,
       max(observed_at) as last_seen
from sequenced
where ready_status = 'False'
  and prev_status != ''
  and prev_status != 'False'
group by kind, ready_reason
order by transitions desc;
```

If the first row in the selected sequence is already `False`, report it as
"observed False at window start" or extend the lookback for baseline rows.

## Endpoint Readiness

For "Services without ready endpoints", collapse Service and EndpointSlice to
latest non-deleted snapshots. Group EndpointSlices by
`metadata.labels["kubernetes.io/service-name"]`, sum endpoints where
`conditions.ready` is true, and keep `ExternalName` Services separate. Use
`kubectl` only as optional current-state validation; kube-insight snapshots are
the evidence source for retained state and coverage.

Do not rely on a plain `arrayJoin(JSONExtractArrayRaw(doc, 'endpoints'))`
without preserving slice rows first: an EndpointSlice with an empty endpoints
array is still evidence that the Service currently has zero endpoints, and a
plain `arrayJoin` can drop that row. Track `slice_count`, `endpoint_count`, and
`ready_endpoint_count`, and classify results as selector-backed Service,
ExternalName, or non-ExternalName without selector.

## Pod Lifecycle Churn

Pod churn should use lifecycle rows, not raw observation counts:

```sql
select namespace,
       countIf(observation_type = 'ADDED') as pod_created,
       countIf(observation_type = 'DELETED') as pod_deleted,
       count() as lifecycle_events,
       min(observed_at) as first_event,
       max(observed_at) as last_event
from observations
where cluster_id = 'CLUSTER_ID'
  and kind = 'Pod'
  and observation_type in ('ADDED', 'DELETED')
  and observed_at >= toDateTime64('2026-05-25 00:00:00', 3, 'UTC')
  and observed_at < toDateTime64('2026-05-28 00:00:00', 3, 'UTC')
group by namespace
order by lifecycle_events desc, pod_created desc, pod_deleted desc
limit 20;
```

For "is it CronJob?", export Pod ownerReferences and Job ownerReferences. Join
Pod -> Job -> CronJob by UID or namespace/name, then compute owner-kind shares
and lifetimes outside SQL. Do not infer CronJob from name alone; DaemonSet
replacement during node churn can look like short-lived task churn.

For "Pod count peak" or point-in-time Pod totals, use a state reconstruction
over observations rather than this lifecycle rollup. Build a baseline from each
Pod UID's latest pre-window observation, then sweep all in-window
ADDED/MODIFIED/DELETED rows. Treat ADDED/MODIFIED as present and DELETED as
absent. This handles windows where existing Pods only emit MODIFIED rows.

## Quantity Parsing Outside SQL

Prefer parsing Kubernetes quantity strings outside SQL. SQL should return
strings such as `500m`, `4`, `16380048Ki`, `10Gi`, or `1Ti`; Python, JS,
Bash+jq, DuckDB UDFs, or another local processor should normalize them. This JS
snippet is only an example:

```js
function cpuCores(v) {
  if (v == null || v === "") return 0;
  const s = String(v);
  if (s.endsWith("m")) return Number(s.slice(0, -1)) / 1000;
  return Number(s);
}

function bytes(v) {
  if (v == null || v === "") return 0;
  const s = String(v);
  const m = s.match(/^([0-9.]+)(Ki|Mi|Gi|Ti|K|M|G|T)?$/);
  if (!m) return Number(s) || 0;
  const n = Number(m[1]);
  const unit = m[2] || "";
  const bin = {Ki: 2**10, Mi: 2**20, Gi: 2**30, Ti: 2**40};
  const dec = {K: 1e3, M: 1e6, G: 1e9, T: 1e12};
  return n * (bin[unit] || dec[unit] || 1);
}

function gib(v) {
  return bytes(v) / (2**30);
}
```

Do not divide `JSONExtractString(...)` values inside SQL. Mixed units and empty
strings make SQL-side string arithmetic brittle.

## PVC Storage Resize Deltas

For PVC resize or storage expansion questions, especially follow-ups such as
"from how much to how much", inherit the PVC names from the previous answer or
from resize facts. If target names are unknown, use the schema recipe
`pvc_resize_candidates_for_js` when present, then use
`pvc_storage_history_for_js` for ordered proof rows. ClickHouse `changes` rows
have `object_id` but no `uid`; get `uid` from `observations` or `versions` only
when needed. A good flow exports bounded PVC observation rows, collapses
adjacent equal storage values, converts units, and returns answer-ready
before/after deltas. Use Python, Bash+jq, DuckDB, or the built-in
`kube_insight_js` fallback for that processing. If a candidate query over
`changes` or `facts` is needed, it can be a focused SQL/export step; avoid
returning broad raw candidate pages to the model.

```sql
select cluster_id, namespace, name, uid, observed_at, observation_type,
       JSONExtractString(doc, 'spec', 'resources', 'requests', 'storage') as requested_storage,
       JSONExtractString(doc, 'status', 'capacity', 'storage') as capacity_storage,
       JSONExtractString(doc, 'status', 'phase') as phase
from observations
where kind = 'PersistentVolumeClaim'
  and observed_at >= toDateTime64('2026-05-20 00:00:00', 3, 'UTC')
  and observed_at < toDateTime64('2026-05-27 00:00:00', 3, 'UTC')
  and name in ('PVC_NAME_1', 'PVC_NAME_2')
  and position(doc, '"storage"') > 0
order by cluster_id, namespace, name, uid, observed_at
limit 2000;
```

Do not return raw history pages to the model unless the user asks for raw
proof. If external processing already returned PVC records with before/after
storage values, stop tool use and answer; do not add a broad lag/window SQL
query that streams raw PVC rows back into context. Pair `requested_storage` and
`capacity_storage` changes into one unique PVC-level resize record. Compute fields such as
`before_requested_gib`, `after_requested_gib`, `delta_gib`,
`before_observed_at`, and `after_observed_at`, then answer from that compact
result instead of re-querying to merge field-level events.

## Node Capacity And Allocatable

For node capacity questions, Kubernetes stores the useful values under Node
`status.capacity` and `status.allocatable`, not `spec`. For current inventory,
first collapse observations to the latest non-deleted Node snapshot. This avoids
counting deleted nodes or summing repeated fact rows from a time window:

```sql
with latest_nodes as (
  select cluster_id, name, uid,
         argMax(doc, observed_at) as doc,
         argMax(observation_type, observed_at) as last_type,
         max(observed_at) as last_seen
  from observations
  where cluster_id = 'CLUSTER_ID'
    and kind = 'Node'
  group by cluster_id, name, uid
), parsed as (
  select cluster_id, name, uid, last_seen,
         JSONExtractString(doc, 'metadata', 'labels', 'node.kubernetes.io/instance-type') as instance_type,
         JSONExtractString(doc, 'status', 'capacity', 'cpu') as capacity_cpu,
         JSONExtractString(doc, 'status', 'capacity', 'memory') as capacity_memory,
         JSONExtractString(doc, 'status', 'allocatable', 'cpu') as allocatable_cpu,
         JSONExtractString(doc, 'status', 'allocatable', 'memory') as allocatable_memory
  from latest_nodes
  where last_type != 'DELETED'
)
select cluster_id, name, uid, instance_type, last_seen,
       capacity_cpu, capacity_memory, allocatable_cpu, allocatable_memory
from parsed
limit 50;
```

Parse the Kubernetes quantity strings outside SQL before grouping by
`instance_type`. Use Python/Bash/JS/DuckDB as available. This is safer than
doing string replacement and division inside ClickHouse SQL, especially for
mixed units such as `m`, `Ki`, `Mi`, `Gi`, and `Ti`.

For recent node lifecycle changes, locate ADDED/DELETED observations first and
then enrich each event from following observations of the same Node. Some
Kubernetes watchers observe an initial ADDED document before labels such as
`node.kubernetes.io/instance-type` are complete.

```sql
with lifecycle as (
  select cluster_id, observation_type, name, uid,
         min(observed_at) as first_seen,
         max(observed_at) as last_seen
  from observations
  where cluster_id = 'CLUSTER_ID'
    and kind = 'Node'
    and observation_type in ('ADDED', 'DELETED')
    and observed_at >= toDateTime64('2026-05-26 04:00:00', 3, 'UTC')
    and observed_at < toDateTime64('2026-05-26 16:00:00', 3, 'UTC')
  group by cluster_id, observation_type, name, uid
), label_docs as (
  select l.cluster_id, l.observation_type, l.name, l.uid, l.first_seen, l.last_seen,
         argMaxIf(o.doc, o.observed_at,
           position(o.doc, 'node.kubernetes.io/instance-type') > 0
         ) as doc
  from lifecycle as l
  left join observations as o
    on o.cluster_id = l.cluster_id
   and o.kind = 'Node'
   and o.name = l.name
   and o.observed_at >= l.first_seen - interval 30 minute
   and o.observed_at <= l.first_seen + interval 60 minute
  group by l.cluster_id, l.observation_type, l.name, l.uid, l.first_seen, l.last_seen
)
select observation_type, name, uid,
       JSONExtractString(doc, 'metadata', 'labels', 'node.kubernetes.io/instance-type') as instance_type,
       JSONExtractString(doc, 'metadata', 'labels', 'cloud.google.com/gke-nodepool') as nodepool,
       JSONExtractString(doc, 'metadata', 'creationTimestamp') as creation_timestamp,
       first_seen,
       last_seen
from label_docs
order by first_seen
limit 100;
```

When summarizing the lifecycle rows, compute `added_count`, `deleted_count`,
and `net_delta = added_count - deleted_count`. Do not say the node count was
stable unless the counts match or a separate before/after snapshot proves it.
If `instance_type` or `nodepool` is still empty, fill from same-name current
Node snapshot rows in your external processor, then run one focused fallback
query by unresolved node names before surfacing `unknown`.

If using facts instead, first take latest values per node and fact key, then
aggregate. Never sum all fact rows in a time window:

```sql
with latest as (
  select
    cluster_id,
    name,
    fact_key,
    argMax(numeric_value, ts) as value
  from facts
  where kind = 'Node'
    and ts >= toDateTime64('2026-05-26 04:00:00', 3, 'UTC')
    and fact_key in (
      'node_capacity.cpu',
      'node_capacity.memory',
      'node_allocatable.cpu',
      'node_allocatable.memory'
    )
  group by cluster_id, name, fact_key
),
per_node as (
  select
    cluster_id,
    name,
    maxIf(value, fact_key = 'node_capacity.cpu') as capacity_cpu_cores,
    maxIf(value, fact_key = 'node_capacity.memory') as capacity_memory_bytes,
    maxIf(value, fact_key = 'node_allocatable.cpu') as allocatable_cpu_cores,
    maxIf(value, fact_key = 'node_allocatable.memory') as allocatable_memory_bytes
  from latest
  group by cluster_id, name
)
select
  cluster_id,
  count() as node_count,
  sum(capacity_cpu_cores) as total_capacity_cpu_cores,
  sum(capacity_memory_bytes) as total_capacity_memory_bytes,
  sum(allocatable_cpu_cores) as total_allocatable_cpu_cores,
  sum(allocatable_memory_bytes) as total_allocatable_memory_bytes
from per_node
group by cluster_id
order by cluster_id
limit 20;
```

If older retained data does not have these facts, use one scoped raw-doc proof
query over Node `observations` or `versions` and extract
`status.capacity/status.allocatable` snippets. Do not infer CPU or memory from
node names, machine pool labels, or instance type strings unless the raw Node
document proves the capacity.
