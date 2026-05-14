# Efficient History Storage V2

This document revisits kube-insight storage after observing a real
`kubeinsight.db` generated from `sample-cluster`.

The main issue is not that raw JSON blobs are stored without compression. The
main issue is that the first PoC conflates three different concepts:

- Kubernetes observations: every list/watch/reconcile sighting.
- Evidence content versions: retained sanitized resource documents that changed.
- Query indexes: latest navigation, facts, edges, changes, and audit rows.

Those concepts need different retention and indexing rules.

## Observed PoC Cost

The analyzed SQLite file was about 155 MiB, excluding an 8 MiB WAL.

Approximate allocation by category:

```text
filter audit rows + indexes   45.67 MiB
unique retained JSON blobs    34.16 MiB
latest JSON cache             34.16 MiB
facts + indexes               18.33 MiB
edges + indexes                9.35 MiB
version metadata + indexes     7.87 MiB
changes + indexes              3.66 MiB
dimensions/offsets/profiles    1.41 MiB
```

Important findings:

- `latest_index.doc` exactly duplicates the latest version blob for every row.
- `versions` had 30,093 rows but only 10,383 distinct retained document hashes.
- ClusterRoleBinding had 17,565 version rows but only 188 distinct blobs.
- Detailed filter decisions were larger than retained JSON.

After the first storage cleanup:

- `latest_index.doc` is migrated away and replaced by `latest_documents`.
- `object_observations` records every observed object sighting.
- unchanged observations keep their timestamp/resourceVersion but point at the
  canonical retained content version.
- `db compact --prune-unchanged` can remove old duplicate content-version rows
  after observations are backfilled.

On the analyzed `sample-cluster` SQLite file this reduced the database from
about 170.5 MiB including WAL to about 92.8 MiB. The retained history still has
30,093 observation rows, but only 10,383 retained content versions.

## Design Principle

Use Kubernetes resource identity and resource revision for collection
correctness. Use sanitized content hash for evidence version boundaries.

These are not interchangeable:

- `uid`, `namespace`, `name`, GVR, and cluster identify the Kubernetes object.
- `resourceVersion` tells us what the API server observed.
- sanitized `doc_hash` tells us whether retained evidence changed after
  filtering and normalization.

If `resourceVersion` changes but `doc_hash` does not, the collector should
record that the object was observed, but it should not write another content
version, facts, edges, changes, or full filter audit row.

## Proposed Model

### Object Identity

`objects` remains the stable identity table:

```text
cluster_id
api_resource_id / kind_id
namespace
name
uid
first_seen_at
last_seen_at
deleted_at
latest_content_id
```

UID should win when present. Name reuse with a different UID is a new object
incarnation. Resources without UID fall back to cluster/GVR/namespace/name.

### Observations

Observations are cheap collection evidence, not full document history:

```sql
object_observations(
  id integer primary key,
  object_id integer not null,
  observed_at integer not null,
  source text not null,              -- list, watch, relist, reconcile
  event_type text not null,          -- ADDED, MODIFIED, DELETED, SNAPSHOT
  resource_version text,
  generation integer,
  content_id integer,                -- nullable when unchanged or bookmark-like
  doc_hash text,
  repeated boolean not null default false,
  observation_count integer not null default 1,
  first_seen_at integer,
  last_seen_at integer
)
```

The current SQLite implementation uses this compact form:

```sql
object_observations(
  id integer primary key,
  cluster_id integer not null,
  object_id integer not null,
  observed_at integer not null,
  observation_type text not null,
  resource_version text,
  version_id integer,
  content_changed boolean not null
)
```

Default retention should coalesce repeated unchanged observations:

- same object,
- same retained `doc_hash`,
- same event type class,
- no meaningful extracted transition.

The row can update `last_seen_at`, `resource_version`, and
`observation_count` instead of inserting more history rows.

For strict audit mode, every Kubernetes resourceVersion can be retained as a
separate observation row, but still without re-writing content indexes.

### Content Versions

Content versions replace the current meaning of `versions`:

```sql
content_versions(
  id integer primary key,
  object_id integer not null,
  seq integer not null,
  first_observed_at integer not null,
  last_observed_at integer not null,
  first_resource_version text,
  last_resource_version text,
  generation integer,
  doc_hash text not null,
  blob_ref text not null,
  materialization text not null,     -- full, delta, tombstone
  codec text not null,               -- identity, zstd, json_patch
  parent_content_id integer,
  raw_size integer not null,
  stored_size integer not null,
  summary text
)
```

Write a new content version only when one of these is true:

- retained sanitized JSON hash changed,
- object deletion state changed,
- extraction profile says the observation is semantically meaningful even if
  JSON is unchanged,
- non-contiguous content reappears after another content version.

If content is unchanged, extend `last_observed_at` and
`last_resource_version` on the current content version, and update the object
`last_seen_at`.

The blob table remains content-addressed:

```sql
content_blobs(
  digest text primary key,
  codec text not null,
  raw_size integer not null,
  stored_size integer not null,
  data blob not null
)
```

### Latest State

`latest_index` should stop storing full JSON.

Use it as a compact navigation and projection table:

```sql
latest_index(
  object_id integer primary key,
  cluster_id integer not null,
  kind_id integer not null,
  namespace text,
  name text not null,
  uid text,
  latest_content_id integer not null,
  observed_at integer not null,
  phase text,
  ready text,
  node_name text,
  owner_name text,
  labels_summary text,
  annotations_summary text
)
```

Expose a compatibility view for agents that need latest JSON:

```sql
create view latest_documents as
select
  li.*,
  cast(b.data as text) as doc
from latest_index li
join content_versions cv on cv.id = li.latest_content_id
join content_blobs b on b.digest = cv.blob_ref
where cv.codec = 'identity';
```

This preserves SQL access to latest JSON without duplicating it. Agents should
prefer facts and projections, and only join to `latest_documents` when proof
JSON is needed.

### Facts, Edges, And Changes

Facts, edges, and changes should be tied to content versions or meaningful
transitions, not observations.

Rules:

- Do not write facts/edges/changes for unchanged content.
- Use integer dictionary IDs for `fact_key`.
- Use partial indexes for sparse columns:
  `where node_id is not null`, `where service_id is not null`,
  `where workload_id is not null`.
- Merge adjacent identical edge intervals.
- Keep an `open_edges` helper table during ingestion, then emit closed intervals
  only when membership changes.
- Store high-cardinality text such as event messages as fingerprints plus small
  samples, not as broadly indexed values.

### Filter Audit

Detailed filter decisions should not be written for every `keep` result by
default.

Default mode:

- store destructive decisions,
- store `keep_modified` decisions,
- treat routine metadata normalization such as `managedFields` removal as
  aggregated normalization, not per-object audit,
- aggregate ordinary `keep` counts by filter/resource/time bucket,
- expose debug-level per-observation filter logs when needed.

Strict audit mode:

- store every decision,
- allow TTL/retention independent of content versions,
- keep it disabled for the local PoC default.

This directly targets the largest observed category in the PoC database.

## Resource Profiles

Storage should be profile-driven, not one-size-fits-all.

### RBAC And APIService

RBAC and APIService resources are often stable but can be observed many times.

Default:

- content-version only on sanitized hash change,
- no repeated facts/edges when unchanged,
- keep relationship facts and roleRef/subject edges only on change.

### Events

Events should use a rollup model:

```sql
event_rollups(
  id integer primary key,
  cluster_id integer not null,
  involved_object_id integer,
  reason text,
  type text,
  message_fingerprint text,
  first_seen_at integer,
  last_seen_at integer,
  count integer,
  sample_message text,
  latest_event_content_id integer
)
```

Raw Event versions can have shorter retention than rollups and facts.

### CRDs

CRDs are large but important. Store content versions only when the retained CRD
document changes. Extract and index:

- served/storage versions,
- conversion webhook service/CABundle presence,
- schema fingerprint,
- names and scope,
- status conditions.

Do not blindly strip CRD content; only remove genuinely sensitive fields.

### Secrets

Secrets keep metadata, key names, type, owner refs, and generated value
fingerprints only when configured. Values remain synthetic or redacted.

### Leases And Heartbeats

Skip by default or downsample into observation counts. They should not create
content versions unless explicitly enabled.

## Hot And Cold Storage

SQLite local mode:

- keep latest and recent content blobs with `identity` codec,
- rely on facts/edges/changes/projections for queries,
- optionally compress older content blobs after a retention window,
- keep `latest_documents` available only for identity blobs.

PostgreSQL:

- keep latest/recent as JSONB,
- move cold content to compressed bytea or a partition with TimescaleDB
  compression when available,
- keep facts/edges/changes in normal indexed tables.

CockroachDB:

- avoid engine-specific compression assumptions,
- use range/TTL policies and content-addressed blobs,
- keep read replicas/API-only instances query-focused.

## Expected Impact On The Current Database Shape

Based on the analyzed 155 MiB SQLite file:

- Removing duplicated `latest_index.doc` should save about 34 MiB.
- Default filter audit aggregation should save a large part of 45 MiB.
- Skipping unchanged content versions should prevent repeated version/fact/edge
  writes for resources such as ClusterRoleBinding and APIService.
- Partial indexes and dictionary IDs should reduce fact index size.

A realistic first target for the same dataset is below 70 MiB without
compressing primary query data. With cold compression later, lower is possible.

## Migration Path

### Step 1: Low-Risk PoC Fixes

1. Stop storing full JSON in `latest_index`; add `latest_documents` view.
2. In `PutObservation`, compare current latest `doc_hash` before writing a new
   version.
3. If unchanged, update `objects.last_seen_at`, latest observed time, and
   ingestion offset only.
4. Store detailed filter decisions only for destructive or modified outcomes by
   default.

### Step 2: Proper V2 Schema

1. Add `object_observations`.
2. Rename/evolve `versions` to `content_versions`.
3. Add content-run fields: `first_observed_at`, `last_observed_at`,
   `first_resource_version`, `last_resource_version`.
4. Add migration/rebuild command from V1 tables.

### Step 3: Index Compaction

1. Add fact key dictionary.
2. Convert sparse fact indexes to partial indexes.
3. Merge edge intervals.
4. Add event rollups.

### Step 4: Cold Tier

1. Add `codec=zstd` support for old content blobs.
2. Keep latest/recent identity blobs for SQL JSON access.
3. Add backend-specific policies for PostgreSQL, CockroachDB, and TimescaleDB.
