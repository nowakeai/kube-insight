# KubeChronicle Storage, Index, And Query Design

This document turns the product shape into an implementation-oriented storage
and query design.

Scope:

- Store all Kubernetes resource versions.
- Store historical topology.
- Store compact troubleshooting facts.
- Query service and workload incidents quickly without scanning all historical
  JSON.

## 1. Design Summary

KubeChronicle should not put all query pressure on the raw JSON history table.
Use four data layers:

```text
+-------------------------+
| latest_index            | current navigation and hot JSON filters
+-------------------------+
| object_facts            | incident facts: OOM, eviction, rollout, node pressure
+-------------------------+
| object_edges            | historical topology: service->pod, pod->node, owners
+-------------------------+
| versions + blobs        | full recoverable resource history
+-------------------------+
```

Query rule:

```text
Use edges and facts to find candidates.
Use versions only to reconstruct evidence and show exact diffs.
```

This keeps storage compact and incident queries interactive.

## 2. ID And Dimension Tables

Use numeric IDs internally. Kubernetes object names, namespaces, kinds, and edge
types repeat heavily, so long text should not be repeated on every fact or edge.

Logical tables:

```sql
create table clusters (
  id bigint primary key,
  name text not null,
  uid text,
  source text,
  created_at timestamptz not null
);

create table object_kinds (
  id smallint primary key,
  api_group text,
  api_version text not null,
  kind text not null,
  unique(api_group, api_version, kind)
);

create table edge_types (
  id smallint primary key,
  name text not null unique
);

create table fact_keys (
  id integer primary key,
  family text not null,
  key text not null,
  value_type text not null,
  unique(family, key)
);
```

For SQLite, use `integer primary key` and store timestamps as milliseconds since
epoch. For PostgreSQL, use `bigint` or identity columns and `timestamptz`.

## 3. Resource History

Resource history is the source of truth. Every derived index must be rebuildable
from this layer.

```sql
create table objects (
  id bigint primary key,
  cluster_id bigint not null,
  kind_id smallint not null,
  namespace text,
  name text not null,
  uid text,
  latest_version_id bigint,
  first_seen_at timestamptz not null,
  last_seen_at timestamptz not null,
  deleted_at timestamptz,
  unique(cluster_id, kind_id, uid),
  unique(cluster_id, kind_id, namespace, name)
);

create table versions (
  id bigint primary key,
  object_id bigint not null,
  seq integer not null,
  observed_at timestamptz not null,
  resource_version text,
  generation bigint,
  doc_hash bytea not null,
  materialization text not null,       -- full, reverse_delta, cdc_manifest
  strategy text not null,              -- full_zstd, json_patch_zstd, cdc_zstd
  blob_ref bytea not null,
  parent_version_id bigint,            -- usually nearest newer version
  raw_size integer not null,
  stored_size integer not null,
  replay_depth smallint not null,
  summary jsonb,
  unique(object_id, seq)
);

create index versions_object_seq_idx
on versions(object_id, seq desc);

create index versions_object_time_idx
on versions(object_id, observed_at desc);
```

Blob storage:

```sql
create table blobs (
  digest bytea primary key,
  codec text not null,                 -- zstd, zstd_dict, none
  raw_size integer not null,
  stored_size integer not null,
  data bytea not null
);
```

Implementation notes:

- Latest version should be stored as a full document.
- Older versions can be reverse deltas from the next newer version.
- Store a full snapshot when `replay_depth >= max_delta_chain`.
- Store full when compressed delta is not smaller than compressed full.
- `summary` stores small change metadata, not the full document.

SQLite mapping:

- `bytea` -> `blob`
- `jsonb` -> `text`
- `timestamptz` -> `integer` milliseconds

## 4. Latest Index

`latest_index` supports current browsing and hot object filters.

```sql
create table latest_index (
  object_id bigint primary key,
  cluster_id bigint not null,
  kind_id smallint not null,
  namespace text,
  name text not null,
  uid text,
  latest_version_id bigint not null,
  observed_at timestamptz not null,
  doc jsonb not null
);

create index latest_kind_ns_name_idx
on latest_index(cluster_id, kind_id, namespace, name);
```

PostgreSQL hot JSON query:

```sql
create index latest_doc_gin_idx
on latest_index using gin (doc jsonb_path_ops);
```

SQLite generated-column example:

```sql
create table latest_index (
  object_id integer primary key,
  cluster_id integer not null,
  kind_id integer not null,
  namespace text,
  name text not null,
  uid text,
  latest_version_id integer not null,
  observed_at_ms integer not null,
  doc text not null,
  app text generated always as
    (json_extract(doc, '$.metadata.labels.app')) virtual
);

create index latest_kind_ns_name_idx
on latest_index(cluster_id, kind_id, namespace, name);

create index latest_app_idx
on latest_index(cluster_id, app);
```

Do not rely on `latest_index` for historical investigation. It is only the
current-state layer.

## 5. Historical Topology

Store topology as validity intervals. Do not store a full graph snapshot for
every watch event.

```sql
create table object_edges (
  id bigint primary key,
  cluster_id bigint not null,
  edge_type smallint not null,
  src_id bigint not null,
  dst_id bigint not null,
  valid_from timestamptz not null,
  valid_to timestamptz,
  src_version_id bigint,
  dst_version_id bigint,
  confidence smallint not null default 100,
  detail jsonb,
  unique(cluster_id, edge_type, src_id, dst_id, valid_from)
);
```

Open edge state:

```sql
create table open_edges (
  cluster_id bigint not null,
  edge_type smallint not null,
  src_id bigint not null,
  dst_id bigint not null,
  edge_id bigint not null,
  primary key(cluster_id, edge_type, src_id, dst_id)
);
```

`open_edges` makes ingestion cheap:

```text
new edge set for object
  -> compare with open_edges
  -> unchanged: do nothing
  -> added: insert object_edges(valid_from), insert open_edges
  -> removed: update object_edges.valid_to, delete open_edges
```

Default edge types:

```text
service_selects_pod
endpointslice_targets_pod
pod_owned_by_replicaset
replicaset_owned_by_deployment
pod_on_node
pod_uses_configmap
pod_uses_secret
workload_uses_pvc
```

Prefer `EndpointSlice -> Pod` for Service membership. Selector matching can be
expensive and can create many edges if recalculated naively.

PostgreSQL interval indexes:

```sql
create extension if not exists btree_gist;

alter table object_edges
add column valid_range tstzrange generated always as
  (tstzrange(valid_from, coalesce(valid_to, 'infinity'), '[)')) stored;

create index object_edges_src_range_idx
on object_edges using gist (cluster_id, edge_type, src_id, valid_range);

create index object_edges_dst_range_idx
on object_edges using gist (cluster_id, edge_type, dst_id, valid_range);
```

SQLite interval indexes:

```sql
create table object_edges (
  id integer primary key,
  cluster_id integer not null,
  edge_type integer not null,
  src_id integer not null,
  dst_id integer not null,
  valid_from_ms integer not null,
  valid_to_ms integer not null,
  src_version_id integer,
  dst_version_id integer,
  confidence integer not null default 100,
  detail text,
  unique(cluster_id, edge_type, src_id, dst_id, valid_from_ms)
);

create index object_edges_src_time_idx
on object_edges(cluster_id, edge_type, src_id, valid_from_ms, valid_to_ms);

create index object_edges_dst_time_idx
on object_edges(cluster_id, edge_type, dst_id, valid_from_ms, valid_to_ms);
```

Use `9223372036854775807` as open-ended `valid_to_ms` in SQLite so the overlap
predicate remains simple.

Interval overlap predicate:

```sql
valid_from < $window_end and valid_to > $window_start
```

## 6. Troubleshooting Facts

Facts are small, indexed rows derived from resource versions and Events.

```sql
create table object_facts (
  id bigint primary key,
  cluster_id bigint not null,
  ts timestamptz not null,
  object_id bigint not null,
  version_id bigint,
  kind_id smallint not null,
  namespace text,
  name text,
  node_id bigint,
  workload_id bigint,
  service_id bigint,
  fact_key_id integer not null,
  fact_value text,
  numeric_value double precision,
  severity smallint not null default 0,
  detail jsonb
);
```

Required indexes:

```sql
create index object_facts_key_value_time_idx
on object_facts(cluster_id, fact_key_id, fact_value, ts desc);

create index object_facts_object_time_idx
on object_facts(cluster_id, object_id, ts desc);

create index object_facts_workload_time_idx
on object_facts(cluster_id, workload_id, ts desc)
where workload_id is not null;

create index object_facts_service_time_idx
on object_facts(cluster_id, service_id, ts desc)
where service_id is not null;

create index object_facts_node_time_idx
on object_facts(cluster_id, node_id, ts desc)
where node_id is not null;
```

High-value fact keys:

| Family | Key examples |
| --- | --- |
| Pod status | `reason`, `last_reason`, `restart_count`, `ready`, `phase`, `qos_class` |
| Pod placement | `node_assigned`, `started`, `deleted` |
| Workload rollout | `deployment_generation`, `replicaset_hash`, `image` |
| Workload config | `memory_limit`, `cpu_limit`, `memory_request`, `probe_changed`, `config_ref` |
| Node condition | `Ready`, `MemoryPressure`, `DiskPressure`, `PIDPressure` |
| Event mirror | `reason`, `involved_object`, `message_fingerprint` |

Only write facts when they are useful:

- Write lifecycle facts every time they happen.
- Write config facts only when the value changes.
- Write node occupancy snapshots on Pod placement changes or coarse intervals.
- Keep `detail` small. Full JSON belongs in `versions`.

For PostgreSQL/TimescaleDB, `object_facts` is a good hypertable candidate:

```sql
select create_hypertable('object_facts', 'ts', if_not_exists => true);
```

For SQLite, keep facts in one table for PoC and partition manually later if
needed.

## 7. Change Summary Index

For investigation UI, store compact change summaries so the product can answer
"what changed around this time" without reconstructing every version.

```sql
create table object_changes (
  id bigint primary key,
  cluster_id bigint not null,
  ts timestamptz not null,
  object_id bigint not null,
  version_id bigint not null,
  change_family text not null,       -- spec, status, metadata, topology
  path text not null,
  op text not null,                  -- add, remove, replace
  old_scalar text,
  new_scalar text,
  severity smallint not null default 0
);

create index object_changes_object_time_idx
on object_changes(cluster_id, object_id, ts desc);

create index object_changes_path_time_idx
on object_changes(cluster_id, change_family, path, ts desc);
```

This is not a full diff store. It is a small query index and timeline aid.

## 8. Ingestion Transaction

For each watch/list event:

```text
1. Redact and normalize resource.
2. Resolve object_id from cluster, kind, namespace, name, uid.
3. Detect duplicate doc_hash. If unchanged, update heartbeat only.
4. Store version:
   - latest full
   - previous latest rewritten to reverse delta when profitable
   - fallback full when delta is too large
5. Upsert latest_index.
6. Extract topology edge candidates.
7. Compare against open_edges and close/open edge intervals.
8. Extract object_facts and object_changes.
9. Commit.
```

Atomicity target:

```text
versions, latest_index, object_edges, object_facts, object_changes
must reflect the same observed resource event.
```

If the backend cannot guarantee that cheaply, store an ingestion offset and make
derived indexes rebuildable.

## 9. Query Recipes

### 9.1 Latest Object Lookup

```sql
select doc
from latest_index
where cluster_id = $1
  and kind_id = $2
  and namespace = $3
  and name = $4;
```

Index:

```text
latest_kind_ns_name_idx
```

### 9.2 Historical Version Reconstruction

```text
input: object_id, target seq

1. Find nearest full version where seq >= target seq.
2. Load full blob.
3. Apply reverse deltas until target seq.
4. Stop after max_delta_chain.
```

Index:

```text
versions_object_seq_idx
```

### 9.3 Service Investigation

Input:

```text
service_id, window_start, window_end, lookback, lookahead
```

Step 1: resolve historical Pods:

```sql
select distinct dst_id as pod_id
from object_edges
where cluster_id = $1
  and edge_type in ($service_selects_pod, $endpointslice_targets_pod)
  and src_id = $service_id
  and valid_from < $window_end
  and coalesce(valid_to, 'infinity') > $window_start;
```

Step 2: expand Pods to Nodes and workload owners:

```sql
select edge_type, src_id, dst_id
from object_edges
where cluster_id = $1
  and src_id = any($pod_ids)
  and edge_type in ($pod_on_node, $pod_owned_by_replicaset)
  and valid_from < $expanded_end
  and coalesce(valid_to, 'infinity') > $expanded_start;
```

Step 3: query facts for Pods, Nodes, and workloads:

```sql
select *
from object_facts
where cluster_id = $1
  and ts between $expanded_start and $expanded_end
  and (
    object_id = any($pod_ids)
    or node_id = any($node_ids)
    or workload_id = any($workload_ids)
  )
order by severity desc, ts;
```

Step 4: reconstruct exact versions for only the top evidence items.

### 9.4 OOMKilled Historical Search

```sql
select distinct object_id
from object_facts
where cluster_id = $1
  and fact_key_id = $reason_key
  and fact_value = 'OOMKilled'
  and ts between $from and $to;
```

Index:

```text
object_facts_key_value_time_idx
```

### 9.5 Pod Moved Or Evicted

Eviction:

```sql
select *
from object_facts
where cluster_id = $1
  and object_id = $pod_id
  and fact_value in ('Evicted', 'Preempted')
  and ts between $from and $to;
```

Placement changes:

```sql
select *
from object_edges
where cluster_id = $1
  and edge_type = $pod_on_node
  and src_id = $pod_id
  and valid_from < $to
  and coalesce(valid_to, 'infinity') > $from
order by valid_from;
```

### 9.6 Deployment Was Briefly Misconfigured

```sql
select *
from object_changes
where cluster_id = $1
  and object_id = $deployment_id
  and ts between $from and $to
  and change_family = 'spec'
  and path in (
    'spec.template.spec.containers[name].image',
    'spec.template.spec.containers[name].resources.requests.memory',
    'spec.template.spec.containers[name].resources.limits.memory',
    'spec.template.spec.containers[name].readinessProbe',
    'spec.template.spec.containers[name].livenessProbe'
  )
order by ts;
```

Then reconstruct before and after versions for display.

### 9.7 Same-Node Noisy Neighbor

Step 1: find Nodes for affected Pods:

```sql
select distinct dst_id as node_id
from object_edges
where cluster_id = $1
  and edge_type = $pod_on_node
  and src_id = any($affected_pod_ids)
  and valid_from < $window_end
  and coalesce(valid_to, 'infinity') > $window_start;
```

Step 2: find other Pods on those Nodes:

```sql
select distinct src_id as pod_id
from object_edges
where cluster_id = $1
  and edge_type = $pod_on_node
  and dst_id = any($node_ids)
  and valid_from < $window_end
  and coalesce(valid_to, 'infinity') > $window_start;
```

Step 3: query resource footprint and churn facts:

```sql
select *
from object_facts
where cluster_id = $1
  and object_id = any($same_node_pod_ids)
  and ts between $expanded_start and $expanded_end
  and fact_key_id in (
    $memory_request_key,
    $memory_limit_key,
    $cpu_request_key,
    $restart_count_key,
    $ready_key
  )
order by severity desc, ts;
```

## 10. Backend Choice

### SQLite

Use for:

- PoC.
- Agent-side cache.
- Single-file archive.

Strengths:

- Simple deployment.
- Good enough B-tree indexes for metadata, edges, and facts.
- JSON generated columns for latest-state filters.

Limitations:

- No native range type.
- No GiST.
- Single writer.
- Weak generic historical JSON indexing.

Implementation rules:

- Store time as integer milliseconds.
- Use max integer for open-ended intervals.
- Keep JSON indexes narrow and explicit.
- Use WAL mode.

### PostgreSQL

Use for:

- Central service backend.
- Stronger concurrency.
- Range indexes and constraints.
- JSONB GIN for recent full JSON.

Strengths:

- `tstzrange` plus GiST for interval overlap.
- JSONB GIN for hot/ad-hoc queries.
- Partial indexes.
- Partitioning.
- Strong transactional semantics.

Implementation rules:

- Use facts and edges as primary query path.
- Use JSONB GIN for hot recent versions, not as the only historical query plan.
- Use TimescaleDB for high-volume time-partitioned facts if needed.

### TimescaleDB

Use for:

- Time-partitioned facts.
- Cold compression.
- Retention policies.

Be careful:

- Compressed chunks may not use the same JSONB GIN path.
- Keep important predicates in fact tables before compression.

## 11. Retention And Compaction

Suggested retention tiers:

```text
0-7 days:
  full facts, topology, versions, latest JSON, optional hot JSONB GIN

7-30 days:
  full facts, topology, compact versions, fewer hot JSON indexes

30+ days:
  compact versions, compact facts, topology intervals, no generic JSON index
```

Compaction jobs:

- Merge adjacent identical topology intervals.
- Drop duplicate unchanged versions.
- Rebuild fact indexes from versions if extractor changes.
- Recompress old full snapshots with stronger Zstd settings.
- Move old blobs to object storage when backend supports it.

## 12. MVP Implementation Order

1. Implement `objects`, `versions`, `blobs`, and `latest_index`.
2. Add `object_edges` and `open_edges`.
3. Extract P0 edges:
   - Pod -> Node
   - Pod -> ReplicaSet
   - ReplicaSet -> Deployment
   - EndpointSlice -> Pod
4. Add `object_facts`.
5. Extract P0 facts:
   - OOMKilled
   - Evicted
   - restart count
   - readiness transitions
   - Deployment image/resource/probe changes
   - Node conditions
6. Add `object_changes` for UI timeline.
7. Build `investigate service --from --to`.
8. Benchmark:
   - stored bytes
   - index bytes
   - ingest throughput
   - service investigation latency
   - historical reconstruction latency

The MVP is successful if a service plus time window can return a useful
evidence bundle without scanning all historical JSON.
