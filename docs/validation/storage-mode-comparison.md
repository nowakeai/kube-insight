# Storage Modes And Performance Positioning

Last updated: 2026-05-18.

kube-insight should be evaluated as an evidence layer, not as a faster spelling
of every `kubectl get` command. There are two broad classes of options:

1. Live Kubernetes API access: direct `kubectl` or an agent tool that calls the
   apiserver on demand.
2. Retained kube-insight evidence: sanitized facts, topology edges,
   observations, and versions stored in a backend chosen for the user's scale.

Within those classes, the current project exposes four practical choices:

| Class | Option | Best fit |
| --- | --- | --- |
| Live API baseline | Raw `kubectl` | Current-state spot checks and manual confirmation. |
| kube-insight local | SQLite default artifact | Smallest install, local investigations, CI fixtures, and first-time users. |
| kube-insight local ClickHouse-compatible | chDB-enabled artifact | Local ClickHouse-style schema/query behavior without running a ClickHouse server. |
| kube-insight central service | ClickHouse backend | Continuous multi-resource history, larger datasets, storage efficiency, and team/API/MCP service deployments. |

The intended product path is: use kube-insight for retained, sanitized,
agent-ready evidence; choose SQLite, chDB, or ClickHouse according to scale and
operational needs; keep `kubectl` as the live-state confirmation tool.

## Summary Matrix

| Option | Strengths | Tradeoffs | Current evidence |
| --- | --- | --- | --- |
| Raw `kubectl` | No database, no watcher, exact live current state, familiar to every Kubernetes user. | Current-state only, repeated broad API calls for agents, no retained Event history after expiry, no pre-storage redaction layer, joins happen in prompt/tool code. | In the recorded agent workflow benchmark, broad `kubectl` operations took `3,104-5,745 ms`. |
| kube-insight + SQLite | Default pure-Go artifact, no external service, simplest local database, good for laptops and small evidence sets, works with CLI/API/MCP. | Row-store local backend; not the target for very large retained history or low-cost cold storage. | Same benchmark used the default SQLite evidence DB: `24-215 ms` for five agent investigation queries, `14.9x-221x` faster than broad raw `kubectl` operations. |
| kube-insight + chDB | Local ClickHouse-compatible tables and SQL shape, useful for users who want ClickHouse semantics without a server, still supports SQLite and remote ClickHouse in the chDB-enabled binary. | Requires `libchdb.so`; release artifact is larger; `serve --metrics` is deferred for this backend; runtime packaging is more complex than the default artifact. | chDB smoke covers schema, SQL, search, history, topology, object investigation, service investigation, and API. Embedded real-data validation showed about `5.8x` compression and service investigation around `607 ms` after store reuse. |
| kube-insight + ClickHouse | Best MVP path for long-running central evidence history, high compression, append-heavy writes, API/MCP service reads, and future cold-tiering. | Requires operating ClickHouse; object-storage tiering is configured but not end-to-end validated in this MVP; local MergeTree inactive parts must be understood when reading disk usage. | Live profiles showed `28.4x-30.53x` active business-table compression, `32.16-33.96` compressed bytes per active row, API health/search/history/topology under `500 ms`, and representative service investigation `224-801 ms`. |

## How To Choose

Start with the default SQLite artifact when:

- you want the smallest install,
- you are trying kube-insight for the first time,
- you need local evidence files for a single cluster or a short-lived run,
- you want the lowest operational burden.

Use the chDB-enabled artifact when:

- you want local ClickHouse-compatible SQL and table names,
- you want to test ClickHouse-style query behavior without a server,
- you can accept the `libchdb.so` runtime dependency,
- you do not need chDB-backed `serve --metrics` yet.

Use ClickHouse when:

- the watcher should run continuously,
- the evidence dataset is large enough that compression and storage cost matter,
- multiple users or agents need API/MCP access to the same retained evidence,
- you want storage efficiency metrics, read profiles, and a path toward cold
  object-storage tiering.

Use raw `kubectl` when:

- you need to confirm the exact current state of one object,
- you do not need retained history,
- you are validating a kube-insight finding against the live apiserver.

## Performance Claims

The current public benchmark is intentionally scoped:

- It compares kube-insight's default SQLite evidence database with raw
  `kubectl` for agent-style investigations that need retained facts, topology,
  search, and inventory.
- It now includes an initial same-dataset storage-mode harness for SQLite,
  ClickHouse, and chDB, but the generated dataset is still small enough that
  fixed query overhead dominates some results.
- ClickHouse and chDB still have separate live-profile and smoke-validation
  results that measure their MVP readiness and storage behavior on real watcher
  data.

This distinction matters. kube-insight is expected to be better than raw
`kubectl` for the project's target problem: retained, sanitized, repeatable
Kubernetes evidence for humans and agents. Inside kube-insight, the storage
backend choice is a tradeoff between simplicity, local ClickHouse compatibility,
central-service scale, and operational complexity.

## Results That Need Careful Interpretation

Some current numbers look counterintuitive if read as a direct engine shootout.
They are not direct engine shootout results yet.

| Observation | Why it looks surprising | Current interpretation |
| --- | --- | --- |
| ClickHouse shows `28.4x-30.53x` compression while embedded chDB shows about `5.8x`. | chDB and ClickHouse are both ClickHouse-compatible, so users may expect similar compression. | The measurements used different datasets, row counts, merge states, and possibly different filesystem/part accounting. ClickHouse was measured on a long-running watcher dataset with about `1.45M-1.79M` active rows. Embedded chDB was measured on a bounded real-data run with `46,365` active rows. This proves chDB compression works, but does not prove chDB is inherently worse. |
| chDB service investigation was around `607 ms`, while ClickHouse service investigation ranged from `224 ms` to `801 ms`. | An embedded local engine might be expected to beat a server over HTTP. | The current numbers are different validation runs, not the same service target on the same dataset. Query cost also includes API routing, result materialization, JSON decode/encode, cache warmth, selected topology size, and store/session lifecycle. The result is within the MVP `1s` guardrail, but it is not enough to rank chDB against ClickHouse. |
| The chDB-enabled API reading the remote ClickHouse backend was faster than the default API in one profile (`224 ms` vs `783 ms` service investigation). | The binary build tag should not make remote ClickHouse queries much faster by itself. | Both profiles read a ClickHouse backend, but the selected service target, row counts, watcher state, cache state, and timing of the run differed. Treat this as evidence that both paths work, not as evidence that one binary is faster. |
| SQLite benchmark queries are much faster than broad raw `kubectl` calls. | SQLite is not the large-history backend, so users may wonder why ClickHouse is needed. | SQLite can be very fast for a local retained evidence database. ClickHouse is chosen for central service scale, compression, long-running append-heavy history, and future cold-tiering, not because every small local query is faster than SQLite. |
| SQLite is also faster than ClickHouse on several same-dataset small reads. | A columnar analytical engine may be expected to beat SQLite everywhere. | The benchmark query set is mostly bounded point lookups and small joins over a few thousand rows. SQLite uses local B-tree indexes in-process, while ClickHouse pays HTTP, server pipeline, and result-format overhead per query. ClickHouse already wins on ingest in the larger run and is expected to pull ahead for larger scans, aggregations, retained history, compression, and central service use. |
| chDB reads are slower than both SQLite and ClickHouse in the same-dataset harness. | chDB is embedded and ClickHouse-compatible, so local reads may be expected to be close to ClickHouse or SQLite. | The current chDB adapter serializes queries through one session, returns JSON-shaped result sets, and decodes into generic maps before typed API objects are built. Small query latency is dominated by session/query/result materialization overhead, not by compressed data scan cost. |

## Optimization Opportunities

There is still query-performance headroom in both ClickHouse and chDB.

ClickHouse opportunities:

- Expand same-dataset profiles before making broader backend ranking claims.
- Reduce service-investigation round trips by combining or parallelizing
  independent reads where it does not make the SQL harder to maintain.
- Add materialized read models or projections for hot paths such as latest
  object lookup, resource health, service topology fan-out, and recent severe
  facts.
- Tune ordering keys, skip indexes, and bloom/text indexes against real query
  patterns rather than speculative JSON searches.
- Continue reducing small-part churn through batching, async insert settings,
  and watcher flush policy; keep `OPTIMIZE` as a benchmark/maintenance tool, not
  a normal hot-path requirement.
- Benchmark native ClickHouse `JSON` and JSON subcolumns for hot recent document
  queries before making them default.

chDB opportunities:

- Keep a process-local chDB session open for API/MCP service reads. The MVP has
  already moved in this direction for `serve --api`; avoid reopening the local
  database per request.
- Measure cold-cache and warm-cache runs separately. Embedded databases can look
  disproportionately slow when every profile starts from a cold session.
- Import in larger batches and investigate whether explicit MergeTree merge or
  `OPTIMIZE` behavior is useful for benchmark datasets.
- Use the same table schema, codecs, dataset, and active-part accounting as the
  ClickHouse benchmark before comparing compression.
- Reduce result-format overhead for typed reads where possible; small queries can
  be dominated by JSON/result materialization rather than storage scan time.
- Add a chDB-specific profile that reports active parts, compressed bytes,
  uncompressed bytes, directory footprint, query timings, and session lifecycle
  costs in one place.

## Same-Dataset Harness Results

The initial harness is available as `make storage-mode-benchmark` and writes
generated reports under `testdata/generated/storage-mode-benchmark/`. It uses
one generated evidence dataset and runs the same API-style query groups against
SQLite, ClickHouse, and chDB when their runtimes are available.

Small local run:

```bash
make storage-mode-benchmark
```

| Backend | Ingest | Health | Search | History | Topology | Service investigation |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| SQLite | `405 ms` | `5 ms` | `5 ms` | `4 ms` | `4 ms` | `23 ms` |
| ClickHouse | `4.6 s` | `63 ms` | `61 ms` | `57 ms` | `78 ms` | `189 ms` |
| chDB | `467 ms` | `260 ms` | `255 ms` | `255 ms` | `259 ms` | `442 ms` |

Larger local run:

```bash
STORAGE_BENCH_CLUSTERS=2 STORAGE_BENCH_COPIES=20 make storage-mode-benchmark
```

| Backend | Ingest | Health | Search | History | Topology | Service investigation | Storage signal |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| SQLite | `18.09 s` | `23.8 ms` | `63.9 ms` | `36.4 ms` | `29.8 ms` | `83.3 ms` | `4.61 MB` database |
| ClickHouse | `7.72 s` | `62.0 ms` | `65.4 ms` | `56.1 ms` | `90.5 ms` | `187.5 ms` | `598 KiB` active compressed bytes, about `4.9x` active compression |
| chDB | `1.41 s` | `252.5 ms` | `240.6 ms` | `235.0 ms` | `261.9 ms` | `493.5 ms` | `1.23 MB` directory, about `5.7x` active compression |

The larger run had `1,760` versions, `2,280` facts, `2,320` edges, and `1,482`
latest objects. This is enough to expose fixed per-query overhead, but it is
still not a large-history benchmark. The result supports these narrower claims:

- SQLite is the fastest local option for small point-lookups and bounded joins.
- ClickHouse is already faster than SQLite on this generated batch ingest path
  and stores active business tables compactly.
- chDB produces ClickHouse-like compressed table storage locally, but its
  current read adapter needs result-format and query-count optimization before
  it should be positioned as the fastest local read path.
- The latest run includes batched evidence bundle loading and deferred topology
  object hydration. On this fixture, ClickHouse service investigation improved
  from the prior `237.0 ms` run to `187.5 ms`, and chDB improved from
  `555.6 ms` to `493.5 ms`.

## Remaining Benchmark Gaps

Raw `kubectl` remains optional in the storage-mode harness because it is a
live-state baseline, not a storage engine for the generated dataset. The next
useful benchmark additions are:

- a larger generated dataset that reaches hundreds of thousands to millions of
  facts, edges, and versions,
- a live raw-`kubectl` comparison for the same service-investigation target,
- cold-cache and warm-cache splits for ClickHouse and chDB,
- p50/p95 timings from repeated runs instead of one-shot timings,
- separate scan/aggregation benchmarks that are expected to favor ClickHouse
  more strongly than point lookups.
