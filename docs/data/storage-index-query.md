# Storage, Index, And Query Design

## Storage Rules

### Resource Versions

- Store latest version as full JSON.
- Store older versions as reverse deltas when profitable.
- Store full snapshots every `max_delta_chain` versions.
- Store full when compressed delta is not smaller than compressed full.
- Store duplicate unchanged versions as heartbeats or skip them, depending on
  retention mode.

Default thresholds:

```text
small_doc_limit = 4 KiB
large_doc_limit = 64 KiB
max_delta_chain = 10
max_delta_full_ratio = 1.0
snapshot_ratio = 0.75
```

### Topology

- Store edges as validity intervals.
- Maintain `open_edges`.
- Merge adjacent identical intervals during compaction.
- Prefer EndpointSlice-based service membership over selector recomputation.

### Facts

- Store only facts useful for investigation.
- Write lifecycle facts when they happen.
- Write config facts only on change.
- Store node occupancy on placement changes or coarse snapshots.

### Resource-Specific Storage Rules

Some resources use specialized storage rules to reduce write amplification while
preserving evidence quality.

Pods:

- keep full retained versions for meaningful lifecycle, spec, placement, owner,
  readiness, and container status changes,
- discard pure heartbeat changes after fact extraction,
- keep Pod facts and edges as the primary investigation path,
- write status transitions to both `object_facts` and `object_changes` when they
  are useful on an evidence timeline.

Nodes:

- extract condition, taint, capacity, allocatable, and label changes as facts,
- write condition and pressure transitions as timeline changes,
- avoid broad JSON indexes on large status payloads,
- prefer stronger compression for old Node versions.

Events:

- roll up repeated Events by involved object, reason, type, and message
  fingerprint,
- store rollup count and last-seen changes as facts,
- keep Event facts longer than raw Event versions when retention requires it,
- avoid indexing raw Event messages directly.

EndpointSlices:

- store Service -> Pod topology interval changes,
- store endpoint readiness/serving/terminating changes as facts,
- avoid writing new edge rows when endpoint membership is unchanged,
- treat address-only churn as low value unless readiness or targetRef changes.

Leases:

- skip by default or downsample aggressively,
- do not include in generic JSON indexes unless explicitly enabled.

## PostgreSQL Indexes

Latest:

```sql
create index latest_kind_ns_name_idx
on latest_index(cluster_id, kind_id, namespace, name);

-- Full latest JSON is exposed through latest_documents by joining the latest
-- content version to blobs. Do not duplicate it in latest_index. If a backend
-- needs fast arbitrary latest JSON predicates, use a materialized hot
-- latest-documents table/view with a GIN index.
```

Versions:

```sql
create index versions_object_seq_idx
on versions(object_id, seq desc);

create index versions_object_time_idx
on versions(object_id, observed_at desc);
```

Observations:

```sql
create index object_observations_object_time_idx
on object_observations(object_id, observed_at desc);

create index object_observations_cluster_time_idx
on object_observations(cluster_id, observed_at desc);
```

Topology:

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

Facts:

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

Maintenance:

```sql
create index ingestion_offsets_resource_scope_idx
on ingestion_offsets(cluster_id, api_resource_id, namespace);

create index maintenance_runs_cluster_task_idx
on maintenance_runs(cluster_id, task, started_at desc);
```

Changes:

```sql
create index object_changes_object_time_idx
on object_changes(cluster_id, object_id, ts desc);

create index object_changes_path_time_idx
on object_changes(cluster_id, change_family, path, ts desc);
```

## SQLite Indexes

The local PoC uses the pure-Go `modernc.org/sqlite` driver. This favors
portable builds over the smaller dependency graph of CGO-backed SQLite drivers.

The initial implementation stores each retained version as full JSON with an
identity blob codec. Compression, reverse deltas, and profitable snapshot
selection remain application-layer work above the storage backend.

Do not compress all SQLite historical blobs by default while SQLite is still the
primary query-shape PoC. Compressed blobs preserve proof, but they remove direct
SQL access to body JSON unless every query pays a decompression scan. Prefer:

- `latest_index` as a compact navigation/projection table,
- `latest_documents` view for full latest JSON when proof is needed,
- `object_facts`, `object_edges`, and `object_changes` for investigation,
- selected JSON-path indexes for configured historical fields,
- FTS5 only for selected text such as Events, status messages, webhook errors,
  and controller reconciliation messages,
- later cold-history compression after the indexed query surface is proven.

SQLite does not have range types or GiST. Store time as integer milliseconds and
use max integer for open-ended intervals.

Topology:

```sql
create index object_edges_src_time_idx
on object_edges(cluster_id, edge_type, src_id, valid_from_ms, valid_to_ms);

create index object_edges_dst_time_idx
on object_edges(cluster_id, edge_type, dst_id, valid_from_ms, valid_to_ms);
```

Overlap predicate:

```sql
valid_from_ms < :window_end_ms
and valid_to_ms > :window_start_ms
```

Latest generated column example:

```sql
app text generated always as
  (json_extract(doc, '$.metadata.labels.app')) virtual
```

Maintenance rules:

- Use WAL mode during ingestion.
- `watch` and `serve --watch` run lightweight maintenance by default:
  `wal_checkpoint(TRUNCATE)`, `pragma optimize`, and incremental vacuum when
  freelist pages exist.
- Run manual `wal_checkpoint` before exporting the SQLite file if the watcher is
  not running.
- Use `kube-insight db compact --prune-unchanged` after backfilling
  observations or after large retention deletes. This preserves observation
  history while removing duplicate retained content versions and their repeated
  derived facts/edges/changes.
- Use incremental vacuum for long-lived local stores when retention purges are
  enabled.
- Run `ANALYZE` after bulk ingest, purge, or index rebuild.

PostgreSQL maintenance rules:

- Keep autovacuum enabled for normal churn.
- Monitor dead tuples and index bloat on `versions`, `latest_index`,
  `object_facts`, `object_edges`, and `ingestion_offsets`.
- Run explicit `VACUUM (ANALYZE)` after large retention purges or derived-index
  rebuilds.
- Treat `VACUUM FULL` or heavy reindexing as offline maintenance, not a normal
  ingestion-path operation.

## Query Recipes

### Latest Object

```sql
select doc
from latest_documents
where cluster_id = $1
  and kind_id = $2
  and namespace = $3
  and name = $4;
```

### Historical Version

```text
1. Find nearest full version with seq >= target seq.
2. Load full blob.
3. Apply reverse deltas until target seq.
4. Enforce max_delta_chain.
```

### Service Investigation

Step 1: find historical Pods backing the service:

```sql
select distinct dst_id as pod_id
from object_edges
where cluster_id = $1
  and edge_type in ($service_selects_pod, $endpointslice_targets_pod)
  and src_id = $service_id
  and valid_from < $window_end
  and coalesce(valid_to, 'infinity') > $window_start;
```

Step 2: expand to Nodes and workload owners:

```sql
select edge_type, src_id, dst_id
from object_edges
where cluster_id = $1
  and src_id = any($pod_ids)
  and edge_type in ($pod_on_node, $pod_owned_by_replicaset)
  and valid_from < $expanded_end
  and coalesce(valid_to, 'infinity') > $expanded_start;
```

Step 3: query facts:

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

Step 4: reconstruct exact JSON only for top evidence.

### OOMKilled Search

```sql
select distinct object_id
from object_facts
where cluster_id = $1
  and fact_key_id = $reason_key
  and fact_value = 'OOMKilled'
  and ts between $from and $to;
```

### Pod Moved Or Evicted

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

### Deployment Misconfiguration

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

### Same-Node Noisy Neighbor

```sql
select distinct src_id as pod_id
from object_edges
where cluster_id = $1
  and edge_type = $pod_on_node
  and dst_id = any($node_ids)
  and valid_from < $window_end
  and coalesce(valid_to, 'infinity') > $window_start;
```

Then query resource request, limit, restart, and readiness facts for those Pods.

## Query Philosophy

```text
facts + edges: candidate search
versions: proof and diff
latest_index: navigation
JSONB GIN: optional hot ad-hoc query
```

## Agent SQL Access

Agents should be able to inspect the logical schema and compose their own
read-only queries instead of relying only on prebuilt investigation endpoints.
The CLI exposes that primitive:

```bash
kube-insight query schema --db kubeinsight.db
kube-insight query sql --db kubeinsight.db \
  --sql "select fact_key, fact_value, severity from object_facts order by ts desc limit 20"
```

`query sql` is intentionally read-only. It opens the SQLite database with
`PRAGMA query_only`, accepts only `SELECT`, `WITH`, and `EXPLAIN` statements,
rejects mutation keywords, and caps returned rows with `--max-rows`.

Future API backends should keep the same product contract:

- expose schema/metadata first,
- execute only read-only SQL for agents,
- apply Kubernetes RBAC-derived row and resource filters before returning rows,
- preserve prebuilt commands as convenience wrappers, not as the only query
  surface.
