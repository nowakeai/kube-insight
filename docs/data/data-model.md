# Data Model

## Logical Layers

```text
clusters / dimensions
api_resources
objects
versions + blobs
latest_raw_index
latest_index
object_edges
object_facts
object_changes
ingestion_offsets
maintenance_runs
```

## Dimensions

Use numeric IDs internally to reduce repeated text in large fact and edge
tables.

```sql
clusters(id, name, uid, source, created_at)
api_resources(
  id,
  api_group,
  api_version,
  resource,
  kind,
  namespaced,
  preferred_version,
  storage_version,
  verbs
)
object_kinds(id, api_resource_id, api_group, api_version, kind)
edge_types(id, name)
fact_keys(id, family, key, value_type)
resource_processing_profiles(
  api_resource_id,
  profile,
  retention_class,
  filter_chain,
  extractor_set,
  compaction_strategy,
  priority,
  max_event_buffer,
  enabled
)
```

`api_resources` is the discovery-backed GroupVersionResource table. It is the
authoritative mapping used by the collector and authorizer when they need the
Kubernetes `group/resource/scope` tuple for list/watch and SAR/SSAR checks.
`object_kinds` can remain as a compact dimension for query tables, but it must
link back to `api_resources`.

Required fields for authorization and watch management:

```text
api_group
api_version
resource
kind
namespaced
verbs
```

Optional discovery metadata:

```text
preferred_version
storage_version
last_discovered_at
removed_at
```

`resource_processing_profiles` stores per-resource behavior. The default profile
uses the generic history path. High-volume or high-value resources can use a
specialized profile without changing the logical storage contract.
The SQLite PoC writes a profile row during discovery and first observation
storage, then reports API resource count and stored version count by profile in
benchmark output.

Initial profiles:

```text
generic
pod_fast_path
node_summary
event_rollup
endpointslice_topology
lease_skip_or_downsample
secret_metadata_only
```

Required default `edge_types`:

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

Required default `fact_keys`:

```text
pod_status.reason
pod_status.last_reason
pod_status.restart_count
pod_status.ready
pod_status.phase
pod_status.qos_class
pod_placement.node_assigned
pod_placement.started
pod_placement.deleted
workload_rollout.deployment_generation
workload_rollout.replicaset_hash
workload_config.image
workload_config.memory_request
workload_config.memory_limit
workload_config.cpu_request
workload_config.cpu_limit
workload_config.probe_changed
node_condition.Ready
node_condition.MemoryPressure
node_condition.DiskPressure
node_condition.PIDPressure
node_status.taint
node_status.capacity
node_status.allocatable
k8s_event.type
k8s_event.reason
k8s_event.message_fingerprint
k8s_event.count
endpoint.ready
endpoint.serving
endpoint.terminating
endpoint.membership
```

## Objects

`objects` is the stable identity table.

```sql
objects(
  id,
  cluster_id,
  kind_id,
  namespace,
  name,
  uid,
  latest_version_id,
  first_seen_at,
  last_seen_at,
  deleted_at
)
```

Identity rules:

- Prefer Kubernetes `metadata.uid` when available.
- Use `cluster/kind/namespace/name` as the human-readable key.
- Track delete/recreate as different objects if UID changes.
- For resources without UID, fall back to namespaced identity.

## Versions

`versions` stores the reconstructable resource history.

```sql
versions(
  id,
  object_id,
  seq,
  observed_at,
  resource_version,
  generation,
  doc_hash,
  materialization,
  strategy,
  blob_ref,
  parent_version_id,
  raw_size,
  stored_size,
  replay_depth,
  summary
)
```

`materialization` values:

```text
full
reverse_delta
cdc_manifest
```

`strategy` values:

```text
full_zstd
json_patch_zstd
cdc_zstd
```

## Blobs

```sql
blobs(
  digest,
  codec,
  raw_size,
  stored_size,
  data
)
```

The blob layer should be content-addressed. It can later move from SQL storage
to object storage without changing the logical model.

## Latest Snapshots

Latest data is split into two query surfaces:

- `latest_raw_index` / `latest_raw_documents`: latest observed sanitized
  cluster snapshot. This preserves runtime fields such as `resourceVersion`,
  `generation`, Event counters, and controller heartbeat values. Secret payload
  values are still redacted; key names can be retained.
- `latest_index` / `latest_documents`: latest retained history proof. This
  points at the newest normalized `versions` row and can intentionally omit
  high-churn fields filtered before retained hashing.

```sql
latest_raw_index(
  object_id,
  cluster_id,
  kind_id,
  namespace,
  name,
  uid,
  observed_at,
  observation_type,
  resource_version,
  generation,
  doc_hash,
  raw_size,
  doc
)

latest_index(
  object_id,
  cluster_id,
  kind_id,
  namespace,
  name,
  uid,
  latest_version_id,
  observed_at
)
```

Use `latest_raw_documents` when a human or agent needs the current observed
cluster shape. Use `latest_documents` when the question needs the latest
retained proof document. `latest_index` remains rebuildable from `versions`;
`latest_raw_index` is overwritten by future observations and is not historical
proof. Deleted objects are removed from `latest_raw_index`; delete history
remains available through observations and retained versions.

## Historical Topology

`object_edges` stores time-valid graph edges.

```sql
object_edges(
  id,
  cluster_id,
  edge_type,
  src_id,
  dst_id,
  valid_from,
  valid_to,
  src_version_id,
  dst_version_id,
  confidence,
  detail
)
```

`open_edges` tracks currently active edges for efficient ingestion:

```sql
open_edges(
  cluster_id,
  edge_type,
  src_id,
  dst_id,
  edge_id
)
```

Only write edge rows when relationships change.

## Troubleshooting Facts

`object_facts` stores queryable incident evidence.

```sql
object_facts(
  id,
  cluster_id,
  ts,
  object_id,
  version_id,
  kind_id,
  namespace,
  name,
  node_id,
  workload_id,
  service_id,
  fact_key_id,
  fact_value,
  numeric_value,
  severity,
  detail
)
```

Keep `detail` small. Full JSON belongs in `versions`.

## Change Summary

`object_changes` stores small timeline entries used by the UI and investigation
ranking.

```sql
object_changes(
  id,
  cluster_id,
  ts,
  object_id,
  version_id,
  change_family,
  path,
  op,
  old_scalar,
  new_scalar,
  severity
)
```

This is not the full diff. It is a query aid.

## Ingestion Offsets

```sql
ingestion_offsets(
  cluster_id,
  api_resource_id,
  namespace,
  resource_version,
  last_list_at,
  last_watch_at,
  last_bookmark_at,
  status,
  error,
  updated_at
)
```

For cluster-scoped resources, `namespace` is null. For namespaced resources,
`namespace` is null for an all-namespaces watch and set to the namespace name
for a namespace-scoped watch. Offsets let the collector resume and make gap
detection explicit.

The same table powers watch health:

```text
kube-insight db resources health --errors-only
kube-insight db resources health --stale-after 5m
```

Health output is intended for humans, automation, and agents to decide whether
an evidence answer is based on fresh complete watch data or a partial/stale
resource stream.

## Storage Maintenance

High-churn watch ingestion creates dead rows and index bloat in SQL backends.
Maintenance policy is part of the data model, not an operational afterthought.

Track maintenance runs:

```sql
maintenance_runs(
  id,
  cluster_id,
  backend,
  task,
  started_at,
  finished_at,
  status,
  rows_scanned,
  rows_changed,
  bytes_before,
  bytes_after,
  error
)
```

Required tasks:

```text
compact_versions
compact_edges
purge_retention
rebuild_derived_indexes
vacuum_or_analyze
```

SQLite should run incremental vacuum when enabled, `wal_checkpoint`, and
`ANALYZE` after large ingestion or purge jobs. PostgreSQL should rely on
autovacuum for normal load, but retention purges and large rebuilds should
schedule explicit `VACUUM (ANALYZE)` or backend-specific maintenance windows
when bloat or query plans degrade.
