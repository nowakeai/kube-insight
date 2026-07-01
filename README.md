

<p align="center">
  <img src="assets/brand/kube-insight-logo.svg" alt="kube-insight" width="680">
</p>

<p align="center">
  <a href="https://github.com/nowakeai/kube-insight/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/nowakeai/kube-insight/actions/workflows/ci.yml/badge.svg"></a>
  <a href="LICENSE"><img alt="License" src="https://img.shields.io/badge/license-Apache--2.0-blue"></a>
</p>

<p align="center">
  <strong>The missing history layer for Kubernetes AIOps.</strong><br>
  kube-insight captures sanitized list/watch history, extracts facts and topology,
  and exposes read-only SQL, API, and MCP tools so humans and agents can
  investigate from retained proof instead of broad live <code>kubectl</code> access.
</p>

---

## Built-in Agent Demo

https://github.com/user-attachments/assets/abc0dd70-9237-4fd8-b17e-235336bee1f0

Demo scenario: ask the built-in agent whether a cluster's node pool changed in
the last 3 days. The answer uses retained Node lifecycle history, SQL
aggregation, current node capacity, and citations.

## Quick Start

Download a release binary. Replace `0.1.2` with the version you want from the
[release page](https://github.com/nowakeai/kube-insight/releases):

```bash
KI_VERSION=0.1.2
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

Take a bounded first capture from the current kubeconfig context into a local
SQLite database:

```bash
./kube-insight watch pods services \
  --db kubeinsight.db \
  --timeout 30s
```

Check collector coverage before trusting an investigation:

```bash
./kube-insight db resources health --db kubeinsight.db --stale-after 10m
./kube-insight db resources health --db kubeinsight.db --errors-only
```

Start SQL investigations by selecting a cluster:

```bash
./kube-insight query sql --db kubeinsight.db --max-rows 20 --sql \
  "select id, name, source from clusters order by id"
```

For a temporary local agent service, keep the watcher running with API, MCP,
and the built-in Web UI enabled:

```bash
./kube-insight serve --watch --app --db kubeinsight.db
```

Open the embedded UI at <http://127.0.0.1:8090>. Release binaries include the
prebuilt React app; no separate frontend checkout or Node.js runtime is needed
to use it.

SQLite is useful for this local test path, short demos, and temporary evidence
files. For long-running retained history, use the Helm chart's default chDB
mode or an external ClickHouse backend.

See the full [quickstart](docs/users/getting-started/quickstart.md) for Web UI, API, MCP, compaction,
and history examples.

## The Problem

`kubectl` is excellent for current state. Incident investigations often need the
state that already changed:

- Kubernetes Events expire or roll out of the apiserver window.
- Rollouts, webhook changes, RBAC edits, endpoint shifts, and deletes can be
  reverted before anyone asks why the incident happened.
- Agents with raw `kubectl` access need live cluster credentials and must rebuild
  joins across Services, EndpointSlices, Pods, Events, owners, and policies in
  prompt/tool code.
- Sensitive fields need to be filtered before storage and before they ever reach
  an agent transcript.

## The kube-insight Approach

kube-insight records the evidence once, shapes it for investigation, and serves
it through narrow read surfaces:

- **Retained proof:** observed objects, content versions, and watch/list
  timestamps.
- **Investigation candidates:** extracted facts, changes, and topology edges.
- **Agent-friendly access:** backend-aware schema, read-only SQL, HTTP API, and
  MCP tools/prompts.
- **Storage choices:** SQLite for tests and temporary local artifacts, chDB for
  embedded ClickHouse-compatible retention, and ClickHouse for continuous
  central history.

## Why Not Just Give Agents kubectl?

Direct `kubectl` access makes an agent repeatedly list live resources, join
relationships in prompt/tool code, and handle raw cluster payloads. That is
slower for historical investigations and expands the security blast radius.

kube-insight gives agents a narrower evidence interface:

| Agent path | Speed | Security model |
| --- | --- | --- |
| Direct `kubectl` | Repeated live API calls, current-state only, agent must reconstruct joins across resource types. | Agent needs Kubernetes credentials and can receive raw object payloads unless every tool call is carefully constrained. |
| kube-insight | Pre-extracted facts, edges, retained versions, and cluster-scoped SQL/MCP tools. | Filters run before storage, destructive filtering is audited, query tools are read-only, and service mode is designed for Kubernetes authz-aware access control. |

In the recorded validation case, kube-insight first ran a bounded watcher
refresh against the same cluster context used by `kubectl`. The timed query
phase then completed common agent investigation steps in **24-215 ms**, while
comparable direct `kubectl` operations took **3,104-5,745 ms**:

| Agent scenario | kube-insight | kubectl | Speedup |
| --- | ---: | ---: | ---: |
| Retained PolicyViolation Event count | 215 ms | 3,214 ms | 14.9x |
| Event to affected resource investigation | 26 ms | 3,307 ms | 127.2x |
| Event message keyword search | 24 ms | 3,794 ms | 158.1x |
| Service topology candidate list | 32 ms | 3,104 ms | 97.0x |
| Workload inventory for scope selection | 26 ms | 5,745 ms | 221.0x |

A live Service investigation on the long-running ClickHouse dev watcher also
used the same current cluster target for both paths. kube-insight answered with
SQL plus the service investigation API in **449 ms total**; the comparable
`kubectl get service`, `endpointslices`, namespace Pods, and namespace Events
calls took **3,463 ms total**.

The speedup is not a universal benchmark claim. It comes from changing the
shape of the problem: kube-insight precomputes investigation candidates and
keeps sanitized proof; `kubectl` asks the live apiserver each time. For
current-state comparisons, keep the watcher running continuously or refresh the
database and check collector health before trusting the result.

## What It Does

| Capability | What you get |
| --- | --- |
| Historical versions | Retained Kubernetes JSON versions and observation timestamps. |
| Searchable facts | Status, Event, rollout, RBAC, certificate, webhook, and endpoint facts. |
| Topology edges | Workload, Service, EndpointSlice, Event, RBAC, cert-manager, and webhook relationships. |
| Faster agent workflows | SQL recipes, MCP tools, and prompts over pre-extracted evidence instead of repeated live `kubectl` joins. |
| Safer agent access | Filters run before hashing and storage; destructive filters write audit decisions; read surfaces are designed for read-only, authz-aware service access. |
| Default local mode | One pure-Go binary with SQLite storage for tests, short demos, temporary evidence files, CLI, HTTP API, and MCP surfaces. |
| Optional local chDB mode | A separate chDB-enabled artifact can use embedded ClickHouse-compatible local storage with a bundled or installed `libchdb.so`. |
| Central ClickHouse mode | ClickHouse for append-heavy evidence history, compression, read-side investigation queries, and cold-tiering experiments. |

## Choosing A Mode

kube-insight is the retained evidence layer; the storage mode controls how much
scale and operational complexity you take on. Raw `kubectl` remains the live
current-state baseline.

| Option | Use it when | Main tradeoff |
| --- | --- | --- |
| Raw `kubectl` | You need one live current-state confirmation. | No retained sanitized history; agents must do broad live calls and joins. |
| kube-insight + SQLite | You want to test kube-insight, run a short demo, or keep a temporary local evidence DB. | Local row-store backend; not for long-running retained-history deployments. |
| kube-insight + chDB | You want local ClickHouse-compatible tables without a server. | Requires `libchdb.so`; larger artifact and more runtime packaging complexity. |
| kube-insight + ClickHouse | You need continuous central evidence history, compression, API/MCP service reads, and future cold-tiering. | Requires operating ClickHouse. |

See [Storage Modes And Performance](docs/users/reference/storage-mode-comparison.md)
for the detailed performance and tradeoff matrix.

## How It Works

```mermaid
%%{init: {
  "theme": "base",
  "flowchart": {
    "curve": "basis",
    "htmlLabels": true,
    "nodeSpacing": 56,
    "rankSpacing": 76
  },
  "themeVariables": {
    "background": "transparent",
    "primaryColor": "#ffffff",
    "primaryBorderColor": "#64748b",
    "primaryTextColor": "#0f172a",
    "lineColor": "#334155",
    "textColor": "#0f172a",
    "clusterBkg": "#f8fafc",
    "clusterBorder": "#cbd5e1",
    "edgeLabelBackground": "#ffffff",
    "tertiaryColor": "#f8fafc",
    "fontFamily": "-apple-system, BlinkMacSystemFont, Segoe UI, sans-serif"
  }
}}%%
flowchart LR
  subgraph K8s["Kubernetes API"]
    A["Discovery"]
    B["List / Watch"]
  end

  subgraph Ingest["kube-insight ingestion"]
    C["Filters<br/>redact, normalize, discard"]
    D["Retained versions<br/>content-addressed JSON"]
    E["Evidence extraction<br/>facts, edges, changes"]
  end

  subgraph Store["Evidence store"]
    F["versions"]
    G["facts"]
    H["edges"]
    I["observations"]
  end

  subgraph Query["Read surfaces"]
    J["CLI"]
    K["HTTP API"]
    L["MCP tools + prompts"]
  end

  A --> B --> C --> D --> F
  C --> E --> G
  E --> H
  B --> I
  F --> J
  G --> J
  H --> K
  I --> L

  classDef source fill:#ecfeff,stroke:#0891b2,color:#0f172a
  classDef ingest fill:#f0fdf4,stroke:#16a34a,color:#0f172a
  classDef store fill:#fff7ed,stroke:#ea580c,color:#0f172a
  classDef surface fill:#eff6ff,stroke:#2563eb,color:#0f172a
  class A,B source
  class C,D,E ingest
  class F,G,H,I store
  class J,K,L surface
```

## Read Surfaces

kube-insight exposes the same retained evidence through:

- CLI commands for local collection, health checks, SQL, search, history, and
  compaction;
- HTTP API for Web UI, external services, and fallback integrations;
- Streamable HTTP MCP tools for agents that choose their own investigation
  steps;
- A2A endpoints for orchestrators that delegate whole investigations.

See the [Quickstart](docs/users/getting-started/quickstart.md) for command
examples and [kagent Integration](docs/users/tutorials/kagent-integration.md)
for the cloud-native agent path.

## Validation Highlights

The detailed numbers live in [Storage Modes And Performance](docs/users/reference/storage-mode-comparison.md).
The important reading is the shape of the work, not a claim that every point
lookup beats `kubectl`:

- Five retained-evidence agent workflows completed in `24-215 ms` from
  kube-insight versus `3,104-5,745 ms` through broad live `kubectl` calls.
- One live same-target Service investigation completed in `449 ms` through
  kube-insight ClickHouse SQL/API versus `3,463 ms` across four raw `kubectl`
  calls for Service, EndpointSlices, namespace Pods, and namespace Events.
- The same-dataset storage benchmark covers SQLite, ClickHouse, and chDB so
  users can choose between smallest local install, local ClickHouse-compatible
  storage, and central ClickHouse service mode.

The point is evidence shape: kube-insight answers from retained, sanitized facts
and topology edges; `kubectl` answers current apiserver state and leaves history
and joins to the caller.

## Documentation

- [Product brief](docs/users/getting-started/product-brief.md)
- [Quickstart](docs/users/getting-started/quickstart.md)
- [Helm chart](charts/kube-insight/README.md)
- [Built-in Web UI agent tutorial](docs/users/tutorials/builtin-webui-agent.md)
- [External agent skill tutorial](docs/users/tutorials/external-agent-skill.md)
- [kagent integration tutorial](docs/users/tutorials/kagent-integration.md)
- [A2A integration tutorial](docs/users/tutorials/a2a-integration.md)
- [Full documentation index](docs/README.md)
- [Configuration](docs/operators/configuration/configuration.md)
- [Data model](docs/operators/data/data-model.md)
- [Storage modes and performance](docs/users/reference/storage-mode-comparison.md)
- [Roadmap](docs/contributors/roadmap/roadmap.md)
- [Agent SQL cookbook](docs/users/workflows/agent-sql-cookbook.md)
- [kube-insight agent skill](kube-insight-skill/SKILL.md)
- [Development commands](docs/dev/commands.md)
- [Contributing](CONTRIBUTING.md)
- [Security policy](SECURITY.md)
- [Support](SUPPORT.md)
- [Maintainers](MAINTAINERS.md)
- [Code of conduct](CODE_OF_CONDUCT.md)
- [Release process](RELEASE.md)

## Development

```bash
make test
make build
make validate
```

The repository keeps Go files at or below 800 lines. `make test` enforces that
rule before running `go test ./...`.

## License

kube-insight is released under the [Apache License 2.0](LICENSE).
