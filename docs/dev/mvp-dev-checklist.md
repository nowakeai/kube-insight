# MVP Dev Checklist

Status date: 2026-05-18

This checklist is the working boundary for the kube-insight MVP. Use it to keep
local development focused. Items outside the stop line are deliberate follow-ups,
not things to opportunistically pull into the MVP.

## MVP Stop Line

The MVP is done when a developer can run a local ClickHouse-backed watcher,
collect real Kubernetes evidence continuously, query the main investigation
surfaces through the API/CLI, inspect storage efficiency, and validate that the
schema/read paths are stable enough for the next product slice.

The MVP is not a production hosted service, not a UI release, and not a full
cold-tier/S3 validation.

## Current Distance

Current estimate: about 96% of the local backend MVP is implemented after the
Phase 6 validation run passed for default, ClickHouse, and chDB-enabled paths,
and the embedded chDB real-data API path was retested with alias-aware topology.

Already working:

- ClickHouse append-only evidence schema and runtime write path.
- API/CLI read paths for health, search, history, topology, service
  investigation, and read-only SQL/schema inspection. Read commands open stores
  without schema mutation, and ClickHouse ad-hoc SQL now rejects multi-statement
  and write/DDL tokens.
- Compose-based local ClickHouse plus watcher dev loop with kubeconfig and
  gcloud credential mounts.
- Live profile reports for query timing, compression ratio, bytes per row,
  active/inactive parts, and ClickHouse explain output for both default and
  chDB-enabled API binaries.
- Prometheus metrics for ClickHouse storage and resource health.
- Schema status and safe `ingestion_offsets` repair flow.

Remaining work is mostly final diff review and release-readiness cleanup. The
merged GoReleaser config now validates the default artifacts plus Linux/macOS
amd64/arm64 chDB-enabled archives and Linux amd64/arm64 chDB Docker image staging.

## Phase 1: Local Dev Environment Hygiene

- [x] Keep checked-in `compose.dev.yaml` portable: no fixed Docker volume name.
- [x] Keep checked-in `compose.dev.yaml` portable: no fixed container name.
- [x] Support gitignored `compose.dev.local.yaml` for local-only volume/container
  overrides.
- [x] Keep `.env` secrets out of terminal reports and generated docs.
- [x] Add a safe cleanup command for empty ClickHouse repair scratch tables.
- [x] Add a dev ClickHouse system-log TTL config and manual cleanup target so
  diagnostic logs do not dominate the local volume.
- [x] Keep migration backup tables visible in footprint reports but excluded from
  storage-efficiency MVP metrics.

Acceptance:

```bash
docker compose -f compose.dev.yaml config
make dev-compose-ps
make clickhouse-status
make clickhouse-cleanup-repair-artifacts
make clickhouse-clean-system-logs
```

Expected result: tracked compose is portable; local override preserves this
workspace's long-lived ClickHouse data; schema status is `ok yes`; dev system
logs have a bounded TTL and can be manually reclaimed without touching business
tables.

## Phase 2: ClickHouse Backend Acceptance

- [x] Fresh schema creates all expected ClickHouse tables and skipping indexes.
- [x] Existing dev schema drift is detected by `db clickhouse status`.
- [x] `ingestion_offsets` uses `ReplacingMergeTree(updated_at)` on fresh and
  repaired dev schemas.
- [x] `serve --watch` writes to ClickHouse through `storage.driver: clickhouse`.
- [x] API reads use ClickHouse for health, search, history, topology, and service
  investigation when ClickHouse storage is selected.
- [x] Run the compose watcher long enough to catch merge/part-count regressions.
- [x] Record a final live profile after the long run for default and
  chDB-enabled API binaries.
- [x] Decide MVP thresholds for compression ratio, compressed bytes per row,
  service investigation latency, inactive part age, and stream health.

Acceptance:

```bash
make clickhouse-status
make clickhouse-live-profile
curl -fsS http://127.0.0.1:8080/api/v1/health?limit=5
```

Expected result: schema is clean, API returns 2xx, watcher has `0` queued and `0`
errors after the initial list phase, and storage efficiency remains materially
better than row-store proof retention.

MVP acceptance thresholds for the local ClickHouse backend:

| Signal | Threshold | Current reference |
| --- | --- | --- |
| Schema status | `make clickhouse-status` reports `ok yes` | `ok yes` |
| Watch errors | `0` errors after initial list phase | `0` |
| Queued resources | `0` queued after initial list phase | `0` |
| Unstable streams | Less than 5% of resources during steady state | latest profile: `7 / 241` retrying, `6 / 241` watching, `228 / 241` bookmark |
| Compression ratio | At least `15x` on active business tables | latest default: `28.4x`; latest chDB-enabled ClickHouse profile: `30.53x`; embedded chDB real-data profile: about `5.8x` |
| Compressed bytes per row | At most `100` bytes on active business tables | latest default: `33.96`; latest chDB-enabled ClickHouse profile: `32.16` |
| Service investigation latency | Under `1s` for representative live service query | latest default: `783.779 ms`; embedded chDB real-data profile: `606.767 ms`; chDB-enabled ClickHouse profile: `224.201 ms` |
| Health/search/history/topology API latency | Under `500ms` each in live profile | latest default: health `93.497ms`, search `84.622ms`, history `70.040ms`, topology `106.344ms` |
| Inactive part age | Oldest inactive `kube_insight` part under `1h` during local dev | about `480s` |
| Repair artifacts | `make clickhouse-cleanup-repair-artifacts` reports `droppable 0` | `droppable 0` |

These thresholds are MVP guardrails, not production SLOs. Tighten them only
after a larger dataset and a stable retention/cold-tier policy exist.

## Phase 3: Query And API Confidence

- [x] Health endpoint returns resource stream status from ClickHouse offsets.
- [x] Search endpoint returns representative fact/change evidence.
- [x] History endpoint reconstructs object versions from ClickHouse.
- [x] Topology endpoint expands edges around a selected object.
- [x] Service investigation endpoint returns service, related objects, facts,
  edges, changes, and summary counts.
- [x] Service investigation honors the API `limit` alias for related evidence
  objects and caps ClickHouse expansion for MVP safety.
- [x] Add or document one repeatable API smoke command set for the compose dev
  watcher.
- [x] MCP uses the same configured read backend as API/CLI for SQLite,
  ClickHouse, and chDB-enabled builds, and schema output tells agents which SQL
  shape is active before they run raw SQL.
- [x] Keep generated profile artifacts under `testdata/generated/` only by default.

Acceptance:

```bash
make clickhouse-live-profile
```

Expected result: `timings.tsv`, `storage-efficiency.tsv`, `footprint.tsv`,
`thresholds.tsv`, and `explain-*.txt` are generated. By default the live profile
fails when active-table compression or representative API latency falls outside
the MVP thresholds; set `CLICKHOUSE_LIVE_PROFILE_ENFORCE_THRESHOLDS=0` only for
ad-hoc exploratory runs. Generated ClickHouse profile, API smoke, and benchmark
artifacts must default to `testdata/generated/...`; scripts may write short-lived
intermediate files under `/tmp`, and custom output directories must require
explicit environment overrides.

## Phase 4: chDB Local Variant

- [x] Add an optional `-tags chdb` execution adapter behind
  `storage.driver: chdb`.
- [x] Reuse the ClickHouse schema/query contract for chDB instead of cloning
  SQLite-specific schema behavior.
- [x] Add `make chdb-smoke` for runtime validation once `libchdb.so` is
  installed.
- [x] Validate the optional adapter against a real `libchdb.so` runtime on the
  local dev machine.
- [x] Keep SQLite as a compatibility/test backend during the migration.
- [x] Add explicit local build variants for default and chDB-enabled binaries.
- [x] Merge Linux/macOS amd64/arm64 chDB-enabled release archives and Linux
  amd64/arm64 chDB Docker image publishing into the default GoReleaser workflow
  without changing the default binary name.
- [x] Add local chDB-enabled image scaffold that stages `libchdb.so` explicitly.
- [x] Publish chDB Docker as a Linux amd64/arm64 multi-architecture tag after
  staging per-architecture `libchdb.so` runtimes explicitly.
- [x] Add a repeatable local chDB smoke path for ingest, CLI/API health,
  search, history, topology, object investigation, service investigation, and
  read-only SQL/schema.
- [x] Run the chDB-enabled binary through full tagged tests, ClickHouse live
  profile/API smoke, SQLite fallback ingest/query, and local image startup.
- [x] Run a bounded real Kubernetes chDB collection/profile using the dev
  watcher container credentials; record API timings, compression, filesystem
  footprint, and alias-aware topology/service investigation behavior in
  `testdata/generated/chdb-live/`.
- [x] Reuse one chDB read store per `serve --api` process instead of opening a
  new `libchdb` session per API request.
- [x] Resolve `object_aliases` in the shared ClickHouse/chDB read path so
  topology and service investigation can connect EndpointSlice/Service alias IDs
  back to canonical objects.
- [x] Make `db clickhouse service` use the same typed service investigation path
  as the HTTP API instead of a separate raw SQL probe.
- [x] Add executable MVP threshold checks to ClickHouse live profile and API
  smoke outputs.
- [x] Document `libchdb` runtime library requirements for the chDB-enabled
  local variant.
- [x] Normal builds fail clearly for `storage.driver: chdb` and point to the
  tagged chDB build path.

Acceptance:

```bash
kube-insight config validate --file config/kube-insight.example.yaml
make chdb-smoke
./bin/kube-insight-chdb --config config/kube-insight.chdb.example.yaml config validate
./bin/kube-insight-chdb --config config/kube-insight.chdb.example.yaml query schema
./bin/kube-insight-chdb --config config/kube-insight.chdb.example.yaml query search kube-system --limit 5
```

Expected result: the default artifact keeps a small SQLite-backed local path,
the chDB-enabled artifact can run `storage.driver: chdb` with API/CLI read paths
matching the ClickHouse-backed behavior, `serve --api` works without metrics,
and a missing chDB runtime dependency fails with an actionable error instead of
silently falling back to SQLite.

Latest embedded chDB real-data reference from 2026-05-17:

| Signal | Result |
| --- | --- |
| Collected rows | `46,365` active business rows across observations, versions, aliases, facts, edges, changes, offsets, and API resources |
| Active compression | `6.13 MiB` compressed vs `33.92 MiB` uncompressed, about `5.8x` overall |
| chDB database directory | `7.6 MiB` after reopening the real-data database and letting temporary files clear |
| API health/search/history/topology | `58.398 ms`, `43.011 ms`, `26.802 ms`, `67.869 ms` |
| Service investigation | `606.767 ms`, with `1` EndpointSlice, `1` Pod, `1` Node, `2` Events, and `13` edges |
| Generated artifacts | `testdata/generated/chdb-live/` |

The embedded chDB service investigation is slower than the ClickHouse HTTP
backend because it executes the BFS read path serially through one local session,
but it is now correct and still inside the MVP `1s` service-query guardrail.

## Phase 5: Documentation And Operator Path

- [x] Document local ClickHouse workflow.
- [x] Document compose watcher workflow and credential mounts.
- [x] Document schema status and repair dry-run/apply behavior.
- [x] Update quickstart/config docs after final command names stabilize.
- [x] Keep ClickHouse MVP closeout aligned with the latest live profile numbers.
- [x] Keep the agent-vs-kubectl benchmark documented with real case mappings,
  reproduction commands, and generated-output hygiene.
- [x] Add a community-facing four-option performance/tradeoff guide covering
  raw `kubectl`, kube-insight SQLite, kube-insight chDB, and kube-insight
  ClickHouse so users can choose the right deployment level.
- [x] Add the first same-dataset storage-mode benchmark harness so SQLite,
  ClickHouse, chDB when available, and optional raw `kubectl` can be measured
  with one generated dataset and common query set.
- [x] Apply first service-investigation read-path optimization; the small
  same-dataset harness improved ClickHouse service investigation from about
  `352 ms` to `189 ms`, while chDB stayed roughly flat and needs a different
  optimization path.
- [x] Run a larger same-dataset storage-mode benchmark
  (`STORAGE_BENCH_CLUSTERS=2 STORAGE_BENCH_COPIES=20`) and document why SQLite
  wins small local point reads while ClickHouse/chDB remain better positioned
  for compression, batch ingest, retained history, and central-service scale.
- [x] Batch ClickHouse/chDB service-investigation evidence reads, defer topology
  object hydration, and add lightweight row parsing for ClickHouse HTTP hot
  paths; the larger same-dataset harness now measures ClickHouse service
  investigation around `182 ms`, while chDB remains around `500 ms` and needs a
  different optimization path.
- [x] Add default-off chDB query tracing and profile one service investigation;
  the run showed `18` queries, about `149 ms` total query time, and no single SQL
  outlier, so deeper chDB optimization is deferred instead of complicating MVP
  read paths.
- [x] Re-review user-facing and validation docs so README, quickstart, docs
  index, agent SQL cookbook, benchmark plans, and acceptance gates match the
  current SQLite/ClickHouse/chDB positioning. Latest structure pass on
  2026-05-18 also checked release docs, Makefile targets, ignored local
  artifacts, and agent skill backend-detection guidance.
- [x] Consolidate validation docs so `docs/validation/` only contains the
  user-facing storage/performance summary, while closeout notes, benchmark
  plans, acceptance plans, and open-source readiness checklists live under
  `docs/dev/`.
- [x] Walk the user documentation path from README to quickstart, storage mode
  comparison, and agent skill; add the missing ClickHouse service-backend quick
  start and keep cold-tiering documented as opt-in.
- [x] Validate the agent skill against the live local ClickHouse dev API with
  SQL-first queries for coverage, cluster scope, fact inventory, high-severity
  candidates, changes, topology edges, observations, versions, and webhook
  evidence; document SQL as the primary agent interface and typed APIs as
  supporting summaries.
- [x] Add a short "known limitations" section for the MVP backend.

Acceptance:

```bash
rg -n "ClickHouse|compose|live profile|repair-ingestion-offsets|cleanup-repair-artifacts" docs
./scripts/benchmark-agent-vs-kubectl.sh kubeinsight.db <kubectl-context> testdata/generated/agent-vs-kubectl-latest
```

Expected result: docs explain the current path without implying production S3
cold tiering or UI is part of the MVP. chDB is documented as a validated
chDB-enabled variant with a `libchdb.so` runtime requirement, while the default
artifact remains the small SQLite-backed local build. The agent-vs-kubectl
benchmark remains positioned as a real-case investigation comparison, not a
claim that every point lookup is faster than direct `kubectl`.

## Phase 6: Final Validation Before Calling MVP Done

Run these before closing the MVP backend slice:

```bash
make test
make build
make chdb-build-check
make chdb-smoke
go test -tags chdb ./...
make build-local-variants
make build-chdb-image
make release-chdb-check
docker run --rm kube-insight-chdb:local version
git diff --check
find cmd internal -name '*.go' -print0 | xargs -0 wc -l | awk '$2 != "total" && $1 > 800 {print}'
make clickhouse-smoke
make clickhouse-live-profile
/bin/bash -lc 'set -a; . ./.env; set +a; CLICKHOUSE_LIVE_PROFILE_OUTPUT=testdata/generated/clickhouse-live-profile-chdb CLICKHOUSE_LIVE_PROFILE_API=http://127.0.0.1:18081 ./scripts/clickhouse-live-profile.sh'
/bin/bash -lc 'set -a; . ./.env; set +a; CLICKHOUSE_API_SMOKE_OUTPUT=testdata/generated/clickhouse-api-smoke-chdb CLICKHOUSE_API_SMOKE_API=http://127.0.0.1:18081 ./scripts/clickhouse-api-smoke.sh'
make clickhouse-status
make clickhouse-cleanup-repair-artifacts
make clickhouse-clean-system-logs
make mcp-sql-first-smoke
make release-artifact-smoke
make live-service-vs-kubectl
```

Expected result: all commands pass, no Go file exceeds 800 lines, ClickHouse
status is clean, and the live profile confirms acceptable storage/query behavior.
Latest run on 2026-05-17 passed core closeout validation. After rebuilding the
compose watcher/API on the long-lived ClickHouse volume, `make clickhouse-live-profile`
and `make clickhouse-api-smoke` passed against `127.0.0.1:8080`; service
investigation was `801.255 ms`, still inside the MVP `1s` guardrail. The release-path run also passed `make release-chdb-check` and a local
`goreleaser release --snapshot --clean --skip=docker`; the generated chDB archive
was `kube-insight_0.0.2-next_chdb_linux_amd64.tar.gz` and contained
`kube-insight`, `libchdb.so`, and `config/kube-insight.chdb.example.yaml`. On
2026-05-18, `make mcp-sql-first-smoke`, `make release-artifact-smoke`, and
`make live-service-vs-kubectl` also passed; the live Service case measured
kube-insight SQL/API at `481.150 ms` total versus raw `kubectl` at
`3,229.201 ms` total for the same current target.

## Explicitly Deferred

These are useful but outside the MVP stop line:

- Real S3/object-storage tier validation.
- ClickHouse native `JSON` type benchmarking.
- `JSONAllPaths`, `JSONAllValues`, and broader text indexes.
- Materialized read models for latest objects or topology closures.
- UI prototype and production in-cluster deployment.
- chDB-enabled `serve --metrics`; current libchdb runtime testing is not stable
  enough for the combined API plus metrics service shape.
- Automated destructive cleanup of migration backup tables.
- Promoting chDB to the default local backend in the default artifact; keep this
  deferred until runtime packaging is reliable enough for normal installs.

## Next Operating Rule

When choosing what to do next, prefer the first unchecked item in this file. If a
new idea does not unblock one of these checklist items, write it under
"Explicitly Deferred" or a separate roadmap doc instead of implementing it in
the MVP branch.
