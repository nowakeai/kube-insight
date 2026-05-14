# Real-World Troubleshooting Cases

These are practical cases kube-insight is designed to handle. They are not
special-case product logic. Each case should be answerable through retained
history, extracted facts, topology edges, and read-only SQL.

The examples reference concrete patterns observed in real clusters, including
admission webhooks, GKE-managed webhooks, Rancher webhooks, external-secrets,
cert-manager, Flux, Kyverno, APIService backends, HPA, EndpointSlice, Pod, Node,
and Kubernetes Events.

## 1. Fail-Closed Admission Webhook Blocks Deployments

Symptom:

A GitOps controller reports apply or dry-run failures. By the time an operator
checks the cluster, the workload may be healthy and the webhook may already be
rolled back.

Why `kubectl` is weak after the fact:

- Events can expire.
- `kubectl get validatingwebhookconfiguration` only shows current config.
- The webhook Service, EndpointSlice, Pod readiness, and certificate chain must
  be correlated manually.

What kube-insight should show:

- `ValidatingWebhookConfiguration` or `MutatingWebhookConfiguration` with
  `failurePolicy=Fail`.
- The webhook Service from `admission_webhook.service`.
- EndpointSlice readiness and webhook Pod facts.
- Certificate/Secret facts when the webhook is TLS-backed.
- The exact version diff showing when the risky webhook existed.

Useful SQL:

```sql
select
  ok.kind,
  li.name as webhook_config,
  f.fact_key,
  f.fact_value,
  f.severity
from object_facts f
join latest_index li on li.object_id = f.object_id
join object_kinds ok on ok.id = f.kind_id
where ok.kind in ('ValidatingWebhookConfiguration', 'MutatingWebhookConfiguration')
  and f.fact_key in ('admission_webhook.failure_policy', 'admission_webhook.service')
order by f.severity desc, f.ts desc
limit 50;
```

Concrete signals seen in the PoC database:

- `warden-validating.config.common-webhooks.networking.gke.io`
- `rancher.cattle.io`
- `externalsecret-validate`
- `secretstore-validate`
- `sample-cluster-cert-manager-webhook`
- Kyverno validating webhook configurations

## 2. Policy Controller Generates Events, Then Events Expire

Symptom:

A deployment is rejected or repeatedly reconciled with warnings such as policy
violations. Later, only the healthy workload remains.

Why `kubectl` is weak after the fact:

- Events are short-lived and often rotated.
- Policy controller state may not identify all affected objects.
- Warning Events need to be joined to Deployments, ReplicaSets, or Pods.

What kube-insight should show:

- Historical `Event` facts: `k8s_event.reason`, `k8s_event.type`,
  `k8s_event.message_fingerprint`.
- `event_*` edges to involved resources.
- Current and historical versions of the involved Deployment/ReplicaSet/Pod.

Useful SQL:

```sql
select
  ev.namespace as event_namespace,
  ev.name as event_name,
  dst_kind.kind as involved_kind,
  dst.namespace as involved_namespace,
  dst.name as involved_name,
  f.fact_value as reason,
  datetime(f.ts / 1000, 'unixepoch') as event_time
from object_facts f
join latest_index ev on ev.object_id = f.object_id
join object_edges e on e.src_id = ev.object_id
join objects dst on dst.id = e.dst_id
join object_kinds dst_kind on dst_kind.id = dst.kind_id
where f.fact_key in ('k8s_event.reason', 'k8s_event.type')
  and (f.fact_value = 'Warning' or f.severity >= 60)
order by f.ts desc
limit 100;
```

Concrete signal seen in the PoC database:

- `PolicyViolation` warning Events in `zot-registry` tied to workload objects.

## 3. cert-manager Certificate Recovers Before Investigation

Symptom:

TLS or webhook calls fail briefly. Later the Certificate and Secret are healthy.

Why `kubectl` is weak after the fact:

- Current `Certificate` may be `Ready=True`.
- The old Secret may have been regenerated.
- The important reason can be hidden in a previous status condition.

What kube-insight should show:

- Historical `status_condition.Ready=False`.
- Reason facts such as `IncorrectIssuer`, `Pending`, or issuance failures.
- Edges from Certificate to Secret and Issuer/ClusterIssuer.
- Exact Certificate version diff proving recovery.

Useful SQL:

```sql
select
  ok.kind,
  li.namespace,
  li.name,
  f.fact_key,
  f.fact_value,
  f.severity,
  datetime(f.ts / 1000, 'unixepoch') as observed_at
from object_facts f
join latest_index li on li.object_id = f.object_id
join object_kinds ok on ok.id = f.kind_id
where ok.kind in ('Certificate', 'CertificateRequest', 'Issuer', 'ClusterIssuer')
  and f.fact_key like 'status_condition.%'
order by f.severity desc, f.ts desc
limit 100;
```

Concrete signal seen in the PoC database:

- Certificate `zot-registry/zot-registry-tls` with `Ready=False` and
  `IncorrectIssuer`.

## 4. CRD Conversion Webhook Breaks Custom Resources

Symptom:

Custom resources start failing validation, conversion, or reconciliation after
a controller upgrade.

Why `kubectl` is weak after the fact:

- CRD schemas are large and hard to diff manually.
- Conversion webhook Services may be fixed before investigation.
- Custom resources are often missed by hardcoded collectors.

What kube-insight should show:

- `CustomResourceDefinition` conversion webhook Service facts.
- `crd_conversion_webhook_uses_service` edges.
- CRD conversion/schema changes over time.
- Custom resource status conditions and Events.

Useful SQL:

```sql
select
  li.name as crd_name,
  ch.path,
  ch.old_scalar,
  ch.new_scalar,
  datetime(ch.ts / 1000, 'unixepoch') as changed_at
from object_changes ch
join latest_index li on li.object_id = ch.object_id
join object_kinds ok on ok.id = li.kind_id
where ok.kind = 'CustomResourceDefinition'
  and (ch.path like '%conversion%' or ch.path like '%schema%')
order by ch.ts desc
limit 100;
```

Concrete signals seen in the PoC database:

- external-secrets CRDs using `external-secrets/external-secrets-webhook`.

## 5. RBAC Rollback Hides Permission Loss

Symptom:

A controller reports `Forbidden` for several minutes. When checked later,
`kubectl auth can-i` says access is allowed.

Why `kubectl` is weak after the fact:

- `kubectl auth can-i` only answers current permission.
- Role and ClusterRole changes may have been rolled back.
- RoleBinding subject and roleRef chains are easy to miss manually.

What kube-insight should show:

- RoleBinding/ClusterRoleBinding facts for `rbac.role_ref` and `rbac.subject`.
- Edges from bindings to roles and subjects.
- Rule diffs on Role/ClusterRole.
- Warning Events from affected controllers.

Useful SQL:

```sql
select
  ok.kind as binding_kind,
  li.namespace,
  li.name as binding_name,
  f.fact_key,
  f.fact_value,
  datetime(f.ts / 1000, 'unixepoch') as observed_at
from object_facts f
join latest_index li on li.object_id = f.object_id
join object_kinds ok on ok.id = f.kind_id
where ok.kind in ('RoleBinding', 'ClusterRoleBinding')
  and f.fact_key like 'rbac.%'
order by f.ts desc
limit 100;
```

## 6. APIService Backend Breaks Aggregated API

Symptom:

Commands against a custom or aggregated API intermittently fail. Current
APIService status is healthy by the time the incident is reviewed.

Why `kubectl` is weak after the fact:

- APIService points to a Service, but the Service/EndpointSlice/Pod state must
  be checked separately.
- Status may have recovered.
- Errors are often seen in controller logs or kubectl output, not durable state.

What kube-insight should show:

- `apiservice_uses_service` edge.
- Historical APIService status condition facts.
- EndpointSlice and Pod readiness for the backing Service.
- Version diffs for APIService or backend Service changes.

Useful SQL:

```sql
select
  src.name as apiservice,
  e.edge_type,
  dst.namespace as service_namespace,
  dst.name as service_name
from object_edges e
join objects src on src.id = e.src_id
join object_kinds src_kind on src_kind.id = src.kind_id
join objects dst on dst.id = e.dst_id
where src_kind.kind = 'APIService'
  and e.edge_type = 'apiservice_uses_service'
order by e.valid_from desc
limit 50;
```

## 7. EndpointSlice Blackhole During Rollout

Symptom:

A Service returns no endpoints or routes to unready Pods for a short window.
Later the Service looks healthy.

Why `kubectl` is weak after the fact:

- Current EndpointSlices only show current endpoints.
- Pod readiness transitions may not be obvious after restart/recreate.
- Events may have expired.

What kube-insight should show:

- `endpointslice_for_service` and `endpointslice_targets_pod` edges.
- Endpoint readiness facts.
- Pod readiness and restart facts.
- Service investigation bundle with exact historical versions.

Useful SQL:

```sql
select
  svc.namespace as service_namespace,
  svc.name as service_name,
  pod.namespace as pod_namespace,
  pod.name as pod_name,
  e2.edge_type
from object_edges e1
join objects eps on eps.id = e1.src_id
join objects svc on svc.id = e1.dst_id
join object_edges e2 on e2.src_id = eps.id
join objects pod on pod.id = e2.dst_id
where e1.edge_type = 'endpointslice_for_service'
  and e2.edge_type = 'endpointslice_targets_pod'
limit 100;
```

## 8. OOMKilled Workload After Pod Was Recreated

Symptom:

A workload had a memory spike, restarted, and later all current Pods are
healthy.

Why `kubectl` is weak after the fact:

- Current Pod may be a different UID.
- Previous container state may be gone after recreation.
- Node pressure and colocated workload state require time correlation.

What kube-insight should show:

- Historical Pod facts such as `pod_status.last_reason=OOMKilled`.
- Pod -> Node edge at the time.
- Owner edges to ReplicaSet/Deployment.
- Node condition facts around the same window.

Useful SQL:

```sql
select
  li.namespace,
  li.name as pod_name,
  f.fact_value as last_reason,
  f.severity,
  datetime(f.ts / 1000, 'unixepoch') as observed_at
from object_facts f
join latest_index li on li.object_id = f.object_id
join object_kinds ok on ok.id = f.kind_id
where ok.kind = 'Pod'
  and f.fact_key = 'pod_status.last_reason'
order by f.severity desc, f.ts desc
limit 100;
```

Concrete signals seen in the PoC database:

- OOMKilled Pods in namespaces such as `loki`, `graphnode`, and application
  namespaces.

## 9. Node Condition Explains Broad Workload Churn

Symptom:

Multiple unrelated workloads restart or become unready at the same time.

Why `kubectl` is weak after the fact:

- Current Node may be Ready.
- Affected Pods may have moved.
- One has to manually correlate Node conditions with Pod placement history.

What kube-insight should show:

- Pod -> Node edges at the incident time.
- Node condition facts and reason facts.
- Same-node Pods with restart/readiness facts.

Useful SQL:

```sql
select
  li.name as node_name,
  f.fact_key,
  f.fact_value,
  f.severity,
  datetime(f.ts / 1000, 'unixepoch') as observed_at
from object_facts f
join latest_index li on li.object_id = f.object_id
join object_kinds ok on ok.id = f.kind_id
where ok.kind = 'Node'
  and (f.fact_key like 'status_condition.%' or f.fact_key like 'node_condition.%')
order by f.severity desc, f.ts desc
limit 100;
```

Concrete signal seen in the PoC database:

- `node_condition.KubeletConfigChanged=True` on a GKE node.

## 10. HPA Scales The Wrong Target Or Fails Metrics

Symptom:

A workload over- or under-scales. Current replicas look normal later.

Why `kubectl` is weak after the fact:

- HPA status is current-state oriented.
- The scale target, metrics availability, and workload rollout must be joined.
- Metric API/APIService health may have been transient.

What kube-insight should show:

- `hpa_scales_target` edge.
- HPA spec/status changes.
- APIService backend health for metrics APIs.
- Deployment/ReplicaSet changes near scaling decisions.

Useful SQL:

```sql
select
  hpa.namespace as hpa_namespace,
  hpa.name as hpa_name,
  e.edge_type,
  target_kind.kind as target_kind,
  target.namespace as target_namespace,
  target.name as target_name
from object_edges e
join objects hpa on hpa.id = e.src_id
join objects target on target.id = e.dst_id
join object_kinds target_kind on target_kind.id = target.kind_id
where e.edge_type = 'hpa_scales_target'
order by e.valid_from desc
limit 100;
```

## 11. Secret Rotation Breaks A Pod Or Webhook

Symptom:

Pods or webhooks briefly fail after a Secret rotation. Later the Secret exists
and keys look correct.

Why `kubectl` is weak after the fact:

- Secret values should not be stored directly.
- The important evidence is often key presence, references, and object versions.
- Consumers may have restarted or reloaded after the issue.

What kube-insight should show:

- Secret metadata and key set, with values redacted or generated.
- Pod/workload/webhook edges to the Secret.
- Certificate or Pod facts around the same window.
- Version diff showing key-set changes, not secret value exposure.

Useful SQL:

```sql
select
  src_kind.kind as source_kind,
  src.namespace as source_namespace,
  src.name as source_name,
  e.edge_type,
  dst.namespace as secret_namespace,
  dst.name as secret_name
from object_edges e
join objects src on src.id = e.src_id
join object_kinds src_kind on src_kind.id = src.kind_id
join objects dst on dst.id = e.dst_id
where e.edge_type in ('pod_uses_secret', 'workload_template_uses_secret', 'certmanager_writes_secret')
order by e.valid_from desc
limit 100;
```

## 12. Cloud Or Platform Webhook Policy Blocks Namespaces

Symptom:

Namespace create/update/delete, Gateway API, or platform CRD operations fail
briefly due to a managed policy webhook.

Why `kubectl` is weak after the fact:

- Managed webhooks may be cluster-scoped and not obvious from the workload.
- Policies can be reverted before the operator investigates.
- Error messages often appear only in Events or controller logs.

What kube-insight should show:

- Fail-closed platform webhook configuration.
- Webhook rules indicating affected resources.
- Warning Events and affected resources.
- Version diff showing rule, selector, timeout, or failurePolicy changes.

Concrete signals seen in the PoC database:

- GKE Warden validating webhook.
- Gateway API deprecation webhook.
- Rancher namespace and cluster webhooks.
