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
