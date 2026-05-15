# Insight vs kubectl Benchmark Notes

Last updated: 2026-05-15.

This document records a point-in-time validation against
a sanitized GKE workload cluster using the local `kubeinsight.db`.

The goal is not to claim a universal speedup. The useful product claim is more
specific:

- kube-insight answers historical and cross-resource questions from retained
  evidence.
- kubectl answers current apiserver state and current Kubernetes Events.
- For agent workflows, kube-insight exposes SQL-ready facts and edges so the
  agent does not need to repeatedly list and join many resource types.

## Reproduce

Build the CLI and run the benchmark helper:

```bash
make build
./scripts/benchmark-insight-vs-kubectl.sh \
  kubeinsight.db \
  <kubectl-context>
```

The script writes command output under `testdata/generated/` and records elapsed
time in `summary.tsv`. Set `GCLOUD_SDK_BIN=/path/to/google-cloud-sdk/bin` if the
GKE auth plugin is not on `PATH`. Set `INSIGHT_CLUSTER_ID` when the target
cluster is not `clusters.id = 1` in the local database.

## Dataset

After `db reindex`:

| Data | Rows |
| --- | ---: |
| Objects | 34,241 |
| Versions | 42,101 |
| Facts | 265,711 |
| Edges | 97,670 |
| Changes | 45,365 |
| Filter decisions | 596 |
| Filter decision rollups | 865 |

Event facts:

| Fact | Rows |
| --- | ---: |
| `k8s_event.type` | 28,451 |
| `k8s_event.reason` | 28,451 |
| `k8s_event.message_preview` | 28,449 |
| `k8s_event.message_fingerprint` | 28,449 |
| `k8s_event.reporting_controller` | 27,513 |
| `k8s_event.reporting_instance` | 25,643 |
| `k8s_event.action` | 22,470 |

## Event History Case

Question:

> Which warning Events show policy admission failures, what resources did they
> affect, and what policy/message caused them?

kube-insight SQL joins `object_facts` and `object_edges`:

```sql
select
  datetime(reason.ts/1000,'unixepoch') as event_time,
  reason.fact_value as reason,
  preview.fact_value as message_preview,
  dst_kind.kind as target_kind,
  coalesce(dst.namespace,'') as target_namespace,
  dst.name as target_name
from object_facts reason
join objects ev on ev.id = reason.object_id
join object_edges e
  on e.src_id = ev.id
 and e.edge_type in (
   'event_regarding_object',
   'event_related_object',
   'event_involves_object'
 )
join objects dst on dst.id = e.dst_id
join object_kinds dst_kind on dst_kind.id = dst.kind_id
left join object_facts preview
  on preview.version_id = reason.version_id
 and preview.fact_key = 'k8s_event.message_preview'
where reason.fact_key = 'k8s_event.reason'
  and reason.cluster_id = 1
  and reason.fact_key <> 'k8s_event.message_preview'
  and reason.fact_value in (
    'PolicyViolation',
    'FailedCreate',
    'FailedScheduling',
    'BackOff',
    'Unhealthy',
    'FailedMount'
  )
order by reason.ts desc
limit 20;
```

Observed result:

- Returned `PolicyViolation` Events tied to `Pod`, workload `App`, and
  `ValidatingAdmissionPolicy` targets.
- Message preview included the failing policy
  `validating-node-p4sa-audience` and the CEL error.
- Runtime: 28 ms on the local SQLite DB with cluster-scoped SQL.

## Current kubectl Comparison

Current apiserver counts:

```bash
kubectl get events.events.k8s.io -A \
  --field-selector type=Warning \
  -o name | wc -l

kubectl get events.events.k8s.io -A \
  --field-selector type=Warning,reason=PolicyViolation \
  -o name | wc -l
```

Observed:

| Source | Query | Count |
| --- | --- | ---: |
| kubectl/apiserver | current Warning Events | 1,850 |
| kubectl/apiserver | current PolicyViolation Events | 1,776 |
| kube-insight | retained PolicyViolation Events | 22,470 |

kube-insight retained `PolicyViolation` window:

| First seen | Latest seen | Rows |
| --- | --- | ---: |
| 2026-05-14 16:31:19 | 2026-05-15 04:17:52 | 22,470 |

The important difference is historical completeness. kubectl can still show the
current live failure. kube-insight can also answer what happened earlier in the
retained window and join those Events to affected resources without additional
apiserver list calls.

Observed helper timings:

| Benchmark | Elapsed |
| --- | ---: |
| insight retained PolicyViolation count | 204 ms |
| insight Event-to-resource edge sample | 28 ms |
| kubectl current Warning Event count | 3,176 ms |
| kubectl current PolicyViolation count | 3,050 ms |

## Storage Notes

`db reindex` rebuilt derived evidence and increased facts from 133,187 to
265,711. Most of the growth came from Event message previews and reporting
fields.

`k8s_event.message_preview` is intentionally stored for triage, but it is not a
good value for the generic exact-match fact index. The SQLite schema now keeps
it out of `object_facts_key_value_time_idx`.

Observed index change:

| Object | Before | After |
| --- | ---: | ---: |
| `object_facts_key_value_time_idx` | 43.6 MB | 14.3 MB |

A lightweight `object_facts_key_time_idx` supports agent queries that first find
candidate rows by fact key, for example latest Event message previews, without
indexing the preview text itself.

After compact:

| File | Size |
| --- | ---: |
| `kubeinsight.db` | 300 MB |
| `kubeinsight.db-wal` | 0 B |

## Query Plan Notes

Exact key/value queries are fastest when the SQL includes `cluster_id` and avoids
long preview facts:

```sql
where reason.cluster_id = 1
  and reason.fact_key <> 'k8s_event.message_preview'
  and reason.fact_key = 'k8s_event.reason'
  and reason.fact_value = 'PolicyViolation'
```

This lets SQLite use `object_facts_key_value_time_idx`.

For exploratory agent queries that do not know `cluster_id` yet, start with
`clusters`, then narrow subsequent queries by `cluster_id`.

For latest-by-fact exploratory queries, SQLite can use
`object_facts_key_time_idx`:

```sql
select fact_value
from object_facts
where fact_key = 'k8s_event.message_preview'
order by ts desc
limit 20;
```
