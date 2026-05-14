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

## Milestone 3: PostgreSQL/Timescale Backend

Deliverables:

- PostgreSQL schema,
- GiST topology interval indexes,
- JSONB hot latest/recent indexes,
- optional Timescale fact hypertable,
- backend comparison report.

Success:

- better central service story than SQLite,
- facts and topology remain primary query path.

## Milestone 4: UI Prototype

Deliverables:

- service/workload search,
- investigation result page,
- evidence timeline,
- topology-at-time graph,
- resource diff view.

Success:

- user can start from a symptom and inspect evidence without writing SQL.

## Milestone 5: Production Readiness

Deliverables:

- in-cluster deployment,
- RBAC and audit,
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

Build the local PoC first with SQLite and real `staging` data.

Do not start with a general-purpose distributed backend. The highest-risk
assumption is not storage backend choice; it is whether the topology and fact
model can answer real troubleshooting questions better than existing tools.
