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
./bin/kube-insight config validate --config config/kube-insight.example.yaml
```

Watch current context:

```bash
./bin/kube-insight watch --db kubeinsight.db
```

Watch a selected resource set:

```bash
./bin/kube-insight watch pods services 'apps/v1/*' --db kubeinsight.db
```

Run local service surfaces:

```bash
./bin/kube-insight serve --watch --api --mcp --metrics --db kubeinsight.db
```

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

Rebuild derived facts, edges, and changes after extractor/profile changes:

```bash
./bin/kube-insight db reindex --db kubeinsight.db
./bin/kube-insight db reindex --db kubeinsight.db --yes
```

Run the local insight vs kubectl benchmark:

```bash
./scripts/benchmark-insight-vs-kubectl.sh kubeinsight.db <kubectl-context>
```

Run open-source readiness checks:

```bash
make open-source-check
```

Scrape metrics:

```bash
curl http://127.0.0.1:9090/metrics
```

More user-facing examples live in `docs/quickstart.md` and
`docs/configuration/configuration.md`.
