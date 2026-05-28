# Roadmap And Open Questions

## Milestone 0: Design Freeze For PoC

Deliverables:

- finalize P0 resource list,
- finalize SQLite schema,
- finalize fact and edge extractors,
- define benchmark output format,
- define generated incident scenarios.

## Milestone 1: Local PoC

Deliverables:

- kubeconfig collector,
- SQLite backend,
- resource version store,
- latest index,
- topology edges,
- core fact extraction,
- investigation CLI.

Success:

```text
service + namespace + time window -> evidence bundle
```

## Milestone 2: Compression And Reconstruction

Deliverables:

- Zstd compression,
- reverse-delta storage,
- snapshot/fallback thresholds,
- historical reconstruction,
- diff summary.

Success:

- storage materially smaller than full compressed history,
- reconstruction bounded by `max_delta_chain`,
- diffs are useful for Deployment/Pod/Node changes.

## Milestone 3: Evidence Backend And MCP MVP

Status: complete for the local MVP baseline across SQLite default mode,
ClickHouse central evidence mode, chDB-enabled local mode, and the core MCP read
surface.

Deliverables:

- ClickHouse schema for append-only observations, versions, facts, edges, and
  changes,
- batched write path or benchmark importer,
- service investigation query over ClickHouse tables,
- API read paths for health, search, history, topology, and service investigation,
- live profiling and Prometheus storage-efficiency metrics,
- JSON payload experiment comparing compressed `String` and new `JSON` type,
- hot/cold storage policy moving old parts to S3-compatible object storage,
- backend comparison report against SQLite local mode,
- chDB-enabled local variant using the ClickHouse-compatible schema and read
  path,
- MCP tools for backend-aware schema, read-only SQL, health, and retained object
  history.

Success:

- storage materially cheaper than retaining equivalent proof payloads in local
  SQLite or a replicated OLTP row-store,
- facts and topology remain primary query path,
- service investigation latency remains interactive,
- cold S3-tiered proof reconstruction is acceptable for incident workflows.

## Milestone 4: Web UI

Status: next milestone.

Deliverables:

- service/workload search,
- investigation result page,
- evidence timeline,
- topology-at-time graph,
- resource diff view,
- links from facts, changes, and topology edges to retained proof versions.

Success:

- user can start from a symptom and inspect evidence without writing SQL.

## Milestone 5: Kubernetes RBAC Support

Status: next milestone after Web UI.

Deliverables:

- Kubernetes-authenticated service mode,
- authorization checks for API, MCP, and UI reads,
- audit records for denied or filtered evidence access,
- predictable handling for historical evidence when live Kubernetes permissions
  have changed,
- tests for namespace-scoped and cluster-scoped resource access.

Success:

- a caller only receives evidence they are authorized to inspect, while retained
  proof and investigation indexes remain usable through constrained read
  surfaces.

## Milestone 6: Production Readiness

Deliverables:

- in-cluster deployment,
- retention policies,
- redaction policies,
- rebuild/repair jobs,
- observability for the collector.

## Open Questions

### Product

- Should the first paid/useful surface be CLI, UI, or API?
- Is the main buyer platform engineering, SRE, or compliance?
- Should kube-insight integrate with Prometheus/Loki/Tempo early, or stay
  Kubernetes-only until the core evidence model is proven?

### Data Model

- How much `managedFields` history should be retained?
- Should ConfigMap data be retained by default?
- How should custom resources be indexed without custom extractors?
- How should repeated Pod names across recreated Pods appear in the UI?

### Storage

- Is reverse-delta worth the write complexity for all resource kinds, or only
  large/high-churn kinds?
- Should blobs live in SQL initially or local/object storage?
- What is the right default snapshot interval?
- How much generic JSONB GIN should be retained for hot history?
- Does ClickHouse hot/cold S3 tiering materially reduce cold proof cost without
  making reconstruction or support bundles too slow?

### Topology

- Should Service -> Pod be materialized from EndpointSlice only, or should
  selector-derived edges also be stored?
- How should topology confidence be represented when data is incomplete?
- Do we need transitive closure tables for common paths such as Service ->
  Deployment -> Pod -> Node?

### Facts

- Which fact keys are P0?
- How should fact severity be tuned?
- Should facts be deduplicated or preserve every observed transition?
- How should Event messages be fingerprinted to avoid cardinality explosion?

### Query

- How much ranking logic should be deterministic rules versus learned scoring?
- Should expensive cold JSON scans be supported synchronously, asynchronously,
  or not at all in MVP?
- What should the evidence bundle schema look like for UI/API stability?

## Current Recommendation

Treat the SQLite, chDB-enabled, and MCP MVP baseline as complete. Build the Web
UI next so humans can inspect retained evidence without writing SQL, then add
Kubernetes RBAC support so API, MCP, and UI reads are constrained by the caller's
cluster permissions.

Keep backend hardening and storage experiments measured behind those product
milestones. The highest-risk assumption has moved from storage viability to
whether the retained evidence model is usable and safe through service-mode UI
and agent surfaces.
