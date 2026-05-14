# Security, Privacy, And Retention

## Sensitive Data

Kubernetes resources can contain sensitive data:

- Secret payloads.
- tokens in env vars.
- credentials in ConfigMaps.
- annotations with embedded config.
- image pull secrets.
- service account references.

Default policy:

```text
Do not store Secret payloads.
Store Secret metadata, references, and optional hashes.
Redact configurable sensitive paths.
Make redaction visible in strategy reports.
```

## Redaction Modes

### Safe Default

- Secret data omitted.
- managedFields omitted.
- common token-like env values redacted.
- ConfigMap data stored unless path policy excludes it.

### Compliance Mode

- Store exact resource JSON except explicitly forbidden paths.
- Keep managedFields.
- Stronger access control required.

### Minimal Mode

- Store metadata, topology, and facts.
- Do not store full JSON for selected sensitive kinds.

Redaction is implemented as part of the ingestion filter pipeline. Filter
configuration and filter versions must be auditable because they determine what
historical evidence exists and what has been intentionally discarded.

## RBAC

`kube-insight` must inherit Kubernetes authorization. If a user cannot access a
resource through the Kubernetes API server, they must not be able to access the
same resource, its historical versions, topology edges, or derived facts through
`kube-insight`.

See [Kubernetes RBAC Inheritance](kubernetes-rbac-inheritance.md) for the
detailed design.

Access is scoped by:

- cluster,
- namespace,
- resource kind,
- resource name,
- time range,
- sensitive object class.

Read access to historical Secret metadata should be separate from workload
history access.

Authorization must also be applied to derived data:

- topology edges,
- facts,
- change summaries,
- investigation results,
- support bundles.

## Audit

Audit:

- who queried historical resources,
- who viewed redacted/sensitive data,
- who changed retention/redaction policy,
- who exported support bundles.

## Retention Tiers

Suggested defaults:

```text
0-7 days:
  full facts, topology, versions, latest JSON, optional hot JSONB GIN

7-30 days:
  full facts, topology, compact versions, fewer hot JSON indexes

30+ days:
  compact versions, compact facts, topology intervals, no generic JSON index
```

## Compaction

Jobs:

- merge adjacent identical topology intervals,
- drop duplicate unchanged versions when allowed,
- recompress old blobs with stronger Zstd settings,
- rebuild facts after extractor changes,
- move old blobs to object storage when supported.

## Vacuum And Maintenance

Retention and compaction delete or rewrite data, so each backend needs an
explicit maintenance policy.

SQLite:

- use WAL mode for ingestion,
- run `wal_checkpoint` after large batches or before packaging archives,
- enable incremental vacuum when long-lived files are expected,
- run `ANALYZE` after large ingest, purge, or index rebuild jobs.

PostgreSQL/TimescaleDB:

- keep autovacuum enabled and monitor dead tuples and index bloat,
- tune autovacuum thresholds for high-churn fact, latest, and offset tables,
- run explicit `VACUUM (ANALYZE)` after large retention purges,
- schedule heavier maintenance outside incident-heavy windows,
- measure query latency before and after maintenance.

Maintenance runs should be recorded so storage savings and query-plan changes
are visible in benchmark and operations reports.

## Deletion

Deletion requirements:

- cluster-level purge,
- namespace-level purge,
- object-level purge,
- retention-based purge,
- redaction policy reprocessing.

Derived indexes must be purged with source versions.

## Support Bundles

If the product exports incident bundles:

- include only relevant object versions,
- include topology and facts for the window,
- preserve redaction,
- include policy metadata,
- include hash/provenance for evidence integrity.
