# DiffStor PoC Plan

## PoC 1: Reverse Delta For Versioned JSON

Goal: prove latest reads stay O(1) while historical storage approaches delta
size.

Dataset:

- Synthetic Kubernetes-like resources.
- Synthetic Node resources with large `status.images` arrays.
- Real exported Kubernetes resources if available later.
- Mutations that change labels, replica counts, status, and noisy metadata.

Measurements:

- Raw full-copy bytes.
- Compressed full-copy bytes.
- Reverse-delta bytes.
- Delta fallback count when patch size approaches compressed full size.
- Latest read time.
- Historical reconstruction time by distance from latest.
- Snapshot frequency impact.

Pass condition:

- Latest reads decode one full payload.
- Historical reads are bounded by configured snapshot chain.
- Stored bytes are materially below compressed full-copy history for repetitive
  objects.

Current caveat:

- Deployment-sized mock objects are about 1 KiB after normalization, so they
  validate the small-object path, not the delta threshold.
- Node mock objects are about 75 KiB after normalization, so they validate the
  large-object path. Naive JSON Patch performs poorly on them because arrays are
  currently replaced as a whole.
- Keyed-list projection now validates a better Node strategy: arrays with stable
  identities are projected to maps before diffing. This is not a final wire
  format, but it shows whether semantic list handling is worth productizing.
- Index-list projection is also measured. It preserves order explicitly and can
  be smaller when list positions are stable, but it should be treated as a
  fallback because insertions/deletions can cause index-shift churn.
- A schema-less inferred-list projection is now measured too. It derives keys
  from item fields such as `id`, `uid`, `name`, `type`, `key`, `hash`, or
  digest-like strings, without requiring known Kubernetes paths.
- The PoC should choose among naive arrays, index-list projection, and inferred
  projection by measuring a small version sample instead of assuming one global
  policy.

## PoC 2: CDC Cross-Record Deduplication

Goal: prove content-defined chunking helps when records share large substrings
but are not versions of the same object.

Dataset:

- Large ConfigMap-like documents.
- Node-like documents with large repeated `status.images` structures.
- JSON log batches with repeated keys and common message templates.

Measurements:

- Unique chunk count.
- Total chunk references.
- Dedup ratio.
- Chunk size distribution.
- CPU cost per MiB.

Pass condition:

- CDC beats independently compressed records on large repetitive documents or
  batches.
- Chunk counts remain operationally reasonable with 512B-2KiB average sizes.

Current caveat:

- Whole-document CDC helps the synthetic ConfigMap case but does not help the
  current Node mock. This suggests Node needs path-aware handling, such as
  keyed-array diff, excluding/indexing noisy status paths separately, or CDC
  applied only to selected large subtrees.
- YAML/text diff should be measured only as a human-facing diff view, not as the
  primary storage diff, because it loses JSON-native path semantics and makes
  robust patch application harder.

## PoC 2.5: Schema-Less List Strategy Selection

Goal: handle unknown JSON shapes without hard-coded resource schemas.

Candidate transforms:

- Keep arrays as arrays and replace them when changed.
- Project all arrays by index while preserving explicit order.
- Infer list item identity from common scalar fields or digest-like strings.
- Apply optional user-provided path hints when available.

Measurements:

- Stored bytes per strategy.
- Strategy selection stability across the first N versions.
- Reconstruction correctness.
- Sensitivity to append, middle insert, delete, reorder, and item mutation.

Pass condition:

- The adaptive selector picks a strategy close to the best measured result for
  unknown resources.
- The system remains correct when inference fails by falling back to index or
  replace behavior.

## PoC 2.6: Delta Fallback Threshold

Goal: avoid low-value deltas when a schemaless object is effectively rewritten.

Experiment:

- Generate disruptive same-object versions where most payload values change.
- Compare `max_delta_full_ratio = 1.0` with lower thresholds such as `0.9` and
  `0.75`.
- Measure stored bytes, fallback-full count, and historical read latency.

Pass condition:

- The default never stores a delta larger than compressed full.
- Lower thresholds can trade a small amount of storage for materially faster
  historical reads.

## PoC 3: SQLite Latest Index

Goal: verify latest-version JSON query ergonomics and index growth without
requiring PostgreSQL.

Schema:

```sql
create table latest_index (
  object_id text primary key,
  collection text not null,
  version_id bigint not null,
  doc text not null,
  kind text generated always as (json_extract(doc, '$.kind')) virtual,
  namespace text generated always as (json_extract(doc, '$.metadata.namespace')) virtual,
  app text generated always as (json_extract(doc, '$.metadata.labels.app')) virtual,
  updated_at text not null default current_timestamp
);

create index latest_collection_kind_ns_idx on latest_index(collection, kind, namespace);
create index latest_collection_app_idx on latest_index(collection, app);
```

Queries:

```sql
select object_id from latest_index
where collection = 'k8s'
  and kind = 'Deployment'
  and namespace = 'default';
```

For arbitrary paths, maintain a materialized key-value index:

```sql
latest_kv_index(object_id, collection, path, value)
```

Measurements:

- Insert/update throughput.
- SQLite file/page size.
- Query latency for common filters.
- Cost of updating latest rows repeatedly.
- Cost of maintaining generated-column indexes versus KV path indexes.

Pass condition:

- Latest-version queries are comfortably fast for active object cardinality.
- Index size tracks active objects, not version history.

Run:

```bash
python3 -m poc.diffstor_poc.sqlite_bench --objects 1000 --versions 20 --repeat 100
```

## PoC 4: Adaptive Strategy Boundary

Goal: tune size thresholds instead of relying on fixed guesses.

Experiment:

- Generate or load documents from 512B to multiple MiB.
- Apply full compression, JSON Patch, and CDC.
- Compare total stored bytes and CPU time.

Output:

- Recommended defaults for `small_doc_limit`, `large_doc_limit`,
  `max_delta_chain`, `chunk_min`, `chunk_avg`, and `chunk_max`.

## PoC 5: Real Kubernetes Sample Collection

Goal: generate realistic data from a local kubeconfig without requiring a
committed production dump.

Collection:

```bash
python3 -m poc.diffstor_poc.collect_kube --out data/kube-samples.json
```

Then use the collected samples to derive repeatable mock versions:

```bash
python3 -m poc.diffstor_poc.sqlite_bench --samples data/kube-samples.json --versions 30
```

Measurements:

- Compression ratio on real resource shapes.
- Query latency for real namespace/kind/label distributions.
- Whether ignored paths such as `resourceVersion`, `managedFields`, and status
  noise dominate version churn.

## Key Metrics Dashboard

The PoCs should report three metric groups.

Compression:

- Raw full-copy bytes.
- Independently compressed full-copy bytes.
- Reverse-delta bytes.
- CDC unique chunk bytes.
- CPU time per MiB.

Query and index efficiency:

- Latest upsert throughput.
- Latest point read latency.
- Indexed latest query latency.
- Historical reconstruction latency by chain distance.
- Index size per active object.

User experience:

- Number of steps to collect data from kubeconfig.
- Clarity of query syntax.
- Whether users can predict which fields are indexed.
- Error messages when a historical scan or unindexed query is expensive.

## Current Research Output

See [Deep research notes](diffstor-deep-research.md) for the latest synthesis from the
strategy matrix and design implications.
