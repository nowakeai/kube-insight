# Query Patterns

For ClickHouse-compatible backends, prefer the schema recipes returned by
`kube_insight_schema`. These patterns show the intended shape; adapt table and
column names only after reading the active schema.

## Coverage

```sql
with latest as (
  select cluster_id, api_group, api_version, resource, kind,
         argMax(status, updated_at) as status,
         argMax(error, updated_at) as error,
         max(updated_at) as latest_update
  from ingestion_offsets
  group by cluster_id, api_group, api_version, resource, kind
)
select cluster_id, api_group, api_version, resource, kind, status, error, latest_update
from latest
where status in ('not_started','queued','retrying','list_error','watch_error')
order by latest_update desc
limit 50;
```

## Recent Fact Rollup

```sql
select cluster_id, kind, namespace, name, fact_key, fact_value,
       count() as rows,
       min(ts) as first_seen,
       max(ts) as last_seen
from facts
where ts >= toDateTime64('2026-05-25 00:00:00', 3, 'UTC')
  and kind = 'Pod'
  and fact_key = 'pod_status.last_reason'
  and fact_value = 'OOMKilled'
group by cluster_id, kind, namespace, name, fact_key, fact_value
order by rows desc, last_seen desc
limit 50;
```

## Exact Recent Changes

```sql
select cluster_id, kind, namespace, name, change_family, path, severity,
       count() as changes,
       min(ts) as first_seen,
       max(ts) as last_seen,
       any(old_scalar) as sample_old,
       any(new_scalar) as sample_new
from changes
where ts >= toDateTime64('2026-05-25 00:00:00', 3, 'UTC')
  and kind = 'Deployment'
  and namespace = 'default'
  and name = 'api'
group by cluster_id, kind, namespace, name, change_family, path, severity
order by last_seen desc, changes desc
limit 50;
```

If this answers an exact recent-change question, stop. Do not expand to Pods,
Events, topology, OOM, root cause, or spec-only follow-up unless the user asks.

## Allocation Or Requests/Limits

Use the schema recipe named `container_resource_allocation_rollup` when present.
If facts do not carry resource configuration, use one scoped `observations.doc`
profile or recipe such as `raw_doc_field_profile` before fetching proof rows.
Avoid repeated JSON syntax probing.
