# Reverse Delta Query And Indexing

Reverse-delta storage should not be queried by scanning compressed patches.
The query model is split into latest indexes and version reconstruction.

## Storage Layout

```text
+------------------+       +-------------------+
| objects          |       | latest_index      |
|------------------|       |-------------------|
| object_id PK     |       | object_id PK      |
| latest_version   |       | version           |
| kind             |       | kind              |
| namespace        |       | namespace         |
+--------+---------+       | doc full JSON     |
         |                 | generated fields  |
         |                 | JSON/path indexes |
         |                 +-------------------+
         |
         v
+-------------------------------------------------------------+
| versions                                                    |
|-------------------------------------------------------------|
| object_id | version | materialization | compressed blob     |
|-------------------------------------------------------------|
| node-a    | 19      | latest/full     | zstd(full JSON)     |
| node-a    | 18      | delta           | zstd(patch 19->18)  |
| node-a    | 17      | delta           | zstd(patch 18->17)  |
| node-a    | 10      | snapshot        | zstd(full JSON)     |
| node-a    |  9      | delta           | zstd(patch 10->9)   |
+-------------------------------------------------------------+
```

Latest queries use `latest_index`. Historical point reads use `versions`.

## Write Flow

```text
+-------------+
| incoming    |
| JSON object |
+------+------+ 
       |
       v
+-------------------+
| normalize/filter  |
| noisy fields      |
+--------+----------+
         |
         v
+-------------------------+
| structural transform    |
| raw/index/inferred list |
+-----------+-------------+
            |
            v
+----------------------------+
| store new latest full blob |
+------------+---------------+
             |
             v
+-------------------------------+
| rewrite previous latest as    |
| reverse delta if worthwhile   |
+---------+---------------------+
          |
          v
+-----------------------------+
| fallback full if delta too  |
| large; snapshot every N     |
+-------------+---------------+
              |
              v
+------------------------------+
| upsert objects + latest_index|
| full JSON for latest only    |
+------------------------------+
```

## Latest Query Flow

```text
Query: kind = 'Node', namespace = 'default'

+-------------+
| user filter |
+------+------+
       |
       v
+------------------------------+
| latest_index B-tree/JSON idx |
+---------------+--------------+
                |
                v
+------------------------------+
| object_id, latest version    |
| full latest JSON             |
+------------------------------+
```

This is why latest reads can stay O(1) by object ID and indexed by selected
fields. The latest document is stored full; no patch replay is needed.

## Historical Point Read Flow

```text
Query: get object_id=node-a version=13

+----------------------------+
| versions(object_id, ver)   |
+-------------+--------------+
              |
              v
+-------------------------------+
| find nearest newer full blob  |
| version >= target             |
+--------------+----------------+
               |
               v
+-------------------------------+
| decompress full snapshot      |
+--------------+----------------+
               |
               v
+-------------------------------+
| apply reverse patches until   |
| target version is reached     |
+--------------+----------------+
               |
               v
+-------------------------------+
| reconstructed JSON version    |
+-------------------------------+
```

The replay cost is bounded by `max_delta_chain`. In current tests this is `10`.

## Historical Field Queries

There are three possible modes:

- `latest indexed query`: fast, default, backed by `latest_index`.
- `historical point lookup`: bounded replay by `object_id/version`.
- `historical field scan`: expensive; reconstruct versions and filter in
  memory or in a batch worker.

The MVP should not build a physical JSON index for every historical version.
That would make index size proportional to total history and could erase the
storage savings. If historical search is required, add one of:

- user-selected historical indexed paths,
- coarse time/object/kind prefilters,
- async materialized search tables,
- export to TimescaleDB/ClickHouse for analytics.

## Example: Historical OOMKilled Pods

Question:

```text
Find all historical Pods whose status indicates OOMKilled.
```

This should not scan and replay every historical Pod version on demand. Instead,
extract a narrow event table at write time while the full JSON version is still
available:

```sql
create table pod_status_events (
  id integer primary key,
  ts text not null,
  object_id text not null,
  version integer not null,
  namespace text,
  pod_name text,
  container text,
  state_group text not null, -- state or lastState
  state_name text not null,  -- waiting/running/terminated
  reason text not null,
  exit_code integer
);

create index pod_status_reason_idx
on pod_status_events(reason, namespace, ts);

create index pod_status_object_idx
on pod_status_events(object_id, version);
```

Extraction paths:

```text
status.containerStatuses[*].state.*.reason
status.containerStatuses[*].lastState.*.reason
```

Query:

```sql
select distinct namespace, pod_name, container
from pod_status_events
where reason = 'OOMKilled'
order by namespace, pod_name, container;
```

Flow:

```text
+------------------------+
| incoming Pod version   |
+-----------+------------+
            |
            v
+-----------------------------+
| extract status event rows   |
| reason=OOMKilled/Error/...  |
+------------+----------------+
             |
             +-------------------------------+
             |                               |
             v                               v
+------------------------+      +-----------------------------+
| versions               |      | pod_status_events           |
| full/delta blobs       |      | narrow historical index     |
+-----------+------------+      +--------------+--------------+
            |                                  |
            | historical get                   | historical field query
            v                                  v
+------------------------+      +-----------------------------+
| replay bounded deltas  |      | search reason index         |
+------------------------+      +-----------------------------+
```

Measured on the real `staging` sample:

```text
k8s objects:                  1807
k8s versions:                18070
oomkilled_pod_query_ms:       0.027
oomkilled_pod_query_count:        9 distinct Pods
oomkilled_event_count:           90 version events
query plan:
  SEARCH pod_status_events USING INDEX pod_status_reason_idx (reason=?)
```

Example results:

```text
8004scan-production | production-8004scan-backend-celery-worker-6864886599-7ntwb | celery-worker
8004scan-production | production-8004scan-backend-celery-worker-6864886599-dv4wq | celery-worker
8004scan-production | production-8004scan-backend-celery-worker-6864886599-qgvr6 | celery-worker
8004scan-production | production-8004scan-backend-flower-6cd845588c-ptlzm        | flower
8004scan-staging    | staging-8004scan-backend-celery-worker-5ff576cf8d-76sbm    | celery-worker
8004scan-staging    | staging-8004scan-backend-celery-worker-5ff576cf8d-rsg7m    | celery-worker
8004scan-staging    | staging-8004scan-backend-flower-9fddc8f89-vpfkr            | flower
graphnode           | graph-node-query-6669f6448c-9tv4c                          | graph-node
loki                | stg20230731v070mp-loki-chunks-cache-0                     | memcached
```

This pattern generalizes: keep full JSON recovery in `versions`, but build
small historical indexes for high-value predicates such as Pod termination
reasons, Kubernetes events, condition transitions, image changes, or selected
user-configured JSON paths.

## Why Not Index Every Historical JSON Field?

A universal scalar KV index is the most generic query model:

```sql
select distinct v.object_id
from version_kv kv
join versions v on v.id = kv.version_row_id
where kv.leaf = 'reason'
  and kv.value = 'OOMKilled'
  and kv.kind = 'Pod';
```

It works, but the storage amplification is high. On the real staging sample:

```text
k8s versions:                 18070
raw bytes:                    150108706
full Zstd payload bytes:       27502403
metadata-only DB bytes:        37933056
universal KV DB bytes:        797208576
scalar KV rows:                2445070
universal KV write time:       153175 ms
OOMKilled query time:          0.132 ms
```

The query is simple, but the index is much larger than the compressed payload.
This is why DiffStor should prefer narrow historical indexes for known
high-value predicates, plus explicit user-selected indexed paths, rather than
indexing every scalar in every historical version by default.

## Postgres JSONB GIN Alternative

PostgreSQL/TimescaleDB can provide a more compact and more native generic JSON
index than SQLite KV:

```sql
create index history_doc_gin_idx
on diffstor_jsonb_history using gin (doc jsonb_path_ops);
```

For OOMKilled:

```sql
select count(distinct object_id)
from diffstor_jsonb_history
where kind = 'Pod'
  and (
    doc @? '$.status.containerStatuses[*].state.terminated.reason ? (@ == "OOMKilled")'
    or doc @? '$.status.containerStatuses[*].lastState.terminated.reason ? (@ == "OOMKilled")'
  );
```

Measured on 1807 staging objects x 10 versions:

```text
uncompressed hypertable total: 102817792 bytes
GIN index bytes:                8060928
OOMKilled query before compression: 8.32 ms, Bitmap Index Scan on GIN

Timescale compressed hypertable: 27254784 bytes
OOMKilled query after compression:  91.68 ms, ColumnarScan
```

So Postgres+GIN is a valid generic historical query backend. The tradeoff is
that Timescale compression can change the query path, and full-version storage
still does not exploit per-object version deltas.

### Can Timescale And GIN Coexist?

Yes, but with an important boundary:

- On uncompressed hypertable chunks, a `jsonb` GIN index behaves like normal
  PostgreSQL indexing. In the staging test, the OOMKilled JSONPath query used a
  Bitmap Index Scan on the chunk GIN index.
- After Timescale compression converts chunks into columnar storage, Timescale
  uses compression-specific access paths based mainly on `segmentby` and
  `orderby`. In the same test, the compressed query used `ColumnarScan` rather
  than the original JSONB GIN index.

This means the practical architecture is usually hybrid:

```text
hot / recent chunks:
  keep uncompressed
  use JSONB GIN for flexible historical JSON predicates

cold / old chunks:
  compress with Timescale
  query via segmentby/orderby-friendly predicates
  keep separate narrow event/path indexes for important predicates
```

## Troubleshooting Query Model

The hardest query is not "find OOMKilled". It is a late investigation:

```text
At time T, service S had a small error-rate or latency spike.
Current state is healthy.
Kubernetes Events may already be expired.
Find whether any related Pod, Deployment, Node, or noisy neighbor changed.
```

This requires time-bounded evidence retrieval, not only JSON search. The query
should start from a service and time window, then expand to related historical
objects:

```text
+--------------------------+
| user: service + time win |
+------------+-------------+
             |
             v
+------------------------------+
| resolve historical topology  |
| Service -> EndpointSlice     |
| -> Pod -> ReplicaSet         |
| -> Deployment -> Node        |
+--------------+---------------+
               |
               v
+---------------------------------------------+
| query fact indexes inside [T-before,T-after] |
| pod lifecycle / status transitions          |
| deployment template/config changes          |
| node condition/resource pressure changes    |
| same-node pod churn and resource footprint  |
+----------------+----------------------------+
                 |
                 v
+---------------------------------------+
| rank evidence by proximity and impact |
+----------------+----------------------+
                 |
                 v
+---------------------------------------------+
| reconstruct exact historical JSON versions  |
| and show diffs for the shortlisted evidence |
+---------------------------------------------+
```

Full historical JSON is still required, but it should be used for drill-down and
proof. The first-pass search should hit compact fact and relationship indexes.

### Required Historical Facts

For Kubernetes troubleshooting, the default extractor should produce these fact
families while ingesting each version:

| Fact family | Examples | Why it matters |
| --- | --- | --- |
| Pod status | `OOMKilled`, `Evicted`, `CrashLoopBackOff`, restart count changes, readiness transitions | Explains brief instance failure even after the Pod recovers. |
| Pod placement | `spec.nodeName`, Pod UID, start time, deletion time | Shows reschedule, replacement, and whether the affected instance moved nodes. |
| Workload config | Deployment generation, ReplicaSet, PodTemplate hash, image, env/config refs, resources, probes | Catches short-lived bad rollout or config drift. |
| Node condition | `MemoryPressure`, `DiskPressure`, `PIDPressure`, `Ready` transitions | Explains evictions and node-local degradation. |
| Node occupancy | Pods co-located on the same node, requested CPU/memory, limits, QoS class | Finds likely noisy-neighbor candidates. |
| Event mirror | Kubernetes Event reason/message when available | Keeps expired Events searchable through DiffStor retention. |

Schemaless storage does not prevent this. The base product can remain
schemaless, while integrations add optional extractors. For unknown JSON, the
same mechanism becomes user-configured path extraction.

### Storage Tables

Keep the compact recoverable version store:

```text
versions(object_id, version, ts, materialization, compressed_blob)
latest_index(object_id, kind, namespace, name, latest_version, latest_doc)
```

Add relationship history:

```sql
create table object_edges (
  cluster text not null,
  edge_type text not null,       -- service_selects_pod, pod_on_node, pod_owned_by_rs
  src_kind text not null,
  src_id text not null,
  dst_kind text not null,
  dst_id text not null,
  valid_from timestamptz not null,
  valid_to timestamptz,
  src_version integer,
  dst_version integer
);

create index object_edges_src_time_idx
on object_edges(cluster, src_kind, src_id, edge_type, valid_from, valid_to);

create index object_edges_dst_time_idx
on object_edges(cluster, dst_kind, dst_id, edge_type, valid_from, valid_to);
```

Add compact fact history:

```sql
create table object_facts (
  cluster text not null,
  ts timestamptz not null,
  object_id text not null,
  version integer not null,
  kind text not null,
  namespace text,
  name text,
  node_name text,
  fact_family text not null,     -- pod_status, deployment_config, node_condition
  fact_key text not null,        -- reason, image, ready, resource_request_memory
  fact_value text,
  numeric_value double precision,
  severity smallint default 0,
  detail jsonb
);

create index object_facts_lookup_idx
on object_facts(cluster, fact_family, fact_key, fact_value, ts desc);

create index object_facts_object_time_idx
on object_facts(cluster, object_id, ts desc);

create index object_facts_node_time_idx
on object_facts(cluster, node_name, ts desc)
where node_name is not null;
```

For PostgreSQL, keep `detail` small and optional. Do not turn this table into a
second full JSON store.

### Example Investigation Query

Step 1: find Pods selected by a Service during the spike window:

```sql
select dst_id as pod_id
from object_edges
where cluster = $1
  and src_kind = 'Service'
  and src_id = $2
  and edge_type = 'service_selects_pod'
  and valid_from <= $window_end
  and coalesce(valid_to, 'infinity') >= $window_start;
```

Step 2: find suspicious Pod facts for those Pods:

```sql
select *
from object_facts
where cluster = $1
  and object_id = any($pod_ids)
  and ts between $window_start - interval '30 minutes'
             and $window_end + interval '30 minutes'
  and (
    (fact_family = 'pod_status'
     and fact_key in ('reason', 'last_reason', 'restart_count', 'ready')
     and fact_value in ('OOMKilled', 'Evicted', 'CrashLoopBackOff', 'False'))
    or fact_family = 'deployment_config'
  )
order by severity desc, abs(extract(epoch from ts - $spike_time));
```

Step 3: expand from affected Pods to Nodes and same-node Pods:

```sql
select distinct dst_id as node_id
from object_edges
where cluster = $1
  and src_kind = 'Pod'
  and src_id = any($pod_ids)
  and edge_type = 'pod_on_node'
  and valid_from <= $window_end
  and coalesce(valid_to, 'infinity') >= $window_start;
```

Then query `object_facts` for Node pressure and same-node Pod churn around the
same time. Only after this narrowing step should the system reconstruct exact
JSON versions from `versions` to show the before/after diff.

### Indexing Rule

For this class of troubleshooting, the efficient default is:

```text
full JSON history:
  compact storage, delta/compression, bounded reconstruction

relationship history:
  indexed by object and validity time range

fact history:
  indexed by fact family/key/value/time and object/time

hot ad-hoc JSON:
  optional Postgres JSONB GIN on recent uncompressed history

cold ad-hoc JSON:
  slower scan or async job, unless the user promoted a path to a fact index
```

This keeps the product generic enough for schemaless JSON, but gives Kubernetes
and log troubleshooting a fast path for the questions users actually ask.


## Indexed Test Result

Using the real `staging` samples:

```text
k8s objects:       1000
versions/object:   20
k8s versions:   20000
raw bytes:      177914492

full_zstd_payload_bytes:  33240483
delta_payload_bytes:      10245592
sqlite db with indexes:   27275264

latest Node query:        0.052 ms
latest Node plan:         SEARCH latest_index USING INDEX latest_kind_ns_idx
objects Node lookup:      0.034 ms
objects plan:             SEARCH objects USING INDEX objects_kind_ns_idx
historical get:           1.717 ms
historical replay steps:  9
```

Adding `objects` and `latest_index` increases the SQLite file size compared with
a versions-only delta table, but it gives fast latest queries without sacrificing
bounded historical lookup.
