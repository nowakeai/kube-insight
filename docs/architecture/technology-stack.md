# Technology Stack

## Decision

Build the first implementation around Go, SQLite, PostgreSQL, and a React UI.

The stack should optimize for Kubernetes correctness, simple PoC iteration,
single-binary local workflows, and a clean path from local evidence stores to a
central service.

## Recommended Stack

| Layer | Choice | Reason |
| --- | --- | --- |
| Collector and watcher | Go with `client-go` dynamic client | Best Kubernetes discovery, list/watch, resourceVersion, RESTMapper, and RBAC/SAR support. |
| Core API | Go | Shares filters, extractors, storage adapters, and authorization logic with the collector. |
| CLI | Go with Cobra | Single binary for local PoC, collection, ingestion, investigation, and benchmark commands. |
| Local storage | SQLite | Simple local PoC, single-file archives, deterministic tests, and easy support bundle workflows. |
| SQLite driver | `modernc.org/sqlite` | Pure Go driver keeps local PoC builds independent from CGO and system SQLite libraries. |
| Compression | Zstd in the application layer | Keeps version storage portable across SQLite and PostgreSQL. |
| Central storage | PostgreSQL | Strong concurrency, transactions, JSONB hot queries, range indexes, and operational maturity. |
| Optional time-series storage | TimescaleDB | Candidate for high-volume facts, retention, and cold compression after the core model is proven. |
| UI | TypeScript, React | Fits investigation views, timelines, topology graphs, and resource diffs. |
| API protocol | REST first | Easy to debug, stable for CLI and UI, and sufficient for MVP query flows. |
| Observability | OpenTelemetry plus Prometheus metrics | Required for watcher, filter, extractor, storage, and query acceptance. |
| Packaging | Container image, Helm or Kustomize later | Keeps local PoC separate from production deployment concerns. |

## Implementation Phases

### Phase 1: Local PoC

Use:

```text
Go CLI + client-go + SQLite + Zstd
```

Build:

- global discovery watcher,
- configurable filter pipeline,
- P0 resource-specific extractors,
- resource history store,
- facts and topology edges,
- investigation CLI,
- benchmark command.

The CLI may read and write SQLite directly. A separate API service is optional
until the evidence model is validated.

### Phase 2: Central Service

Use:

```text
Go API + PostgreSQL + same collector/extractor libraries
```

Build:

- multi-user query API,
- Kubernetes SAR/SSAR authorization,
- audit log,
- retention and maintenance jobs,
- backend adapter for PostgreSQL,
- UI-facing investigation responses.

### Phase 3: UI And Operations

Use:

```text
React UI + REST API + OpenTelemetry + Prometheus
```

Build:

- service/workload search,
- evidence timeline,
- topology-at-time graph,
- resource version diff,
- operational dashboards for watcher and storage health.

## Technology Boundaries

Keep these pieces backend-agnostic:

- object identity,
- filter pipeline,
- resource-specific extractor outputs,
- version materialization strategy,
- fact and edge semantics,
- investigation ranking,
- authorization decisions.

Allow backend-specific implementations for:

- SQL schema and indexes,
- range queries,
- JSON hot indexes,
- vacuum/analyze behavior,
- storage maintenance jobs.

## Alternatives

Rust is a reasonable future option for compression-heavy or embedded storage
components, but it increases early Kubernetes client and team velocity risk.

Python is useful for one-off benchmark scripts, but it should not be the main
watcher or storage engine because long-running watch reliability and deployment
shape matter.

ClickHouse, Kafka, and stream processors should stay out of the MVP critical
path. They may become analytics sidecars after the facts, edges, and version
model are proven.

## Selection Criteria

Use these criteria when revisiting the stack:

1. Kubernetes watch and RBAC correctness.
2. Write throughput under high resource churn.
3. Historical reconstruction latency.
4. Fact and topology query latency.
5. Storage footprint and maintenance behavior.
6. Local PoC to central service migration cost.
7. Debuggability during incidents.
8. Team familiarity.
