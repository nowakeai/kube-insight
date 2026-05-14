# DiffStor Deep Research Notes

## Executive Conclusion

DiffStor should not be designed as "JSON Patch plus compression". That is too
shallow for schemaless data. The stronger product shape is a storage optimizer
with three independent stages:

1. Normalize noisy data.
2. Try reversible structural transforms.
3. Select the cheapest physical representation after compression.

The key rule: every optimization must be optional and measurable. If a delta or
transform does not pay for itself, store a compressed full snapshot.

## Research Baseline

Relevant standards and systems point to the same boundary:

- JSON Patch is a good operation format, but array operations are positional.
  This makes it fragile when insertions, deletions, or reorders shift indexes.
- JSON Merge Patch is simpler, but it cannot patch inside arrays; arrays are
  replaced as whole values.
- Kubernetes Server-Side Apply handles lists better by using schema markers such
  as list map keys. That is valuable evidence, but DiffStor cannot require a
  schema because the product targets schemaless JSON.
- CDC is useful for large repeated byte regions, but whole-document CDC is not a
  universal answer for structured JSON. It can lose against independently
  compressed full records when edits disturb chunk boundaries or when JSON
  structure changes are not byte-local.

References:

- RFC 6902 JSON Patch: https://www.rfc-editor.org/rfc/rfc6902.html
- RFC 7386 JSON Merge Patch: https://www.rfc-editor.org/rfc/rfc7386
- Kubernetes Server-Side Apply merge strategy:
  https://kubernetes.io/docs/reference/using-api/server-side-apply
- FastCDC paper: https://www.usenix.org/conference/atc16/technical-sessions/presentation/xia
- rsync algorithm: https://rsync.samba.org/tech_report/
- SQLite generated columns:
  https://sqlite.org/gencol.html

## Model

Treat each write as a local optimization problem:

```text
input JSON
  -> canonical JSON
  -> ignored/noisy path filtering
  -> candidate structural transforms
  -> semantic diff
  -> compression
  -> fallback decision
  -> physical storage
```

Candidate transforms should include:

- `raw`: keep arrays as arrays.
- `index-list`: project arrays to `{order, items}` keyed by array index.
- `inferred-list`: project arrays to `{order, items}` using inferred item keys.
- `hinted-list`: project arrays using user or integration-provided hints.
- `selected-subtree-cdc`: apply CDC only to large string fields or selected
  subtrees, not blindly to the whole document.

The optimizer should choose a strategy per collection or per object family, then
still evaluate per-version fallback.

## List Strategy Findings

The new strategy benchmark tests:

- Single item mutation.
- Append.
- Head insertion.
- Middle deletion.
- Reorder.
- Full rewrite.
- Mixed mutations.

Command:

```bash
python3 -m poc.diffstor_poc.strategy_bench --items 200 --versions 30 --threshold 1.0
```

Key results from the current synthetic matrix:

| Scenario | Keyed list winner | No-key list winner | Important lesson |
| --- | --- | --- | --- |
| mutate one item | index-list, about 11% | index-list, about 11% | Projection makes item-level mutation cheap. |
| append | index-list, about 11% | index-list, about 11% | Stable position or append-only arrays are easy. |
| head insert | index-list, about 11% | index-list, about 11% | Compression can still make shifted patches small in this synthetic case. |
| delete middle | raw/index/inferred all about 10% | raw/index/inferred all about 10% | For repeated structure, raw array replacement can compress well. |
| reorder | inferred-list, about 14% | no good winner, about 100% | Semantic identity is essential for reorder. |
| rewrite all | index/inferred, about 79-83% | index/inferred, about 83% | Rewrites should be considered for full fallback. |
| mixed | inferred-list, about 14% when keyed | index-list, about 80% without keys | Key inference is the difference between useful and weak deltas. |

Threshold `0.9` shows that fallback full is useful for low-value deltas:

```bash
python3 -m poc.diffstor_poc.strategy_bench --items 200 --versions 30 --threshold 0.9
```

It turns many near-full naive deltas into full snapshots. That does not improve
space much, but it prevents long chains of expensive low-value patches.

## Implications

### 1. Schemaless Does Not Mean Strategy-Free

The system cannot assume Node, Pod, or Deployment schemas. But it can infer
useful structure from data:

- If list items have unique `id`, `uid`, `name`, `type`, `key`, `hash`, or
  digest-like values, infer map-like identity.
- If not, index projection is a safe reversible fallback.
- If neither beats raw compressed full storage, store full.

### 2. There Is No Single Best List Strategy

Index projection is strong for stable arrays and append-like workloads. Inferred
keys are better for reorder and mixed mutation when identities exist. Raw arrays
can compress surprisingly well for repetitive deletes. The product should not
hard-code one winner.

### 3. Fallback Full Is A Feature

Fallback full is not a failure. It is the mechanism that keeps worst-case
storage, read latency, and CPU bounded.

Recommended defaults:

```text
max_delta_full_ratio = 1.0   # storage-priority default
latency profile       = 0.9
aggressive profile    = 0.75
```

### 4. CDC Should Be Subtree-Aware

Whole-document CDC helped the synthetic ConfigMap case but lost on Node-like
whole documents. The next CDC experiment should run on:

- Large string leaves.
- Large repeated object arrays after projection.
- Log batches grouped by stream.
- Whole canonical documents only as a baseline.

## Proposed Strategy Optimizer

For each collection, maintain a small strategy profile:

```json
{
  "normalizer": {
    "ignore_paths": [],
    "list_strategy": "adaptive",
    "path_hints": {}
  },
  "storage": {
    "small_doc_limit": 4096,
    "large_doc_limit": 65536,
    "max_delta_chain": 10,
    "max_delta_full_ratio": 1.0
  },
  "index": {
    "mode": "latest_only",
    "indexed_paths": []
  }
}
```

Selection loop:

1. Sample first N versions or first M objects in a collection.
2. Evaluate `raw`, `index-list`, `inferred-list`, and hinted transforms.
3. Reject transforms that cannot be reconstructed exactly.
4. Choose by compressed stored bytes, then by historical read cost.
5. Continue per-version fallback so bad outliers do not poison the chain.

## Product Shape

The product should expose:

- A "strategy report" per collection.
- A warning when a collection is mostly falling back to full.
- A way to add path hints after observing data.
- A dry-run benchmark command before enabling retention.
- Query index recommendations based on observed JSON paths.

This is important for user experience: users need to understand why storage
shrinks or does not shrink. A black-box compressor is hard to trust.

## Current PoC Gaps

The current PoC is still shallow in several ways:

- It uses zlib, not zstd.
- The JSON diff implementation is intentionally minimal.
- Projection is not yet stored as a reversible transform manifest.
- Strategy selection is offline, not integrated into write path metadata.
- CDC is whole-document only in the benchmark.
- SQLite benchmark does not yet combine storage tables with latest indexes.
- Real kubeconfig data collection exists, but the benchmark has not been run on
  a real cluster dataset in this workspace.

## Next Experiments

1. Add exact reconstruction manifests for list projections.
2. Split CDC into whole document, large string leaf, and projected subtree modes.
3. Add zstd and compare compression levels.
4. Persist reverse-delta metadata into SQLite, not only in memory.
5. Run real kubeconfig collection and produce a strategy report.
6. Add a query UX prototype:
   - path/value filters,
   - saved indexed paths,
   - expensive-query warnings.

## Backend Comparison Update

See [Backend comparison](backend-comparison.md) for the first measured comparison
of SQLite+Zstd and TimescaleDB on generated logs plus Kubernetes resource
history. The early result is that SQLite+Zstd is more compact and faster for
local metadata queries, while TimescaleDB is better positioned for server-side
JSON analytics and operational automation.

The larger backend comparison also answers a core architecture question:
backend compression does not remove the need for diff storage for K8S history.
On a 30k-log plus 6k-K8S-version dataset, SQLite+Zstd full K8S payloads used
about `5.08MB`, while SQLite+Zstd reverse-delta K8S payloads used about
`0.76MB`. Logs remain a different case: they usually benefit more from
dictionary/columnar compression plus materialized query fields than from
per-record deltas.

Using the real `staging` kubeconfig context, 1807 Kubernetes objects were
collected and used as the base for larger generated history tests. At 500
objects x 20 versions, reverse-delta K8S payloads were `15.75%` of full
Zstd-compressed K8S payloads. At 1000 objects x 20 versions, the ratio was
`30.82%` because the larger sample included more irregular large resources, but
the delta layer still gave a substantial reduction.

The indexed reverse-delta SQLite test adds `objects` and `latest_index` tables.
On 1000 staging objects x 20 versions, latest Node lookup used
`latest_kind_ns_idx` and measured about `0.052ms`; bounded historical point
lookup replayed 9 patches and measured about `1.717ms`. See
[Reverse delta query and indexing](diffstor-query-indexing.md).

For historical status predicates, a narrow extracted event index is more
appropriate than indexing every historical JSON document. On the full staging
sample, `pod_status_events(reason, namespace, ts)` answered historical
`OOMKilled` Pod lookup in about `0.027ms`, finding 9 distinct Pods and 90
version events.

A full-version generic-index experiment tested the opposite extreme: store every
K8S version as full Zstd-compressed JSON and build one universal scalar KV index
for every JSON value. It made the OOMKilled query simple and fast
(`0.132ms`), but expanded the SQLite database to about `797MB` for `150MB` raw
JSON and `27.5MB` compressed payloads, with `2.45M` scalar index rows and
`153s` write time. This is too much amplification for the default path.

PostgreSQL/TimescaleDB with `jsonb_path_ops` GIN is a stronger generic-history
query option than SQLite KV. On the same 1807 x 10 staging history, uncompressed
Timescale hypertable total size was about `102.8MB`, GIN was about `8.1MB`, and
the historical OOMKilled JSONPath query used the GIN path at about `8.3ms`.
After Timescale compression, hypertable size dropped to about `27.3MB`, but the
same JSONPath query used a compressed ColumnarScan and slowed to about `91.7ms`.
This makes Postgres/Timescale+GIN a good analytics-oriented option, but not a
replacement for reverse-delta storage when minimum footprint is the priority.

The nuance is that Timescale and GIN can coexist for uncompressed chunks. The
conflict appears when old chunks are converted to Timescale's compressed
columnar representation: Timescale uses compression-specific segment/order
indexes, so arbitrary JSONB GIN predicates may no longer be the primary access
path. A practical design is hot uncompressed chunks with GIN, plus cold
compressed chunks with narrow extracted indexes for important historical
predicates.

The most important user workflow is late troubleshooting: a small service
latency or error-rate spike happened hours ago, current state is healthy, and
Kubernetes Events may already be expired. This cannot be served well by only
storing compressed JSON blobs or by indexing every historical scalar. The
practical design is:

- keep full recoverable resource history for proof and diff views,
- extract relationship history such as Service -> Pod, Pod -> Node, Pod ->
  ReplicaSet, ReplicaSet -> Deployment,
- extract compact fact history for Pod status, Deployment config, Node
  conditions, placement, resource requests/limits, and mirrored Kubernetes
  Events,
- query the relationship and fact indexes first, then reconstruct the exact JSON
  versions only for shortlisted evidence,
- keep optional hot JSONB GIN for recent ad-hoc predicates, while cold
  compressed history relies on fact indexes or asynchronous scans.

This makes DiffStor more like a historical evidence engine than a generic JSON
warehouse. Full history provides correctness; fact and relationship indexes
provide interactive troubleshooting.
