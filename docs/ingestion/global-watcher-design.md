# Global Watcher Design

## Goal

`kube-insight` should automatically watch and persist all Kubernetes resources
that the collector is allowed to list/watch.

This includes:

- built-in resources,
- namespaced and cluster-scoped resources,
- CRDs,
- newly added resources discovered after startup,
- Kubernetes Events.

Important boundary:

```text
Kubernetes does not expose one single "watch all resources" API.
```

The implementation is a global watcher manager:

```text
API discovery
  -> discover all list/watch-capable GroupVersionResources
  -> start one list/watch loop per GVR and scope
  -> apply filters and persist every retained object
  -> run typed extractors when available
```

## Architecture

```text
+----------------------+
| discovery loop       |
| APIResourceList      |
+----------+-----------+
           |
           v
+----------------------+
| GVR registry         |
| resources + scope    |
+----------+-----------+
           |
           v
+-----------------------------+
| watcher scheduler           |
| concurrency/rate limits     |
+-------------+---------------+
              |
              v
+-----------------------------+
| per-GVR list/watch workers  |
| list -> watch -> relist     |
+-------------+---------------+
              |
              v
+-----------------------------+
| resource observation stream |
+-------------+---------------+
              |
              v
+-----------------------------+
| filter/store                |
| versions + latest_index     |
+-------------+---------------+
              |
              v
+-----------------------------+
| optional typed extractors   |
| topology/facts/changes      |
+-----------------------------+
```

## Discovery

The collector should periodically call Kubernetes discovery APIs and build a
registry of watchable resources.

For each discovered resource, record:

```text
group
version
resource
kind
namespaced
verbs
preferred_version
storage_version when known
```

Persist this metadata in the `api_resources` dimension table. That table is the
collector and authorization source of truth for mapping stored objects back to
Kubernetes GroupVersionResource and scope.

Include only resources with:

```text
list
watch
```

Skip by default:

- subresources such as `pods/status`, `deployments/scale`,
- resources without list/watch,
- resources blocked by local policy,
- high-risk payload resources when redaction policy excludes them.

CRDs:

- Start watchers for new CRDs after discovery refresh.
- Stop or mark watchers stale when CRDs are removed.
- Store CRD instances as generic resources even without typed extractors.

Recommended discovery refresh:

```text
startup
every 5-10 minutes
on discovery failure backoff
```

## Watch Worker Lifecycle

Each GVR worker follows this loop:

```text
1. Initial LIST with pagination.
2. Persist every returned object.
3. Save list resourceVersion.
4. Start WATCH from that resourceVersion.
5. Persist ADDED/MODIFIED/DELETED events.
6. Accept BOOKMARK events when supported.
7. On timeout/network error, restart watch from latest saved resourceVersion.
8. On resourceVersion too old, perform full relist.
```

The worker should treat watch as an optimization over periodic list. The source
of correctness is:

```text
list snapshot + watch stream + relist after gaps
```

## Offsets

Track offsets per cluster and GVR:

```sql
ingestion_offsets(
  cluster_id,
  api_resource_id,
  namespace,
  resource_version,
  last_list_at,
  last_watch_at,
  last_bookmark_at,
  status,
  error
)
```

For cluster-scoped resources, `namespace` is empty.

For namespaced resources, the collector can choose:

- one all-namespaces watch when permitted,
- one watch per namespace when permissions are scoped.

Offsets are committed only after the corresponding retained version, tombstone,
or discard decision is durable.

Current PoC implementation:

- `kube-insight watch` discovers and watches all watchable resources.
- `kube-insight watch pods services` watches explicit resource names.
- `kube-insight watch 'v1/*' 'apps/v1/*'` resolves group/version wildcard
  patterns through discovery.
- By default, watch targets the current kubeconfig context and writes to
  `./kubeinsight.db`; `--all-contexts` watches every configured context into the
  same database unless `--db` selects another path.
- Runtime cluster identity is stored as a stable cluster ID derived from
  Kubernetes cluster identity, with the kubeconfig context retained as metadata.
- Multi-resource watch runs in two phases: bounded-concurrency initial LIST for
  every selected GVR first, then long-running WATCH streams from each list
  resourceVersion. This prevents early long-lived watch streams from blocking
  coverage of later resources.
- Multiple GVR workers record per-resource health, queue, retry, and error
  summaries.
- `kube-insight db resources health` reports each GVR's last list/watch/bookmark
  time, current status, last error, resourceVersion, latest object count, and
  stale state.
- ADDED/MODIFIED/DELETED events are ingested through the same JSON pipeline.
- BOOKMARK events update `last_bookmark_at` and `resource_version`.
- Watch failures retry from the latest observed resourceVersion; stale
  resourceVersion/410 Gone errors clear the resourceVersion and force a full
  relist.
- Each LIST snapshot reconciles deletes by comparing previously active latest
  objects for the GVR with the current LIST items and writing `DELETED`
  observations for missing objects.
- Offset `namespace` is stored as an empty string for all-namespaces and
  cluster-scoped watches so SQLite upserts remain stable.

## Namespaced vs Cluster-Scoped

Preferred mode for in-cluster global collector:

```text
ClusterRole with list/watch across all target resources.
Use all-namespaces watches for namespaced resources.
Use cluster-scope watches for cluster-scoped resources.
```

Restricted mode:

```text
Discover allowed namespaces.
Start per-namespace watchers.
Store only what the collector service account can read.
```

The query-time user RBAC model is still separate. Collector permissions decide
what data exists; user authorization decides what each user can read.

## Resource Storage For Unknown Types

All resources should go into the generic resource history store:

```text
objects
versions
blobs
latest_index
```

Typed extractors are optional:

```text
known K8S types:
  resource history + topology + facts + changes

unknown CRDs:
  resource history + latest index + generic change summary
```

This preserves future value. A new extractor can later rebuild facts from
stored versions.

## Typed Extractor Coverage

P0 typed extractors:

```text
Pod
Deployment
ReplicaSet
Service
EndpointSlice
Node
Event
```

P1 typed extractors:

```text
StatefulSet
DaemonSet
Job
CronJob
ConfigMap
Secret metadata
PVC
HPA
PDB
Ingress
Gateway API
```

Generic fallback:

```text
identity
latest state
version history
ownerReferences edges
basic metadata changes
schema-less summary diffs
```

## Filters And Policy

Global watch increases sensitive-data risk. The filter pipeline must run before
storage.

Default filters:

```text
resource policy:
  include/exclude by group/resource/namespace/name/labels

secrets redaction:
  store metadata only, redact data/stringData

token redaction:
  redact token-like fields

configmaps policy:
  store by default, allow path/name-based exclusion

metadata normalization:
  omit by default unless compliance mode is enabled

change discard:
  drop unchanged or explicitly ignored changes, but never drop deletes
```

Resource-specific processing profiles can override the default filter chain and
extractor set:

```text
pods:
  pod_fast_path

nodes:
  node_summary

events:
  event_rollup

endpointslices:
  endpointslice_topology

leases:
  lease_skip_or_downsample
```

Profiles are performance tools, not separate storage models. They must still
emit retained versions, facts, edges, filter decisions, and offsets through the
same logical pipeline.

Support include/exclude policies:

```yaml
include:
  - group: "*"
    resource: "*"
exclude:
  - group: ""
    resource: "secrets"
    mode: "metadata-only"
  - group: "coordination.k8s.io"
    resource: "leases"
    mode: "skip"
```

Leases are often high-churn and low troubleshooting value. They should be
skippable by default or stored with aggressive compaction.

Every filter decision should emit metrics:

```text
kept
modified
discarded_change
discarded_resource
redacted_fields
removed_fields
secret_payload_removals
secret_payload_violations
```

## Backpressure And Rate Limits

Watching every GVR can overload both the API server and the storage backend if
done naively.

Controls:

- max concurrent initial lists,
- max concurrent watches,
- per-GVR event buffer,
- per-GVR priority,
- storage write batch size,
- API client QPS/burst,
- adaptive pause for noisy resources,
- dead-letter queue for failed decodes/stores.

PoC watch summaries should report the theoretical max queued GVR count for the
run, the number of workers that had to wait behind the concurrency semaphore,
and total queue wait milliseconds.

Suggested defaults for PoC:

```text
max_initial_lists = 4
max_active_watches = 64
event_buffer_per_gvr = 1000
write_batch_size = 100
resync_interval = 6h
discovery_refresh = 10m
```

Production defaults should be tuned with real cluster size.

Suggested priority classes:

```text
critical:
  pods, events, endpointslices

important:
  deployments, replicasets, services, nodes

normal:
  configmaps, pvc, jobs, statefulsets, daemonsets

low:
  leases and other noisy low-value resources
```

Backpressure should degrade low-priority resources first. It must not delay
offset commits for already-processed high-priority delete events.

## Handling Deletes

Kubernetes watch is best-effort over a bounded resourceVersion window. The
system should not assume it will observe every individual delete event forever.
Correctness comes from combining watch deletes with periodic and gap-triggered
reconciliation.

For delete events:

- Store a tombstone version when the final object is available.
- Mark `objects.deleted_at`.
- Close open topology edges involving the object.
- Write lifecycle facts such as Pod deleted when useful.

If the delete event only contains a tombstone with partial metadata, store the
metadata and mark the version as partial.

To avoid missing deletes:

- Persist watch offsets only after delete handling is durable.
- Use BOOKMARK events when available, but do not treat them as proof that no
  delete was missed.
- Run periodic full LIST reconciliation for every GVR/scope.
- On relist, compare listed object identities with `latest_index` for that
  GVR/scope.
- Mark missing objects as deleted only when collector permissions and namespace
  scope are unchanged.
- Otherwise mark missing objects as `unknown_visibility` and keep prior
  evidence without inventing a delete.
The PoC watch summary and benchmark report expose both relist-confirmed deletes
and unknown-visibility counts so false-delete risk is visible during live
validation.

Delete confidence:

```text
watch_delete:
  high confidence

missing_after_relist_same_scope:
  high confidence

missing_after_permission_or_scope_change:
  unknown visibility
```

## Handling Watch Gaps

When the API server returns that a resourceVersion is too old:

```text
1. Mark offset as stale.
2. Run a full LIST for that GVR/scope.
3. Diff listed object identities against latest_index for that GVR/scope.
4. Store changed objects.
5. Mark missing objects as deleted or unknown depending on confidence.
6. Restart WATCH from the new list resourceVersion.
```

Use caution when marking missing objects deleted after an authorization or
namespace-scope change. The object may be invisible rather than deleted.

Periodic reconciliation should also run without an explicit stale
resourceVersion error. The interval is a product setting; the PoC default is
`resync_interval = 6h`.

## RBAC Requirements For Collector

The global collector needs list/watch on every resource it should retain.

Example shape:

```yaml
rules:
  - apiGroups: ["*"]
    resources: ["*"]
    verbs: ["get", "list", "watch"]
```

This is powerful and must be deployed carefully.

Safer production options:

- namespace-scoped collectors,
- exclude sensitive resources,
- metadata-only Secret handling,
- separate collector identities by tenant/namespace,
- audit collector permissions at startup.

User-facing query authorization is still enforced with Kubernetes
SubjectAccessReview/SelfSubjectAccessReview.

## Storage Implications

Global watch changes the storage profile:

- many small low-value resources,
- high-churn coordination resources,
- CRDs with unknown shapes,
- more latest_index rows,
- larger `api_resources` and kind dimension tables,
- more generic history where typed facts are unavailable.

Therefore the write path must support:

- per-GVR retention policy,
- per-GVR redaction policy,
- per-GVR compaction strategy,
- per-GVR extractor configuration,
- per-GVR processing profile and priority,
- ability to disable or downsample noisy resources.

## MVP Implementation Plan

1. Add `api_resources` discovery registry for all list/watch resources.
2. Add dynamic unstructured client.
3. Add per-GVR list/watch workers.
4. Store all observed resources in generic history.
5. Keep existing typed extractors for P0 resources.
6. Add ingestion offset table.
7. Add configurable filter pipeline.
8. Add benchmark metrics per GVR:
   - events/sec,
   - stored versions,
   - skipped/redacted objects,
   - filter decisions,
   - delete tombstones,
   - unknown-visibility objects,
   - watch restarts,
   - relists,
   - storage bytes.

## Open Questions

- Should Leases be skipped by default?
- Should all Secrets be metadata-only with no opt-out, or configurable?
- Should unknown CRDs get generic scalar fact extraction?
- Should collector RBAC be one global ClusterRole or namespace-scoped agents?
- How should per-GVR retention defaults be selected automatically?
