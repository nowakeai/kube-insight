# Kubernetes RBAC Inheritance

## Goal

`kube-insight` must not become a privilege escalation path.

If a user cannot access a Kubernetes resource through the Kubernetes API server,
the user must not be able to access the same resource, its historical versions,
or derived evidence through `kube-insight`.

This applies to:

- latest resources,
- historical versions,
- resource diffs,
- topology edges,
- troubleshooting facts,
- investigation results,
- support bundle exports.

## Core Rule

```text
kube-insight authorization is Kubernetes authorization plus product-local
restrictions.
```

Kubernetes RBAC decides whether a user can see a resource. `kube-insight` may
add stricter product policies, but should not grant broader access than the
Kubernetes API server would.

## Threat Model

Derived data can leak information even when raw JSON is hidden.

Examples:

- A Service investigation can reveal Pod names the user cannot list.
- A topology graph can reveal which Node a restricted Pod ran on.
- An OOMKilled fact can reveal that a hidden workload exists.
- A diff can reveal old Secret references or ConfigMap names.
- A same-node query can reveal other tenants colocated on a Node.

Therefore authorization must be applied before returning facts and topology, not
only before returning raw resource JSON.

## Identity Model

Each API request must carry an authenticated Kubernetes-compatible identity:

```text
user
groups
extra attributes
cluster
```

Supported authentication modes:

### Mode 1: Kubernetes Token Pass-Through

The client sends a Kubernetes bearer token to `kube-insight`.

`kube-insight` uses that identity for access checks against the target cluster.

Best for:

- CLI,
- early PoC,
- single-cluster deployments.

### Mode 2: OIDC Identity Mapping

The user authenticates to `kube-insight` with OIDC. The product maps OIDC user
and groups to Kubernetes user/groups, then performs Kubernetes authorization
checks through the API server.

Best for:

- web UI,
- central service,
- multi-cluster environments.

### Mode 3: Trusted Front Proxy

A trusted gateway authenticates the user and forwards Kubernetes-compatible
identity headers.

This mode requires strict deployment controls. Do not trust arbitrary identity
headers from direct clients.

## Authorization Mechanism

Use the Kubernetes API server as the authorization oracle.

Two implementation strategies:

### SelfSubjectAccessReview

Use when `kube-insight` can check access using the user's own Kubernetes token.

Pros:

- Strong inheritance of the user's exact Kubernetes permissions.
- No need for `kube-insight` to impersonate arbitrary users.

Cons:

- More complex for browser/OIDC deployments.
- Requires careful token handling and audience/lifetime management.

### SubjectAccessReview

Use when `kube-insight` authenticates the user itself and asks the API server
whether that user/groups can perform an action.

Pros:

- Works well for server-side UI/API.
- Central service can cache decisions briefly.

Cons:

- The `kube-insight` service account needs permission to create
  `subjectaccessreviews`.
- Identity mapping must be exact and auditable.

## Resource Access Mapping

Map each `kube-insight` operation to Kubernetes verbs.

| kube-insight operation | Kubernetes authorization required |
| --- | --- |
| Read latest object | `get` on that resource name. |
| Read historical version | `get` on that resource name. |
| Diff two versions | `get` on that resource name. |
| List/search objects | `list` on that resource type in the namespace or cluster scope. |
| Watch-like live stream | `watch` on that resource type. |
| Query facts for object | `get` on the source object. |
| Query facts across objects | `list` on each involved resource type/scope. |
| Query topology from object | `get` on the root object plus permission on returned neighbor objects. |
| Export support bundle | permission for every included resource and fact. |

Historical access uses current Kubernetes RBAC policy by default:

```text
If the user can get the resource now, the user can get retained historical
versions of that same resource.
```

This is simple and predictable. A stricter future mode can evaluate historical
RBAC snapshots, but that requires storing RBAC history and is not MVP scope.

## Resource Scope

Authorization checks must respect:

- API group,
- resource,
- namespace,
- name,
- verb,
- subresource when relevant.

Examples:

```text
apps/deployments namespace=production name=checkout-api verb=get
core/pods namespace=production name=checkout-api-abc123 verb=get
core/nodes name=ip-10-0-4-91 verb=get
discovery.k8s.io/endpointslices namespace=production verb=list
```

Cluster-scoped resources such as Nodes require cluster-scoped permissions.

## Result Filtering

Queries should be planned in two phases:

```text
1. Candidate retrieval from kube-insight indexes.
2. Authorization filter before response.
```

For each candidate resource:

```text
if authorized(user, get, resource):
  include raw resource, facts, and topology edge endpoint
else:
  omit or redact it
```

For list/search queries:

```text
if authorized(user, list, resource type and namespace):
  allow index query for that scope
else:
  require named get-style access or deny
```

This prevents a user with `get` on one object from discovering all object names
through search.

## Topology Authorization

Topology edges connect two resources. Returning an edge can leak both endpoints.

Default rule:

```text
Return an edge only if the user can get both src and dst objects.
```

Alternative for UI navigation:

```text
Return visible endpoint and a redacted hidden endpoint marker.
```

Example:

```json
{
  "edge_type": "pod_on_node",
  "src": {"kind": "Pod", "name": "checkout-api-abc123"},
  "dst": {"redacted": true, "reason": "forbidden"}
}
```

Use the stricter "omit hidden endpoint" default for MVP.

## Fact Authorization

Facts inherit access from their source object.

Rules:

- Pod fact requires `get pod`.
- Deployment config fact requires `get deployment`.
- Node condition fact requires `get node`.
- Event mirror fact requires access to the involved object and/or Event,
  depending on policy.

For cross-object facts, such as same-node noisy-neighbor evidence:

```text
Return only facts for objects the user can get.
```

If this hides part of the evidence, include a generic note only when doing so
does not leak sensitive object details:

```text
Some related resources were omitted because of Kubernetes authorization.
```

## Investigation Authorization

Investigation queries combine many resource types.

Example:

```text
investigate service checkout-api --namespace production
```

Required minimum:

```text
get service checkout-api
```

Expansion rules:

1. Resolve the root Service only if `get service` is allowed.
2. Expand to Pods only if the user can list Pods in the namespace, or if the
   candidate Pod is individually authorized by `get`.
3. Expand to ReplicaSets/Deployments only if authorized.
4. Expand to Nodes only if the user can get Nodes.
5. Expand to same-node neighbor Pods only if the user can list/get those Pods.

The result must degrade gracefully:

```text
visible evidence:
  service-level facts
  authorized pod facts
  authorized deployment facts

omitted evidence:
  node facts if node access is denied
  same-node neighbors if pod list access is denied
```

## Caching

Authorization checks can be expensive during large investigations.

Cache short-lived decisions by:

```text
cluster
user
groups hash
verb
apiGroup
resource
namespace
name or wildcard
```

Recommended TTL:

```text
30-120 seconds
```

Cache invalidation:

- keep TTL short,
- clear user cache on explicit logout,
- optionally watch RBAC objects later.

Do not cache denies for too long because permissions may be granted during an
incident.

## Storage-Level Enforcement

Application-level filtering is mandatory. Storage-level isolation is optional
but useful later.

MVP:

```text
single shared storage
application authorization before response
audit every query
```

Future:

```text
PostgreSQL row-level security for defense in depth
namespace/tenant partitioning
separate encryption keys by cluster or tenant
```

Do not rely only on database RLS because Kubernetes RBAC is dynamic and
resource-specific.

## Audit

Audit all authorization-sensitive operations:

- user identity,
- groups hash,
- cluster,
- operation,
- requested resource scope,
- allowed/denied,
- number of returned objects,
- number of omitted objects,
- support bundle exports.

Do not log sensitive raw payloads in audit rows.

## Failure Mode

Authorization failure must be fail-closed.

If `kube-insight` cannot reach the Kubernetes API server to verify access:

```text
deny by default
```

Optional emergency mode can allow already-authenticated administrators to query
cached data, but that must be explicitly configured and audited.

## MVP Implementation Plan

1. Require Kubernetes bearer token or configured user identity on every request.
2. Implement an `Authorizer` interface:

```text
allowed(user, verb, group, resource, namespace, name) -> allow/deny
```

3. Implement Kubernetes SAR/SSAR-backed authorizer.
4. Add authorization checks to:
   - latest object read,
   - historical version read,
   - diff,
   - facts query,
   - topology query,
   - service investigation.
5. Add per-request authz cache.
6. Add audit rows for allowed, denied, and partially redacted responses.

## Open Questions

- Should historical access use current RBAC only, or should kube-insight later
  support historical RBAC snapshots?
- Should a user with `get service` but no `list pods` see selected Pod names
  from EndpointSlices?
- Should Node evidence be hidden by default in multi-tenant clusters?
- Should Event mirror access follow Event RBAC, involved-object RBAC, or both?
