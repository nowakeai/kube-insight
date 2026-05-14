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
- Plain historical JSON during the query-shape PoC.
- Optional FTS5 tables for selected message fields, not full JSON.
- Optional selected JSON-path index tables for configured historical fields.
- Integer millisecond timestamps.

Limitations:

- Single writer.
- No native range type.
- No GiST indexes.
- Weak arbitrary historical JSON query support.

Rules:

- Keep generic JSON indexes narrow.
- Keep `latest_index` compact; expose full latest JSON through a
  `latest_documents` join/view.
- Do not compress all historical blobs until ad-hoc query needs are covered by
  facts, path indexes, FTS, or a hot-window shadow table.
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
- JSONB GIN on recent/latest JSON and explicit hot-window history.
- Partial indexes for facts.
- Expression indexes for high-value JSON paths.
- Table partitioning for large facts/history.

Important:

- Facts and topology should remain the primary investigation path.
- JSONB GIN is useful for hot/ad-hoc predicates, not a replacement for facts.
- Cold proof payloads can move to cheaper partitions or external storage once
  derived indexes are durable and rebuildable.

## TimescaleDB

Best for:

- Time-partitioned facts.
- Retention policies.
- Compressed cold data.

Use:

- `object_facts`, `object_changes`, Event-like facts, and optional recent
  versions as hypertables when volume justifies it.
- Compression for cold facts/history after materialized predicates exist.
- Segment/order keys aligned with common filters: cluster, resource/kind,
  object, and observed time.

Important:

- Compressed chunks may not use original JSONB GIN query paths.
- Promote important JSON predicates into fact rows before compression.
- Timescale compression is a storage-cost tool, not a license to query cold JSON
  by decompression scan.

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
- Distributed API/query serving where locality matters.

Risks:

- Operational complexity.
- Latency and cost for a small early product.
- No Timescale-style compression assumptions.
- Postgres extension compatibility gaps.

Rules:

- Keep schema close to the Postgres logical model, but feature-gate
  extension-specific indexes and compression.
- Use locality and TTL policies deliberately so cold history does not become
  expensive globally replicated data by accident.
- Use CockroachDB for availability and scale-out metadata/query needs, not as
  the primary cold archive unless measurements prove the cost.

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
