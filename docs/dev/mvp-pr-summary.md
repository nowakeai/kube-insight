# MVP PR Summary

## Suggested Title

ClickHouse storage MVP with chDB local variant and release packaging

## Summary

This change promotes the storage MVP from SQLite-only local persistence to a
multi-backend architecture centered on ClickHouse-compatible query semantics.
ClickHouse becomes the scalable server backend, chDB becomes the optional local
ClickHouse-compatible variant, and SQLite remains the default pure-Go local and
test backend.

The PR also adds repeatable local dev workflows, live profiling, API smoke tests,
and release packaging for default and chDB-enabled artifacts. ClickHouse maintenance commands are grouped under `db clickhouse maintenance` while older flat paths remain hidden compatibility commands.

## Main Change Groups

### ClickHouse Storage Backend

- Adds `internal/storage/clickhouse` with schema management, HTTP client access,
  inserts, typed reads, search, history, topology, service investigation,
  storage stats, schema status, and safe repair helpers.
- Adds ClickHouse-backed CLI wiring for `serve`, `collect`, query paths, and
  `db clickhouse` operational commands. Explicit backfill/repair/cleanup tasks
  live under `db clickhouse maintenance`.
- Keeps retention/TTL tiering opt-in; default local/dev schema does not enable
  cold-tier movement.
- Adds ClickHouse storage metrics for compression ratio, bytes per row, active
  and inactive footprint, and part counts.

### chDB Local Variant

- Adds optional `storage.driver: chdb` behind the `chdb` build tag.
- Reuses the ClickHouse-compatible schema/query contract rather than forking
  SQLite-specific behavior.
- Adds unavailable-placeholder behavior for default builds so SQLite remains the
  default pure-Go local backend.
- Adds `make chdb-smoke`, `make chdb-build-check`, `make build-chdb`, and local
  chDB image support.

### API, CLI, And Shared Query Contract

- Extends the storage interface with typed health/query capabilities where
  needed by API and CLI paths.
- Moves API service investigation, history, search, topology, and health paths
  toward shared typed store behavior across SQLite, ClickHouse, and chDB.
- Makes MCP use the same configured read backend as API/CLI for SQLite,
  ClickHouse, and chDB-enabled builds; MCP schema and prompts tell agents to
  detect the active backend before writing SQL.
- Adds tests around API/CLI/MCP backend selection, typed query behavior, and
  error handling for unavailable optional backends.

### Dev Environment And Validation

- Adds portable `compose.dev.yaml` plus ClickHouse and watcher containers for
  local development.
- Adds ClickHouse smoke, benchmark, API smoke, and read-only live profile scripts.
- Adds MCP SQL-first smoke, release archive smoke, and a live same-target Service
  comparison against raw `kubectl`.
- Live profile reports query timings, storage compression, bytes per row,
  ClickHouse footprint, skip indexes, and explain output.
- Adds docs for ClickHouse local workflows and the MVP development checklist.

### Release And Open-Source Packaging

- Merges default and chDB release paths into `.goreleaser.yaml`.
- Publishes default archives for Linux amd64/arm64, macOS amd64/arm64, and
  Windows amd64.
- Publishes chDB archives for Linux amd64/arm64 and macOS amd64/arm64; Windows
  chDB is intentionally excluded because upstream chDB and `chdb-go` do not
  currently provide a working Windows runtime.
- Publishes Docker images from one GHCR package:
  - `ghcr.io/nowakeai/kube-insight:<tag>`
  - `ghcr.io/nowakeai/kube-insight:<tag>-chdb`
- Uses `dockers_v2` and distroless images so multi-arch Docker builds do not
  need target-architecture `RUN` execution.
- Stages official chDB `libchdb` runtimes for Linux/macOS amd64/arm64 before
  release.
- Builds categorized GitHub Release notes with the changelog builder action,
  appends artifact guidance, and passes the generated notes to GoReleaser.

## Notable Files

- Release and packaging: `.goreleaser.yaml`, `.github/workflows/release.yml`,
  `Dockerfile`, `docker/chdb.Dockerfile`, `RELEASE.md`.
- Local dev: `compose.dev.yaml`, `docker/dev-watcher.Dockerfile`,
  `docker/clickhouse-dev/config.d/system-logs.xml`, `docs/dev/commands.md`,
  `docs/dev/clickhouse-local-workflow.md`.
- Storage backends: `internal/storage/clickhouse/`, `internal/storage/chdb/`,
  `internal/storage/query.go`, `internal/storage/health.go`.
- CLI/API/MCP integration: `internal/cli/`, `internal/api/`, `internal/mcp/`,
  `internal/metrics/`.
- Validation scripts: `scripts/clickhouse-smoke.sh`,
  `scripts/clickhouse-live-profile.sh`, `scripts/clickhouse-api-smoke.sh`,
  `scripts/clickhouse-benchmark.sh`, `scripts/chdb-smoke.sh`,
  `scripts/benchmark-agent-vs-kubectl.sh`, `scripts/mcp-sql-first-smoke.sh`,
  `scripts/release-artifact-smoke.sh`, `scripts/live-service-vs-kubectl.sh`.
- Agent and storage benchmark docs: `docs/users/reference/storage-mode-comparison.md`.

## Validation Run

Latest local validation completed on 2026-05-18:

```bash
make test
make build
make chdb-build-check
go test -tags chdb ./...
timeout 240 make chdb-smoke
make release-chdb-check
make build-chdb-image
docker run --rm kube-insight-chdb:local version
make clickhouse-smoke
make clickhouse-live-profile
make clickhouse-api-smoke
make clickhouse-status
make clickhouse-cleanup-repair-artifacts
make clickhouse-clean-system-logs
make mcp-sql-first-smoke
make release-artifact-smoke
make live-service-vs-kubectl
make open-source-check
git diff --check
find cmd internal -name '*.go' -print0 | xargs -0 wc -l | awk '$2 != "total" && $1 > 800 {print}'
```

The archive snapshot produced 9 release archives:

- default: Linux amd64, Linux arm64, macOS amd64, macOS arm64, Windows amd64
- chDB: Linux amd64, Linux arm64, macOS amd64, macOS arm64

Each chDB archive was inspected and contains:

- `kube-insight`
- `libchdb.so`
- `config/kube-insight.chdb.example.yaml`

The Docker snapshot built default and chDB Linux amd64/arm64 images locally
without publishing. Snapshot mode appends temporary `-amd64` and `-arm64` tags;
release mode publishes the configured multi-architecture tags.

The release artifact smoke unpacked the local Linux amd64 default and chDB
snapshot archives, then ran `version`, `config validate`, `ingest`, `query
schema`, and `query sql` from the extracted binaries.

The latest ClickHouse live profile measured active business-table compression at
`29.58x`, `31.13` compressed bytes per active row, API health/search/history/topology
under `500 ms`, and service investigation at `262.618 ms` after per-object
fact/change caps. API smoke measured service investigation at `279.171 ms`.

The live same-target Service comparison used the current kubeconfig context and
target `8004scan-production/production-8004scan-backend-api`: kube-insight
ClickHouse SQL/API completed in `448.746 ms` total, while the comparable raw
`kubectl` calls completed in `3,462.546 ms` total.

## Release Notes

- The default binary remains pure Go and keeps SQLite as the local fallback.
- The chDB-enabled binary is additive and still supports SQLite and ClickHouse.
- chDB archives are larger because each one includes the matching `libchdb.so`.
- GHCR package visibility must be confirmed after first publish. The workflow
  can publish packages, and images include the OCI source label, but the current
  local GitHub token lacks `read:packages`, so package visibility could not be
  checked from this workspace.

## Known Limitations

- Windows chDB is not released until upstream chDB and `chdb-go` support it.
- ClickHouse S3/cold-tiering is configured as an option but not end-to-end
  validated against real object storage in this MVP.
- Native ClickHouse `JSON` type experiments remain follow-up work; MVP schema
  stays conservative.
- chDB `serve --metrics` remains deferred because combined `serve --api --metrics`
  was not stable enough under the current local `libchdb` runtime.

## Final Review Notes

- No stale `.goreleaser.chdb.yaml`, standalone `ghcr.io/nowakeai/kube-insight-chdb`,
  or Linux/amd64-only chDB release references were found in current release docs.
- Local `.env`, `kubeinsight.db`, `todos.md`, `dist/`, `build/`, and
  `testdata/generated/` are ignored and should not be staged.
- The checked-in docs intentionally keep developer-only workflows under
  `docs/dev/` and user-facing backend positioning in `README.md`,
  `docs/users/getting-started/quickstart.md`, and `docs/operators/configuration/configuration.md`.
- `docs/users/reference/storage-mode-comparison.md` is the user-facing evidence,
  kubectl comparison, live same-target Service comparison, and storage-mode
  benchmark doc for retained kube-insight evidence versus repeated broad live
  `kubectl` calls.
- The stale pre-MVP `scripts/benchmark-insight-vs-kubectl.sh` helper was removed;
  `scripts/benchmark-agent-vs-kubectl.sh` is the canonical agent-vs-kubectl
  benchmark script.
- The final documentation pass aligned README, quickstart, docs index, agent SQL
  cookbook, data/query docs, and validation gates with the current default
  SQLite artifact, ClickHouse service backend, chDB-enabled variant, and
  benchmark/profile evidence.


## Suggested Commit Slices

These are logical review slices. They do not require separate commits if the PR
is squashed, but they are useful for staging and review.

1. Storage contracts and ClickHouse backend
   - `internal/storage/query.go`, `internal/storage/health.go`
   - `internal/storage/clickhouse/`
   - storage tests and shared SQL/query behavior

2. CLI/API/metrics integration
   - `internal/cli/`
   - `internal/api/`
   - `internal/metrics/`
   - backend selection, typed reads, service investigation, and health paths

3. chDB optional local variant
   - `internal/storage/chdb/`
   - `config/kube-insight.chdb.example.yaml`
   - chDB build tags, smoke test, and unavailable default-build behavior

4. Local dev and validation tooling
   - `compose.dev.yaml`
   - `docker/dev-watcher.Dockerfile`
   - `docker/clickhouse-dev/`
   - `scripts/clickhouse-*.sh`, `scripts/chdb-smoke.sh`
   - generated-output and live-profile docs

5. Release and open-source packaging
   - `.goreleaser.yaml`
   - `.github/workflows/release.yml`
   - `Dockerfile`, `docker/chdb.Dockerfile`, `.dockerignore`
   - `RELEASE.md`, open-source readiness checks

6. Documentation pass
   - user-facing docs: `README.md`, `docs/users/getting-started/quickstart.md`, configuration docs
   - architecture/data docs: backend strategy, storage/index/query docs
   - dev docs: workflow, checklist, validation closeout, PR summary

## PR Body Template

```markdown
## Summary

- add ClickHouse storage backend with typed query/read paths and storage metrics
- add optional chDB local variant using the ClickHouse-compatible storage contract
- add local dev compose, smoke tests, live profile, and benchmark scripts
- merge default and chDB release packaging into one GoReleaser config

## Release Impact

- default archives: linux amd64/arm64, darwin amd64/arm64, windows amd64
- chDB archives: linux amd64/arm64, darwin amd64/arm64, each bundled with `libchdb.so`
- Docker tags: `ghcr.io/nowakeai/kube-insight:<tag>` and `<tag>-chdb`
- GHCR package visibility must be confirmed public after first publish

## Validation

- [x] `make test`
- [x] `make build`
- [x] `make chdb-build-check`
- [x] `go test -tags chdb ./...`
- [x] `timeout 240 make chdb-smoke`
- [x] `make build-chdb-image`
- [x] `docker run --rm kube-insight-chdb:local version`
- [x] `make release-chdb-check`
- [x] `make clickhouse-smoke`
- [x] `make clickhouse-live-profile`
- [x] `make clickhouse-api-smoke`
- [x] `make clickhouse-status`
- [x] `make clickhouse-cleanup-repair-artifacts`
- [x] `make mcp-sql-first-smoke`
- [x] `make release-artifact-smoke`
- [x] `make live-service-vs-kubectl`
- [x] `make open-source-check`
- [x] `git diff --check`
- [x] Go 800-line rule

## Known Limitations

- Windows chDB is intentionally excluded until upstream runtime support exists.
- ClickHouse cold-tiering to object storage is configured but not end-to-end validated.
- Native ClickHouse `JSON` type benchmarking remains a follow-up.
- chDB `serve --metrics` remains deferred.
```

## Suggested Review Order

1. Review storage contracts and ClickHouse/chDB backend shape.
2. Review API/CLI routing and typed query behavior.
3. Review dev scripts and generated-output paths.
4. Review release packaging and GHCR tag policy.
5. Review docs for user-facing clarity versus internal dev notes.
