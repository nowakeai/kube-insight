# Backend Comparison: SQLite+Zstd vs TimescaleDB

## Scope

This test evaluates two ready-made storage approaches for:

- JSON logs.
- Kubernetes resource history with all versions retained.

The goal is not to prove a final architecture. It is to measure fit for the
DiffStor workload:

- Compression ratio.
- Ingestion cost.
- Metadata query latency.
- Payload query or scan cost.
- Operational complexity.

TimescaleDB was run on host port `55432`, not `5432`, to avoid local port
conflicts.

## Test Data

The benchmark generates two datasets in one run:

- `logs`: repetitive JSON log records with service, namespace, level, route,
  status, trace ID, and labels.
- `k8s`: generated Kubernetes-like resource versions, including large Node
  resources.

Command:

```bash
.venv/bin/python -m poc.diffstor_poc.backend_compare \
  --logs 10000 \
  --k8s-objects 120 \
  --k8s-versions 10 \
  --timescale-dsn postgresql://postgres:diffstor@localhost:55432/postgres
```

Dataset size:

```text
records:      11200
logs:         10000
k8s versions: 1200
raw bytes:    22805287
```

## SQLite + Zstd Result

Implementation:

- SQLite table stores metadata columns plus a compressed payload blob.
- Zstd dictionary is trained from input samples.
- Metadata fields are indexed directly.
- Payload filtering requires decompression unless the field is materialized.

Result:

```text
records:                   11200
raw bytes:                 22805287
compressed payload bytes:   1919650
sqlite file bytes:          4804608
write time:                 441.83 ms
logs error query:             0.38 ms
k8s Node query:               0.10 ms
payload scan:                89.38 ms
dictionary bytes:            65536
```

Compression:

- Payload-only ratio: about `8.42%` of raw JSON.
- SQLite file ratio including table/index/dictionary overhead: about `21.07%`
  of raw JSON.

Fit:

- Strong fit for embedded/local retention.
- Strong fit for short repetitive logs when common query fields are
  materialized.
- Good for K8S history if latest/history lookup is mostly by metadata.
- Weak for arbitrary payload queries unless you materialize paths or accept
  decompression scans.

## TimescaleDB Result

Implementation:

- TimescaleDB hypertable with `jsonb` document and raw text payload.
- Metadata fields are normal columns.
- Compression is enabled and chunks are compressed.
- Size is measured with `hypertable_size()`, not parent table size.

Result:

```text
records:                   11200
raw bytes:                 22805287
hypertable bytes before:   19161088
hypertable bytes after:     6225920
database bytes before:     28776115
database bytes after:      15873715
compressed chunks:                1
write time:                 1296.77 ms
logs error query:              1.81 ms
k8s Node query:                1.06 ms
payload JSON query:            4.34 ms
```

Compression:

- Hypertable ratio after compression: about `27.30%` of raw JSON.
- Database-level ratio after compression: about `69.60%` of raw JSON, including
  PostgreSQL/Timescale catalog and database overhead.

Fit:

- Better fit for centralized analytics than embedded storage.
- Metadata queries are easy to express and keep server-side.
- JSON payload queries are possible without application-side decompression.
- Compression is automated once hypertable/chunk policies are configured.
- Operational cost is much higher than SQLite.
- For this small single-chunk dataset, it is larger and slower than
  SQLite+Zstd.

## Side-by-Side

| Metric | SQLite + Zstd | TimescaleDB |
| --- | ---: | ---: |
| Raw JSON bytes | 22.81 MB | 22.81 MB |
| Main stored bytes | 4.80 MB file | 6.23 MB hypertable |
| Main stored ratio | 21.07% | 27.30% |
| Write time | 441.83 ms | 1296.77 ms |
| Logs metadata query | 0.38 ms | 1.81 ms |
| K8S Node metadata query | 0.10 ms | 1.06 ms |
| Payload-level query | 89.38 ms app decompression scan | 4.34 ms server JSON query |
| Deployment complexity | embedded file | database server + extension |
| Best workload | local retention, edge, single-node archive | centralized analytics, SQL operations |

## Interpretation

SQLite+Zstd is the better match for "squeeze local history into a compact file".
It gives excellent compression on repetitive JSON and very fast indexed metadata
queries. The cost is that payload queries require either materialized indexes or
decompression scans.

TimescaleDB is the better match when query ergonomics and centralized operations
matter more than minimal footprint. It can query JSON payloads server-side, and
it has a built-in path toward retention policies, chunk compression, and object
storage tiering. For small-to-medium local retention, it is heavier than needed.

For DiffStor, this suggests:

- Keep SQLite+Zstd as the first embedded backend target.
- Treat TimescaleDB as an analytics/export backend, not the minimal storage
  core.
- Materialize latest/current query fields even when payloads are dictionary
  compressed.
- Keep reverse-delta/adaptive structural transforms above both backends; neither
  backend alone solves "all K8S versions with small historical deltas".

## Larger Test: Do We Still Need Deltas?

To answer whether SQLite+Zstd or TimescaleDB makes custom diff storage
unnecessary, a larger mixed dataset was generated:

```bash
.venv/bin/python -m poc.diffstor_poc.backend_compare \
  --logs 30000 \
  --k8s-objects 300 \
  --k8s-versions 20 \
  --skip-timescale
```

Then TimescaleDB was run on the same generated dataset using host port `55432`:

```bash
.venv/bin/python -m poc.diffstor_poc.backend_compare \
  --logs 30000 \
  --k8s-objects 300 \
  --k8s-versions 20 \
  --skip-sqlite \
  --skip-delta \
  --start-timescale \
  --timescale-dsn postgresql://postgres:diffstor@localhost:55432/postgres
```

Dataset:

```text
records:      36000
logs:         30000
k8s versions: 6000
raw bytes:    107343598
```

Results:

| Storage mode | Scope | Stored bytes | Notes |
| --- | --- | ---: | --- |
| SQLite+Zstd full rows | logs + K8S full versions | 17,911,808 file bytes | Includes metadata indexes and all full payloads. |
| SQLite+Zstd full K8S payloads | K8S only | 5,076,433 payload bytes | Payload-only baseline for all full K8S versions. |
| SQLite+Zstd reverse delta | K8S only | 764,739 payload bytes / 2,252,800 file bytes | 5,400 deltas, 300 chain snapshots, 300 latest full versions. |
| TimescaleDB compressed hypertable | logs + K8S full versions | 26,599,424 hypertable bytes | Server-side SQL/JSON queryable, compressed chunk. |
| TimescaleDB database size | logs + K8S full versions | 36,263,603 database bytes | Includes database/catalog overhead. |

Query/write observations:

| Metric | SQLite+Zstd full | SQLite+Zstd reverse delta K8S | TimescaleDB |
| --- | ---: | ---: | ---: |
| Write time | 1,786.97 ms | 9,453.83 ms | 5,650.31 ms |
| K8S Node metadata query | 0.48 ms | 0.47 ms | 1.31 ms |
| Logs error metadata query | 0.98 ms | N/A | 1.97 ms |
| Logs payload JSON query/scan | 291.46 ms app scan | N/A | 19.40 ms server JSON query |

Interpretation:

- For logs, custom version deltas usually do not apply because log records are
  independent events. SQLite+Zstd is very compact if query fields are
  materialized; TimescaleDB is better when server-side payload analytics matter.
- For K8S resource history, full-row compression is not enough. On this dataset,
  reverse-delta payloads were about `15.1%` of the SQLite+Zstd full K8S payload
  baseline (`764,739 / 5,076,433`).
- TimescaleDB compression helps operational analytics, but it stores full
  versions and does not exploit per-object version chains. It should not replace
  DiffStor's delta layer for K8S history.
- The delta layer is more CPU-heavy at write time in this Python PoC. That is an
  implementation cost to optimize in Go/Rust, batch workers, or async
  compaction.

Conclusion:

```text
Logs:      backend compression may be enough; focus on dictionary/columnar + indexes.
K8S:       keep reverse delta/adaptive transforms above the backend.
Timescale: useful analytics/export backend, not a replacement for version diffing.
SQLite:    strong embedded storage backend, especially with Zstd and materialized indexes.
```

## Real Staging Kubeconfig Dataset

The correct kubeconfig context for this environment is:

```text
staging
```

Collection command:

```bash
python3 -m poc.diffstor_poc.collect_kube \
  --context staging \
  --out data/kube-samples-staging.json
```

Collected sample:

```text
objects: 1807
file:    20MB
kind distribution:
  Pod         771
  Service     342
  ConfigMap   318
  Deployment  187
  StatefulSet  72
  Node         63
  Job          39
  DaemonSet     8
  CronJob       7
canonical object size:
  min 239B, avg 8177B, p50 5435B, p90 11837B, p99 90673B, max 459742B
```

### Staging-Based Test: 500 Objects

Command:

```bash
.venv/bin/python -m poc.diffstor_poc.backend_compare \
  --samples data/kube-samples-staging.json \
  --logs 30000 \
  --k8s-objects 500 \
  --k8s-versions 20 \
  --skip-timescale
```

Result:

```text
records:      40000
logs:         30000
k8s versions: 10000
raw bytes:    59190083

SQLite+Zstd full:
  compressed_payload_bytes: 17889981
  db_bytes:                 32903168

SQLite+Zstd reverse delta K8S:
  k8s raw bytes:            49131422
  full_zstd_payload_bytes:   7115949
  delta_payload_bytes:       1120758
  db_bytes:                  4001792
  delta_versions:               9000
  snapshots:                      500
  fallback_full_versions:           0
```

K8S delta payload ratio:

```text
1120758 / 7115949 = 15.75%
```

### Staging-Based Test: 1000 Objects

SQLite and reverse-delta command:

```bash
.venv/bin/python -m poc.diffstor_poc.backend_compare \
  --samples data/kube-samples-staging.json \
  --logs 50000 \
  --k8s-objects 1000 \
  --k8s-versions 20 \
  --skip-timescale
```

Timescale command, still avoiding host port `5432`:

```bash
.venv/bin/python -m poc.diffstor_poc.backend_compare \
  --samples data/kube-samples-staging.json \
  --logs 50000 \
  --k8s-objects 1000 \
  --k8s-versions 20 \
  --skip-sqlite \
  --skip-delta \
  --start-timescale \
  --timescale-dsn postgresql://postgres:diffstor@localhost:55432/postgres
```

Result:

```text
records:      70000
logs:         50000
k8s versions: 20000
raw bytes:    194678876

SQLite+Zstd full:
  compressed_payload_bytes: 49500208
  db_bytes:                 75399168
  write_ms:                 5598.32
  logs_error_query_ms:      1.57
  k8s_node_query_ms:        0.52

SQLite+Zstd reverse delta K8S:
  k8s raw bytes:            177914492
  full_zstd_payload_bytes:  33240483
  delta_payload_bytes:      10245592
  db_bytes:                 16699392
  write_ms:                 9540.66
  k8s_node_query_ms:        0.48
  latest_versions_query_ms: 0.37
  delta_versions:           17809
  snapshots:                1000
  fallback_full_versions:   191

TimescaleDB compressed:
  hypertable_bytes_before:  231202816
  hypertable_bytes_after:   73867264
  database_bytes_after:     83419663
  write_ms:                 11116.01
  logs_error_query_ms:      1.71
  k8s_node_query_ms:        1.25
  payload_json_query_ms:    27.70
```

K8S delta payload ratio:

```text
10245592 / 33240483 = 30.82%
```

This is worse than the 500-object test because the larger sample includes more
large and irregular real resources, but it is still a major reduction versus
storing all K8S versions as full compressed JSON.

Updated conclusion:

```text
Logs:
  Use backend compression and materialized query fields first.
  Per-record deltas are usually not the right abstraction.

K8S resource history:
  Keep diff storage. Real staging samples still show 3x-6x payload reduction
  versus full Zstd-compressed versions.

TimescaleDB:
  Good analytics backend, but it stores full versions. It does not replace the
  version-delta layer for K8S history.

SQLite+Zstd:
  Strong embedded backend. With reverse delta above it, it gives the best
  storage footprint in these tests.
```

### Indexed Reverse-Delta Test

The reverse-delta SQLite layout was then extended with:

- `objects`: one row per logical object with latest version metadata.
- `latest_index`: one full latest JSON document per object, with generated
  fields and indexes.
- `versions`: all full snapshots, fallback full versions, and reverse deltas.

Command:

```bash
.venv/bin/python -m poc.diffstor_poc.backend_compare \
  --samples data/kube-samples-staging.json \
  --logs 50000 \
  --k8s-objects 1000 \
  --k8s-versions 20 \
  --skip-sqlite \
  --skip-timescale \
  --out tmp/backend-compare-staging-1000-indexed
```

Result:

```text
records:                    20000
raw_bytes:                  177914492
full_zstd_payload_bytes:    33240483
delta_payload_bytes:        10245592
db_bytes:                   27275264
latest_node_query_ms:       0.052
latest_node_query_plan:     SEARCH latest_index USING INDEX latest_kind_ns_idx
objects_node_lookup_ms:     0.034
objects_node_lookup_plan:   SEARCH objects USING INDEX objects_kind_ns_idx
historical_get_ms:          1.717
historical_replay_steps:    9
```

This answers the indexing question: latest queries should be indexed against a
separate latest-only materialized table. Historical point reads should be routed
through version metadata and bounded patch replay. Full historical field indexes
should be optional because indexing every historical JSON document can dominate
storage.

### Historical OOMKilled Query

For historical status predicates such as "which Pods were OOMKilled", the
indexed layout adds a narrow `pod_status_events` table extracted at write time:

```sql
select distinct namespace, pod_name, container
from pod_status_events
where reason = 'OOMKilled'
order by namespace, pod_name, container;
```

On the full real `staging` sample:

```text
k8s objects:                1807
k8s versions:              18070
oomkilled_pod_query_ms:     0.027
oomkilled_pod_query_count:  9
oomkilled_event_count:      90
query plan:
  SEARCH pod_status_events USING INDEX pod_status_reason_idx (reason=?)
```

This avoids replaying every historical Pod version during query time.

### Full-Version Generic Index Test

To test the alternative design "no delta, one table for all resource versions,
same indexes for everything", a separate benchmark stored every K8S version as
a full Zstd-compressed payload and optionally built a universal scalar KV index
for all JSON values.

Command:

```bash
.venv/bin/python -m poc.diffstor_poc.generic_index_bench \
  --samples data/kube-samples-staging.json \
  --objects 1807 \
  --versions 10 \
  --out tmp/generic-index-staging-all
```

Dataset:

```text
k8s objects:   1807
k8s versions: 18070
raw bytes:     150108706
```

Results:

```text
SQLite+Zstd full, metadata indexes only:
  compressed_payload_bytes: 27502403
  db_bytes:                 37933056
  write_ms:                 3243.42
  node_query_ms:            0.251
  oomkilled_query_ms:       2490.13  -- scans/decompresses Pod payloads

SQLite+Zstd full, universal scalar KV index:
  compressed_payload_bytes: 27502403
  scalar_entries:           2445070
  db_bytes:                 797208576
  write_ms:                 153175.50
  node_query_ms:            0.537
  oomkilled_query_ms:       0.132
```

Interpretation:

- Querying becomes simpler with a universal KV index:

  ```sql
  select distinct v.object_id
  from version_kv kv
  join versions v on v.id = kv.version_row_id
  where kv.leaf = 'reason'
    and kv.value = 'OOMKilled'
    and kv.kind = 'Pod';
  ```

- But the index dominates storage: about `797MB` database size for `150MB` raw
  JSON and `27.5MB` compressed payloads.
- Write time also becomes very high because every scalar field in every
  historical version becomes an index row.
- Metadata-only full storage is compact enough, but arbitrary historical
  status queries require payload scans and decompression.

Conclusion for the "one table, same indexes for all versions" idea:

```text
Full versions + metadata indexes:
  Simple and reasonably compact, but historical JSON predicates are slow.

Full versions + universal KV index:
  Query-simple and generic, but storage/write amplification is too high.

Reverse delta + narrow extracted indexes:
  Less generic than universal KV, but much better storage/write balance for
  high-value historical queries like OOMKilled.
```

### Postgres/Timescale JSONB GIN Test

SQLite's universal KV index is inefficient, so the same "full versions, generic
JSON index" idea was tested with PostgreSQL/TimescaleDB `jsonb` and a GIN index.

Command:

```bash
.venv/bin/python -m poc.diffstor_poc.pg_gin_bench \
  --samples data/kube-samples-staging.json \
  --objects 1807 \
  --versions 10 \
  --compress \
  --start-timescale \
  --dsn postgresql://postgres:diffstor@localhost:55432/postgres
```

Dataset:

```text
k8s versions: 18070
raw JSON:     150108706 bytes
```

Schema shape:

```sql
create table diffstor_jsonb_history (
  ts timestamptz not null,
  object_id text not null,
  version integer not null,
  kind text not null,
  namespace text,
  name text,
  doc jsonb not null
);

create index history_kind_ns_idx on diffstor_jsonb_history(kind, namespace, ts desc);
create index history_object_idx on diffstor_jsonb_history(object_id, version);
create index history_doc_gin_idx on diffstor_jsonb_history using gin (doc jsonb_path_ops);
```

OOMKilled query:

```sql
select count(distinct object_id)
from diffstor_jsonb_history
where kind = 'Pod'
  and (
    doc @? '$.status.containerStatuses[*].state.terminated.reason ? (@ == "OOMKilled")'
    or doc @? '$.status.containerStatuses[*].lastState.terminated.reason ? (@ == "OOMKilled")'
  );
```

Result:

```text
insert_ms:                         6329
index_build_ms:                    1953

before Timescale compression:
  table_bytes:                     12083200
  index_bytes:                     11157504
  toast_bytes:                     79577088
  hypertable_total_bytes:          102817792
  gin_index_bytes:                 8060928
  metadata_index_bytes:            2342912
  oom_query_ms:                    8.32
  oom_query_plan:                  Bitmap Index Scan on history_doc_gin_idx

after Timescale compression:
  hypertable_total_bytes:          27254784
  database_bytes:                  110909107
  oom_query_ms:                    91.68
  oom_query_plan:                  ColumnarScan, not GIN-driven
```

Interpretation:

- PostgreSQL GIN is far more reasonable than SQLite universal KV for generic
  historical JSON predicates.
- It is still much larger than reverse-delta payload storage:
  - Postgres uncompressed hypertable total: about `102.8MB`.
  - Timescale compressed hypertable total: about `27.3MB`.
  - SQLite reverse-delta payload for the same 1807 x 10 staging history:
    about `6.85MB`; indexed reverse-delta DB with latest/events was about
    `32MB`.
- Timescale compression and JSONB GIN have a tradeoff: compressed chunks reduce
  storage but JSONPath queries scan compressed columnar chunks instead of using
  the original GIN path, making OOMKilled slower in this test.

Updated backend guidance:

```text
Postgres/Timescale + JSONB GIN:
  Best generic historical JSON query experience.
  Good if query flexibility matters more than minimum footprint.
  Be careful combining Timescale compression with JSONB GIN expectations.

SQLite universal KV:
  Too much write/storage amplification for default use.

Reverse delta + narrow event/path indexes:
  Best footprint for known high-value historical predicates.
```

## Reproduce

Create local Python environment:

```bash
python3 -m venv .venv
.venv/bin/python -m pip install zstandard psycopg[binary]
```

Start TimescaleDB on port `55432`:

```bash
docker run -d \
  --name diffstor-timescale-poc \
  -p 55432:5432 \
  -e POSTGRES_PASSWORD=diffstor \
  -e TIMESCALEDB_TELEMETRY=off \
  timescale/timescaledb:latest-pg17
```

Run comparison:

```bash
.venv/bin/python -m poc.diffstor_poc.backend_compare \
  --logs 10000 \
  --k8s-objects 120 \
  --k8s-versions 10 \
  --timescale-dsn postgresql://postgres:diffstor@localhost:55432/postgres
```
