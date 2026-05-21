# Quickstart

This quickstart uses the default kube-insight artifact. The default artifact has
no storage-backend suffix, stays small and pure Go, and uses SQLite for local
single-file runs. A separate chDB-enabled artifact is available for local
ClickHouse-compatible storage when `libchdb.so` is installed. The current central
backend MVP targets ClickHouse for append-only evidence history, JSON search,
storage-efficiency metrics, and low-cost cold object-storage tiering experiments.

## Install

Use a version from the [release page](https://github.com/nowakeai/kube-insight/releases):

```bash
KI_VERSION=0.0.1
KI_OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
KI_ARCH="$(uname -m)"
case "${KI_ARCH}" in
  x86_64) KI_ARCH=amd64 ;;
  aarch64) KI_ARCH=arm64 ;;
esac

curl -L -o kube-insight.tar.gz \
  "https://github.com/nowakeai/kube-insight/releases/download/v${KI_VERSION}/kube-insight_${KI_VERSION}_${KI_OS}_${KI_ARCH}.tar.gz"
tar -xzf kube-insight.tar.gz kube-insight
chmod +x kube-insight
```

Windows users can download the `.zip` artifact from the
[release page](https://github.com/nowakeai/kube-insight/releases).

## Local Storage Variants

For performance numbers and backend tradeoffs, see
[Storage Modes And Performance](validation/storage-mode-comparison.md).

Use the default `kube-insight` binary for the smallest local install:

```bash
./kube-insight watch --db kubeinsight.db
```

Use the chDB-enabled variant when you want the embedded local store to share the
ClickHouse table/query contract. Local development builds write
`bin/kube-insight-chdb`; release chDB archives are named with `_chdb_` and still
contain a binary named `kube-insight`:

```bash
# Source checkout local build
./bin/kube-insight-chdb --config config/kube-insight.chdb.example.yaml \
  watch pods services --timeout 30s

# Release chDB archive, after extracting kube-insight and libchdb.so
CHDB_LIB_PATH=./libchdb.so ./kube-insight \
  --config config/kube-insight.chdb.example.yaml \
  watch pods services --timeout 30s
```

The chDB-enabled binary still supports SQLite and ClickHouse. It additionally
requires a compatible `libchdb.so` discoverable through the system dynamic
linker, `LD_LIBRARY_PATH`, or `CHDB_LIB_PATH`. The default binary does not link
chDB; selecting `storage.driver: chdb` with it fails with an explicit setup
error.

## ClickHouse Service Backend

Use ClickHouse when kube-insight should keep continuous central evidence history
for a team, API service, or MCP service. Start from the example config and pass
the HTTP DSN through the configured environment variable:

```bash
export KUBE_INSIGHT_CLICKHOUSE_DSN='http://127.0.0.1:8123/?user=kube_insight&password=...'
./kube-insight --config config/kube-insight.clickhouse.example.yaml serve \
  --watch pods services endpointslices.discovery.k8s.io \
  --api \
  --mcp \
  --metrics
```

For source checkouts, the local Docker Compose workflow starts ClickHouse and a
dev watcher environment:

```bash
make dev-compose-up-detached
make dev-compose-ps
make clickhouse-live-profile
```

Cold object-storage movement is opt-in. The default example config does not move
data to S3 or another object store unless a matching ClickHouse storage policy is
configured explicitly.

## Watch Current Cluster

Watch all list/watch-capable resources in the current kubeconfig context:

```bash
./kube-insight watch --db kubeinsight.db
```

Watch a smaller set while testing:

```bash
./kube-insight watch pods services events.events.k8s.io \
  --db kubeinsight.db \
  --timeout 30s
```

Check collector coverage:

```bash
./kube-insight db resources health --db kubeinsight.db --stale-after 10m
./kube-insight db resources health --db kubeinsight.db --errors-only
```

## Query From CLI

Start by inspecting schema:

```bash
./kube-insight query schema --db kubeinsight.db
```

Run read-only SQL:

```bash
./kube-insight query sql --db kubeinsight.db --max-rows 20 --sql \
  "select kind, name from object_kinds order by kind limit 20"
```

Search indexed evidence:

```bash
./kube-insight query search webhook --db kubeinsight.db --limit 10
```

Add full evidence only when needed:

```bash
./kube-insight query search webhook --db kubeinsight.db \
  --limit 3 \
  --include-bundles \
  --max-versions-per-object 2
```

Inspect one object's retained content versions and observation trail:

```bash
./kube-insight query history --db kubeinsight.db \
  --kind ClusterRepo \
  --name rancher-charts \
  --max-versions 5 \
  --max-observations 20
```

`versions` are retained content changes. `observations` are list/watch sightings;
unchanged observations keep the time/resourceVersion without duplicating JSON,
facts, edges, or changes.

## Compact SQLite Storage

Long-running `watch` and `serve --watch` processes run lightweight SQLite
maintenance automatically. The periodic task checkpoints/truncates WAL and runs
incremental vacuum when possible, so normal watch operation should not require
frequent full compaction.

After stopping a watcher, compact the local SQLite store:

```bash
./kube-insight db compact --db kubeinsight.db
```

If the database has `object_observations` backfilled, prune duplicate unchanged
content versions while keeping every observation timestamp:

```bash
./kube-insight db compact --db kubeinsight.db --prune-unchanged
```

## Service Mode

For a local all-in-one service process, run watcher plus read surfaces together:

```bash
./kube-insight serve --watch --api --mcp --db kubeinsight.db
```

The combined command supports these components:

- `--watch`: discovery, list/watch, extraction, and writes.
- `--api`: read-only HTTP API.
- `--mcp`: HTTP MCP endpoint at `/mcp`.
- `--webui`: embedded Web UI listener for the React app built from `web/`.
  The first formal UI milestone is the agent-first chat surface described in
  [Agent-First Web UI Design](product/agent-first-web-ui.md).

Example with all current and planned service surfaces:

```bash
./kube-insight serve --watch --api --mcp --webui \
  --db kubeinsight.db \
  --api-listen 127.0.0.1:8080 \
  --mcp-listen 127.0.0.1:8090 \
  --webui-listen 127.0.0.1:8081
```

`serve --mcp` is HTTP for service deployments. Use `serve mcp` when an agent
expects stdio MCP.

## Serve API

```bash
./kube-insight serve api --db kubeinsight.db --listen 127.0.0.1:8080
```

Smoke test:

```bash
curl http://127.0.0.1:8080/healthz
curl http://127.0.0.1:8080/api/v1/schema
curl -X POST http://127.0.0.1:8080/api/v1/sql \
  -H 'content-type: application/json' \
  -d '{"sql":"select name from latest_index limit 5","maxRows":5}'
curl 'http://127.0.0.1:8080/api/v1/health?errorsOnly=true&limit=20'
curl 'http://127.0.0.1:8080/api/v1/history?kind=ClusterRepo&name=rancher-charts&maxVersions=5&maxObservations=20'
```

## Serve MCP

```bash
./kube-insight serve mcp --db kubeinsight.db

# ClickHouse-backed stdio MCP
KUBE_INSIGHT_CLICKHOUSE_DSN=http://127.0.0.1:8123/?user=kube_insight \
  ./kube-insight --config config/kube-insight.clickhouse.example.yaml serve mcp
```

MCP follows the configured `storage.driver`: SQLite by default, ClickHouse when
started with `storage.driver: clickhouse`, and chDB in the chDB-enabled build.
Agents should call `kube_insight_schema` first because SQLite and
ClickHouse-compatible backends expose different SQL table names.

MCP stdio currently exposes:

- `kube_insight_schema`
- `kube_insight_sql`
- `kube_insight_health`
- `kube_insight_history`

It also exposes prompts for common agent workflows:

- `kube_insight_coverage_first`
- `kube_insight_event_history`
- `kube_insight_object_history`

## Validate A Checkout

For source checkouts, run the same quick validation used during development:

```bash
make test
make build
git diff --check
```

`make validate` runs the generated PoC fixture validation and writes reports
under `testdata/generated/`; use it when changing ingestion, extraction, storage,
query, API, or MCP behavior. ClickHouse and chDB validation commands are listed
in [Development Commands](dev/commands.md).
