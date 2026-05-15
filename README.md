# kube-insight

`kube-insight` records Kubernetes history and turns retained cluster state into
queryable troubleshooting evidence for humans and agents.

Kubernetes is excellent at showing the current state of a cluster. Incident
investigation often needs a different view: what changed, which objects were
related, and what evidence still exists after Events expired or live state moved
on. kube-insight keeps sanitized resource versions, extracts compact facts and
edges, and exposes read-only query surfaces over local SQLite.

## Why kube-insight

- Historical evidence: retained versions and observation timestamps show what
  the cluster looked like when a problem happened.
- Agent-ready data: facts, edges, status changes, and SQL recipes let agents
  inspect evidence without repeatedly listing and joining Kubernetes resources.
- Proof-preserving storage: compact facts point to retained JSON versions, so
  summaries can be checked against source documents.
- Local-first PoC: run against a kubeconfig context, store data in SQLite, and
  expose CLI, HTTP API, and MCP surfaces from one binary.
- Privacy-aware ingestion: default processing removes noisy or sensitive fields
  before retained hashing and storage.

## Quickstart

```bash
make build
./bin/kube-insight watch --db kubeinsight.db
```

Check collector coverage:

```bash
./bin/kube-insight db resources health --db kubeinsight.db --stale-after 10m
```

Run a read-only SQL investigation:

```bash
./bin/kube-insight query sql --db kubeinsight.db --max-rows 20 --sql \
  "select id, name, source from clusters order by id"
```

Serve local API and MCP endpoints:

```bash
./bin/kube-insight serve --watch --api --mcp --db kubeinsight.db
```

See [docs/quickstart.md](docs/quickstart.md) for the full local workflow.

## Benchmark Snapshot

The validation case in
[docs/validation/insight-vs-kubectl-benchmark.md](docs/validation/insight-vs-kubectl-benchmark.md)
compares current `kubectl` Event queries with retained kube-insight evidence on
a sanitized GKE workload cluster.

Observed point-in-time results:

| Query | Result |
| --- | ---: |
| kubectl current Warning Events | 1,850 |
| kubectl current PolicyViolation Events | 1,776 |
| kube-insight retained PolicyViolation Events | 22,470 |
| insight retained PolicyViolation count | 204 ms |
| insight Event-to-resource edge sample | 28 ms |
| kubectl current Warning Event count | 3,176 ms |

The product claim is not a universal speedup. The useful difference is that
kube-insight can answer historical and cross-resource questions from retained
evidence, while kubectl answers current apiserver state.

## Agent Workflows

kube-insight exposes MCP tools for schema discovery, read-only SQL, collector
health, and object history:

- `kube_insight_schema`
- `kube_insight_sql`
- `kube_insight_health`
- `kube_insight_history`

Start with collector health, list clusters, keep `cluster_id` in follow-up SQL,
then use facts and edges to find candidates before requesting retained versions
as proof. The detailed agent guide is in
[docs/workflows/agent-sql-cookbook.md](docs/workflows/agent-sql-cookbook.md).

## Documentation

- [Documentation index](docs/README.md)
- [Quickstart](docs/quickstart.md)
- [Configuration](docs/configuration/configuration.md)
- [Data model](docs/data/data-model.md)
- [Agent SQL cookbook](docs/workflows/agent-sql-cookbook.md)
- [Development commands](docs/dev/commands.md)

## Development

```bash
make test
make build
make validate
```

The repo keeps Go files under 800 lines; `make test` enforces that rule before
running `go test ./...`.
