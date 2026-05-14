# Agent SQL RBAC Filtering

Agent SQL access is powerful and must not bypass Kubernetes RBAC.

The current PoC exposes read-only SQL for local development. Before this is
served to untrusted users, API and MCP queries must be rewritten or constrained
so every returned row is authorized for the caller.

## Goal

```text
An agent can ask flexible SQL questions, but the answer contains only resources,
facts, changes, edges, and versions the caller could read through Kubernetes.
```

## Enforcement Shape

Use three layers:

1. Authenticate the caller and resolve Kubernetes user, groups, extras, and
   target cluster.
2. Build an authorization scope from Kubernetes SelfSubjectAccessReview or
   SubjectAccessReview results.
3. Execute SQL through authorized views or a query rewriter, never directly
   against base tables for remote users.

## Authorized Object View

Every query should be constrained by authorized object IDs:

```sql
authorized_objects(object_id)
```

That set is built from resource permissions:

```text
list pods in namespace default -> all matching Pod objects in default
get pod default/api-1 -> only that Pod object
get nodes cluster-scope -> matching Node objects
```

The base view is:

```sql
select o.id
from objects o
join object_kinds ok on ok.id = o.kind_id
join api_resources ar on ar.id = ok.api_resource_id
join clusters c on c.id = o.cluster_id
where allowed(c.name, ar.api_group, ar.resource, o.namespace, o.name)
```

`allowed(...)` is conceptual. SQLite PoC can materialize a temp table. Postgres
and Cockroach can use session-local temp tables, CTEs, or security-barrier
views.

## Table Rules

| Table | Filter rule |
| --- | --- |
| `latest_index` | `object_id in authorized_objects` |
| `objects` | `id in authorized_objects` |
| `versions` | `object_id in authorized_objects` |
| `object_facts` | `object_id in authorized_objects` and related object IDs if present |
| `object_changes` | `object_id in authorized_objects` |
| `object_edges` | `src_id in authorized_objects and dst_id in authorized_objects` |
| `blobs` | only reachable through authorized `versions.blob_ref` |
| `filter_decisions` | filter by authorized resource scope before exposing |
| `api_resources` | safe to expose, but may reveal installed CRDs; make this policy-controlled |
| `ingestion_offsets` | safe for operators; policy-controlled for tenant users |

## SQL Interface Contract

Remote API/MCP SQL must not execute arbitrary SQL against base tables. It should
either:

- expose only authorized views with the same logical column names, or
- rewrite user SQL to inject joins against `authorized_objects`.

The safer first version is view-based:

```text
latest_index -> visible_latest_index
objects -> visible_objects
versions -> visible_versions
object_facts -> visible_object_facts
object_changes -> visible_object_changes
object_edges -> visible_object_edges
```

Agents can still inspect schema, but schema should describe visible views for
remote identities.

## Edge Leakage

An edge leaks both endpoints. Default rule:

```text
return edge only when src and dst are both authorized
```

A future UI may show a hidden endpoint marker, but SQL tools should start with
strict omission.

## Historical RBAC

MVP uses current RBAC:

```text
If the caller can read the resource now, they can read retained history for the
same object.
```

Historical RBAC replay is future work and requires retaining RBAC state over
time.
