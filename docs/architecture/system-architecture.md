# System Architecture

## Overview

```text
+--------------------+      +--------------------+
| Kubernetes API      |----->| global watcher      |
| discovery/list/watch|      | discovery + workers |
+--------------------+      +----------+---------+
                                      |
                                      v
                           +----------+---------+
                           | filter pipeline    |
                           +----------+---------+
                                      |
                                      v
             +------------------------+------------------------+
             |                                                 |
             v                                                 v
+------------------------+                         +------------------------+
| resource history store |                         | derived index pipeline |
| versions + blobs       |                         | facts + edges + change |
+-----------+------------+                         +-----------+------------+
            |                                                  |
            v                                                  v
+------------------------+                         +------------------------+
| latest_index           |                         | object_facts/edges     |
+-----------+------------+                         +-----------+------------+
            |                                                  |
            +------------------------+-------------------------+
                                     |
                                     v
                         +-----------+------------+
                         | Kubernetes authorizer |
                         | SAR / SSAR checks     |
                         +-----------+------------+
                                     |
                                     v
                         +-----------+------------+
                         | query API / CLI / UI   |
                         +------------------------+
```

## Components

### Collector

Responsibilities:

- Connect to Kubernetes using kubeconfig or in-cluster credentials.
- Discover every list/watch-capable Kubernetes resource.
- Run dynamic list/watch workers for all resources allowed by policy and RBAC.
- Run periodic list snapshots to recover from watch gaps.
- Mirror Kubernetes Events before their native retention expires.
- Emit filtered resource observations.

See [Global Watcher Design](../ingestion/global-watcher-design.md) for the detailed
collector design.

### Filter Pipeline

Responsibilities:

- Canonicalize JSON.
- Remove, normalize, or discard high-churn fields and changes when configured.
- Redact sensitive data before storage.
- Discard resources blocked by policy.
- Compute document hash.
- Record filter decisions and filter versions.
- Preserve enough metadata for exact reconstruction when compliance mode is on.

### Version Store

Responsibilities:

- Store full latest version.
- Store older versions as reverse deltas or full snapshots.
- Enforce maximum replay depth.
- Store blobs content-addressed.
- Support exact historical reconstruction.

### Topology Extractor

Responsibilities:

- Extract historical relationships from resource versions.
- Maintain time-valid edges.
- Avoid writing unchanged relationships repeatedly.
- Prefer Kubernetes-computed relationships such as EndpointSlice targets.

### Fact Extractor

Responsibilities:

- Extract compact troubleshooting signals.
- Store only useful historical facts.
- Keep full JSON out of fact rows.
- Make facts rebuildable from resource history.

### Query Engine

Responsibilities:

- Resolve service/workload/time-window investigations.
- Use topology and facts before reconstructing JSON.
- Filter every candidate through Kubernetes authorization before response.
- Rank evidence.
- Reconstruct exact versions and diffs for shortlisted evidence.

### Kubernetes Authorizer

Responsibilities:

- Map `kube-insight` operations to Kubernetes verbs and resources.
- Check access with SubjectAccessReview or SelfSubjectAccessReview.
- Apply authorization to raw resources, facts, topology edges, diffs, and
  support bundles.
- Fail closed when authorization cannot be verified.

## Deployment Modes

### Local PoC

```text
kubeconfig collector + SQLite file + CLI
```

Purpose:

- Validate data model.
- Validate compression and index size.
- Validate investigation query shape.

### Single-Cluster Service

```text
in-cluster collector + PostgreSQL/TimescaleDB + API + UI
```

Purpose:

- Centralized retention.
- Multi-user investigation.
- Better query concurrency.

### Multi-Cluster Service

```text
agent per cluster + central API/storage
```

Purpose:

- Cross-cluster incident history.
- Shared UI and retention policy.

## Data Flow

```text
watch event
  -> apply filters
  -> resolve object identity
  -> store version
  -> update latest index
  -> update topology edge intervals
  -> write facts and changes
  -> commit ingestion offset
```

Derived indexes must be rebuildable from `versions`. If a write fails after the
version is stored, background repair can replay version rows to rebuild facts
and edges.

## Reliability Model

- Watch streams can be interrupted.
- API discovery can change when CRDs are added or removed.
- Resource versions can be missed.
- Periodic list snapshots are required.
- Delete events can be missed when a watch gap exceeds the API server retention
  window; relist reconciliation must detect missing objects.
- Duplicates are expected and deduplicated by `doc_hash`.
- Ingestion offsets should be persisted per cluster/GVR/scope.

## Performance Model

Fast paths:

- Latest lookup by object ID.
- Topology lookup by object/time.
- Fact lookup by key/value/time.
- Investigation by service/time window.

Expensive paths:

- Arbitrary cold historical JSON query.
- Full historical scan.
- Rebuild of derived indexes.

Expensive paths should be explicit in API and UI.
