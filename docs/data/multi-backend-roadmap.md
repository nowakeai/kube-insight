# Multi Backend Roadmap

SQLite is the local PoC backend. Production-oriented deployments should support
Postgres and CockroachDB with multiple kube-insight application instances.

## Goals

- Keep `internal/storage.Store` as the core write contract.
- Add read-only query interfaces for investigation, topology, facts, and
  version reconstruction.
- Support one writer instance that owns discovery/watch/ingest and multiple
  API instances that only serve reads.
- Keep versions as proof and facts/edges/changes as indexes.

## Backend Roles

SQLite:

- local PoC,
- desktop/agent cache,
- support bundles,
- deterministic tests.

Postgres:

- central service,
- stronger concurrent writes,
- read replicas,
- JSONB support for optional hot-window ad hoc queries.

CockroachDB:

- distributed central service,
- multi-region durability,
- SQL compatibility with Postgres-oriented schema where practical.

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
  condition messages, webhook errors, and controller reconcile errors.
- Add selected JSON-path indexes for configured fields such as images,
  resources, webhook service references, condition reasons, and owner/template
  references.
- Avoid compressing all historical blobs until query coverage is proven.

Postgres central mode:

- Use JSONB for latest and recent hot-window versions.
- Use GIN only for recent/ad-hoc JSON predicates. It is not a replacement for
  facts, topology, or explicit path indexes.
- Partition large history/fact tables by time or cluster where needed.
- Use partial and covering indexes for common incident predicates.
- Keep cold proof payloads in cheaper partitions or external object storage
  when version reconstruction can tolerate the extra read.

TimescaleDB mode:

- Use hypertables for high-volume time-indexed tables such as facts, changes,
  Events, and optional recent version rows.
- Use Timescale compression for cold chunks after the fact/path indexes needed
  for incident queries are materialized.
- Choose `segmentby` and `orderby` around the actual query shape, usually
  cluster, resource/kind/object, and observed time.
- Do not expect compressed chunks to behave like normal JSONB+GIN. Compressed
  JSON queries can degrade into chunk scans, so cold chunks should be queried
  through facts/path indexes first.

CockroachDB mode:

- Prefer the Postgres logical schema where possible, but avoid depending on
  Postgres-only extensions such as Timescale compression or GiST range indexes.
- Use regional locality and TTL carefully; distributed durability can cost more
  than the saved storage if all history is replicated everywhere.
- Consider separating hot indexes from cold proof payloads by table locality or
  retention policy.
- Treat CockroachDB as a distributed metadata/query backend first, not as the
  cheapest cold-history compression engine.

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
  driver: postgres
  postgres:
    dsnEnv: KUBE_INSIGHT_POSTGRES_DSN
```

Write path:

```text
collect/watch -> filters -> extractors -> write store
```

Read path:

```text
CLI/API/MCP/Web -> query store -> evidence bundle
```

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
