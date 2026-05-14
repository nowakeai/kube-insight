# Test Acceptance Plan

## Goal

Prove that `kube-insight` can collect Kubernetes history, preserve useful
evidence, answer incident questions without scanning all historical JSON, and do
so within acceptable storage, performance, and security limits.

This plan defines test layers and release gates. Benchmark metric definitions
live in [PoC And Benchmark Plan](poc-and-benchmark-plan.md).

## Acceptance Gates

### Gate 0: Developer Correctness

Required before merging core changes:

- unit tests pass,
- schema migration tests pass,
- extractor golden tests pass,
- filter golden tests pass,
- no Secret payload is stored in safe-default mode,
- generated evidence fixtures are deterministic.

### Gate 1: Local PoC

Required before declaring the local PoC useful:

- collect from a real kubeconfig context,
- store all allowed resources or explicitly skipped resources by policy,
- reconstruct retained versions within `max_delta_chain`,
- answer service investigation scenarios from SQLite,
- produce a benchmark JSON report,
- pass `kube-insight validate poc`,
- pass all generated incident scenarios.

### Gate 2: Central Service

Required before multi-user service testing:

- PostgreSQL backend passes the same correctness suite as SQLite,
- SAR/SSAR authorization filters raw resources, facts, edges, diffs, and
  support bundles,
- audit rows are written for allow, deny, and partial responses,
- retention purge removes source versions and derived indexes,
- vacuum/analyze maintenance is measured after purge and rebuild jobs.

### Gate 3: Production Readiness

Required before production deployment:

- sustained watcher load test passes for target cluster size,
- watch gaps recover through relist,
- delete events are represented by watch tombstones or relist reconciliation,
- API server throttling stays within configured limits,
- storage bloat and query latency remain stable after retention cycles,
- security regression suite passes.

## Test Layers

## Unit Tests

Focus:

- object identity resolution,
- UID delete/recreate behavior,
- filter outcomes,
- document hash behavior after filtering,
- redaction rules,
- resource-specific extractor functions,
- reverse-delta reconstruction,
- fact severity scoring,
- edge interval open/close logic,
- authorization decision mapping.

Acceptance:

- deterministic outputs for golden inputs,
- no mutation of input objects unless explicitly intended,
- every destructive filter has an auditable reason,
- every extractor can run during rebuild from stored versions.

## Schema And Storage Tests

SQLite:

- create schema from scratch,
- migrate schema forward,
- insert versions, blobs, latest rows, facts, edges, offsets, and maintenance
  rows,
- reconstruct versions across full snapshots and reverse deltas,
- run `ANALYZE`, `wal_checkpoint`, and incremental vacuum where enabled.

PostgreSQL:

- create schema from scratch,
- verify indexes exist,
- query topology interval overlap,
- test JSONB hot latest query,
- run retention purge and explicit `VACUUM (ANALYZE)`,
- verify derived indexes are purged with source versions.

Acceptance:

- schema migrations are idempotent in test environments,
- all required indexes are present,
- historical reconstruction is bounded by `max_delta_chain`,
- purge does not leave orphan facts, edges, changes, or blobs.

## Filter Tests

Test cases:

- Secret data and `stringData` are removed,
- token-like env values are redacted,
- ConfigMap path exclusions work,
- managedFields handling changes by mode,
- unchanged documents become `discard_change`,
- Lease skip/downsample decisions are counted,
- delete observations are never discarded by change filters.

Acceptance:

- zero Secret payload retention violations,
- redaction metadata records filter name and version,
- discarded changes still advance durable offsets,
- compliance mode disables destructive normalization except forbidden paths.

## Extractor Tests

Resource-specific golden fixtures:

- Pod lifecycle, restart, OOMKilled, readiness, scheduling, deletion,
- Node condition, taint, capacity, allocatable, label changes,
- Event rollup by involved object, reason, type, message fingerprint,
- EndpointSlice membership and readiness transitions,
- Service selector, port, type, and load balancer status changes,
- Deployment and ReplicaSet rollout/template changes,
- ConfigMap and Secret metadata/hash changes.

Acceptance:

- expected facts are produced exactly once per meaningful transition,
- expected edges open and close at the correct times,
- expected `object_changes` rows are produced for evidence timelines,
- pure heartbeat updates are discarded only after required facts and changes are
  extracted.

## Watcher Integration Tests

Use a local Kubernetes cluster such as kind.

Scenarios:

- initial LIST with pagination,
- watch ADDED/MODIFIED/DELETED,
- BOOKMARK handling,
- stale resourceVersion relist,
- periodic relist without watch failure,
- CRD added after startup,
- CRD removed after startup,
- namespace-scoped collector,
- permission loss during relist,
- noisy Lease workload.

Acceptance:

- offsets are committed only after durable handling,
- watch delete writes tombstone and closes edges,
- relist detects missing objects,
- permission or scope changes produce `unknown_visibility`, not false deletes,
- high-priority resources are not starved by low-priority churn.

## Scenario Tests

Generated incident scenarios:

1. OOMKilled Pod recovered before investigation.
2. Pod evicted and recreated on another Node.
3. Deployment memory limit briefly reduced and rolled back.
4. Node entered MemoryPressure during a service spike.
5. High-resource batch Pod colocated with affected service Pod.
6. Kubernetes Event expired but mirrored fact remains queryable.
7. EndpointSlice membership changed during a rollout.
8. Service selector changed and temporarily selected no Pods.
9. Pod was invisible after collector permission change and must not be marked
   deleted.

Acceptance:

- investigation returns the injected evidence in the ranked result,
- evidence includes object IDs, versions, timestamps, facts, edges, and diffs
  needed for proof,
- query does not scan all historical JSON,
- then-vs-now output distinguishes incident state from current state.

## Authorization Tests

Test identities:

- namespace workload reader,
- service reader without Pod list,
- Pod reader without Node access,
- cluster admin,
- unauthenticated request,
- identity with changed permissions.

Acceptance:

- raw resources require `get`,
- searches require `list` for the resource scope,
- facts inherit source-object authorization,
- topology edges are returned only when both endpoints are allowed by default,
- support bundles include only authorized evidence,
- API server authorization failure fails closed,
- audit rows do not contain sensitive raw payloads.

## Performance Tests

Run against generated samples and real staging samples.

Measure:

- latest lookup p50/p95,
- historical reconstruction p50/p95,
- fact lookup p50/p95,
- topology interval query p50/p95,
- service investigation p50/p95,
- sustained ingest versions/sec,
- watcher event lag,
- queue depth by GVR and priority,
- API server QPS and throttling,
- storage write latency p50/p95.

Acceptance:

- investigation latency remains interactive for the target dataset,
- historical reconstruction stays bounded by `max_delta_chain`,
- high-priority watcher lag remains lower than low-priority lag under
  backpressure,
- performance report includes p50 and p95, not only averages.

## Storage And Maintenance Tests

Measure:

- raw bytes,
- retained version bytes,
- blob bytes,
- fact, edge, change, and index bytes,
- compression ratio,
- bytes per object-version,
- dead rows and index bloat where supported,
- bytes reclaimed by compaction and vacuum,
- query latency before and after maintenance.

Acceptance:

- facts and edges remain much smaller than universal JSON indexing,
- compaction does not break reconstruction,
- retention purge removes derived data,
- SQLite and PostgreSQL maintenance jobs are observable and repeatable,
- post-maintenance query latency does not regress materially.

## Security Tests

Required checks:

- safe-default mode stores no Secret payload,
- redaction is applied before storage,
- support bundle preserves redaction,
- audit is written for sensitive reads and exports,
- filter policy changes are auditable,
- backup/export does not bypass authorization policy.

Acceptance:

- zero known Secret payload retention violations,
- no sensitive raw payloads in audit rows,
- denied authorization checks return no partial hidden object details unless the
  response shape explicitly allows redacted markers.

## Test Artifacts

Each validation run should produce:

- benchmark JSON report,
- scenario result JSON,
- schema version,
- filter and extractor versions,
- dataset manifest,
- collector policy,
- authorization test matrix,
- storage size report,
- maintenance report,
- failure log with dead-letter samples.

## Exit Criteria For MVP

MVP is accepted when:

- all Gate 0 and Gate 1 tests pass,
- every required generated scenario is detected,
- safe-default redaction has zero Secret payload violations,
- service investigation uses facts and edges as the candidate path,
- retained versions can prove top evidence,
- benchmark report includes performance, throughput, storage, security, and
  resource-specific metrics.
