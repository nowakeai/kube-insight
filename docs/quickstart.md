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
```

## Serve MCP

```bash
./bin/kube-insight serve mcp --db kubeinsight.db
```

MCP stdio currently exposes:

- `kube_insight_schema`
- `kube_insight_sql`
- `kube_insight_health`

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
