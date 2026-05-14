# Configuration

The default configuration file is:

```bash
config/kube-insight.example.yaml
```

Validate it with:

```bash
kube-insight config validate --file config/kube-insight.example.yaml
```

Runtime configuration is resolved in this order:

1. YAML configuration file.
2. Environment variables.
3. Command-line flags.

Environment variables use the `KUBE_INSIGHT_` prefix plus the YAML path in
upper snake case:

```bash
KUBE_INSIGHT_INSTANCE_ROLE=writer
KUBE_INSIGHT_LOGGING_LEVEL=info
KUBE_INSIGHT_LOGGING_FORMAT=text
KUBE_INSIGHT_STORAGE_SQLITE_PATH=./kubeinsight.db
KUBE_INSIGHT_COLLECTION_KUBECONFIG=$HOME/.kube/config
KUBE_INSIGHT_COLLECTION_CONTEXTS=staging,prod
KUBE_INSIGHT_COLLECTION_NAMESPACE=payments
KUBE_INSIGHT_COLLECTION_CONCURRENCY=8
```

Lists of strings use comma-separated values. Structured lists such as
`filters` and `extractors` can be replaced with inline YAML:

```bash
KUBE_INSIGHT_FILTERS='[{name: managed_fields, action: keep_modified, removePaths: [/metadata/managedFields]}]'
```

Equivalent flag overrides:

```bash
kube-insight --config kube-insight.yaml \
  --role writer \
  --kubeconfig "$HOME/.kube/config" \
  --context staging \
  --namespace payments \
  --log-level debug \
  --log-format json \
  --db ./kubeinsight.db \
  collect ingest --discover-resources
```

## Logging

Logs are written to stderr so stdout can stay machine-readable for JSON command
results. The CLI keeps the standard `slog` call surface, but uses
`charm.land/log/v2` underneath for more readable terminal output. Supported
formats are `text`, `json`, and `logfmt`:

```yaml
logging:
  level: info
  format: text
```

Equivalent overrides:

```bash
kube-insight --log-level debug --log-format json watch pods
kube-insight --log-format logfmt serve --watch --api --mcp
```

Default `info` logs are tuned for long-running service mode: lifecycle,
batch ingest summaries, watch start/finish, and warnings stay visible; high
volume per-object watch events, bookmarks, individual resource list details,
and single-object ingest summaries move to `debug`. Recoverable watch stream
disconnects are reported as reconnects instead of warnings; health status keeps
them visible as `retrying` until the stream resumes.

## Instance Roles

Kube-insight should support multiple application instances against the same
backend. Exactly one production instance should normally own discovery/watch/
ingest writes, while other instances only serve API/Web/MCP reads.

```yaml
instance:
  role: writer

collection:
  enabled: true

server:
  api:
    enabled: false
  web:
    enabled: false
mcp:
  enabled: false
```

Reader/API instance:

```yaml
instance:
  role: api

collection:
  enabled: false

server:
  api:
    enabled: true
```

Supported roles:

- `all`: local single-process mode; may collect/watch and serve APIs.
- `writer`: discovery/watch/ingest only; API/Web/MCP listeners disabled.
- `api`: query/API/Web/MCP only; collection/watch disabled.

## Supported Running Modes

Kube-insight supports these operational shapes:

| Mode | Command | Writes | Read surfaces | Intended use |
| --- | --- | --- | --- | --- |
| One-shot ingest | `kube-insight ingest --file/--dir ...` | yes | no | Offline samples, CI, fixture import |
| Watcher only | `kube-insight watch [RESOURCE_PATTERN ...]` | yes | no | Dedicated collector process |
| API only | `kube-insight serve --api` or `serve api` | no | HTTP API | Read-only query service |
| MCP stdio | `kube-insight serve mcp` | no | stdio MCP | Local agent process launch |
| MCP HTTP | `kube-insight serve --mcp` | no | HTTP `/mcp` | Long-running service deployment |
| Web UI | `kube-insight serve --webui` | no | HTTP Web UI | Future human UI surface |
| All-in-one local | `kube-insight serve --watch --api --mcp --webui` | yes | HTTP API, HTTP MCP, Web UI | Local PoC or small single-instance deployment |
| Split production | one `--watch` writer plus N `--api/--mcp/--webui` readers | writer only | readers only | HA/scale-out with one writer owner |

In production, prefer one writer instance and multiple read-only instances
against the same backend. This avoids duplicate watch streams and duplicate
history writes while still allowing API/MCP/WebUI scale-out.

Compact service examples:

```bash
kube-insight serve --watch --api --mcp --db kubeinsight.db
kube-insight serve --watch --api --mcp --webui --db kubeinsight.db
kube-insight serve --watch pods events.events.k8s.io --api --db kubeinsight.db
```

`watch` and `serve --watch` start lightweight SQLite maintenance by default.
This periodic task checkpoints/truncates WAL, runs `pragma optimize`, and runs
incremental vacuum when possible. It does not run full `VACUUM`; use
`kube-insight db compact` for offline compaction after large migrations,
retention purges, or duplicate-version pruning.

Service listen flags:

```bash
kube-insight serve --api --mcp --webui \
  --api-listen 0.0.0.0:8080 \
  --mcp-listen 0.0.0.0:8090 \
  --webui-listen 0.0.0.0:8081
```

With no `serve` component flags, `kube-insight serve --config <file>` uses the
enabled components in the configuration file.

## Watch All Resources

For local PoC and broad cluster capture, use discovery-backed collection:

```yaml
collection:
  enabled: true
  kubeconfig: ""
  useClientGo: true
  contexts: []
  allContexts: false
  namespace: ""
  resources:
    all: true
    include: []
    exclude:
      - leases.coordination.k8s.io
```

Equivalent CLI paths:

```bash
kube-insight dev collect ingest --client-go --discover-resources --db kubeinsight.db
kube-insight watch
kube-insight watch pods services
kube-insight watch 'v1/*' 'apps/v1/*'
```

Discovery writes `api_resources`, and later ingestion uses that table as the
authoritative GVR, scope, and kind mapping.

Without `--db`, watch and storage management commands use `./kubeinsight.db`.
The watcher resolves a stable cluster ID from the Kubernetes cluster identity
and stores the kubeconfig context as metadata, so context renames do not create
a new cluster record.

Stored clusters can be inspected and removed with:

```bash
kube-insight db clusters
kube-insight db clusters delete <cluster-id> --yes
```

## Filters

Filters are ordered. A filter can be disabled, can target resources/kinds, and
can remove JSON pointer paths.

```yaml
filters:
  - name: secret_metadata_only
    enabled: true
    action: keep_modified
    resources: [secrets]
    removePaths: [/data, /stringData]
    keepSecretKeys: true
```

Supported first-version actions:

- `keep`
- `keep_modified`
- `discard_change`
- `discard_resource`

Future plugin path:

```yaml
filters:
  - name: custom_noise_filter
    enabled: true
    action: keep_modified
    script: ./plugins/filters/noise.js
```

The Goja plugin contract should receive an observation and return the same
auditable decision shape as built-in filters.

## Extractors

Extractors can be enabled or disabled and can target resources/kinds.

```yaml
extractors:
  - name: pod
    enabled: true
    resources: [pods]
```

Future plugin path:

```yaml
extractors:
  - name: custom_rollout_facts
    enabled: true
    resources: [deployments.apps]
    script: ./plugins/extractors/rollout.js
```

Extractor plugins should emit the same logical outputs as built-ins: facts,
edges, changes, and optional summaries. Versions remain proof.

## Storage

SQLite is the local PoC default:

```yaml
storage:
  driver: sqlite
  sqlite:
    path: kubeinsight.db
```

Postgres and CockroachDB are planned as shared central backends. Application
role separation is controlled by `instance.role`, not by separate read/write
DSNs:

```yaml
storage:
  driver: postgres
  postgres:
    dsnEnv: KUBE_INSIGHT_POSTGRES_DSN
```
