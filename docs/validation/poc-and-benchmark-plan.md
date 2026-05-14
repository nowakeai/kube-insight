# PoC And Benchmark Plan

This document defines what to measure during the PoC. The broader correctness,
security, integration, and release gates are defined in
[Test Acceptance Plan](test-acceptance-plan.md).

## Goals

Validate:

1. Resource history compression ratio.
2. Historical reconstruction latency.
3. Topology storage size and query latency.
4. Fact index size and query latency.
5. Service investigation end-to-end latency.
6. Global watcher coverage, stability, and API/storage pressure.
7. Backend tradeoffs across SQLite and PostgreSQL/TimescaleDB.
8. Filter correctness, redaction safety, and discarded-change accounting.
9. Delete detection and relist reconciliation correctness.
10. Storage maintenance impact, including vacuum/analyze behavior.

## Dataset

Use the existing kubeconfig context:

```text
staging
```

Collect via global discovery watcher:

```bash
kube-insight dev collect samples --context staging --discover-resources --out data/kube-samples-staging.json
```

Minimum typed resources:

```text
Pod
Deployment
ReplicaSet
Service
EndpointSlice
Node
Event
ConfigMap
StatefulSet
DaemonSet
Job
CronJob
```

Also store discovered CRDs and other list/watch-capable resources as generic
history, unless excluded by policy.

Generate mock history from real samples:

- Pod restarts.
- OOMKilled and lastState transitions.
- Node condition transitions.
- Deployment image/resource/probe changes.
- Pod rescheduling.
- EndpointSlice membership changes.
- Same-node workload churn.

## PoC 1: Resource History Store

Measure:

- raw full-copy bytes,
- full Zstd bytes,
- reverse-delta bytes,
- fallback full count,
- snapshot count,
- latest read latency,
- historical reconstruction latency.

Pass condition:

- Stored bytes materially lower than full compressed history.
- Historical reconstruction bounded by `max_delta_chain`.

## PoC 1.5: Global Watcher

Implement:

- discovery registry,
- dynamic unstructured client,
- per-GVR list/watch workers,
- ingestion offset table,
- configurable filter pipeline,
- Secret metadata-only redaction.

Measure:

- discovered GVR count,
- active watcher count,
- initial list duration,
- events/sec by GVR,
- watch restarts,
- relists after stale resourceVersion,
- periodic relist duration,
- delete tombstones written,
- missing objects reconciled,
- unknown-visibility objects,
- filter decisions by outcome,
- skipped/redacted objects,
- stored versions by GVR,
- API QPS,
- write throughput.

Pass condition:

- All allowed resources are stored as generic history.
- P0 resources still feed typed topology/fact extractors.
- Watch gaps recover through relist.
- Deletes are represented by watch tombstones or relist reconciliation.
- Missing objects are not falsely marked deleted after permission/scope changes.
- No Secret payloads are stored by default.

## PoC 2: Topology Store

Implement:

- `object_edges`
- `open_edges`
- Pod -> Node
- Pod -> ReplicaSet
- ReplicaSet -> Deployment
- EndpointSlice -> Pod

Measure:

- edge rows per resource version,
- open edge count,
- interval query latency,
- service->pod query latency,
- node->pod query latency.

Pass condition:

- Edge rows grow with relationship changes, not raw version count.
- Service/time-window lookup is interactive.

## PoC 3: Fact Store

Implement facts:

- OOMKilled,
- Evicted,
- restart count,
- Ready condition,
- Node conditions,
- Deployment image/resource/probe changes.

Measure:

- fact rows per version,
- fact index bytes,
- OOMKilled query latency,
- rollout-change query latency,
- node-pressure query latency.

Pass condition:

- Fact index remains much smaller than universal historical JSON KV index.
- Common incident predicates are fast.

## PoC 4: Investigation Query

Implement:

```bash
kube-insight query service SERVICE --namespace NS --from T1 --to T2
```

Flow:

```text
service -> endpointslices -> pods -> nodes/events -> facts -> changes -> reconstruct evidence
```

The current SQLite path accepts `--from` and `--to` for RFC3339 or
`YYYY-MM-DD` windows. It filters facts, changes, and topology edges by the
requested interval, then ranks related objects by evidence score before
returning the bundle. It reconstructs exact JSON versions from `versions` and
`blobs` for the Service and top-ranked evidence objects, bounded by
`--max-evidence-objects` and `--max-versions-per-object`.

Measure:

- total query latency,
- service investigation latency from the benchmark `service_investigation_*`
  fields,
- number of candidate objects,
- number of reconstructed versions,
- result usefulness on generated scenarios.

Pass condition:

- Investigation does not scan all historical JSON.
- Evidence bundle identifies injected scenarios.

## PoC 5: Backend Comparison

SQLite:

- single file size,
- write throughput,
- query latency,
- index size.

PostgreSQL:

- JSONB GIN hot query latency,
- GiST topology interval latency,
- write throughput,
- table/index size.

TimescaleDB:

- fact hypertable compression,
- compressed cold query latency,
- retention behavior.

Maintenance:

- SQLite `wal_checkpoint`, incremental vacuum when enabled, and `ANALYZE`.
- PostgreSQL autovacuum behavior after sustained ingestion.
- Explicit `VACUUM (ANALYZE)` after retention purge or large rebuild.
- Index bloat before and after purge/compaction.

Do not use local host port `5432` for tests if occupied. Use `55432` or another
free port.

## Core Acceptance Metrics

Performance:

- latest object lookup p50/p95,
- historical version reconstruction p50/p95 and replay steps,
- topology interval query p50/p95,
- fact lookup p50/p95,
- service investigation p50/p95,
- cold rebuild duration for facts and edges.

Throughput and reliability:

- watch events/sec by GVR,
- sustained stored versions/sec,
- stored versions/sec by processing profile,
- API resource count by processing profile,
- initial LIST duration by GVR,
- watch restart count,
- stale resourceVersion relist count,
- offset commit lag,
- max queue depth,
- backpressure event count,
- queue wait duration,
- dead-letter count,
- delete tombstone count,
- relist-confirmed delete count,
- unknown-visibility count.

Storage:

- raw JSON bytes,
- retained version bytes,
- blob bytes,
- index bytes,
- latest_index bytes,
- fact and edge bytes,
- compression ratio,
- bytes per object-version,
- bytes reclaimed by compaction and vacuum.

Filter and security:

- processing profile selected by GVR,
- resources discarded by policy,
- changes discarded by policy,
- redacted and removed field counts from filter decision metadata,
- Secret payload removals and retained-payload violations,
- ConfigMap/path exclusions applied,
- authorization denied count,
- partially redacted response count,
- audit rows written for sensitive operations.

Operational health:

- API server QPS and throttling,
- storage write latency p50/p95,
- queue depth and wait duration by GVR,
- queue depth by priority class,
- max queue depth and total queue wait time for a benchmark run,
- filter latency p50/p95,
- extractor latency p50/p95,
- vacuum/analyze duration,
- post-maintenance query latency delta.

Resource-specific acceptance:

- Pod heartbeat discard ratio and missed-transition count,
- Pod fact rows and status change rows per meaningful transition,
- Node status bytes retained versus raw bytes,
- Node condition fact coverage and node change summary count,
- Event rollup ratio and unique message fingerprint count,
- Event fact coverage by reason/type/involved object,
- EndpointSlice edge rows per membership change,
- EndpointSlice readiness fact coverage,
- Lease skipped/downsampled count,
- high-priority event processing lag versus low-priority lag.

## Metrics Report

The local generated-data benchmark is available as:

```bash
kube-insight dev benchmark local --fixtures testdata/fixtures/kube --output testdata/generated/benchmark-samples --db kube-insight-benchmark.db --query-runs 25
```

The live watch benchmark is available as:

```bash
kube-insight dev benchmark watch --context staging --db kube-insight-watch-benchmark.db --resource pods --resource services --duration 30s --concurrency 2 --retries 3
```

The local PoC validation gate is available as:

```bash
kube-insight dev validate poc --fixtures testdata/fixtures/kube --output testdata/generated/poc-validation --db kube-insight-poc-validation.db --clusters 1 --copies 2 --query-runs 3
```

The local demo entrypoint runs validation and writes a Service evidence bundle:

```bash
./scripts/poc-demo.sh
```

It runs the generated-data benchmark and emits a JSON pass/fail report with
checks for storage, resource profiles, Secret safety, Event facts, Pod/Node/
EndpointSlice evidence, Service investigation latency, reconstructed versions,
compact version diffs, and a fake multi-GVR watch reconciliation scenario. The
fake watch check verifies relist-confirmed deletes and unknown-visibility counts
without requiring a live cluster. It also verifies per-GVR queue metrics through
the fake watch `resourceQueue` report, including processing-profile priority for
each queued resource. The report includes a compact
`summaryText` line plus threshold checks for storage growth, latest lookup p95,
historical get p95, Service investigation p95, reconstructed versions, and
version diffs.

Each benchmark should output:

```json
{
  "dataset": "staging",
  "duration_seconds": 0,
  "objects": 1807,
  "versions": 18070,
  "deleted_objects": 0,
  "unknown_visibility_objects": 0,
  "relist_confirmed_deletes": 0,
  "raw_bytes": 150108706,
  "stored_bytes": 0,
  "index_bytes": 0,
  "blob_bytes": 0,
  "fact_bytes": 0,
  "edge_bytes": 0,
  "compression_ratio": 0,
  "watch_events_per_second": 0,
  "stored_versions_per_second": 0,
  "stored_versions_by_profile": {},
  "api_resources_by_profile": {},
  "processing_profiles": 0,
  "disabled_processing_profiles": 0,
  "watch_restarts": 0,
  "stale_relist_count": 0,
  "periodic_relist_count": 0,
  "offset_lag_ms": 0,
  "max_queue_depth": 0,
  "backpressure_events": 0,
  "queue_wait_ms": 0,
  "watch_resource_queue": [],
  "filter_kept": 0,
  "filter_modified": 0,
  "filter_discarded_change": 0,
  "filter_discarded_resource": 0,
  "pod_heartbeat_discard_ratio": 0,
  "pod_missed_transition_count": 0,
  "pod_status_change_rows": 0,
  "node_condition_fact_rows": 0,
  "node_retained_bytes": 0,
  "event_rollup_ratio": 0,
  "event_message_fingerprints": 0,
  "event_fact_rows": 0,
  "endpointslice_edge_rows_per_change": 0,
  "endpointslice_readiness_fact_rows": 0,
  "lease_skipped_count": 0,
  "redacted_fields": 0,
  "secret_payload_violations": 0,
  "vacuum_ms": 0,
  "bytes_reclaimed": 0,
  "write_ms": 0,
  "latest_lookup_ms": 0,
  "latest_lookup_p50_ms": 0,
  "latest_lookup_p95_ms": 0,
  "historical_get_ms": 0,
  "historical_get_p50_ms": 0,
  "historical_get_p95_ms": 0,
  "service_investigation_ms": 0,
  "service_investigation_p50_ms": 0,
  "service_investigation_p95_ms": 0,
  "service_investigation_versions": 0,
  "service_investigation_diffs": 0,
  "fact_query_ms": 0,
  "fact_query_p50_ms": 0,
  "fact_query_p95_ms": 0,
  "topology_query_ms": 0,
  "topology_query_p50_ms": 0,
  "topology_query_p95_ms": 0,
  "query_runs": 25
}
```

## Scenario Tests

Required generated scenarios:

1. OOMKilled Pod recovered before investigation.
2. Pod evicted and recreated on another Node.
3. Deployment memory limit briefly reduced and rolled back.
4. Node entered MemoryPressure during a service spike.
5. High-resource batch Pod colocated with affected service Pod.
6. Kubernetes Event expired but mirrored fact remains queryable.

## MVP Success Criteria

```text
Given service + namespace + time window,
return a ranked evidence bundle with topology, facts, and exact historical
resource diffs without scanning all resource history.
```
