# Agent SQL Cookbook

Agents should start with `query schema`, then use read-only SQL to find
candidates. Wrapper commands are convenience shortcuts; SQL is the primary
agent primitive.

```bash
kube-insight query schema --db kubeinsight.db
kube-insight query sql --db kubeinsight.db --max-rows 50 --sql '...'
```

The same primitives are available over the read-only HTTP API:

```bash
kube-insight serve api --db kubeinsight.db --listen 127.0.0.1:8080
curl http://127.0.0.1:8080/api/v1/schema
curl -X POST http://127.0.0.1:8080/api/v1/sql \
  -H 'content-type: application/json' \
  -d '{"sql":"select name from latest_index limit 10","maxRows":10}'
curl http://127.0.0.1:8080/api/v1/health
curl 'http://127.0.0.1:8080/api/v1/history?kind=Pod&namespace=default&name=api-1&maxVersions=5&maxObservations=20'
```

All timestamps are Unix milliseconds. The examples below intentionally use
tables that are stable across SQLite, Postgres, and Cockroach-oriented designs:
`clusters`, `api_resources`, `object_kinds`, `objects`, `latest_index`,
`object_facts`, `object_changes`, `object_edges`, `versions`,
`object_observations`, and `blobs`.

## Coverage First

Before making a strong claim, inspect collector health:

```sql
select
  c.name as cluster,
  ar.api_group,
  ar.api_version,
  ar.resource,
  coalesce(io.status, 'not_started') as status,
  io.error,
  datetime(io.updated_at / 1000, 'unixepoch') as updated_at
from clusters c
join api_resources ar on ar.removed_at is null
left join ingestion_offsets io
  on io.cluster_id = c.id
 and io.api_resource_id = ar.id
where coalesce(io.status, 'not_started') in ('not_started', 'retrying', 'list_error', 'watch_error')
order by status, ar.api_group, ar.resource
limit 50;
```

## Webhook Broke GitOps

Find admission webhooks that fail closed and the Service they call:

```sql
select
  c.name as cluster,
  ok.kind,
  li.name as webhook_config,
  f.fact_key,
  f.fact_value,
  f.severity,
  datetime(f.ts / 1000, 'unixepoch') as observed_at
from object_facts f
join clusters c on c.id = f.cluster_id
join latest_index li on li.object_id = f.object_id
join object_kinds ok on ok.id = f.kind_id
where ok.kind in ('ValidatingWebhookConfiguration', 'MutatingWebhookConfiguration')
  and f.fact_key in ('admission_webhook.failure_policy', 'admission_webhook.service')
order by f.severity desc, f.ts desc
limit 100;
```

Connect webhook error Events to likely webhook configs by text:

```sql
select
  ev.namespace,
  ev.name as event_name,
  reason.fact_value as event_reason,
  ev.doc as event_doc,
  datetime(reason.ts / 1000, 'unixepoch') as event_time
from object_facts f
join latest_index ev on ev.object_id = f.object_id
join object_kinds evk on evk.id = f.kind_id
join object_facts reason
  on reason.object_id = ev.object_id
 and reason.fact_key = 'k8s_event.reason'
where evk.kind = 'Event'
  and f.fact_key = 'k8s_event.message_fingerprint'
  and lower(ev.doc) like '%webhook%'
order by reason.ts desc
limit 50;
```

## Events And Involved Resources

Show warning Events and the object they point at through extracted edges:

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

## RBAC Role And Binding Chain

Find RoleBinding or ClusterRoleBinding changes and their referenced roles or
subjects:

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

Find role rule mutations during an outage window:

```sql
select
  ok.kind,
  o.namespace,
  o.name,
  ch.path,
  ch.old_scalar,
  ch.new_scalar,
  datetime(ch.ts / 1000, 'unixepoch') as changed_at
from object_changes ch
join objects o on o.id = ch.object_id
join object_kinds ok on ok.id = o.kind_id
where ok.kind in ('Role', 'ClusterRole')
  and (ch.path like '%rules%' or ch.change_family = 'rbac')
order by ch.ts desc
limit 100;
```

## cert-manager Certificate Chain

Find unhealthy or recently changed cert-manager Certificates and related
Secrets or Issuers:

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

Follow extracted cert-manager edges:

```sql
select
  src_kind.kind as source_kind,
  src.namespace as source_namespace,
  src.name as source_name,
  e.edge_type,
  dst_kind.kind as target_kind,
  dst.namespace as target_namespace,
  dst.name as target_name
from object_edges e
join objects src on src.id = e.src_id
join object_kinds src_kind on src_kind.id = src.kind_id
join objects dst on dst.id = e.dst_id
join object_kinds dst_kind on dst_kind.id = dst.kind_id
where e.edge_type like 'certmanager_%'
order by e.valid_from desc
limit 100;
```

## CRD Schema Or Conversion Regression

Find CRD conversion webhook changes:

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

Find custom resources with degraded status conditions after a CRD change:

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
where f.fact_key like 'status_condition.%'
  and f.severity >= 50
order by f.ts desc
limit 100;
```
