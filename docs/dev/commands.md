# Development Commands

This page keeps command examples out of `AGENTS.md` so agent rules stay short.

## Checks

```bash
make test
make build
git diff --check
```

Focused checks:

```bash
go test ./internal/filter ./internal/ingest ./internal/collector ./internal/cli
go test ./internal/storage/sqlite ./internal/api ./internal/mcp ./internal/metrics
```

Line-limit check:

```bash
find cmd internal -name '*.go' -print0 | xargs -0 wc -l | awk '$2 != "total" && $1 > 800 {print}'
```

Formatting:

```bash
gofmt -w cmd internal
```

## Common CLI

Build first:

```bash
make build
```

Validate config:

```bash
./bin/kube-insight config validate --file config/kube-insight.example.yaml
```

Watch current context:

```bash
./bin/kube-insight watch --db kubeinsight.db
```

Watch a selected resource set:

```bash
./bin/kube-insight watch pods services 'apps/v1/*' --db kubeinsight.db
```

Run local service surfaces for backend-only tests or isolated smoke runs:

```bash
./bin/kube-insight serve --watch --api --mcp --metrics --db kubeinsight.db
```

For Web UI or frontend development, do not start a separate host
`kube-insight serve` process. Use the Docker compose dev backend, then run the
React Web UI dev server against that API:

```bash
make dev-compose-up-detached
make dev-compose-ps
npm --prefix web run dev
```

Stop one-off host `serve` processes after backend tests finish. Stop the compose
backend with `make dev-compose-down` when the shared dev environment should be
shut down.

When the dev server is exposed through a remote workspace domain, configure Vite
host checking in the gitignored root `.env` file:

```dotenv
KUBE_INSIGHT_WEB_ALLOWED_HOSTS=5173--main--rj--renjie.coder.autonome.fun
```

Use a comma or whitespace separated list for multiple hosts. `VITE_ALLOWED_HOSTS`
is also accepted for local Vite compatibility, but
`KUBE_INSIGHT_WEB_ALLOWED_HOSTS` is the preferred project variable.

Query schema and read-only SQL:

```bash
./bin/kube-insight query schema --db kubeinsight.db
./bin/kube-insight query sql --db kubeinsight.db --output table --sql \
  "select status, count(*) from ingestion_offsets group by status"
```

Inspect storage and collector health:

```bash
./bin/kube-insight db resources health --db kubeinsight.db
./bin/kube-insight db compact --db kubeinsight.db
```

Run a long-lived local ClickHouse container for development:

```bash
make clickhouse-up
make clickhouse-down
make clickhouse-status
make clickhouse-repair-plan
make clickhouse-cleanup-repair-artifacts
make clickhouse-clean-system-logs
```

`make clickhouse-up` reads local credentials from `.env` and exposes the dev
HTTP endpoint on `127.0.0.1:8123`. `.env` is intentionally gitignored.
`make clickhouse-status` is read-only and reports existing ClickHouse table
engine/sorting-key drift with password redaction. `make clickhouse-repair-plan`
prints the non-mutating repair plan for the known `ingestion_offsets` dev-volume
drift. `make clickhouse-cleanup-repair-artifacts` lists empty repair scratch
tables without dropping them. `make clickhouse-clean-system-logs` flushes and
truncates local ClickHouse `system.*_log` diagnostic tables without touching the
`kube_insight` database. Use `make clickhouse-serve-dev` to run the local
ClickHouse watcher/API/metrics loop. Use `make dev-compose-up-detached` to run
both ClickHouse and the watcher in containers, and `make dev-compose-ps` to
inspect their status. See [ClickHouse Local Workflow](clickhouse-local-workflow.md) for
the full profile loop.

`make clickhouse-smoke` starts a separate temporary container on `127.0.0.1:18123`
and removes it by default when the smoke test exits. Use
`KEEP_CLICKHOUSE=1 make clickhouse-smoke` when you want to inspect that
smoke-test container after the run.

Generate or apply ClickHouse schema DDL:

```bash
./bin/kube-insight db clickhouse schema
./bin/kube-insight db clickhouse schema --json-type --output json
./bin/kube-insight db clickhouse schema \
  --database kube_insight \
  --cold-volume cold \
  --cold-after 168h

KUBE_INSIGHT_CLICKHOUSE_DSN=http://localhost:8123 \
  ./bin/kube-insight db clickhouse init
./bin/kube-insight db clickhouse init --endpoint http://localhost:8123
./bin/kube-insight db clickhouse status --endpoint http://localhost:8123
./bin/kube-insight db clickhouse maintenance repair-ingestion-offsets --endpoint http://localhost:8123
./bin/kube-insight db clickhouse maintenance repair-ingestion-offsets --endpoint http://localhost:8123 --apply --yes
./bin/kube-insight db clickhouse maintenance cleanup-repair-artifacts --endpoint http://localhost:8123
./bin/kube-insight db clickhouse maintenance cleanup-repair-artifacts --endpoint http://localhost:8123 --yes

./bin/kube-insight db clickhouse import \
  --endpoint http://localhost:8123 \
  --file testdata/fixtures/kube/core.json
./bin/kube-insight db clickhouse service default api \
  --endpoint http://localhost:8123
```

`db clickhouse init` and `import` use the ClickHouse HTTP interface directly.
`db clickhouse service` opens a ClickHouse store and returns the same typed
service investigation shape as `query service` and the HTTP API.
Native TCP DSNs are not supported by these ClickHouse commands. To make normal
`ingest`, `watch`, and `serve --watch` write to ClickHouse, set
`storage.driver: clickhouse` and export `KUBE_INSIGHT_CLICKHOUSE_DSN`.

Backfill missing Service LoadBalancer facts after adding the Service extractor
or after restoring older ClickHouse history. This is append-only and defaults to
dry-run; it does not rewrite retained `observations` or `versions`:

```bash
./bin/kube-insight db clickhouse maintenance backfill-service-facts \
  --endpoint http://localhost:8123
./bin/kube-insight db clickhouse maintenance backfill-service-facts \
  --endpoint http://localhost:8123 \
  --namespace svc-mux-eip-test \
  --yes
```

Repair legacy ClickHouse edge rows where `src_kind` or `dst_kind` is empty. This
is for old dev/live volumes created before edge kind inference was added. It
defaults to dry-run and uses explicit ClickHouse mutations only when `--yes` is
set:

```bash
./bin/kube-insight db clickhouse maintenance repair-edge-kinds \
  --endpoint http://localhost:8123
./bin/kube-insight db clickhouse maintenance repair-edge-kinds \
  --endpoint http://localhost:8123 \
  --yes
```

Rebuild derived facts, edges, and changes after extractor/profile changes:

```bash
./bin/kube-insight db reindex --db kubeinsight.db
./bin/kube-insight db reindex --db kubeinsight.db --yes
```

Run the stable built-in agent evaluation tests. These score replayable
`agent.RunEvent` transcripts for tool choice, candidate evidence artifacts,
verified answer citation coverage, answer terms, failed tools, tool-call count,
and latency without calling a live model:

```bash
go test ./internal/agent
```

Run the opt-in live LLM agent evaluation against one or more
OpenAI-compatible models. This uses real models with controlled kube-insight
fake tools and writes an optional JSON report:

```bash
KUBE_INSIGHT_AGENT_LIVE_EVAL=1 \
KUBE_INSIGHT_AGENT_LIVE_EVAL_MODELS='gpt52|gpt-5.2|OPENAI_API_KEY|OPENAI_BASE_URL;mimo|mimo-v2.5-pro|MIMO_API_KEY|MIMO_OPENAI_BASEURL' \
KUBE_INSIGHT_AGENT_LIVE_EVAL_MAX_ITERATIONS=12 \
KUBE_INSIGHT_AGENT_LIVE_EVAL_OUTPUT="$PWD/testdata/generated/agent-eval-live" \
go test ./internal/agent -run TestLiveLLMEvaluation -count=1 -v
```

Run the opt-in real DB prompt-context comparison when evaluating whether a
larger system prompt reduces discovery calls. This sends selected evidence DB
content and user questions to the configured live model endpoint, so use only
with approved data/export boundaries:

```bash
KUBE_INSIGHT_AGENT_REAL_PROMPT_EVAL=1 \
KUBE_INSIGHT_AGENT_REAL_EVAL_DB="$PWD/testdata/generated/real-cluster/kubeinsight.db" \
KUBE_INSIGHT_AGENT_REAL_EVAL_CONTEXT_FILE="$PWD/testdata/generated/real-cluster/context.md" \
KUBE_INSIGHT_AGENT_REAL_EVAL_QUESTIONS='Is ns/name Service healthy?;;Map topology around ns/name Service.' \
KUBE_INSIGHT_AGENT_REAL_EVAL_MODES='baseline,rich' \
KUBE_INSIGHT_AGENT_LIVE_EVAL_MODEL='mimo-v2.5-pro' \
KUBE_INSIGHT_AGENT_LIVE_EVAL_OUTPUT="$PWD/testdata/generated/agent-eval-real" \
go test ./internal/agent -run TestRealDBPromptContextEvaluation -count=1 -v
```

Run the local agent-vs-kubectl benchmark. Refresh the evidence database first
when you need freshness-controlled numbers for documentation or release notes:

```bash
./scripts/benchmark-agent-vs-kubectl.sh \
  kubeinsight.db \
  <kubectl-context> \
  testdata/generated/agent-vs-kubectl-latest
```

Run Docker-based ClickHouse smoke test:

```bash
make clickhouse-smoke
CLICKHOUSE_SMOKE_HTTP_PORT=18123 make clickhouse-smoke
CLICKHOUSE_USER=default CLICKHOUSE_PASSWORD=kube-insight make clickhouse-smoke
KEEP_CLICKHOUSE=1 make clickhouse-smoke
```

This starts `clickhouse/clickhouse-server:25.3`, initializes the MVP schema,
imports `testdata/fixtures/kube/core.json`, runs a typed service investigation
query, then verifies the `storage.driver: clickhouse` ingest path against a
second ClickHouse database. It removes the container unless `KEEP_CLICKHOUSE=1` is set.
The script configures a local ClickHouse HTTP user/password explicitly so it
does not depend on image defaults.

Run a larger benchmark against the long-lived dev ClickHouse on `8123`:

```bash
make clickhouse-benchmark
CLICKHOUSE_BENCH_CLUSTERS=4 CLICKHOUSE_BENCH_COPIES=50 make clickhouse-benchmark
CLICKHOUSE_BENCH_DATABASE=kube_insight_bench_2 make clickhouse-benchmark
CLICKHOUSE_BENCH_KEEP_DATA=0 make clickhouse-benchmark
```

The benchmark reads `.env`, generates deterministic samples, imports them into
a ClickHouse benchmark database, runs a typed service investigation query, and
prints row counts plus compressed/uncompressed bytes from `system.parts`. By
default it resets databases whose names contain `bench` before importing, then keeps the
finished dataset for inspection. Use `CLICKHOUSE_BENCH_RESET=0` to append to an
existing benchmark database. It redacts the password in terminal output.

Smoke-test the ClickHouse-backed API against live watcher data:

```bash
make clickhouse-api-smoke
CLICKHOUSE_API_SMOKE_API=http://127.0.0.1:8080 make clickhouse-api-smoke
```

The smoke test selects a real Pod and Service from ClickHouse, calls health,
search, history, topology, and service investigation endpoints, and writes
responses under `testdata/generated/clickhouse-api-smoke/` by default. Override
`CLICKHOUSE_API_SMOKE_OUTPUT` only when an external output directory is needed.
It fails on missing API/ClickHouse connectivity, non-2xx responses, or missing
expected top-level response fields.

Profile the live ClickHouse database written by a running watcher:

```bash
make clickhouse-live-profile
CLICKHOUSE_LIVE_DATABASE=kube_insight make clickhouse-live-profile
CLICKHOUSE_LIVE_PROFILE_API=http://127.0.0.1:8080 make clickhouse-live-profile
```

Run a same-dataset storage-mode benchmark across the local backends that are
available in the current environment:

```bash
make storage-mode-benchmark
STORAGE_BENCH_CLUSTERS=2 STORAGE_BENCH_COPIES=20 make storage-mode-benchmark
STORAGE_BENCH_INCLUDE_KUBECTL=1 STORAGE_BENCH_KUBECTL_CONTEXT=<kubectl-context> make storage-mode-benchmark
```

The benchmark generates one deterministic fixture-derived dataset, imports it
into SQLite, ClickHouse when reachable, and chDB when `libchdb.so` is available,
then times health, search, history, topology, and service-investigation queries.
It writes reports under `testdata/generated/storage-mode-benchmark/` by default.
Raw `kubectl` is optional because it is a live-current-state baseline, not a
storage backend for the generated dataset.

Run the live same-target Service comparison against the current dev watcher and
kubeconfig context:

```bash
make live-service-vs-kubectl
LIVE_SERVICE_NAMESPACE=<namespace> LIVE_SERVICE_NAME=<service> make live-service-vs-kubectl
LIVE_SERVICE_KUBECTL_CONTEXT=<kubectl-context> make live-service-vs-kubectl
```

Run the MCP SQL-first smoke against the configured backend:

```bash
make mcp-sql-first-smoke
MCP_SQL_FIRST_SMOKE_CONFIG=config/kube-insight.clickhouse.example.yaml make mcp-sql-first-smoke
```

Smoke-test local GoReleaser snapshot archives after creating `dist/` artifacts:

```bash
make release-artifact-smoke
RELEASE_SMOKE_REQUIRE_CHDB=0 make release-artifact-smoke
```

For the full local dev loop, use `config/kube-insight.clickhouse.example.yaml`
with the workflow in [ClickHouse Local Workflow](clickhouse-local-workflow.md).

The live profile is read-only. It queries active-table storage efficiency
(compression ratio, compressed bytes per row, and proof/derived-table byte
share), broader ClickHouse footprint by active/inactive part state, skip indexes,
watcher coverage, representative object/service targets, direct SQL timings,
optional API timings, and ClickHouse `EXPLAIN` output.
When a previous report directory exists, it also writes trend files comparing
storage efficiency and query timings against the previous run. Reports are
written under `testdata/generated/clickhouse-live-profile/` by default; override
`CLICKHOUSE_LIVE_PROFILE_OUTPUT` only when an external output directory is
needed. Endpoint passwords are redacted in terminal output.

Run open-source readiness checks:

```bash
make open-source-check
```

Scrape metrics:

```bash
curl http://127.0.0.1:9090/metrics
```

When `storage.driver: clickhouse` is selected, `serve --metrics` reads from the
ClickHouse backend instead of the SQLite path and exposes storage efficiency and
footprint gauges such as `kube_insight_storage_compression_ratio`,
`kube_insight_storage_compressed_bytes_per_row`, active/inactive
`kube_insight_storage_bytes` labels, and `kube_insight_storage_parts` labels for
ClickHouse database part state.

More user-facing examples live in `docs/quickstart.md` and
`docs/configuration/configuration.md`.

## chDB Optional Build Check

The chDB-enabled local variant is separate from the default build. Normal
default builds keep SQLite as the local fallback and use the unavailable chDB
placeholder. Compile the optional adapter without running it:

```bash
make chdb-build-check
make build-local-variants
make build-default
make build-chdb
make build-chdb-image
make release-chdb-check
```

`make release-chdb-check` stages Linux and macOS amd64/arm64 chDB runtimes under
`build/chdb-runtime/` and validates the merged GoReleaser config. The default tag
workflow publishes the default pure-Go artifacts plus Linux/macOS amd64/arm64
chDB-enabled archives. Docker publishing uses one multi-architecture GHCR
package: `ghcr.io/nowakeai/kube-insight:<tag>` for the default image and
`ghcr.io/nowakeai/kube-insight:<tag>-chdb` for the chDB image. `make
build-chdb-image` stages `bin/kube-insight-chdb` and the local amd64
`libchdb.so` into `dist/chdb-image/`, then builds a local single-arch
`kube-insight-chdb:local` image with `docker/chdb.Dockerfile`.

`make build-default` is an alias for the normal `make build` path and writes
`bin/kube-insight` without a storage-backend suffix. The default binary keeps
SQLite as its local fallback and does not link chDB. `make build-chdb` writes
`bin/kube-insight-chdb` with `-tags chdb`; this chDB-enabled binary is additive
and still supports the SQLite and ClickHouse drivers. `make build-local-variants`
builds the default and chDB-enabled binaries. `make chdb-build-check`
also compiles the tagged chDB storage and CLI test packages. These build checks
do not prove the runtime library is installed or that local chDB smoke tests
pass. The chDB release path is part of the default tag workflow for Linux and
macOS amd64/arm64; Windows chDB is intentionally excluded until upstream chDB and
`chdb-go` provide a working Windows runtime.

After installing `libchdb.so` or setting `CHDB_LIB_PATH`, run the runtime smoke:

```bash
make chdb-smoke
```

The smoke builds a tagged binary under `/tmp`, ingests fixture data into
`testdata/generated/chdb-smoke/`, runs CLI query commands for schema, SQL,
search, history, topology, object investigation, and service investigation,
starts the API on `127.0.0.1:18080`, and saves matching API health,
search, history, topology, schema, and service-investigation responses next
to the generated chDB path.
It exits early with setup instructions when `libchdb.so` is not discoverable.
The chDB binary expects `libchdb.so` to be discoverable through the system
dynamic linker, `LD_LIBRARY_PATH`, or `CHDB_LIB_PATH` on hosts that use the
chDB runtime package.
