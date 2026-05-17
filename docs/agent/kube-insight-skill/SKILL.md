---
name: kube-insight
description: Use kube-insight to investigate Kubernetes history, collector coverage, retained facts, topology edges, and object versions through CLI, HTTP API, or MCP tools.
---

# kube-insight Agent Skill

Use this skill when the user asks what happened in a Kubernetes cluster, why an
object changed, whether an Event or relationship existed earlier, or how current
kubectl output compares with retained historical evidence.

## Operating Rules

- Start with coverage. A missing fact is not meaningful until collector health
  is healthy for the resource types involved.
- Determine cluster scope first and keep the selected `cluster_id` in follow-up SQL.
- Use facts and edges to find candidate objects; use retained versions as proof.
- Treat `object_observations` as the observation trail and `versions` as retained
  content changes.
- Treat `objects.deleted_at` as the kube-insight delete observation time, not
  Kubernetes `metadata.deletionTimestamp`.
- Keep SQL read-only. Use `SELECT`, `WITH`, or `EXPLAIN` only.
- Always call `kube_insight_schema` or `query schema` before writing SQL. The
  schema notes identify whether the active backend is SQLite or
  ClickHouse-compatible, and the table names differ.
- Bound exploratory queries with `limit`, exact `fact_key` predicates, and
  `cluster_id`.

## Backend Detection

Agents should not guess the SQL shape from memory. Detect it at runtime:

- MCP route: call `kube_insight_schema` first.
- CLI route: run `./bin/kube-insight query schema ...` first.
- SQLite schema notes say `Active SQL backend: SQLite` and expose tables such as
  `object_facts`, `object_edges`, `object_changes`, `object_observations`, and
  `latest_index`.
- ClickHouse/chDB schema notes say `Active SQL backend: ClickHouse-compatible`
  and expose tables such as `facts`, `edges`, `changes`, `observations`,
  `versions`, `api_resources`, and `ingestion_offsets`.
- Prefer typed tools (`kube_insight_health`, `kube_insight_history`, API search,
  API topology, and service investigation) when available; use raw SQL for
  custom joins and exploratory evidence queries.

## MCP Tools

Prefer MCP tools when connected to a running kube-insight MCP server. MCP follows
the configured `storage.driver` for SQLite, ClickHouse, and chDB-enabled builds:

- `kube_insight_health`: check coverage, errors, stale resources, and skipped
  resource types.
- `kube_insight_schema`: inspect the active backend, tables, columns, indexes,
  relationships, and query recipes.
- `kube_insight_sql`: run read-only evidence SQL for the active backend.
- `kube_insight_history`: fetch retained versions, observations, and diffs for a
  specific object.

Useful MCP prompts:

- `kube_insight_coverage_first`
- `kube_insight_event_history`
- `kube_insight_object_history`

## CLI Fallback

When MCP is not available, use the CLI:

```bash
./bin/kube-insight db resources health --db kubeinsight.db --errors-only
./bin/kube-insight query schema --db kubeinsight.db
./bin/kube-insight query sql --db kubeinsight.db --max-rows 50 --sql 'select id, name, source from clusters order by id'
./bin/kube-insight query history --db kubeinsight.db --kind Pod --namespace default --name example --max-versions 10 --max-observations 50
```

## Investigation Flow

0. Detect active backend:

```text
Call kube_insight_schema and read notes. Use SQLite examples only when the
schema exposes object_facts/object_edges. Use ClickHouse examples when the
schema exposes facts/edges/versions.
```

1. Check health. Prefer `kube_insight_health`; when using SQL, choose the
   backend-specific shape:

```sql
-- SQLite
select c.name as cluster, ar.api_group, ar.api_version, ar.resource, ar.kind,
       coalesce(io.status, 'not_started') as status, io.error
from clusters c
join api_resources ar on ar.removed_at is null
left join ingestion_offsets io
  on io.cluster_id = c.id
 and io.api_resource_id = ar.id
where coalesce(io.status, 'not_started') in ('not_started','retrying','list_error','watch_error')
order by status, ar.api_group, ar.resource
limit 50;

-- ClickHouse-compatible
select cluster_id, api_group, api_version, resource, kind,
       argMax(status, updated_at) as status, argMax(error, updated_at) as error
from ingestion_offsets
group by cluster_id, api_group, api_version, resource, kind
having status in ('retrying','list_error','watch_error')
order by cluster_id, api_group, resource
limit 50;
```

2. Select cluster. SQLite commonly exposes `clusters`; ClickHouse-compatible
   stores keep `cluster_id` directly on evidence rows, so use the returned
   schema and recent evidence rows to choose scope:

```sql
-- SQLite
select id, name, source from clusters order by id;

-- ClickHouse-compatible
select cluster_id, count() as rows
from versions
group by cluster_id
order by rows desc;
```

3. Find candidates by facts. Use the table shape reported by schema:

```sql
select datetime(ts / 1000, 'unixepoch') as seen_at,
       namespace, name, fact_key, fact_value, severity
from object_facts
where cluster_id = 1
  and fact_key = 'k8s_event.reason'
  and fact_value = 'PolicyViolation'
order by ts desc
limit 50;

-- ClickHouse-compatible
select ts as seen_at, object_id, fact_key, fact_value, severity
from facts
where cluster_id = 'c1'
  and fact_key = 'k8s_event.reason'
  and fact_value = 'PolicyViolation'
order by ts desc
limit 50;
```

4. Follow topology edges:

```sql
select src.name as source_name, e.edge_type, dst.namespace, dst.name as target_name
from object_edges e
join objects src on src.id = e.src_id
join objects dst on dst.id = e.dst_id
where e.cluster_id = 1
order by e.valid_from desc
limit 50;

-- ClickHouse-compatible
select edge_type, src_id, dst_id, valid_from, valid_to
from edges
where cluster_id = 'c1'
order by valid_from desc
limit 50;
```

5. Pull retained history for proof with `kube_insight_history` or
   `query history`.

## Output Style

When answering, separate:

- Evidence found: concrete facts, edges, versions, and timestamps.
- Coverage limits: stale or failed collectors that weaken the conclusion.
- Current-state comparison: kubectl/live apiserver output, if used.
- Next checks: the smallest query or object history request that would reduce
  uncertainty.
