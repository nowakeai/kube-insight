# Storage Modes And Performance Positioning

Last updated: 2026-05-18.

kube-insight should be evaluated as an evidence layer, not as a faster spelling
of every `kubectl get` command. There are two broad classes of options:

1. Live Kubernetes API access: direct `kubectl` or an agent tool that calls the
   apiserver on demand.
2. Retained kube-insight evidence: sanitized facts, topology edges,
   observations, and versions stored in a backend chosen for the user's scale.

Within those classes, the current project exposes four practical choices:

| Class | Option | Best fit |
| --- | --- | --- |
| Live API baseline | Raw `kubectl` | Current-state spot checks and manual confirmation. |
| kube-insight local | SQLite default artifact | Smallest install, local investigations, CI fixtures, and first-time users. |
| kube-insight local ClickHouse-compatible | chDB-enabled artifact | Local ClickHouse-style schema/query behavior without running a ClickHouse server. |
| kube-insight central service | ClickHouse backend | Continuous multi-resource history, larger datasets, storage efficiency, and team/API/MCP service deployments. |

The intended product path is: use kube-insight for retained, sanitized,
agent-ready evidence; choose SQLite, chDB, or ClickHouse according to scale and
operational needs; keep `kubectl` as the live-state confirmation tool.

## Summary Matrix

| Option | Strengths | Tradeoffs | Current evidence |
| --- | --- | --- | --- |
| Raw `kubectl` | No database, no watcher, exact live current state, familiar to every Kubernetes user. | Current-state only, repeated broad API calls for agents, no retained Event history after expiry, no pre-storage redaction layer, joins happen in prompt/tool code. | In the recorded agent workflow benchmark, broad `kubectl` operations took `3,104-5,745 ms`. |
| kube-insight + SQLite | Default pure-Go artifact, no external service, simplest local database, good for laptops and small evidence sets, works with CLI/API/MCP. | Row-store local backend; not the target for very large retained history or low-cost cold storage. | Same benchmark used the default SQLite evidence DB: `24-215 ms` for five agent investigation queries, `14.9x-221x` faster than broad raw `kubectl` operations. |
| kube-insight + chDB | Local ClickHouse-compatible tables and SQL shape, useful for users who want ClickHouse semantics without a server, still supports SQLite and remote ClickHouse in the chDB-enabled binary. | Requires `libchdb.so`; release artifact is larger; `serve --metrics` is deferred for this backend; runtime packaging is more complex than the default artifact. | chDB smoke covers schema, SQL, search, history, topology, object investigation, service investigation, and API. Embedded real-data validation showed about `5.8x` compression and service investigation around `607 ms` after store reuse. |
| kube-insight + ClickHouse | Best MVP path for long-running central evidence history, high compression, append-heavy writes, API/MCP service reads, and future cold-tiering. | Requires operating ClickHouse; object-storage tiering is configured but not end-to-end validated in this MVP; local MergeTree inactive parts must be understood when reading disk usage. | Live profiles showed `28.4x-30.53x` active business-table compression, `32.16-33.96` compressed bytes per active row, API health/search/history/topology under `500 ms`, and representative service investigation `224-801 ms`. |

## How To Choose

Start with the default SQLite artifact when:

- you want the smallest install,
- you are trying kube-insight for the first time,
- you need local evidence files for a single cluster or a short-lived run,
- you want the lowest operational burden.

Use the chDB-enabled artifact when:

- you want local ClickHouse-compatible SQL and table names,
- you want to test ClickHouse-style query behavior without a server,
- you can accept the `libchdb.so` runtime dependency,
- you do not need chDB-backed `serve --metrics` yet.

Use ClickHouse when:

- the watcher should run continuously,
- the evidence dataset is large enough that compression and storage cost matter,
- multiple users or agents need API/MCP access to the same retained evidence,
- you want storage efficiency metrics, read profiles, and a path toward cold
  object-storage tiering.

Use raw `kubectl` when:

- you need to confirm the exact current state of one object,
- you do not need retained history,
- you are validating a kube-insight finding against the live apiserver.

## Performance Claims

The current public benchmark is intentionally scoped:

- It compares kube-insight's default SQLite evidence database with raw
  `kubectl` for agent-style investigations that need retained facts, topology,
  search, and inventory.
- It does not claim SQLite, chDB, and ClickHouse have been measured against each
  other on one identical dataset under a single harness.
- ClickHouse and chDB have separate live-profile and smoke-validation results
  that measure their MVP readiness and storage behavior.

This distinction matters. kube-insight is expected to be better than raw
`kubectl` for the project's target problem: retained, sanitized, repeatable
Kubernetes evidence for humans and agents. Inside kube-insight, the storage
backend choice is a tradeoff between simplicity, local ClickHouse compatibility,
central-service scale, and operational complexity.

## Next Benchmark Gap

The next useful benchmark is a same-dataset matrix:

| Query group | SQLite | chDB | ClickHouse | raw kubectl |
| --- | ---: | ---: | ---: | ---: |
| resource health | planned | planned | planned | not applicable |
| evidence search | planned | planned | planned | current-state search baseline |
| object history | planned | planned | planned | limited by live history |
| topology expansion | planned | planned | planned | broad live list/join baseline |
| service investigation | planned | planned | planned | broad live list/join baseline |
| storage footprint | planned | planned | planned | no retained storage |

That matrix should use one reproducible dataset for SQLite, chDB, and
ClickHouse, then report query latency, database size, compressed bytes per row,
and import/write cost. Raw `kubectl` should remain a live-state baseline, not a
storage engine.
