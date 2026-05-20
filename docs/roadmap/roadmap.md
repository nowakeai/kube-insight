# Roadmap

Status date: 2026-05-20

This page is the consolidated roadmap for kube-insight. Detailed working notes
remain in the linked documents; this page is the stable entry point for readers
who need to understand what is done, what comes next, and what is intentionally
deferred.

## Current Position

kube-insight is released as a local-first retained evidence tool. The default
artifact uses SQLite so the normal install stays small and pure Go. ClickHouse
is the primary central evidence backend candidate for long-running append-heavy
history. A separate chDB-enabled artifact provides embedded
ClickHouse-compatible local storage for users who accept the larger runtime
dependency.

The MVP baseline is complete for SQLite default local mode, the chDB-enabled
local variant, and the core MCP read surface. The next product milestones are
Web UI and Kubernetes RBAC support.

## Completed Foundation

- Kubernetes discovery, list/watch collection, and offline ingest.
- Pre-storage filtering with auditable destructive and sensitive filter
  decisions.
- Retained object versions, observations, facts, changes, topology edges, and
  resource health.
- SQLite default storage with CLI, HTTP API, and MCP read surfaces.
- ClickHouse append-only evidence schema and write path for central evidence
  history.
- ClickHouse read paths for schema, read-only SQL, resource health, search,
  object history, topology, and service investigation.
- chDB-enabled local variant that reuses the ClickHouse-compatible schema and
  read path when built with the optional chDB adapter.
- MCP tools for backend-aware schema, read-only SQL, health, and retained object
  history.
- Validation reports covering storage modes, agent-vs-kubectl workflows, live
  ClickHouse profiling, and chDB real-data smoke paths.

## Next Milestones

1. Web UI.
   Build the first human operator surface on top of the existing API: service
   and workload search, investigation result page, evidence timeline,
   topology-at-time graph, resource diff panel, and clear links from candidate
   facts/edges to retained proof versions.

2. Kubernetes RBAC support.
   Add Kubernetes authn/authz-aware service access so API, MCP, and future UI
   reads can be constrained by the caller's Kubernetes permissions. The first
   cut should cover request identity, SubjectAccessReview-style checks for
   requested resources, auditability, and predictable behavior when historical
   evidence refers to objects the caller can no longer read live.

3. Backend hardening.
   Validate real S3/object-storage cold tiering, keep ClickHouse active and
   inactive footprint reporting visible, and add repair or materialized read
   models only where measured query cost requires them.

4. Measured storage experiments.
   Benchmark native ClickHouse `JSON`, selected JSON/text skipping indexes, and
   any heavier path indexes before making them defaults.

5. Production readiness.
   Add in-cluster deployment guidance, retention and redaction operator controls,
   rebuild/repair jobs, and collector observability suitable for long-running
   service mode.

## Explicitly Deferred

- chDB is not promoted to the default local backend until runtime packaging is
  small and reliable enough for normal installs.
- chDB-enabled `serve --metrics` is deferred because the current libchDB runtime
  is not stable enough for the combined API plus metrics service shape.
- PostgreSQL and CockroachDB remain possible metadata/control-plane or
  compatibility backends, not the next primary evidence store.
- Broad synchronous cold JSON scans are not an MVP default; investigation should
  use facts, changes, topology edges, retained versions, and measured indexes.

## Roadmap Documents

- [Roadmap And Open Questions](roadmap-open-questions.md): milestone history,
  success criteria, and unresolved product/data/storage/query questions.
- [Multi Backend Roadmap](../data/multi-backend-roadmap.md): backend roles,
  storage-cost strategy, and storage acceptance metrics.
- [Agent And UI Roadmap](../product/agent-and-ui-roadmap.md): CLI, API, MCP,
  web/chat, and agent skill direction.
- [Kubernetes RBAC Inheritance](../security/kubernetes-rbac-inheritance.md):
  Kubernetes authorization model and historical RBAC inheritance notes.
- [Agent SQL RBAC Filtering](../security/agent-rbac-sql-filtering.md): SQL and
  agent-facing filtering rules for RBAC-constrained reads.
- [MVP Dev Checklist](../dev/mvp-dev-checklist.md): completed local backend MVP
  checklist and stop line.
- [ClickHouse MVP Closeout](../dev/clickhouse-mvp-closeout.md): detailed
  ClickHouse/chDB completion notes, validation results, and follow-ups.
