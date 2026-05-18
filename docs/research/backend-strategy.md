# Backend Strategy

## Decision

Keep the product model backend-agnostic, but optimize the MVP evidence backend
for storage cost and append-heavy evidence queries:

```text
SQLite:
  local PoC, agent cache, single-file archive

ClickHouse:
  MVP central evidence backend for append-only history, facts, edges,
  changes, JSON search, and cold S3/object-storage tiering experiments
```

Do not pick CockroachDB or PostgreSQL only because they are familiar OLTP SQL
systems. kube-insight stores mostly historical evidence: many append-only
observations, large proof payloads, time-window investigation queries, and cold
retention. That shape is closer to analytical/event storage than a traditional
row-store workload.

The current backend milestone is ClickHouse MVP validation, not a permanent
backend monopoly. ClickHouse must continue proving storage cost, object-storage
tiering behavior, JSON query quality, and investigation latency on generated and
real Kubernetes samples.

## SQLite

Best for:

- PoC.
- Local kubeconfig experiments.
- Edge collection.
- Single-file support bundles.
- Deterministic unit and scenario tests.

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
- Local disk retention can become expensive for long evidence windows.

Rules:

- Keep generic JSON indexes narrow.
- Keep `latest_index` compact; expose full latest JSON through a
  `latest_documents` join/view.
- Do not compress all historical blobs until ad-hoc query needs are covered by
  facts, path indexes, FTS, or a hot-window shadow table.
- Use max integer for open-ended topology intervals.
- Use WAL mode.
- Treat SQLite as validation and embedded mode, not the central service backend.

## ClickHouse

Best for:

- Append-only observations and retained resource versions.
- Large fact/change/event tables filtered by cluster, kind, namespace, key, and
  time window.
- Low-cost historical storage through columnar compression and cold S3/object
  storage volumes.
- JSON and semi-structured search on recent or selected historical payloads.
- Batch ingestion from watcher buffers or queues.

Use:

- MergeTree-family tables with explicit partitioning by time and cluster where
  appropriate.
- `ORDER BY` keys aligned to investigation access paths, not generic JSON scans.
- ZSTD codecs for proof payloads and repeated string dimensions.
- Hot/cold storage policies: recent parts on local SSD, older parts moved to
  object storage by TTL.
- New `JSON` type experiments for hot/recent documents and selected generated
  subcolumns where supported by the deployed ClickHouse version.
- Data skipping indexes only when measured useful: targeted path indexes,
  `JSONAllPaths`, `JSONAllValues`, bloom/token indexes for messages.
- Materialized views or ReplacingMergeTree read models for latest state and
  open-edge convenience, while preserving append-only source tables.

Important:

- ClickHouse is not an OLTP database. Avoid frequent row updates, deletes, and
  synchronous tiny inserts.
- Watch ingestion must batch writes. Use an in-process buffer, queue, or
  ClickHouse async inserts; validate part counts and merge pressure.
- Latest state, open edges, and offsets should be modeled as append-only events
  plus read models, not as heavily updated rows.
- JSON skip indexes are not PostgreSQL GIN. They skip granules and depend on
  data clustering, `ORDER BY`, granularity, and query shape.
- S3/object-storage tiering is a reason to test ClickHouse first, but cold-query
  latency and operational behavior must be measured before committing.

## PostgreSQL

Best for:

- Compatibility reference for SQL API thinking.
- Deployments that prefer conventional managed OLTP storage.
- Optional small metadata/control-plane database if ClickHouse owns evidence
  history.

Rules:

- Do not require PostgreSQL as the first central evidence backend.
- PostgreSQL-specific range/GiST/extension features should remain optional.
- Facts, edges, changes, and versions must keep portable semantics.

## CockroachDB

Best for:

- Distributed OLTP metadata or control-plane state if the product later needs
  multi-region transactional writes.
- PostgreSQL-wire compatible application integration.

Risks for kube-insight evidence storage:

- Primary data is still replicated database storage; object storage integration
  is mainly backup/import/export/changefeed oriented, not transparent cold table
  tiering for live analytical history.
- JSONB GIN helps hot ad-hoc queries but does not by itself solve cold proof
  payload cost.
- Serializable distributed writes require retry-safe ingestion code.

Use CockroachDB only after a measured comparison shows that distributed OLTP
benefits outweigh ClickHouse's storage and analytical-query advantages.

## TimescaleDB

Treat TimescaleDB as a future PostgreSQL-compatible analytics/export option,
not the main backend path. Its compression can help time-series facts, but it
still keeps the product in a PostgreSQL extension ecosystem and can complicate
JSONB GIN expectations on compressed chunks.

## Storage Adapter

Logical capabilities should be separated by workload:

```text
write evidence:
  put_observation_batch()
  put_version_batch()
  put_fact_batch()
  put_edge_batch()
  put_change_batch()

read investigation:
  query_latest()
  query_history()
  query_edges()
  query_facts()
  query_changes()
  query_service_investigation()

maintenance:
  build_read_models()
  compact_or_tier_history()
  report_storage_cost()
```

The application should own:

- object identity,
- filter pipeline,
- delta strategy,
- fact extraction,
- topology extraction,
- investigation ranking,
- proof reconstruction semantics.

The backend should provide:

- durable append storage,
- query indexes,
- compression/tiering primitives,
- read model support where useful,
- measurable storage and query cost.
