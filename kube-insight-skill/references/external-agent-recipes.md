# External Agent Recipes

These recipes are intended minimum plans for generic agents using kube-insight
without the built-in agent. Replace placeholder timestamps, namespace/name, and
database paths with values from the user request and client context. If MCP is
available, call the named MCP tools with equivalent arguments; otherwise use the
CLI fallback.

## OOM Existence

User intent: "Was there an OOM recently?" or a follow-up such as "what about the
last hour?"

MCP plan:

```json
[
  {"tool": "kube_insight_health", "arguments": {}},
  {
    "tool": "kube_insight_search",
    "arguments": {
      "query": "OOMKilled",
      "kind": "Pod",
      "from": "2026-05-26T14:00:00Z",
      "to": "2026-05-26T15:00:00Z",
      "includeBundles": true,
      "limit": 5
    }
  }
]
```

CLI equivalent:

```bash
./bin/kube-insight db resources health --db kubeinsight.db --errors-only
./bin/kube-insight query search OOMKilled --db kubeinsight.db \
  --kind Pod \
  --from 2026-05-26T14:00:00Z \
  --to 2026-05-26T15:00:00Z \
  --include-bundles \
  --limit 5
```

Stop after the search. If it returns matches, answer with Pod identity,
timestamps, fact/change evidence, and coverage caveats. If it returns zero and
coverage is incomplete, answer that kube-insight cannot prove absence for the
uncovered streams.

## Exact Recent Change

User intent: "What changed for this Deployment in the last 24 hours?"

MCP plan:

```json
[
  {"tool": "kube_insight_health", "arguments": {}},
  {"tool": "kube_insight_schema", "arguments": {}},
  {
    "tool": "kube_insight_sql",
    "arguments": {
      "maxRows": 50,
      "sql": "<one recent-change rollup SQL query shaped like the CLI example below>"
    }
  }
]
```

If the active schema is SQLite, adapt this shape to the SQLite tables and
timestamp functions shown by `kube_insight_schema`. After this rollup returns
rows, answer only the observed changes and coverage. Do not add Pods, Events,
topology, OOM, or root-cause checks unless the user asks.

CLI equivalent for a ClickHouse-compatible backend:

```bash
./bin/kube-insight query schema --db kubeinsight.db
./bin/kube-insight query sql --db kubeinsight.db --max-rows 50 --sql "
select cluster_id, kind, namespace, name, change_family, path, severity,
       count() as changes,
       min(ts) as first_seen,
       max(ts) as last_seen,
       any(old_scalar) as sample_old,
       any(new_scalar) as sample_new
from changes
where ts >= toDateTime64('2026-05-25 15:00:00', 3, 'UTC')
  and ts < toDateTime64('2026-05-26 15:00:00', 3, 'UTC')
  and kind = 'Deployment'
  and namespace = 'default'
  and name = 'api'
group by cluster_id, kind, namespace, name, change_family, path, severity
order by last_seen desc, changes desc
limit 50"
```

## Allocation Or Requests/Limits

User intent: "Show resource allocation, not actual usage."

MCP plan:

```json
[
  {"tool": "kube_insight_health", "arguments": {}},
  {"tool": "kube_insight_schema", "arguments": {}}
]
```

Then call `kube_insight_sql` with the SQL copied from the schema recipe named
`container_resource_allocation_rollup`, after replacing placeholder cluster,
namespace, and time filters. The final SQL call should look like this shape:

```json
[
  {
    "tool": "kube_insight_sql",
    "arguments": {
      "maxRows": 100,
      "sql": "<schema-provided container_resource_allocation_rollup SQL>"
    }
  }
]
```

If the schema does not expose a ready allocation recipe or facts do not carry
request/limit keys, run one scoped raw-document field profile first, then one
proof query. Stop once request/limit rows or snippets are available. Do not pivot
to live usage, Node capacity, OOM root cause, or topology unless asked.

CLI equivalent:

```bash
./bin/kube-insight query schema --db kubeinsight.db
./bin/kube-insight query sql --db kubeinsight.db --max-rows 100 --sql \
  '<schema-provided container_resource_allocation_rollup SQL>'
```

## Exact Service Health

User intent: "Is namespace/name Service healthy? Are endpoints ready?"

MCP plan:

```json
[
  {"tool": "kube_insight_health", "arguments": {}},
  {
    "tool": "kube_insight_service_investigation",
    "arguments": {"namespace": "default", "name": "api"}
  }
]
```

This is terminal when the bundle includes Service, EndpointSlice, Pod, and Event
evidence relevant to the question. Do not add search, schema, SQL, or topology
unless that bundle is missing the requested proof.

CLI equivalent:

```bash
./bin/kube-insight db resources health --db kubeinsight.db --errors-only
./bin/kube-insight query service api --db kubeinsight.db \
  --namespace default \
  --from 2026-05-26T14:00:00Z \
  --to 2026-05-26T15:00:00Z \
  --max-evidence-objects 20 \
  --max-versions-per-object 2
```
