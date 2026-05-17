# ClickHouse MVP Closeout

Status date: 2026-05-17

## Current Decision

ClickHouse is the primary backend for the MVP central evidence store. chDB is
the MVP target for embedded ClickHouse-compatible local mode, but the default
artifact remains the small pure-Go SQLite-backed local build until chDB runtime
packaging is reliable enough for normal installs.

The MVP does not support dual-write. Operators choose one configured storage
backend with `storage.driver`. Releases should provide both default and
chDB-enabled binary/image variants. The default artifact stays small and pure-Go
without chDB linkage; the chDB-enabled artifact adds embedded
ClickHouse-compatible local storage and still supports SQLite.

## Done

- Append-only ClickHouse schema for `api_resources`, `observations`,
  `object_aliases`, `versions`, `facts`, `edges`, `changes`,
  `filter_decisions`, and `ingestion_offsets`.
- Idempotent ClickHouse DDL through `db clickhouse init` and `initOnStart`.
- Runtime `ingest`, `watch`, and `serve --watch` writes through
  `storage.driver: clickhouse`.
- Read-side API coverage over ClickHouse for schema, read-only SQL, resource
  health, object history, evidence search, topology expansion, and service
  investigation.
- Docker-based local dev ClickHouse on `127.0.0.1:8123` and isolated smoke-test
  ClickHouse on `127.0.0.1:18123`. The compose dev path can also run both
  ClickHouse and the watcher while mounting local kubeconfig and gcloud creds.
  The ClickHouse dev config raises `maxConcurrentStreams` so full-resource
  profiling does not leave most discovered GVRs queued.
- Read-only live profiling with timing reports, ClickHouse `EXPLAIN`, active
  table storage efficiency, broader active/inactive footprint reporting, and
  run-over-run trend files under `testdata/generated/clickhouse-live-profile/`.
- Prometheus metrics for ClickHouse-backed `serve --metrics`, including
  compressed/uncompressed bytes, proof/derived compressed bytes, active/inactive
  footprint bytes, part counts, inactive part age, compression ratio, compressed
  bytes per row, resource stream health, and row counts.
- ClickHouse write-path small-part mitigation: client-side batching, pending
  ingestion-offset coalescing by resource/event, HTTP async insert settings when
  `asyncInsert` is enabled, and a fresh-schema `ReplacingMergeTree(updated_at)`
  layout for `ingestion_offsets` so current-state rows can collapse by resource
  and event.
- ClickHouse now persists auditable destructive and sensitive filter decisions;
  raw-latest and cluster metadata are intentionally not advertised as supported
  ClickHouse interfaces because latest state is derived from versions/offsets.
- chDB configuration validation, a normal-build unavailable-adapter
  placeholder, and an optional `-tags chdb` adapter that compiles against
  `chdb-go`. Local runtime validation with `/usr/local/lib/libchdb.so` passed
  `make chdb-smoke` on 2026-05-17, covering ingest plus CLI/API health,
  schema, read-only SQL, search, history, topology, object investigation, and service investigation.
- The shared ClickHouse/chDB read path resolves `object_aliases` for topology and
  service investigation, so alias IDs emitted by EndpointSlice/Service edges map
  back to canonical version rows. chDB `serve --api` now reuses a process-local
  read store instead of reopening a `libchdb` session for every request.

## Latest Live Validation

The live watcher dataset confirmed the main MVP read paths are working after
switching the dev environment to compose-managed ClickHouse plus watcher. The
latest validation was run on 2026-05-17 against the same `kube_insight` database
with both the default binary API and the chDB-enabled binary API.

Default API on `127.0.0.1:8080`:

- `db clickhouse status` reported `ok yes` for all expected tables and sorting
  keys.
- API smoke returned 2xx for health, search, history, topology, and service
  investigation.
- Live profile selected a real Service and a recent Pod from the running dev
  watcher dataset.
- Active business-table storage efficiency after the compose watcher/API rebuild:
  latest threshold-enabled profile reported `1,792,264` rows, `58.25 MiB`
  active bytes on disk, `58.05 MiB` compressed, `1.61 GiB` uncompressed,
  `28.4x` compression ratio, and `33.96` compressed bytes per row.
- After rebuilding the compose watcher/API on 2026-05-17, API timings stayed
  inside MVP thresholds: latest threshold-enabled profile reported health
  `93.497 ms`, search `84.622 ms`, history `70.040 ms`, topology `106.344 ms`,
  and service investigation `783.779 ms`.
- `make clickhouse-live-profile` now writes `thresholds.tsv` and fails by
  default when active-table compression or representative API latency falls
  outside the MVP guardrails. `make clickhouse-api-smoke` also writes
  `thresholds.tsv` and failed-threshold API calls fail the smoke test.

chDB-enabled API on `127.0.0.1:18081` reading the same ClickHouse backend:

- API smoke returned 2xx for health, search, history, topology, and service
  investigation.
- Active business-table storage efficiency: `1,453,668` rows, `44.75 MiB`
  active bytes on disk, `44.59 MiB` compressed, `1.33 GiB` uncompressed,
  `30.53x` compression ratio, and `32.16` compressed bytes per row.
- API timings stayed inside MVP thresholds: health `96.750 ms`, search
  `77.625 ms`, history `75.419 ms`, topology `73.162 ms`, and service
  investigation `224.201 ms`.

Watcher and footprint observations:

- Offset status in the latest default profile immediately after the compose
  watcher/API rebuild was `237 / 241` listed and `4 / 241` watching. API health
  reported `241 / 241` healthy resources with `0` errors and `0` queued. The
  latest chDB-enabled profile before that rebuild was `228 / 241` bookmark,
  `6 / 241` watching, and `7 / 241` retrying.
- `make clickhouse-cleanup-repair-artifacts` reported `droppable 0`; the retained
  `ingestion_offsets_backup_20260516_195840` table remains visible but is not
  eligible for automatic cleanup.
- Broader footprint reporting still shows the Docker volume concern is mostly
  inactive MergeTree parts rather than poor active compression: the latest
  chDB-enabled run reported `kube_insight` inactive parts around `914.88 MiB`
  and active `kube_insight` data around `45.98 MiB`.
- Backup/scratch migration tables remain visible in footprint reports but are
  excluded from the core storage-efficiency metrics and Prometheus compression
  gauges.
- Metrics scraping against the live ClickHouse backend exposed
  `kube_insight_storage_compression_ratio`,
  `kube_insight_storage_compressed_bytes_per_row`, and ClickHouse
  active/inactive footprint gauges successfully.
- Generated validation artifacts were written under:
  `testdata/generated/clickhouse-live-profile-default/`,
  `testdata/generated/clickhouse-api-smoke-default/`,
  `testdata/generated/clickhouse-live-profile-chdb/`, and
  `testdata/generated/clickhouse-api-smoke-chdb/`.

Embedded chDB real-data validation also passed on 2026-05-17 using the dev
watcher container credentials and reports under `testdata/generated/chdb-live/`:

- Bounded Kubernetes collection wrote `46,365` active business rows.
- Active chDB table data compressed from about `33.92 MiB` uncompressed to
  `6.13 MiB` compressed, about `5.8x` overall.
- The chDB database directory measured `7.6 MiB` after the API reopen cleared
  temporary files; the staged `libchdb.so` remains the packaging size concern at
  about `508 MiB`.
- API timings after chDB store reuse were health `58.398 ms`, search `43.011 ms`,
  history `26.802 ms`, topology `67.869 ms`, and service investigation
  `606.767 ms`.
- Service investigation correctly expanded alias-backed topology for a real
  Service target: `1` EndpointSlice, `1` Pod, `1` Node, `2` Events, and `13`
  edges.

chDB-enabled full-test coverage also passed on 2026-05-17:

- `go test -tags chdb ./...`
- `make chdb-smoke`
- `make build-chdb`
- `make build-local-variants`
- chDB-enabled binary using SQLite fallback for fixture ingest and history query
- `make build-chdb-image`
- `make release-chdb-check` with GoReleaser v2.15.4
- `docker run --rm kube-insight-chdb:local version`
- normal `make test`, `make build`, `make clickhouse-smoke`,
  `make clickhouse-api-smoke`, `make clickhouse-live-profile`,
  `git diff --check`, and the Go 800-line rule
- `make clickhouse-status` and `make clickhouse-cleanup-repair-artifacts`
- chDB-enabled API live profile and API smoke against a local dev API port

MVP local-backend thresholds are captured in
`docs/dev/mvp-dev-checklist.md`; the current default and chDB-enabled live
profiles are inside those thresholds.

## MVP Must Finish

- Prepare the final PR/commit grouping and review release notes before tagging.
  The default artifact remains pure-Go and SQLite-backed until chDB runtime
  packaging is reliable enough for normal user installs.

## Known Limitations

- The chDB-enabled artifact needs a compatible `libchdb.so` at runtime. The
  default artifact intentionally does not link chDB and fails clearly when
  `storage.driver: chdb` is selected.
- The local `kube-insight-chdb:local` image is single-architecture. The release
  image publishes Linux amd64/arm64 as `ghcr.io/nowakeai/kube-insight:<tag>-chdb`
  only after staging each architecture's matching `libchdb.so`.
- ClickHouse cold tiering is configured but not validated end to end with real
  S3/object storage in the MVP.
- Native ClickHouse `JSON` type and broader JSON/text skipping indexes remain
  opt-in follow-ups, not MVP defaults.
- chDB-enabled `serve --metrics` is deferred. Local testing showed the current
  `libchdb` runtime is not stable enough for the combined `serve --api --metrics`
  process shape; keep metrics on SQLite/ClickHouse backends for the MVP.
- Inactive MergeTree parts can make the Docker volume look much larger than
  active business data. The live profile reports active and inactive footprint
  separately for that reason.

## Explicit Follow-ups

These are useful, but not MVP blockers:

- Real S3/object-storage tier validation. The schema can emit TTL-to-volume DDL,
  but local dev defaults keep cold tiering disabled until a storage policy exists.
- ClickHouse native `JSON` type benchmarking. The MVP default remains compressed
  `String CODEC(ZSTD)` for proof payloads.
- Heavier JSON/text skipping indexes such as `JSONAllPaths`, `JSONAllValues`, or
  selected Event message indexes. Keep them measured and opt-in first.
- Materialized read models for latest objects or open topology edges if argMax
  queries become too expensive at larger scale.
