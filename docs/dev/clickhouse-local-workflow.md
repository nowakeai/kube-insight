# ClickHouse Local Workflow

This workflow runs kube-insight against a long-lived local ClickHouse container
on `127.0.0.1:8123`. It is intended for development and MVP validation, not for
production cold-tier validation.

## 1. Configure Local Credentials

Keep local credentials in `.env`. The file is intentionally gitignored.

```bash
CLICKHOUSE_DATABASE=kube_insight
CLICKHOUSE_USER=kube_insight
CLICKHOUSE_PASSWORD=<local password>
KUBE_INSIGHT_CLICKHOUSE_DSN=http://127.0.0.1:8123/?user=kube_insight&password=<local password>
```

Do not commit `.env` or paste the real password into docs, issues, or reports.
The ClickHouse scripts redact `password=` values in terminal summaries.

## 2. Start Local ClickHouse

```bash
make clickhouse-up
```

This starts or reuses the Docker container named by `CLICKHOUSE_CONTAINER` and
exposes the HTTP endpoint on `127.0.0.1:8123`. The smoke test uses a separate
container and port, so it will not reset the long-lived dev database.

To stop and remove the long-lived dev container:

```bash
make clickhouse-down
```

## 2.1 Start ClickHouse And Watcher With Compose

For a containerized local dev loop, use the compose file instead of running the
watcher on the host. This is also the default backend for Web UI/frontend
development, so the compose Web UI service proxies to the compose watcher/app
instead of requiring a separate host `kube-insight serve` process:

```bash
make clickhouse-down
make dev-compose-up
```

The compose workflow starts three services:

- `clickhouse` on `127.0.0.1:8123` with the compose project's default
  `clickhouse-data` volume. This keeps the checked-in compose file portable
  across different checkouts and machines. The service mounts
  `docker/clickhouse-dev/config.d/system-logs.xml` so ClickHouse diagnostic
  system logs use a 1-day TTL in the dev environment.
- `watcher`, built from `docker/dev-watcher.Dockerfile`, running
  `serve --watch --app --metrics --listen 0.0.0.0:8090` against the
  ClickHouse service name.
- `web`, built from `docker/dev-web.Dockerfile`, running Vite on
  `127.0.0.1:5173` and proxying API and metrics requests to the compose
  `watcher` service.

The watcher container mounts local Kubernetes credentials by default:

```bash
${HOME}/.kube/config -> /home/kube-insight/.kube/config
${HOME}/.config/gcloud -> /home/kube-insight/.config/gcloud
  (writable for token refresh and logs)
```

Override credential paths when needed:

```bash
KUBECONFIG_PATH=/path/to/kubeconfig \
GCLOUD_CONFIG_PATH=/path/to/gcloud-config \
make dev-compose-up
```

To reuse a pre-existing Docker volume or preserve a local container name,
create a local `compose.dev.local.yaml` file. The Makefile automatically adds
this file when it exists, and the file is gitignored:

```yaml
services:
  clickhouse:
    container_name: kube-insight-clickhouse-dev

volumes:
  clickhouse-data:
    name: my-existing-clickhouse-volume
    external: true
```

Use detached mode when running the full dev environment in the background:

```bash
make dev-compose-up-detached
make dev-compose-ps
```

The dev watcher image includes the Google Cloud SDK and
`gke-gcloud-auth-plugin`, so kubeconfigs that use GKE exec auth can work inside
the container. Stop the compose stack with:

```bash
make dev-compose-down
```

Use `make dev-compose-logs` to follow logs from the ClickHouse, watcher, and web services.

The `web` service bind-mounts `./web` and keeps dependencies in the
`web-node-modules` Docker volume. Vite hot module reload should pick up normal
frontend source changes automatically. If `package.json` or
`package-lock.json` changes, rebuild the Web UI service:

```bash
make dev-compose-rebuild-web
```

The `watcher` service is image-based, so backend Go/config changes require a
compose rebuild/recreate. The normal safe command is:

```bash
make dev-compose-up-detached
```

For targeted log streams during development:

```bash
make dev-compose-logs-web
make dev-compose-logs-watcher
```

### 2.2 Dev Volume Hygiene

ClickHouse inactive parts are normal MergeTree merge leftovers and are removed
by the server after `old_parts_lifetime` (480 seconds by default). The checked-in
compose config does not change this business-table behavior.

ClickHouse diagnostic tables under the `system` database can grow quickly in a
busy dev loop. The compose workflow mounts a local-only config that sets a
1-day TTL for the common system log tables. The TTL is applied when the
ClickHouse service starts with the mounted config; restart the compose stack
after changing it.

To reclaim dev disk space immediately without touching `kube_insight` business
tables, run:

```bash
make clickhouse-clean-system-logs
```

This flushes and truncates ClickHouse system log tables such as `query_log`,
`text_log`, `trace_log`, `part_log`, and metric logs. It is intended for local
dev only.

## 3. Validate The ClickHouse Config

```bash
make build
./bin/kube-insight config validate --file config/kube-insight.clickhouse.example.yaml
```

The ClickHouse example overrides storage and raises
`collection.watch.maxConcurrentStreams` for local profiling so a full-resource
dev watcher can keep all discovered streams live on clusters with many GVRs.
The production-safe default remains lower in `config/kube-insight.example.yaml`.

## 4. Run Watcher, API, And Metrics

```bash
make clickhouse-serve-dev
```

This target depends on `clickhouse-up`, so it also ensures the long-lived local
ClickHouse container is running before starting kube-insight.

For a short run, pass extra serve flags:

```bash
CLICKHOUSE_DEV_SERVE_FLAGS='--api --metrics --timeout 2m' make clickhouse-serve-dev
```

By default the target watches `pods`, `services`, and
`endpointslices.discovery.k8s.io`. Override `CLICKHOUSE_DEV_RESOURCES` to change
the resource set. Use a longer timeout or omit `--timeout` for continuous local
testing. When
`storage.driver: clickhouse` is selected, `initOnStart` applies idempotent DDL
before writes. The local example keeps writes batched, coalesces pending offset updates, and
enables ClickHouse async insert settings to reduce small-part churn during
watch loops. Cold tiering remains disabled because the Docker server has no
S3-backed storage policy.

## 5. Check Schema Drift

Run this after schema changes or when reusing an older local ClickHouse volume:

```bash
make clickhouse-status
```

This is read-only. It compares existing table engines and sorting keys against
the current expected schema and redacts endpoint passwords. Existing tables are
not mutated by `db clickhouse init`, so drift means a follow-up migration or dev
DB rebuild is needed.

For the known `ingestion_offsets` drift from older dev volumes, inspect the
non-mutating repair plan first:

```bash
make clickhouse-repair-plan
```

The generated plan creates a new `ReplacingMergeTree(updated_at)` offsets table,
copies the latest offset per resource/event key with `argMax`, and keeps the old
table under an `ingestion_offsets_backup_*` name. Applying it is deliberately not
wrapped in a Make target; run `db clickhouse maintenance repair-ingestion-offsets --apply
--yes` only after checking the printed statements and coordinating with the
running watcher.

After a failed or completed repair, list cleanup candidates with:

```bash
make clickhouse-cleanup-repair-artifacts
```

This is read-only. The underlying `db clickhouse maintenance cleanup-repair-artifacts --yes`
command only drops empty `ingestion_offsets_repair_*` scratch tables. Backup
tables are reported but never dropped automatically.

## 6. Smoke-Test The API

Run this while the watcher is still collecting data:

```bash
make clickhouse-api-smoke
```

This checks ClickHouse connectivity, API connectivity, and the representative
health, search, history, topology, and service investigation endpoints against
real objects selected from the live database. Responses are written under
`testdata/generated/clickhouse-api-smoke/`.

## 7. Profile Live Query And Storage Behavior

Run this while the watcher is still collecting data:

```bash
make clickhouse-live-profile
```

The profile is read-only. It writes reports under
`testdata/generated/clickhouse-live-profile/`, including:

- `summary.txt`
- `storage-efficiency.tsv` for active `kube_insight` business tables
- `footprint.tsv` for active/inactive parts across ClickHouse databases
- `inactive-parts.tsv` for merge leftovers waiting on cleanup
- `storage-efficiency-trend.tsv` when a previous run exists
- `timings.tsv`
- `timing-trend.tsv` when a previous run exists
- `explain-*.txt`

Primary MVP signals:

- compression ratio should stay materially above row-store proof storage,
- compressed bytes per row should not trend upward unexpectedly,
- proof/derived compressed byte shares should make sense for the resource mix,
- inactive parts should not be mistaken for active retained evidence,
- API health, search, history, topology, and service investigation should stay
  interactive at the live data size.

## 8. Scrape Metrics

```bash
curl http://127.0.0.1:8090/metrics | rg \
  'kube_insight_storage_(compression_ratio|compressed_bytes_per_row)|clickhouse_(compressed|uncompressed|proof_compressed|derived_compressed)'
```

Useful ClickHouse storage metrics include:

- `kube_insight_storage_compression_ratio`
- `kube_insight_storage_compressed_bytes_per_row`
- `kube_insight_storage_bytes{kind="clickhouse_compressed"}`
- `kube_insight_storage_bytes{kind="clickhouse_uncompressed"}`
- `kube_insight_storage_bytes{kind="clickhouse_proof_compressed"}`
- `kube_insight_storage_bytes{kind="clickhouse_derived_compressed"}`
- `kube_insight_storage_bytes{kind="clickhouse_kube_insight_active_bytes_on_disk"}`
- `kube_insight_storage_bytes{kind="clickhouse_kube_insight_inactive_bytes_on_disk"}`
- `kube_insight_storage_parts{kind="clickhouse_kube_insight_inactive_parts"}`
- `kube_insight_storage_part_age_seconds{kind="clickhouse_kube_insight_inactive_oldest_inactive"}`

## 9. Isolated Regression Checks

Use these when changing schema, query, or write-path code:

```bash
make clickhouse-smoke
make clickhouse-api-smoke
make clickhouse-benchmark
make clickhouse-live-profile
```

`make clickhouse-smoke` uses `127.0.0.1:18123` and does not touch the long-lived
ClickHouse container on `8123`. Benchmark databases should keep `bench` in their
name so reset protection remains clear.
