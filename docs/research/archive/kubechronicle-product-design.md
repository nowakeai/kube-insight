# KubeChronicle Product Design

## 1. Product Definition

KubeChronicle is a historical evidence store for Kubernetes clusters.

It continuously records Kubernetes resource history, reconstructs historical
topology, and extracts troubleshooting facts so platform teams can investigate
incidents after the cluster has already returned to a healthy state.

The product is not a generic JSON compression database anymore. The storage
optimizer is still important, but it becomes an internal engine. The product
promise is:

```text
When something briefly went wrong in Kubernetes, KubeChronicle can show what
changed, where it ran, what it depended on, and what else was happening nearby.
```

Working name:

```text
KubeChronicle
```

Other naming candidates:

| Name | Positioning |
| --- | --- |
| KubeChronicle | Historical cluster record and timeline. |
| KubeTrail | Kubernetes resource audit trail, similar mental model to CloudTrail. |
| ClusterFlightRecorder | Strong troubleshooting metaphor, but longer and less product-like. |
| KubeEvidence | Strong incident-analysis positioning, but narrower and more forensic. |
| KubeReplay | Emphasizes historical reconstruction and topology replay. |

For now, use `KubeChronicle` in product docs and keep `DiffStor` as the name of
the internal compact-history storage engine.

## 2. Problem

Kubernetes is extremely dynamic. Pods are recreated, nodes change pressure
states, controllers mutate status, Deployments roll forward and back, and Events
expire quickly.

This creates a common troubleshooting gap:

```text
A service had a small latency or error-rate spike several hours ago.
No alert fired.
The current cluster state is healthy.
The affected Pods may have restarted or moved.
Kubernetes Events may already be gone.
The team wants to know whether the cause was OOM, eviction, rollout,
misconfiguration, node pressure, or noisy neighbors.
```

Existing tools usually hold only part of the evidence:

| Tool type | What it keeps | Gap |
| --- | --- | --- |
| Metrics | Time-series symptoms | Weak object-level causality. |
| Logs | Application/runtime messages | Missing full K8S object history and topology. |
| Kubernetes Events | Useful lifecycle clues | Short retention and incomplete coverage. |
| GitOps history | Desired state changes | Misses live status, scheduler placement, controllers, and runtime drift. |
| Current `kubectl` state | Current truth | Useless after recovery. |
| Generic log warehouse | Searchable facts | Does not preserve reconstructable K8S resource versions by default. |

KubeChronicle fills the missing layer: historical Kubernetes state and topology
as an investigation data source.

## 3. Primary Users

### Platform Engineers

They debug cluster and workload behavior:

- Which Pods backed this Service during the spike?
- Did any affected Pod restart, get OOMKilled, or become unready?
- Did the Pod move to another Node?
- Was the Node under memory, disk, PID, or network pressure?
- Was there a rollout, image change, env/config change, probe change, or
  resource limit change?
- Which other Pods were on the same Node at the time?

### SRE / Observability Engineers

They connect telemetry symptoms to Kubernetes evidence:

- Start from a time window and service name.
- Pull an evidence bundle around the spike.
- Link K8S changes to metrics, logs, traces, and incident notes.
- Preserve expired Events and historical object versions for later review.

### Compliance / Platform Governance

They need historical reconstruction:

- What was the exact Deployment spec last Tuesday?
- Which controller changed this object?
- What resources had privileged settings during a time range?
- Which workloads ran on a given Node during an incident window?

## 4. Product Principles

### 4.1 Full History Is The Source Of Truth

KubeChronicle must preserve enough data to reconstruct every retained resource
version exactly, or with explicitly configured ignored fields.

Derived indexes can be rebuilt. Historical versions cannot.

### 4.2 Troubleshooting Should Not Require Full Scans

The product should not answer common incident questions by scanning and
decompressing every historical JSON document.

It should query compact relationship and fact indexes first, then reconstruct
exact JSON versions only for shortlisted evidence.

### 4.3 Kubernetes-Aware, Storage-Engine-Agnostic

The product is Kubernetes-specific at the ingestion, topology, and
troubleshooting layers.

The physical backend should remain pluggable:

- SQLite for local agent, PoC, edge, and single-file archives.
- PostgreSQL/TimescaleDB for central service and flexible recent JSON queries.
- CockroachDB for distributed metadata if needed later.
- ClickHouse for analytical sidecar workloads if query volume demands it.

### 4.4 Do Not Index Everything By Default

Universal historical JSON indexing is attractive but expensive. Previous PoC
results showed that a full scalar KV index made queries fast but expanded the
database far beyond the compressed payload.

Default indexing should focus on:

- topology relationships,
- lifecycle and condition facts,
- rollout/config facts,
- placement and resource facts,
- user-promoted JSON paths.

## 5. Core Product Experience

The main product flow starts from a symptom, not from a database query.

```text
User input:
  cluster
  namespace/service/workload
  time window
  optional symptom: latency spike, error spike, restart, node issue

KubeChronicle output:
  historical topology
  suspicious facts
  ranked evidence timeline
  exact before/after resource diffs
  links to related metrics/logs/traces if configured
```

Example:

```text
Investigate service checkout-api in production from 10:05 to 10:20.
```

Expected answer:

```text
10:06:12 Pod checkout-api-7f9c... became unready for 44s.
10:06:20 Container app lastState terminated with OOMKilled.
10:06:23 Pod restarted on node ip-10-0-4-91.
10:05:58 Same node had batch-worker-... memory request 8Gi, limit 16Gi.
10:04:11 Node condition MemoryPressure changed to True.
10:02:45 Deployment checkout-api template changed memory limit from 512Mi to 384Mi.
```

Then the user can open exact historical JSON and diffs for each evidence item.

## 6. Data Model

KubeChronicle stores four logical data products:

```text
1. Resource history
2. Topology history
3. Troubleshooting facts
4. Latest state index
```

See [Storage, index, and query design](kubechronicle-storage-index-query-design.md) for the
implementation-level schema, indexes, and query recipes.

### 6.1 Resource History

This is the reconstructable source of truth.

Logical schema:

```sql
clusters(
  id,
  name,
  uid,
  source,
  created_at
)

objects(
  id,
  cluster_id,
  api_version,
  kind,
  namespace,
  name,
  uid,
  latest_version_id,
  first_seen_at,
  last_seen_at,
  deleted_at
)

versions(
  id,
  object_id,
  seq,
  observed_at,
  resource_version,
  generation,
  strategy,
  materialization,
  blob_ref,
  parent_version_id,
  raw_size,
  stored_size,
  summary
)
```

Resource identity should prefer Kubernetes UID when available, with
`cluster/kind/namespace/name` as the human-readable key.

`parent_version_id` can point to the next newer version for reverse-delta
storage. The latest version should remain full so latest reads stay cheap.

### 6.2 Topology History

Topology is time-valid, because Service membership, Pod placement, and owner
chains change.

```sql
object_edges(
  cluster_id,
  edge_type,
  src_kind,
  src_object_id,
  dst_kind,
  dst_object_id,
  valid_from,
  valid_to,
  src_version_id,
  dst_version_id,
  confidence
)
```

Default edge types:

| Edge | Meaning |
| --- | --- |
| `service_selects_pod` | Service selector matched a Pod during this interval. |
| `endpointslice_targets_pod` | EndpointSlice pointed at a Pod. |
| `pod_owned_by_replicaset` | Pod owner reference points to ReplicaSet. |
| `replicaset_owned_by_deployment` | ReplicaSet owner reference points to Deployment. |
| `pod_on_node` | Pod was scheduled on a Node. |
| `pod_uses_configmap` | Pod template references a ConfigMap. |
| `pod_uses_secret` | Pod template references a Secret. |
| `workload_uses_pvc` | Workload references persistent storage. |

Indexes:

```sql
create index object_edges_src_time_idx
on object_edges(cluster_id, src_kind, src_object_id, edge_type, valid_from, valid_to);

create index object_edges_dst_time_idx
on object_edges(cluster_id, dst_kind, dst_object_id, edge_type, valid_from, valid_to);
```

### 6.3 Troubleshooting Facts

Facts are compact extracted signals optimized for incident queries.

```sql
object_facts(
  cluster_id,
  ts,
  object_id,
  version_id,
  kind,
  namespace,
  name,
  node_name,
  workload_id,
  service_id,
  fact_family,
  fact_key,
  fact_value,
  numeric_value,
  severity,
  detail
)
```

Default fact families:

| Family | Examples |
| --- | --- |
| `pod_status` | `OOMKilled`, `Evicted`, `CrashLoopBackOff`, restart count, readiness transitions. |
| `pod_placement` | Node assignment, Pod UID changes, start time, deletion time. |
| `workload_rollout` | Deployment generation, ReplicaSet hash, image changes, rollout windows. |
| `workload_config` | env, config refs, secret refs, resource requests/limits, probes. |
| `node_condition` | `Ready`, `MemoryPressure`, `DiskPressure`, `PIDPressure`. |
| `node_occupancy` | same-node Pods, QoS class, requests/limits, high resource footprint. |
| `k8s_event` | mirrored Event reason/message before Kubernetes retention drops it. |

Indexes:

```sql
create index object_facts_lookup_idx
on object_facts(cluster_id, fact_family, fact_key, fact_value, ts desc);

create index object_facts_object_time_idx
on object_facts(cluster_id, object_id, ts desc);

create index object_facts_workload_time_idx
on object_facts(cluster_id, workload_id, ts desc)
where workload_id is not null;

create index object_facts_node_time_idx
on object_facts(cluster_id, node_name, ts desc)
where node_name is not null;
```

The `detail` field can hold a small JSON payload, but it should not duplicate
the full resource document.

### 6.4 Latest State Index

Latest state supports interactive browse and current-state filters.

```sql
latest_index(
  object_id primary key,
  cluster_id,
  api_version,
  kind,
  namespace,
  name,
  uid,
  latest_version_id,
  observed_at,
  doc
)
```

Backends can index this differently:

- SQLite: generated columns and expression indexes.
- PostgreSQL: JSONB GIN and expression indexes.
- CockroachDB: inverted JSON indexes.
- ClickHouse: materialized columns and projections.

Latest state is not the main incident data source; it is the navigation and
current-state layer.

## 7. Query Model

### 7.1 Investigation Query

Input:

```text
cluster = staging
service = checkout-api
namespace = production
time_window = 10:05..10:20
lookback = 30m
lookahead = 30m
```

Execution:

```text
+-------------------------+
| service + time window   |
+-----------+-------------+
            |
            v
+-------------------------+
| resolve historical Pods |
+-----------+-------------+
            |
            v
+------------------------------+
| expand to workload and Nodes |
+--------------+---------------+
               |
               v
+---------------------------------+
| query facts around the window   |
| pod status / rollout / node     |
| pressure / same-node occupancy  |
+--------------+------------------+
               |
               v
+-------------------------------+
| rank suspicious evidence       |
+--------------+----------------+
               |
               v
+-------------------------------+
| reconstruct exact JSON versions|
| and show diffs                 |
+-------------------------------+
```

The query result should be an evidence bundle, not a raw row list.

### 7.2 Evidence Ranking

Initial ranking signals:

- Time proximity to the spike.
- Severity: OOMKilled, Evicted, Node pressure, readiness loss.
- Scope: one Pod, all Pods of a workload, all Pods on a Node.
- Causality hints: rollout before failures, Node pressure before eviction,
  config change before restart.
- Change magnitude: resource limit reduced, image changed, probe changed.

### 7.3 Historical Diff

Diff is a view, not the first query primitive.

The system should reconstruct two versions and produce a human-readable summary:

```text
Deployment checkout-api changed at 10:02:45

spec.template.spec.containers[name=app].resources.limits.memory
  512Mi -> 384Mi

spec.template.spec.containers[name=app].image
  checkout:v1.42.1 -> checkout:v1.42.2
```

For Kubernetes lists, use semantic identities such as container name, env var
name, volume name, condition type, and owner UID when known. Fall back to
schema-less list strategies when the resource shape is unknown.

## 8. Ingestion

### 8.1 Sources

MVP ingestion sources:

- Kubernetes watch API for resources.
- Periodic full list snapshots to recover from watch gaps.
- Kubernetes Events watch, mirrored into facts.
- Optional local kubeconfig collector for PoC and offline sample generation.

Resource types for MVP:

| Priority | Resources |
| --- | --- |
| P0 | Pod, Deployment, ReplicaSet, Service, EndpointSlice, Node, Event. |
| P1 | StatefulSet, DaemonSet, Job, CronJob, ConfigMap, Secret metadata only, PVC. |
| P2 | Ingress, Gateway API resources, HPA, PDB, NetworkPolicy, custom resources. |

Secret payloads should be excluded by default. Store metadata, refs, and hashes
only unless explicitly configured.

### 8.2 Write Pipeline

```text
watch/list event
  -> normalize and redact
  -> identify object
  -> store resource version
  -> update latest_index
  -> extract topology edges
  -> extract troubleshooting facts
  -> commit
```

The version store and derived indexes should be committed atomically when the
backend supports it. If not, indexes must be rebuildable from version history.

### 8.3 Normalization

Default ignored or normalized fields:

- `metadata.resourceVersion`
- `metadata.managedFields`, unless audit-level retention is enabled
- high-churn timestamps when not needed for troubleshooting
- controller-populated status fields that are extracted separately as facts

Normalization must be explicit and visible in strategy reports. Users should be
able to disable it for compliance-grade retention.

## 9. Storage Strategy

KubeChronicle should keep storage optimization, but it should not be the product
story.

Recommended physical strategy:

| Data | Default storage |
| --- | --- |
| Latest resource | Full JSON for fast read and indexing. |
| Historical resource | Reverse delta + compressed snapshots, with full fallback. |
| Large subtrees | Optional subtree CDC for ConfigMap data and large Node status arrays. |
| Topology edges | Relational rows with validity intervals. |
| Facts | Relational rows, optionally time-partitioned. |
| Hot ad-hoc JSON | Optional PostgreSQL JSONB GIN on recent full versions. |
| Cold ad-hoc JSON | Async scan or promoted fact path. |

Reverse delta remains useful because Kubernetes resources mutate frequently and
share large stable structure across versions. But if a delta is too large, store
a compressed full snapshot.

Default thresholds:

```text
small_doc_limit = 4 KiB
large_doc_limit = 64 KiB
max_delta_chain = 10
max_delta_full_ratio = 1.0
snapshot_ratio = 0.75
```

## 10. Backend Strategy

The product should expose a logical storage adapter:

```text
put_version()
get_version()
query_latest()
query_edges()
query_facts()
rebuild_indexes()
compact_history()
```

Backend recommendations:

| Backend | Role |
| --- | --- |
| SQLite | Local PoC, single-node archive, agent-side cache. |
| PostgreSQL | Default service backend for metadata, facts, topology, and JSONB hot queries. |
| TimescaleDB | PostgreSQL extension for time-partitioned facts and compressed cold history. |
| CockroachDB | Future distributed metadata store if multi-region writes matter. |
| ClickHouse | Optional analytical sidecar for high-volume fact/log exploration. |

For PostgreSQL/TimescaleDB:

- Keep recent full JSON chunks uncompressed if JSONB GIN queries matter.
- Compress old chunks for retention.
- Do not expect compressed Timescale chunks to behave like normal JSONB GIN
  storage.
- Keep facts and topology as first-class indexed tables.

## 11. Product Surface

### 11.1 CLI

```bash
kubechronicle collect --context staging --out data/kube-samples.json
kubechronicle ingest --context staging --cluster staging
kubechronicle investigate service checkout-api \
  --namespace production \
  --from 2026-05-11T10:05:00Z \
  --to 2026-05-11T10:20:00Z
kubechronicle diff deployment checkout-api --at 2026-05-11T10:10:00Z
kubechronicle topology service checkout-api --at 2026-05-11T10:10:00Z
```

### 11.2 API

```text
GET /clusters
GET /objects?kind=Pod&namespace=production
GET /objects/{id}/latest
GET /objects/{id}/versions
GET /objects/{id}/versions/{version_id}
GET /objects/{id}/diff?from=&to=

POST /investigations
GET /investigations/{id}
GET /topology?root=&at=
GET /facts?object_id=&from=&to=
```

### 11.3 UI

MVP screens:

- Cluster timeline.
- Namespace/service/workload explorer.
- Investigation result view.
- Topology-at-time graph.
- Evidence timeline.
- Resource version diff.
- Storage/index strategy report.

The UI should start from operational questions, not JSON browsing.

## 12. MVP Scope

### Build First

- Kubeconfig collector using a selected context.
- Watch/list ingestion for P0 resources.
- Resource version store with compression and reverse-delta fallback.
- Latest index.
- Topology extraction for Service -> Pod -> ReplicaSet -> Deployment and Pod ->
  Node.
- Fact extraction for Pod status, restart, OOMKilled, Evicted, readiness,
  Deployment rollout/config, and Node conditions.
- Investigation CLI that returns an evidence bundle for service + time window.
- SQLite PoC backend.
- PostgreSQL/TimescaleDB benchmark backend.

### Defer

- Full SaaS UI.
- Multi-cluster RBAC and tenancy.
- Arbitrary historical JSON indexing for all fields.
- Long-term object storage tiering.
- ClickHouse sidecar.
- Automated root-cause claims. The product should show evidence first.

## 13. Validation Plan

Use real `staging` kubeconfig data as the baseline, then generate realistic
version histories.

Core measurements:

| Metric | Target |
| --- | --- |
| Resource compression ratio | Materially smaller than full Zstd history. |
| Latest object lookup | O(1) logical lookup. |
| Historical reconstruction | Bounded by `max_delta_chain`. |
| Service investigation latency | Interactive for common windows. |
| Fact index size | Much smaller than universal historical JSON KV index. |
| Topology correctness | Service/Pod/Node relationships match Kubernetes snapshots. |
| Evidence usefulness | Finds OOM, eviction, rollout, node pressure, and noisy-neighbor candidates. |

PoC scenarios:

1. OOMKilled Pod recovered before investigation.
2. Pod evicted and rescheduled to a different Node.
3. Deployment memory limit briefly reduced and rolled back.
4. Node entered MemoryPressure during a small latency spike.
5. Same-node batch Pod increased resource footprint near the spike.
6. Kubernetes Events expired, but mirrored facts remain searchable.

## 14. Key Product Decision

The product should be positioned as:

```text
Kubernetes historical state, topology, and troubleshooting evidence.
```

Not:

```text
Generic schemaless JSON diff storage.
```

This sharper scope makes the design easier to explain, easier to sell, and
easier to validate with real operational incidents. The compression engine is
still a differentiator, but the user-facing value is faster incident
understanding after the original evidence has disappeared from Kubernetes.
