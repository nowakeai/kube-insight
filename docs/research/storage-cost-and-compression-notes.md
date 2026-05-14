# Storage Cost And Compression Notes

This note intentionally defers compression implementation. It records the
current decision so the PoC can focus on real troubleshooting scenarios first.

## Current Decision

Do not compress SQLite historical JSON by default yet.

Reasons:

- SQLite JSON functions and future expression indexes need readable JSON.
- `json_extract(zstd_decompress(data), '$...')` becomes a decompression scan and
  does not solve ad-hoc historical query performance.
- The product still needs to prove which fields deserve materialized facts,
  path indexes, or text indexes before cold payload compression is useful.

## Near-Term SQLite Plan

Keep three query surfaces separate:

- `latest_index`: compact current-state navigation/projection rows.
- `latest_documents`: view that joins latest content to blobs for full JSON
  inspection without duplicating payloads.
- `versions` and `blobs`: plain proof payloads during PoC.
- `object_facts`, `object_edges`, `object_changes`: primary investigation
  indexes.

Add targeted indexes before compression:

- FTS5 only for selected text such as Event messages, status condition messages,
  admission webhook errors, and controller reconcile errors.
- JSON-path index rows only for configured high-value paths such as images,
  resource limits, webhook service references, RBAC role refs, and status
  condition reasons.

Avoid full JSON FTS. It is likely too expensive and noisy for the operational
questions kube-insight should answer.

## Future Compression Research

Compression can return after query coverage is proven.

Candidate approaches:

- hot window plain JSON plus cold compressed proof blobs,
- reverse deltas for per-object version chains,
- optional compressed support-bundle export,
- backend-specific cold storage for Postgres/TimescaleDB/CockroachDB.

Each candidate must report:

- raw JSON bytes,
- retained proof payload bytes,
- fact/edge/change bytes,
- text/path index bytes,
- total database bytes,
- latest lookup p95,
- fact/path/text query p95,
- service investigation p95,
- cold version reconstruction p95,
- write amplification under watch load.

## Backend-Specific Principle

Do not force one compression strategy across all backends.

- SQLite should optimize for portability, local queryability, and support-bundle
  usefulness.
- Postgres should use JSONB/GIN for hot-window history and expression indexes
  for high-value paths.
- TimescaleDB should use hypertables and chunk compression for cold time-series
  tables only after predicates are materialized.
- CockroachDB should use locality and TTL carefully; distributed replication can
  cost more than it saves if cold history is copied everywhere.
