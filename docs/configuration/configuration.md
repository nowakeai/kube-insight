# Configuration

The default configuration file is:

```bash
config/kube-insight.example.yaml
```

The normalized processing model is documented in
[`processing-model.md`](./processing-model.md). In short,
`resourceProfiles.rules` is the single resource classification point; filter
chains, extractor sets, and retention policies are reusable named components
selected by the profile.

Validate it with:

```bash
kube-insight config validate
kube-insight config validate --file config/kube-insight.example.yaml
kube-insight config validate --output json
```

Print the final effective config after embedded defaults, file overlay,
environment variables, and CLI flags:

```bash
kube-insight --config kube-insight.yaml --log-level debug config show
kube-insight --config kube-insight.yaml config show --output json
```

`config show` expands built-in resource profile rules into
`resourceProfiles.rules` and sets `resourceProfiles.replaceDefaults: true` in
the printed output. This makes the output self-contained: humans and agents can
see the exact classification order, and the output can be saved as a fully
materialized config if a deployment wants to own every profile rule explicitly.

List the built-in processing catalog:

```bash
kube-insight config catalog
kube-insight config catalog --output json
```

This is the supported-discovery path for agents that need to generate or review
`processing.filters`, `processing.filterChains`, `processing.extractors`, and
`processing.extractorSets` without reading source code.

Runtime configuration is resolved in this order:

1. Embedded default config.
2. YAML configuration file overlay.
3. Environment variables.
4. Command-line flags.

YAML overlay rules:

- Mapping/object fields are deep-merged.
- Scalar fields replace the default value.
- List fields replace the whole default list. They are not appended.
- Empty lists explicitly clear the default list.
- Empty config files load the embedded default config.
- Unknown YAML fields are rejected so typos fail during `config validate`.

For example, this keeps all default storage, processing, and server settings,
but replaces the default resource exclude list with only `pods`:

```yaml
collection:
  resources:
    exclude: [pods]
```

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

Environment list overrides also replace the whole list. Lists of strings use
comma-separated values. Structured maps such as `processing.filters` can be
replaced with inline YAML:

```bash
KUBE_INSIGHT_PROCESSING_FILTERS='{managed_fields: {type: builtin, action: keep_modified, removePaths: [/metadata/managedFields]}}'
```

## Design References

The config model intentionally follows the operational shape of Prometheus and
Loki/Promtail-style tools:

- A YAML file is the primary runtime configuration surface.
- CLI flags are for selecting files, startup role, listen addresses, and
  targeted overrides.
- Defaults are explicit and inspectable through `config validate` and
  `config show`.
- Invalid config should fail before runtime behavior changes are applied.

Prometheus separates immutable command-line flags from reloadable YAML and only
applies a reload when the new config is well formed. Loki documents the same
YAML-first approach and has a `-print-config-stderr` workflow that prints the
fully materialized config after built-in defaults, file values, and flags.
Promtail is now EOL, so it is useful as historical reference only; current
Grafana-family behavior should be checked against Loki/Alloy. The model
kube-insight should keep moving toward is: make the effective config easy for
humans and agents to inspect, but keep merging rules simple enough that
list-heavy sections do not surprise users.

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
them visible as `retrying` and counts them as unstable until the stream resumes.

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
| Metrics only | `kube-insight serve --metrics` | no | Prometheus `/metrics` | Scrape storage, filter, and watch health metrics |
| MCP stdio | `kube-insight serve mcp` | no | stdio MCP | Local agent process launch |
| MCP HTTP | `kube-insight serve --mcp` | no | HTTP `/mcp` | Long-running service deployment |
| Web UI | `kube-insight serve --webui` | no | HTTP Web UI | Future human UI surface |
| All-in-one local | `kube-insight serve --watch --api --mcp --metrics --webui` | yes | HTTP API, HTTP MCP, Metrics, Web UI | Local PoC or small single-instance deployment |
| Split production | one `--watch` writer plus N `--api/--mcp/--metrics/--webui` readers | writer only | readers only | HA/scale-out with one writer owner |

In production, prefer one writer instance and multiple read-only instances
against the same backend. This avoids duplicate watch streams and duplicate
history writes while still allowing API/MCP/WebUI scale-out.

Compact service examples:

```bash
kube-insight serve --watch --api --mcp --db kubeinsight.db
kube-insight serve --watch --api --mcp --metrics --db kubeinsight.db
kube-insight serve --watch --api --mcp --webui --db kubeinsight.db
kube-insight serve --watch pods events.events.k8s.io --api --db kubeinsight.db
```

`watch` and `serve --watch` start lightweight SQLite maintenance by default.
This periodic task checkpoints/truncates WAL, runs `pragma optimize`, and runs
incremental vacuum when possible. It does not run full `VACUUM`; use
`kube-insight db compact` for offline compaction after large migrations,
retention purges, or duplicate-version pruning.

When processing filters change, existing retained history can be reprocessed
with the current effective configuration:

```bash
kube-insight db backfill --db kubeinsight.db          # dry run
kube-insight db backfill --db kubeinsight.db --yes    # apply
```

Backfill only rewrites retained history (`versions`, `blobs`, facts, edges,
changes, and observation version pointers). It does not rewrite
`latest_raw_index`; that table reflects future latest observations from the
watcher. Existing databases can only be backfilled from the JSON that was
already retained.

Retention is optional and disabled by default. Enable it when a deployment wants
bounded local storage instead of full historical proof:

```yaml
storage:
  retention:
    enabled: true
    maxAgeSeconds: 2592000 # 30 days
    minVersionsPerObject: 2
    filterDecisionMaxAgeSeconds: 604800 # 7 days
    policies:
      standard:
        minVersionsPerObject: 2
      events_short_window:
        maxAgeSeconds: 604800 # 7 days
        minVersionsPerObject: 1
      crd_long_window:
        maxAgeSeconds: 7776000 # 90 days
        minVersionsPerObject: 2
```

Retention always keeps each object's latest version and at least
`minVersionsPerObject` retained versions. It deletes expired older versions,
their version-scoped facts/edges/changes, unreferenced blobs, and expired filter
decision audit rows. Large purges create free pages; run `kube-insight db compact`
after a manual purge when immediate disk reclamation matters.
Policies are selected by `resourceProfiles.rules[].retentionPolicy`; resource
classification stays in one place.

Manual retention can be run explicitly:

```bash
kube-insight db retention --max-age 720h --min-versions-per-object 2 --yes
kube-insight db retention --profile event_rollup --max-age 168h --yes
```

Service listen flags:

```bash
kube-insight serve --api --mcp --webui \
  --api-listen 0.0.0.0:8080 \
  --mcp-listen 0.0.0.0:8090 \
  --metrics-listen 0.0.0.0:9090 \
  --webui-listen 0.0.0.0:8081
```

Metrics are exposed in Prometheus text format at `/metrics`. The metrics server
uses the official Prometheus Go client and currently exports storage row counts,
storage byte counts, processing profile counts, filter decision results,
redaction/removal counters, secret safety counters, ingestion offset lag, and
resource stream health.

For production watchers, prefer the cloud/provider direct Kubernetes endpoint
over UI/proxy paths such as Rancher when available. Proxy paths can be fine for
human `kubectl` usage while still being less stable for many long-lived WATCH
streams.

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
  watch:
    disableHttp2: false
    maxConcurrentStreams: 64
    minBackoffMillis: 500
    maxBackoffMillis: 30000
    streamStartStaggerMillis: 200
  resources:
    all: true
    include: []
    exclude:
      # Prefer events.events.k8s.io as the canonical Event source; core v1
      # events usually duplicates the same evidence.
      - events
      - leases.coordination.k8s.io
      - "*policyreports.wgpolicyk8s.io"
      - "*ephemeralreports.reports.kyverno.io"
```

Watch tuning defaults are conservative for clusters with many aggregated APIs
or CRDs:

- `disableHttp2: false` keeps Kubernetes' normal HTTP/2 behavior. Set it to
  `true` only for a cluster or proxy proven to behave better with HTTP/1.1.
- `maxConcurrentStreams` limits active long-running WATCH streams. Initial LIST
  coverage still runs for every selected resource; lower-priority watch streams
  wait instead of overloading the API front door.
- `streamStartStaggerMillis` spreads initial watch stream creation so reconnects
  and bookmarks do not arrive as one burst.
- `minBackoffMillis` and `maxBackoffMillis` control exponential reconnect
  backoff with jitter.

The default excludes keep the high-value evidence path small: canonical Events
come from `events.events.k8s.io`, Leases are dropped, and derived policy/report
resources are skipped unless explicitly included for policy-engine debugging.
Skipped resources are hidden from `db resources health` by default and can be
shown with `--include-skipped`.

Equivalent CLI paths:

```bash
kube-insight dev collect ingest --client-go --discover-resources --db kubeinsight.db
kube-insight watch
kube-insight watch pods services
kube-insight watch 'v1/*' 'apps/v1/*'
kube-insight watch '*.cert-manager.io' 'gateway.networking.k8s.io/*/*'
```

Every config field named `resources` uses the same resource-pattern forms:

- bare resource: `pods`
- qualified resource: `deployments.apps`
- GVR: `apps/v1/deployments`
- glob over any of those forms: `*.reports.kyverno.io`, `apps/v1/*`,
  `cert-manager.io/*/*`

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

## Resource Profiles

Resource profiles classify resources into processing lanes. They decide the
retention policy, filter chain, extractor set, compaction strategy, priority,
stream queue order, and whether a low-value resource is disabled by default.

Custom profile rules are matched before built-in defaults:

```yaml
resourceProfiles:
  defaults:
    enabled: true
    retentionPolicy: standard
    filterChain: default
    extractorSet: generic
    compactionStrategy: full_json
    priority: normal
    maxEventBuffer: 256
  replaceDefaults: false
  rules:
    - name: custom_gateway
      resources: [gateways.gateway.networking.k8s.io]
      retentionPolicy: standard
      extractorSet: service_topology
      priority: high
```

Rule matching supports:

- `resources`: bare resource (`pods`), qualified resource
  (`events.events.k8s.io`), GVR (`events.k8s.io/v1/events`), or glob
  (`*.cert-manager.io`, `gateway.networking.k8s.io/*/*`).
- `groups`: API groups such as `apps` or `cert-manager.io`.
- `kinds`: Kubernetes kinds such as `Pod` or `Certificate`.

When multiple matchers are set on one rule they are combined with AND. Values
inside the same matcher list are OR. Set `enabled: false` to keep a resource
discoverable in `resource_processing_profiles` while making watch scheduling
treat it as disabled/low value. Set `replaceDefaults: true` only when the config
fully defines every resource profile rule the deployment needs.

At ingest time, the selected resource profile chooses the active `filterChain`
and `extractorSet`. `filterChain: default` runs the configured default filter
chain; `filterChain: none` skips filters for that profile. `extractorSet: none`
skips evidence extraction, and named sets run the configured extractor
components when their guard also matches.

## Processing

Processing contains reusable component libraries. It should not duplicate
resource classification; use `guard` only as a safety boundary for components
that must not run on the wrong resource.

```yaml
processing:
  filterChains:
    default: [managed_fields, resource_version, metadata_generation, status_condition_set]
    secret_metadata_only: [managed_fields, resource_version, secret_metadata_only]
    none: []
  filters:
    secret_metadata_only:
      type: builtin
      enabled: true
      action: keep_modified
      guard:
        resources: [secrets]
      removePaths: [/data, /stringData]
      keepSecretKeys: true
```

Component `guard` blocks support `resources`, `kinds`, `namespaces`, and
`names`. Resource and kind guards protect by type; namespace and name guards
protect object-specific normalizers:

```yaml
guard:
  resources: [configmaps]
  namespaces: [kube-system]
  names: [cluster-autoscaler-status]
```

The current runtime supports these built-in filters:

| Filter | Required action | Notes |
| --- | --- | --- |
| `managed_fields` | `keep_modified` | Removes `/metadata/managedFields` when `removePaths` is set. |
| `resource_version` | `keep_modified` | Removes `/metadata/resourceVersion` from retained document hashes. |
| `metadata_generation` | `keep_modified` | Removes derived `/metadata/generation`; spec/status content still carries the actual change. |
| `status_condition_set` | `keep_modified` | Removes condition timestamp fields and sorts conditions by `type` so order-only churn does not create retained versions. |
| `status_condition_timestamps` | `keep_modified` | Removes condition timestamp churn while preserving condition status/reason/message. |
| `leader_election_configmap` | `keep_modified` | Removes legacy leader-election annotation churn from ConfigMaps. |
| `cluster_autoscaler_status` | `keep_modified` | Normalizes `kube-system/cluster-autoscaler-status` probe timestamps and node group order. |
| `gke_webhook_heartbeat` | `keep_modified` | Normalizes `kube-system/gke-common-webhook-heartbeat` timestamp-keyed heartbeat entries into host/version evidence. |
| `event_series` | `keep_modified` | Removes repeated Event count/last-seen timestamp churn while preserving the event proof. |
| `secret_metadata_only` | `keep_modified` | Removes Secret payload values while preserving metadata and keys. |
| `lease_skip` | `discard_resource` | Drops Lease resources from retained history. |
| `report_skip` | `discard_resource` | Drops low-value derived report resources. |

For built-ins, `enabled`, chain order, `guard`, `action`, and `removePaths` are
applied on the write path before retained document hashing.
`action` must match the built-in action; `config validate` fails fast when a
built-in name or action is wrong. Use `enabled: false` to disable a built-in
filter.

Supported first-version actions:

- `keep`
- `keep_modified`
- `discard_change`
- `discard_resource`

Future plugin path:

```yaml
processing:
  filters:
    custom_noise_filter:
      type: goja
      enabled: true
      action: keep_modified
      script: ./plugins/filters/noise.js
```

The Goja plugin contract should receive an observation and return the same
auditable decision shape as built-in filters.

Extractors can be enabled or disabled and can set a `guard` over
resources/kinds. Their resource scopes use the same resource-pattern forms as
collection and resource profiles. The selected resource profile's
`extractorSet` decides which configured extractor set runs for a resource.

The current runtime supports these built-in extractors:

- `reference`
- `pod`
- `node`
- `event`
- `endpointslice`

```yaml
processing:
  extractorSets:
    generic: [reference]
    pod: [reference, pod]
    none: []
  extractors:
    pod:
      type: builtin
      enabled: true
      guard:
        resources: [pods]
```

Future plugin path:

```yaml
processing:
  extractors:
    custom_rollout_facts:
      type: goja
      enabled: true
      guard:
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
