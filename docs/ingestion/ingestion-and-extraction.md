# Ingestion And Extraction

## Sources

MVP sources:

- Kubernetes discovery API.
- Dynamic Kubernetes list/watch API for all allowed resources.
- Periodic full list snapshots.
- Kubernetes Events watch.
- Local kubeconfig collection for PoC.

The default collector should be a global watcher. It discovers every
list/watch-capable GroupVersionResource and stores every observed object in the
generic resource history store, subject to the configured filter pipeline.

See [Global Watcher Design](global-watcher-design.md).

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
Secret metadata only
PVC
```

P2 typed extractors:

```text
Ingress
Gateway API resources
HPA
PDB
NetworkPolicy
custom resources
```

Unknown CRDs and resources without typed extractors still get:

- resource history,
- latest index,
- generic metadata identity,
- basic ownerReferences topology,
- generic change summary when feasible.

## Ingestion Pipeline

```text
discovery/list/watch event
  -> decode
  -> apply resource filters
  -> resolve object identity
  -> deduplicate by doc_hash
  -> store resource version
  -> update latest index
  -> extract topology
  -> extract facts
  -> extract change summaries
  -> commit offset
```

## Filter Pipeline

Filters are ordered, configurable write-path stages. They can discard a whole
observation, transform a resource into its retained form, or attach metadata for
storage and later audit.

Default filter chain:

```text
resource_policy_filter
  -> secret_redaction_filter
  -> token_redaction_filter
  -> metadata_normalization_filter
  -> high_churn_field_filter
  -> change_discard_filter
```

Filter outcomes:

```text
keep
keep_modified
discard_change
discard_resource
```

Filter requirements:

- Run before storage and before `doc_hash` calculation for retained documents.
- Persist filter decisions to the audit table, including decisions that discard
  the resource before a retained version exists.
- For destructive `keep_modified` decisions, include structured audit metadata
  such as `removedFields`, `redactedFields`, and policy-specific markers like
  `secretPayloadRemoved` so benchmark and safety checks can aggregate them.
- Make destructive filters explicit so compliance mode can disable or replace
  them.
- Keep enough object metadata to write tombstones and close topology edges.

### Redaction Filters

Default behavior:

- Do not store Secret payloads.
- Store Secret metadata, refs, and optionally data hashes.
- Count removed Secret `data` and `stringData` fields as redacted fields and
  fail the safety metric if retained latest Secret JSON still contains either
  payload field.
- Redact known token-like env values when configured.
- Keep ConfigMap data by default, but allow path-based exclusion.

### Normalization Filters

Default ignored or normalized fields:

```text
metadata.resourceVersion
metadata.managedFields
metadata.annotations["kubectl.kubernetes.io/last-applied-configuration"]
```

Do not blindly drop status. Status contains most incident evidence. Instead,
extract useful status facts and let storage compression handle the retained JSON.

### Change Discard Filters

Some observations are valid but not useful enough to store as new versions.

Examples:

- unchanged document hash,
- only ignored heartbeat fields changed,
- noisy resources such as Leases when policy chooses skip or downsample.

Discarding a change must still update ingestion metrics and offsets. It must not
hide a delete, a UID change, or a topology/fact transition.

The ingestion pipeline preserves `DELETED` observations even if a change discard
filter requests `discard_change`; the decision is still audited, but the delete
observation continues to storage.

## Resource-Specific Processing

The global watcher provides coverage, but some resources need specialized
processing profiles for performance and signal quality. A profile can choose a
filter chain, extractor set, retention class, compaction strategy, and queue
priority for one GVR.

Default rule:

```text
unknown resources:
  generic history + latest index + generic metadata/change summary

known high-value resources:
  generic history + resource-specific filters/extractors/index hints
```

Resource identity resolution should be data-driven before it falls back to
compiled defaults. The order is:

```text
1. discovered api_resources persisted from client-go or kubectl discovery
2. CRD definitions observed in the current batch
3. built-in Kubernetes seed resources
4. conservative fallback for unknown resources
```

Kind-only and resource-only seed matches must not cross API groups. If a grouped
resource is not discovered, the resolver should return an explicit conservative
fallback for that group rather than borrowing a built-in resource or scope from
another group.

Do not add new independent kind/resource/group switch tables in extractors,
ingest, or storage. Route GVK/GVR/scope lookups through the shared resolver so
CRDs and uncommon API groups keep their real plural, group, version, and scope.
The ingestion pipeline passes the active resolver, including persisted discovery
data and CRDs observed in the batch, into extractors. Reference-style extractors
must use that resolver for ownerReferences, Events, scaleTargetRef, Gateway
refs, RBAC subjects, webhook services, and other object-reference edges.
SQLite edge-target materialization also checks persisted `api_resources` before
falling back to compiled defaults, so topology queries preserve discovered CRD
plural names and scope.

Each profile must define five outputs:

```text
retained versions:
  which observations become reconstructable evidence

facts:
  compact queryable signals written to object_facts

edges:
  topology relationships written to object_edges

status changes:
  status transitions that should become facts and/or object_changes

change summaries:
  small timeline entries written to object_changes
```

Resource-specific output contract:

| Resource | Facts | Edges | Status changes | Change summaries |
| --- | --- | --- | --- | --- |
| Pod | phase, reason, last reason, restart count, ready, QoS, scheduled/deleted | Pod -> Node, Pod -> owner, Pod -> ConfigMap/Secret/PVC | container state transitions, readiness transitions, scheduling, deletion | spec, ownerRefs, labels affecting selection, resources, image, probes |
| Node | Ready, MemoryPressure, DiskPressure, PIDPressure, NetworkUnavailable, taints, capacity/allocatable changes | optional Node -> hosted Pod is queried through Pod -> Node, not duplicated | condition transitions and pressure changes | labels, taints, capacity/allocatable, provider/zone metadata |
| Event | reason, type, involved object, reporting controller, message fingerprint, count | involved object reference when resolvable | count/lastTimestamp changes for repeated Events | rollup bucket opened/updated/closed |
| EndpointSlice | endpoint readiness, serving/terminating when present, endpoint count | Service -> Pod via targetRef, EndpointSlice -> Pod optional for proof | endpoint readiness/serving/terminating transitions | membership added/removed, targetRef changed |
| Service | selector, type, ports, clusterIP/load balancer changes | Service -> EndpointSlice, selector-derived Service -> Pod fallback | load balancer ingress/status transitions | selector, ports, type, external traffic policy |
| Deployment/ReplicaSet | generation, observedGeneration, replica counts, rollout condition, template hash | ReplicaSet -> Deployment, Pod -> ReplicaSet | rollout condition and replica availability transitions | Pod template image/env/resources/probes/selectors |
| ConfigMap/Secret metadata | reference count when used, optional data hash by policy | Pod/workload -> ConfigMap/Secret | normally none | key set/hash changed, metadata changed |

### Pod Fast Path

Pods are high-volume and central to most investigations.

Special handling:

- Always detect UID changes, delete/recreate, scheduling changes, and readiness
  transitions.
- Extract status facts before deciding whether a change is worth storing.
- Write `object_changes` for meaningful status transitions, not only spec
  changes.
- Discard pure heartbeat changes only when status, owner refs, labels,
  annotations relevant to selection, spec, and placement are unchanged.
- Store Pod -> Node, Pod -> owner, and Pod -> ConfigMap/Secret/PVC edges from
  compact typed extraction.
- Preserve enough retained JSON to reconstruct evidence for selected versions.

### Node Summary

Nodes are large and status-heavy. Full JSON is useful as evidence, but every
status heartbeat should not become a high-cost query path.

Special handling:

- Extract condition transitions, allocatable/capacity changes, taints, labels,
  and pressure signals as facts.
- Write `object_changes` for condition, taint, capacity, allocatable, and
  topology-relevant label changes.
- Avoid indexing large status arrays generically.
- Use stronger compression or less frequent full snapshots for large Node
  documents when reconstruction latency remains bounded.
- Optionally downsample unchanged Node status observations after facts are
  extracted.

### Event Rollup

Events are high-churn and often repetitive.

Special handling:

- Mirror Event reason, type, involved object, reporting controller, count, and
  first/last timestamp into facts.
- Fingerprint messages to control cardinality.
- Write count changes as Event rollup change summaries so investigation results
  show repeated Event progression even after native Events expire.
- Roll up repeated Events with the same involved object, reason, and message
  fingerprint inside a short time bucket.
- Write change summaries when an Event rollup bucket is opened, materially
  incremented, or closed.
- Keep raw Event versions only according to retention policy; facts are the
  primary query path after native Kubernetes Event expiry.

### EndpointSlice Topology

EndpointSlices are the preferred source for Service membership.

Special handling:

- Extract namespace-qualified Service -> Pod edges from
  `endpoints[*].targetRef`.
- Extract endpoint readiness, serving, and terminating state as facts when
  present.
- Store edge interval changes instead of repeatedly writing identical service
  membership.
- Write change summaries for membership added/removed and readiness transitions.
- Treat endpoint address churn without targetRef changes as lower priority
  unless it affects readiness or topology evidence.

### Lease And Coordination Resources

Leases are usually high-churn and low troubleshooting value.

Special handling:

- Skip by default in the PoC, or downsample aggressively when enabled.
- Never let Lease churn starve Pod, Event, Node, or EndpointSlice processing.
- Record skipped/downsampled counts by GVR for transparency.

## Object Identity

Identity resolution:

```text
cluster_id + kind_id + metadata.uid
```

Fallback:

```text
cluster_id + kind_id + namespace + name
```

Delete/recreate with a new UID should become a new object while keeping the same
human-readable key.

## Topology Extraction

### Pod -> Node

Source:

```text
Pod.spec.nodeName
```

Edge:

```text
pod_on_node
```

### Pod -> ReplicaSet

Source:

```text
Pod.metadata.ownerReferences
```

Edge:

```text
pod_owned_by_replicaset
```

### ReplicaSet -> Deployment

Source:

```text
ReplicaSet.metadata.ownerReferences
```

Edge:

```text
replicaset_owned_by_deployment
```

### Service -> Pod

Preferred source:

```text
EndpointSlice.endpoints[*].targetRef
```

Edges:

```text
endpointslice_targets_pod
service_selects_pod
```

Selector-based matching is fallback only.

### Pod -> ConfigMap / Secret / PVC

Sources:

```text
volumes[*].configMap
volumes[*].secret
volumes[*].persistentVolumeClaim
envFrom[*].configMapRef
envFrom[*].secretRef
env[*].valueFrom.configMapKeyRef
env[*].valueFrom.secretKeyRef
```

## Fact Extraction

### Pod Status

Extract:

- `status.phase`
- container restart count changes
- `state.*.reason`
- `lastState.*.reason`
- readiness condition transitions
- start time and deletion time
- QoS class

High severity:

```text
OOMKilled
Evicted
Preempted
CrashLoopBackOff
ImagePullBackOff
Ready=False
```

### Workload Config

Extract from Pod template:

- image
- env and envFrom refs
- ConfigMap and Secret refs
- CPU/memory requests and limits
- liveness/readiness/startup probes
- labels and annotations that affect selection

Write config facts only on changes.

### Node Conditions

Extract:

- Ready
- MemoryPressure
- DiskPressure
- PIDPressure
- NetworkUnavailable when present

Write facts on transitions.

### Event Mirror

Mirror Kubernetes Events into `object_facts`:

- reason
- type
- involved object
- message fingerprint
- count
- first/last timestamp

Keep enough message data to search, but avoid unbounded cardinality from raw
messages.

## Change Summary Extraction

Generate `object_changes` from semantic diff summaries:

- spec image/resource/probe changes
- selector changes
- owner reference changes
- scheduling and placement changes
- status condition transitions

This supports timeline UI and fast "what changed near this time" queries.

## Rebuildability

Every extractor must be deterministic enough that facts and edges can be
rebuilt from stored versions.

Extractor version should be recorded so changes can trigger selective rebuilds.
