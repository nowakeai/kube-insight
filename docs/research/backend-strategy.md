# Backend Strategy

## Decision

Keep the domain model backend-agnostic, but optimize for two likely modes:

```text
SQLite:
  local PoC, agent cache, single-file archive

PostgreSQL/TimescaleDB:
  central service backend
```

Do not design the product around one backend-specific JSON trick.

## SQLite

Best for:

- PoC.
- Local kubeconfig experiments.
- Edge collection.
- Single-file support bundles.

Use:

- B-tree indexes for metadata, facts, and topology.
- JSON generated columns for latest-state filters.
- Zstd blobs stored as `blob`.
- Integer millisecond timestamps.

Limitations:

- Single writer.
- No native range type.
- No GiST indexes.
- Weak arbitrary historical JSON query support.

Rules:

- Keep generic JSON indexes narrow.
- Use max integer for open-ended topology intervals.
- Use WAL mode.
- Treat SQLite as validation and embedded mode, not necessarily the final
  central service backend.

## PostgreSQL

Best for:

- Central API service.
- Stronger concurrency.
- Range indexes.
- JSONB hot queries.
- Transactional derived indexes.

Use:

- `tstzrange` and GiST for topology intervals.
- JSONB GIN on recent/latest JSON.
- Partial indexes for facts.
- Table partitioning for large facts/history.

Important:

- Facts and topology should remain the primary investigation path.
- JSONB GIN is useful for hot/ad-hoc predicates, not a replacement for facts.

## TimescaleDB

Best for:

- Time-partitioned facts.
- Retention policies.
- Compressed cold data.

Use:

- `object_facts` as a hypertable if fact volume grows.
- Compression for cold facts/history.
- Segment/order keys aligned with common filters.

Important:

- Compressed chunks may not use original JSONB GIN query paths.
- Promote important JSON predicates into fact rows before compression.

## ClickHouse

Best for:

- Analytical sidecar.
- Large-scale fact exploration.
- High-volume event/log-style queries.

Do not use as the only source of truth for resource version graph in the MVP.

## CockroachDB

Best for:

- Future distributed metadata.
- Multi-region write availability.

Risks:

- Operational complexity.
- Latency and cost for a small early product.

## Storage Adapter

Logical interface:

```text
begin_tx()
put_version()
put_blob()
upsert_latest()
update_edges()
insert_facts()
insert_changes()
query_latest()
query_edges()
query_facts()
get_version_chain()
rebuild_indexes()
compact_history()
```

The application should own:

- object identity,
- delta strategy,
- fact extraction,
- topology extraction,
- investigation ranking.

The backend should provide:

- persistence,
- transaction boundaries,
- index primitives,
- compression/tiering where available.
