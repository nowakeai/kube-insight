# Facts Catalog

`object_facts` in SQLite and `facts` in ClickHouse store compact investigation
predicates extracted from retained Kubernetes observations. Versions remain the
proof source; facts are the fast candidate and aggregation path.

Keep this catalog updated when adding, renaming, or removing extractor facts.

## Storage Shape

Common logical fields:

- `cluster_id`, `ts`, `object_id`, `kind`, `namespace`, `name`
- `fact_key`, `fact_value`
- optional `numeric_value`
- `severity`
- optional compact `detail`

SQLite stores object/kind/cluster references through local integer ids.
ClickHouse stores the same logical values directly in append-only evidence
tables. `detail` must stay small; full object proof belongs in retained
versions.

## Current Fact Families

| Fact family | Source extractor | Example keys | Purpose | Growth risk |
| --- | --- | --- | --- | --- |
| Pod status | `PodExtractor` | `pod_status.phase`, `pod_status.ready`, `pod_status.restart_count`, `pod_status.reason`, `pod_status.last_reason` | OOM/restart/readiness triage, Pod state ranking, workload impact candidates. | Medium: emitted per retained Pod status observation; restart counts can repeat across unchanged snapshots unless upstream storage dedupe suppresses unchanged content. |
| Pod placement | `PodExtractor` | `pod_placement.node_assigned`, `pod_placement.deleted` | Node impact joins and lifecycle evidence. | Low to medium: one node assignment per retained Pod version plus delete facts. |
| Node condition | `NodeExtractor` | `node_condition.Ready`, `node_condition.MemoryPressure`, `node_condition.DiskPressure`, `node_condition.PIDPressure` | Node health, pressure, and availability triage. | Medium: conditions are repeated in Node status snapshots; dedupe and retention should control old unchanged versions. |
| Node resource capacity | `NodeExtractor` | `node_capacity.cpu`, `node_capacity.memory`, `node_capacity.pods`, `node_capacity.ephemeral_storage` | Current and historical node inventory/capacity answers without raw-doc scans. | Medium: several facts per retained Node version; useful enough to keep because capacity questions are common and expensive through raw docs. |
| Node allocatable resources | `NodeExtractor` | `node_allocatable.cpu`, `node_allocatable.memory`, `node_allocatable.pods`, `node_allocatable.ephemeral_storage` | Schedulable capacity and allocatable-vs-capacity comparisons. | Medium, same as node capacity. |
| Service core | `ServiceExtractor` | `service.type`, `service.cluster_ip`, `service.deleted` | Service discovery, deleted-service proof, and LoadBalancer triage. | Low. |
| Service LoadBalancer | `ServiceExtractor` | `service.load_balancer.ingress_count`, `service.load_balancer.pending`, `service.load_balancer.ingress_ip`, `service.load_balancer.ingress_hostname` | Pending/exposed LoadBalancer investigation and external endpoint proof. | Low to medium: ingress values are usually small; ClickHouse has a maintenance backfill for older retained Service versions. |
| Kubernetes Event | `EventExtractor` | `k8s_event.reason`, `k8s_event.type`, `k8s_event.action`, `k8s_event.reporting_controller`, `k8s_event.reporting_instance`, `k8s_event.count`, `k8s_event.series_count`, `k8s_event.message_fingerprint`, `k8s_event.message_preview` | Warning/Event ranking after native Event expiry, OOMKilling and controller failure discovery. | High: Events are high-cardinality and frequent. Message previews are intentionally capped to 512 runes and excluded from SQLite key/value/time index. |
| Endpoint readiness | `EndpointSliceExtractor` | `endpoint.ready`, `endpoint.serving`, `endpoint.terminating` | Service endpoint health and readiness proof. | Medium: one set per endpoint in retained EndpointSlice versions. |
| Generic status conditions | `ReferenceExtractor` | `status_condition.Ready`, `status_condition.Ready.reason`, `status_condition.Synced`, `status_condition.Reconciled` | CRD and controller health without custom extractor code. | Medium to high: condition type/reason cardinality grows with CRDs; keep because it is the generic health path. |
| RBAC binding | `ReferenceExtractor` | `rbac.role_ref`, `rbac.subject` | Security/RBAC investigation candidates. | Low to medium: grows with subjects per binding. |
| Admission webhook | `ReferenceExtractor` | `admission_webhook.name`, `admission_webhook.failure_policy`, `admission_webhook.service` | Admission outage and dependency investigation. | Low. |

## Not Currently Stored As Facts

- Container requests/limits are not stored as facts today. Complex resource
  allocation questions use ClickHouse schema recipes over retained Pod docs,
  especially `pod_resource_rows_for_js` and
  `namespace_resource_delta_for_js`.
- PVC requested/capacity storage history is not stored as facts today. PVC
  resize questions use retained PVC versions and the
  `pvc_storage_history_for_js` recipe.
- Arbitrary labels and annotations are not globally exploded into facts. Query
  raw retained versions only after kind/namespace/name/time predicates are tight.

## Add/Remove Checklist

Before adding a fact:

- Confirm it is an investigation predicate or aggregation input, not only proof.
- Estimate row multiplier per retained version.
- Prefer numeric values in `numeric_value` when the fact is commonly summed,
  ranked, or compared.
- Keep `fact_value` bounded and avoid full messages or raw JSON.
- Add or update a smoke/eval case when the fact is expected to change agent
  behavior.
- Update this catalog and any schema recipes that should prefer the new fact.

Before removing a fact:

- Check MCP schema recipes, agent prompts, skill docs, and live smoke cases.
- Keep retained versions as proof fallback, but do not remove the only fast path
  for common high-value questions such as OOM/restarts, node capacity, Service
  LoadBalancer state, endpoint readiness, or Event rollups.
