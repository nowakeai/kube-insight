# Quickstart

This quickstart runs kube-insight as a local PoC with SQLite.

## Build

```bash
make build
```

## Watch Current Cluster

Watch all list/watch-capable resources in the current kubeconfig context:

```bash
./bin/kube-insight watch --db kubeinsight.db
```

Watch a smaller set while testing:

```bash
./bin/kube-insight watch pods services events.events.k8s.io \
  --db kubeinsight.db \
  --timeout 30s
```

Check collector coverage:

```bash
./bin/kube-insight db resources health --db kubeinsight.db --stale-after 10m
./bin/kube-insight db resources health --db kubeinsight.db --errors-only
```

## Query From CLI

Start by inspecting schema:

```bash
./bin/kube-insight query schema --db kubeinsight.db
```

Run read-only SQL:

```bash
./bin/kube-insight query sql --db kubeinsight.db --max-rows 20 --sql \
  "select kind, name from object_kinds order by kind limit 20"
```

Search indexed evidence:

```bash
./bin/kube-insight query search webhook --db kubeinsight.db --limit 10
```

Add full evidence only when needed:

```bash
./bin/kube-insight query search webhook --db kubeinsight.db \
  --limit 3 \
  --include-bundles \
  --max-versions-per-object 2
```

Inspect one object's retained content versions and observation trail:

```bash
./bin/kube-insight query history --db kubeinsight.db \
  --kind ClusterRepo \
  --name rancher-charts \
  --max-versions 5 \
  --max-observations 20
```

`versions` are retained content changes. `observations` are list/watch sightings;
unchanged observations keep the time/resourceVersion without duplicating JSON,
facts, edges, or changes.

## Compact Storage

Long-running `watch` and `serve --watch` processes run lightweight SQLite
maintenance automatically. The periodic task checkpoints/truncates WAL and runs
incremental vacuum when possible, so normal watch operation should not require
frequent full compaction.

After stopping a watcher, compact the local SQLite store:

```bash
./bin/kube-insight db compact --db kubeinsight.db
```

If the database has `object_observations` backfilled, prune duplicate unchanged
content versions while keeping every observation timestamp:

```bash
./bin/kube-insight db compact --db kubeinsight.db --prune-unchanged
```

## Service Mode

For a local all-in-one service process, run watcher plus read surfaces together:

```bash
./bin/kube-insight serve --watch --api --mcp --db kubeinsight.db
```

The combined command supports these components:

- `--watch`: discovery, list/watch, extraction, and writes.
- `--api`: read-only HTTP API.
- `--mcp`: HTTP MCP endpoint at `/mcp`.
- `--webui`: web UI listener. The PoC exposes only a placeholder until the UI
  is implemented.

Example with all current and planned service surfaces:

```bash
./bin/kube-insight serve --watch --api --mcp --webui \
  --db kubeinsight.db \
  --api-listen 127.0.0.1:8080 \
  --mcp-listen 127.0.0.1:8090 \
  --webui-listen 127.0.0.1:8081
```

`serve --mcp` is HTTP for service deployments. Use `serve mcp` when an agent
expects stdio MCP.

## Serve API

```bash
./bin/kube-insight serve api --db kubeinsight.db --listen 127.0.0.1:8080
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
./bin/kube-insight serve mcp --db kubeinsight.db
```

MCP stdio currently exposes:

- `kube_insight_schema`
- `kube_insight_sql`
- `kube_insight_health`
- `kube_insight_history`

## Validate PoC

```bash
make check-lines
go test ./...
make validate
```

Expected PoC validation output should include:

```text
PASS checks=22/22
```
