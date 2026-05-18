# Case Report: Reconstructing a Service-Mux Test From History

This report is based on a real kube-insight dev run against a GKE cluster. Names,
cluster IDs, and external IPs are anonymized. The goal is to show the kind of
post-hoc investigation kube-insight enables after short-lived Kubernetes objects
have already been deleted.

## Scenario

An operator ran a service-mux experiment across four namespaces:

- `mux-alpha`: short-lived mux workload.
- `mux-alpha-target`: short-lived echo workload targeted by the mux.
- `mux-eip`: retained mux workload with a shared external IP.
- `mux-eip-target`: retained echo workload plus many LoadBalancer channel
  Services.

Some resources were created, rolled out, and deleted within minutes. Other
resources were left running. A later `kubectl get` can only show the current
state, so the useful question is historical: what happened, in what order, and
which Services actually received external ingress?

## Method

The investigation used read-only queries against kube-insight history:

- `observations` to find object lifetimes and latest observed state.
- `facts` to reconstruct Pod readiness, rollout, EndpointSlice readiness, and
  service-network endpoint group state.
- `versions` to inspect retained Service JSON for LoadBalancer ingress when a
  specific proof document was needed.
- `kubectl get` only as a final live-state cross-check.

Representative ClickHouse query:

```sql
select namespace, kind, latest_type, count() as objects
from (
  select
    namespace,
    kind,
    name,
    uid,
    argMax(observation_type, observed_at) as latest_type
  from observations
  where namespace like 'mux-%'
  group by namespace, kind, name, uid
)
group by namespace, kind, latest_type
order by namespace, kind, latest_type;
```

After the Service extractor is enabled, LoadBalancer state can be queried from
facts directly:

```sql
select ts, namespace, name, fact_key, fact_value, severity
from facts
where kind = 'Service'
  and fact_key in (
    'service.load_balancer.pending',
    'service.load_balancer.ingress_ip',
    'service.load_balancer.ingress_hostname'
  )
order by ts desc
limit 100;
```

## Findings

### Short-lived alpha test

kube-insight reconstructed the full lifecycle even though the namespaces no
longer existed during the later live check.

- `mux-alpha` was created at about `12:55:29 UTC`.
- `mux-alpha-target` was created at about `12:56:24 UTC`.
- The target echo Deployment created two Pods. They became Running and Ready by
  about `12:56:46 UTC`.
- The mux Service EndpointSlice pointed at those target Pods, proving the mux
  relationship without recomputing selectors from current cluster state.
- The mux workload rolled from one Pod to another around `13:03 UTC`.
- Both namespaces and all related objects were deleted between about
  `13:07:35 UTC` and `13:11:23 UTC`.

Historical object summary:

| Namespace | Latest State | Objects |
| --- | --- | ---: |
| `mux-alpha` | deleted | 23 |
| `mux-alpha-target` | deleted | 15 |

### Retained EIP test

The retained namespaces were still active during the live check, and their
history showed both normal and intentionally invalid LoadBalancer cases.

- `mux-eip` was created at about `13:22:37 UTC`.
- `mux-eip-target` was created at about `13:23:28 UTC`.
- The target echo Deployment had three Pods, all Running and Ready by about
  `13:23:34 UTC`.
- One hundred channel LoadBalancer Services were created from about
  `13:23:29 UTC` to `13:24:25 UTC`.
- Six special Services were created around `13:32 UTC` to test explicit ports,
  port conflicts, invalid port/name cases, and unnamed ports.
- The mux workload rolled at about `13:44-13:45 UTC`; the old Pod completed and
  the new Pod became Ready.

Historical object summary:

| Namespace | Latest State | Objects |
| --- | --- | ---: |
| `mux-eip` | active, with two old deleted objects from rollout | 23 |
| `mux-eip-target` | active | 329 |

LoadBalancer outcome summary from retained Service versions:

| Service Pattern | Outcome |
| --- | --- |
| normal channels | received the shared external ingress IP |
| explicit port case | received the shared external ingress IP |
| first conflict case | received the shared external ingress IP |
| second conflict case | remained pending |
| invalid port/name cases | remained pending |
| unnamed port case | remained pending |

## Why This Is Hard With Only `kubectl`

A later `kubectl get namespace` only showed that the retained EIP namespaces
still existed and the alpha namespaces were gone. It could not explain the alpha
namespace lifecycle, the rollout, deleted Pods, or which deleted EndpointSlices
had pointed at which target Pods.

For the active EIP test, `kubectl` could show current Services and pending
external IPs, but it could not answer when each channel was created, which
objects changed during the rollout, or prove what happened before the current
state.

kube-insight preserved both the compact investigation path and the proof path:

- facts and edges for fast candidate discovery,
- observations for object lifetimes,
- retained versions for exact JSON proof,
- SQL access so an agent can ask cluster-specific follow-up questions without a
  custom endpoint for every scenario.

## Product Takeaway

This is the target kube-insight workflow: use lightweight continuous history to
make Kubernetes incidents queryable after they have moved on. APIs are useful
for common UI flows, but SQL over facts, edges, observations, and versions lets
an agent reconstruct domain-specific experiments such as service muxing, EIP
allocation, GitOps rollouts, and cleanup behavior.
