# Storage, Index, And Query Design

Audience: contributors working on storage internals and query surfaces. Start
with [Data Model](../../operators/data/data-model.md) for the user-facing schema explanation.

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

## ClickHouse Evidence Tables

The current central-backend MVP uses ClickHouse for append-heavy evidence
history and low-cost cold storage experiments. The baseline shape is append-only;
mutable read models can be added after correctness and cost are measured.

Core tables:

```text
observations: raw retained observations and filtered documents
versions: retained content versions and proof payloads
facts: extracted investigation predicates
edges: topology intervals
changes: timeline-oriented scalar/path changes
filter_decisions: auditable destructive and sensitive filter decisions
ingestion_offsets: current list/watch progress and health state
```

Generate or apply the current MVP DDL:

```bash
kube-insight db clickhouse schema
kube-insight db clickhouse schema --json-type --output json
KUBE_INSIGHT_CLICKHOUSE_DSN=http://localhost:8123 kube-insight db clickhouse init
kube-insight db clickhouse import --endpoint http://localhost:8123 --file testdata/fixtures/kube/core.json
kube-insight db clickhouse service default api --endpoint http://localhost:8123
```

`db clickhouse init` sends each statement over the ClickHouse HTTP interface.
`db clickhouse import` runs the existing filter/extractor pipeline in memory and
then writes observations, object aliases, versions, facts, edges, changes, and
auditable filter decisions as JSONEachRow batches. Runtime `ingest`, `watch`,
and `serve --watch` can write directly to the same tables when
`storage.driver: clickhouse` is selected.
`db clickhouse service` now uses the same typed service investigation store
path as the HTTP API. That keeps CLI smoke, API responses, and storage adapter
behavior aligned around `internal/storage.ServiceInvestigation` instead of a
separate raw SQL probe. The HTTP API opens storage through read-side interfaces
in `internal/storage`, so ClickHouse serves generic schema, SQL, resource
health, object history, evidence search, service investigation, topology reads,
and Prometheus metrics. The current service/topology implementation intentionally
uses append-only source tables plus bounded graph expansion; materialized read
models can be added later when query cost under larger datasets is measured.

Remote ClickHouse and embedded chDB share the ClickHouse schema and most SQL row
mapping. The default release can stay SQLite-compatible, while the chDB-enabled
variant adds the `chdb-go` executor and `libchdb.so` runtime dependency. ClickHouse
stores are constructed with `NewStore` or `NewHTTPStore`; callers should depend
on the store and the small executor/read-side interfaces rather than reaching
into the concrete HTTP client. Low-level `QueryRunner` helpers remain useful for
schema status, SQL probes, and focused tests, but product-facing investigation
commands should go through the typed store interfaces.

Storage rules:

- Use MergeTree-family tables.
- Partition by observed month or day depending on volume.
- Order by access path, for example `(cluster_id, kind, namespace, name,
  observed_at, uid)` for observations and `(cluster_id, fact_key, fact_value,
  ts, object_id)` for facts.
- Keep topology intervals as `valid_from_ms` and `valid_to_ms` so overlap
  predicates stay portable.
- Store proof payloads as `String CODEC(ZSTD)` first; benchmark the new
  ClickHouse `JSON` type for hot/recent documents before making it default.
- Use TTL `TO VOLUME` policies to move older parts to S3-compatible cold
  storage after the hot window. Keep this disabled unless the ClickHouse server
  has a matching storage policy.
- The ClickHouse MVP schema adds lightweight bloom-filter skip indexes for the
  interactive read paths: `versions.object_id`, `versions.uid`,
  `versions.name`, `observations.uid`, `facts.object_id`, `changes.object_id`,
  and both `edges.src_id`/`edges.dst_id`. These keep service investigation,
  topology expansion, object history, and evidence bundles from depending only
  on broad primary-key scans.
- Defer heavier text/JSON skip indexes until measured. Candidate follow-ups
  include selected JSON paths, Event message text, `JSONAllPaths`, and
  `JSONAllValues`.

Important constraints:

- Do not issue frequent tiny synchronous inserts from watch workers. Buffer and
  batch writes, or validate ClickHouse async insert settings.
- Do not model latest rows and open edges as mutation-heavy OLTP tables. Use
  append-only source rows plus materialized views, ReplacingMergeTree read
  models, or argMax queries. `ingestion_offsets` is a current-state health table
  and uses `ReplacingMergeTree(updated_at)` keyed by resource and event so old
  bookmark/watch/list rows can collapse during background merges.
- Treat S3-tiered cold proof retrieval as a measured workflow. Cold query latency
  may be acceptable for proof but not for every interactive filter.

## SQLite Indexes

The default local backend uses the pure-Go `modernc.org/sqlite` driver. This
favors portable builds over the smaller dependency graph of CGO-backed SQLite
drivers.

The initial implementation stores each retained version as full JSON with an
identity blob codec. Compression, reverse deltas, and profitable snapshot
selection remain application-layer work above the storage backend.

Do not compress all SQLite historical blobs by default while SQLite remains the
default local query shape. Compressed blobs preserve proof, but they remove
direct SQL access to body JSON unless every query pays a decompression scan.
Prefer:

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

Future OLTP metadata backend maintenance rules:

- Keep PostgreSQL autovacuum or CockroachDB equivalent maintenance enabled for
  normal churn if those backends are added.
- Monitor dead tuples, range/table bloat, and index bloat on metadata tables.
- Run explicit backend-native maintenance after large retention purges or
  derived-index rebuilds.
- Treat heavy reindexing or table rewrites as offline maintenance, not a normal
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
ClickHouse JSON/skip indexes: optional hot ad-hoc query
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

Future API backends, including ClickHouse, should keep the same product contract:

- expose schema/metadata first,
- execute only read-only SQL for agents,
- apply Kubernetes RBAC-derived row and resource filters before returning rows,
- preserve prebuilt commands as convenience wrappers, not as the only query
  surface.
