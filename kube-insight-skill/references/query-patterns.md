# Query Patterns

For ClickHouse-compatible backends, prefer the schema recipes returned by
`kube_insight_schema`. These patterns show the intended shape; adapt table and
column names only after reading the active schema.

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
If facts do not carry resource configuration, use one scoped `observations.doc`
profile or recipe such as `raw_doc_field_profile` before fetching proof rows.
Avoid repeated JSON syntax probing.

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
         toFloat64OrZero(JSONExtractString(doc, 'status', 'capacity', 'cpu')) as capacity_cpu_cores,
         toFloat64OrZero(replaceRegexpAll(JSONExtractString(doc, 'status', 'capacity', 'memory'), 'Ki$', '')) / 1048576 as capacity_memory_gib
  from latest_nodes
  where last_type != 'DELETED'
)
select cluster_id, instance_type,
       count() as nodes,
       sum(capacity_cpu_cores) as capacity_cpu_cores,
       round(sum(capacity_memory_gib), 2) as capacity_memory_gib
from parsed
group by cluster_id, instance_type
order by instance_type
limit 50;
```

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
), enriched as (
  select l.cluster_id, l.observation_type, l.name, l.uid, l.first_seen, l.last_seen,
         argMax(o.doc, o.observed_at) as doc
  from lifecycle as l
  left join observations as o
    on o.cluster_id = l.cluster_id
   and o.kind = 'Node'
   and o.name = l.name
   and o.uid = l.uid
   and o.observed_at >= l.first_seen
   and o.observed_at <= l.first_seen + interval 15 minute
  group by l.cluster_id, l.observation_type, l.name, l.uid, l.first_seen, l.last_seen
)
select observation_type, name, uid,
       JSONExtractString(doc, 'metadata', 'labels', 'node.kubernetes.io/instance-type') as instance_type,
       JSONExtractString(doc, 'metadata', 'labels', 'cloud.google.com/gke-nodepool') as nodepool,
       JSONExtractString(doc, 'metadata', 'creationTimestamp') as creation_timestamp,
       first_seen,
       last_seen
from enriched
order by first_seen
limit 100;
```

When summarizing the lifecycle rows, compute `added_count`, `deleted_count`,
and `net_delta = added_count - deleted_count`. Do not say the node count was
stable unless the counts match or a separate before/after snapshot proves it.

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
