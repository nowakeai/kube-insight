# Multi Backend Roadmap

SQLite is the default local single-file backend in the pure-Go artifact and the
deterministic test backend. The chDB-enabled local variant gives embedded runs
the same ClickHouse-compatible table/query contract as the central ClickHouse
backend without making the default artifact carry the large dynamic runtime.
ClickHouse remains the production-oriented central evidence backend candidate
because kube-insight is append-heavy, history-heavy, and storage-cost sensitive.
PostgreSQL and CockroachDB remain candidates for metadata/control-plane or
compatibility needs, but they are not the next evidence default.

## Goals

- Keep `internal/storage.Store` as the core write contract.
- Keep read-only query DTOs and interfaces in `internal/storage` so API/MCP/CLI
  code is not coupled to a concrete backend.
- Add backend implementations for investigation, topology, facts, and version
  reconstruction incrementally behind those interfaces. ClickHouse now covers
  the MVP read-side surface: generic SQL/schema, resource health, object
  history, evidence search, service investigation, and topology.
- Support one writer instance that owns discovery/watch/ingest and multiple
  API instances that only serve reads.
- Test ClickHouse as the first shared evidence backend before committing to a
  central OLTP row-store.
- Keep versions as proof and facts/edges/changes as indexes.

## Backend Roles

chDB / SQLite local mode:

- chDB is the MVP target for the chDB-enabled local variant so local and central
  backends share ClickHouse-compatible schema, query behavior, and storage
  measurements.
- SQLite remains the default local single-file backend in the pure-Go default
  artifact and the deterministic test backend.
- The MVP exposes `storage.driver: chdb` configuration and an unavailable
  adapter placeholder in normal builds plus an optional `-tags chdb` adapter;
  keep chDB as a separate artifact until runtime packaging is reliable for
  normal installs.
- chDB should reuse the ClickHouse schema/query code where possible and only
  swap the execution adapter from remote HTTP to embedded `chdb-go`.
- Do not introduce dual-write for this migration; select one configured driver.
- Release local-mode artifacts/images in two variants once chDB is promoted:
  a default build without chDB linkage for users who want the smaller pure-Go
  binary/image, and a chDB-enabled build for ClickHouse-compatible local storage
  that still keeps SQLite available.

ClickHouse:

- primary central evidence-backend candidate,
- append-only observations, versions, facts, edges, and changes,
- columnar compression and object-storage cold tiering,
- JSON/search experiments over hot or selected historical payloads,
- service/time-window investigation queries over large retained history.

PostgreSQL:

- optional compatibility backend,
- possible small metadata/control-plane store,
- conventional managed SQL deployments if ClickHouse is operationally too much.

CockroachDB:

- optional distributed metadata/control-plane backend,
- candidate only if multi-region transactional writes matter more than cold
  evidence storage cost.

## Storage Cost Strategy

Storage cost is a product requirement, not an implementation detail. Each
backend should use its native strengths instead of forcing one universal
compression plan.

Common rules across backends:

- Keep exact versions as proof, but do not make historical JSON scans the normal
  query path.
- Keep facts, edges, changes, and selected path indexes as the primary
  investigation indexes.
- Keep recent or latest JSON queryable enough for debugging and ad-hoc
  inspection.
- Compress or tier cold proof payloads only after important predicates have
  already been materialized.
- Measure storage per retained version, index bytes per version, and query p95
  before and after compaction.

SQLite local mode:

- Store latest JSON as plain text in `latest_index`.
- Keep historical version JSON plain during the PoC so SQLite JSON functions,
  generated columns, and future FTS/path indexes remain possible.
- Add FTS only for selected human text such as Event messages, status
  condition messages, webhook errors, and controller reconciliation messages.
- Add selected JSON-path indexes for configured fields such as images,
  resources, webhook service references, condition reasons, and owner/template
  references.
- Avoid compressing all historical blobs until query coverage is proven.

ClickHouse central evidence mode:

- Keep source evidence tables append-only.
- Batch watcher writes; avoid synchronous tiny inserts and mutation-heavy paths.
- Use MergeTree-family tables with partitions by month/day and order keys shaped
  around cluster, kind/resource, namespace, name/object, and observed time.
- Store proof payloads as compressed strings first, then benchmark the new JSON
  type for hot/recent documents and selected subcolumn queries.
- Use hot/cold storage policies so recent parts stay on local SSD and older
  parts move to S3-compatible object storage by TTL.
- Build latest/open-edge convenience read models with materialized views,
  ReplacingMergeTree, or argMax-style queries after source-table correctness is
  stable.
- Measure JSONAllPaths/JSONAllValues and targeted skipping indexes before making
  them defaults.

PostgreSQL/Cockroach compatibility mode:

- Keep facts, edges, changes, and versions semantically portable.
- Treat row-store backends as metadata/control-plane candidates until evidence
  storage measurements justify them.
- Do not require PostgreSQL range/GiST, Timescale compression, or CockroachDB
  distributed transaction behavior for the core product contract.

Expected storage tiers:

```text
hot:
  latest_index + recent versions + facts/edges/changes + FTS/path indexes

warm:
  retained versions + facts/edges/changes + selected path indexes

cold:
  compressed or tiered proof payloads + compact facts/edges/changes
```

Acceptance metrics for each backend:

- raw JSON bytes,
- proof payload bytes,
- fact/edge/change bytes,
- ad-hoc text/path index bytes,
- total database bytes,
- latest lookup p95,
- fact/path/FTS query p95,
- service investigation p95,
- cold reconstruction p95,
- write amplification under watch load.

## Application Role Split

This project uses application-level role separation, not separate read/write
DSN configuration as the primary model.

One writer instance:

```yaml
instance:
  role: writer
collection:
  enabled: true
server:
  api:
    enabled: false
mcp:
  enabled: false
```

Multiple API instances:

```yaml
instance:
  role: api
collection:
  enabled: false
server:
  api:
    enabled: true
```

All instances point at the same backend DSN:

```yaml
storage:
  driver: clickhouse
  clickhouse:
    dsnEnv: KUBE_INSIGHT_CLICKHOUSE_DSN
    database: kube_insight
```

Write path:

```text
collect/watch -> filters -> extractors -> write store
```

Read path:

```text
CLI/API/MCP/Web -> storage read interface -> backend query adapter -> evidence bundle
```

The read-side DTOs live in `internal/storage`, while SQLite currently aliases
those types for backward compatibility. This is intentional: remote ClickHouse
and embedded chDB should expose the same product interfaces, even though one
executes over HTTP and the other executes in-process through `chdb-go`.

API-only instances must reject collection/watch/admin-write operations. The
writer instance should normally disable public API/Web/MCP listeners unless
explicitly running in local `all` mode.

## API Examples

Write:

```bash
curl -X POST http://localhost:8080/api/v1/ingest \
  -H 'content-type: application/json' \
  --data-binary @samples.json
```

Read:

```bash
curl 'http://localhost:8080/api/v1/services/default/api/investigation?from=2026-05-14T10:00:00Z&to=2026-05-14T11:00:00Z'
```

Admin:

```bash
curl -X POST http://localhost:8080/api/v1/watch/resources \
  -H 'content-type: application/json' \
  -d '{"discoverResources":true,"context":"staging"}'
```
