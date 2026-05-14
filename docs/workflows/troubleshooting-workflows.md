# Troubleshooting Workflows

## Workflow 1: Service Latency Spike

Input:

```text
cluster
namespace
service
time window
```

Flow:

```text
service/time
  -> historical Pods
  -> owner Deployment/ReplicaSet
  -> Nodes
  -> Pod facts
  -> workload changes
  -> Node pressure
  -> same-node Pods
  -> ranked evidence bundle
  -> exact JSON/diff for evidence
```

Expected output:

```text
10:02:45 Deployment memory limit changed 512Mi -> 384Mi
10:04:11 Node ip-10-0-4-91 MemoryPressure=True
10:06:12 Pod checkout-api-... Ready=False
10:06:20 Container app lastState.reason=OOMKilled
10:06:23 Pod restarted on ip-10-0-4-91
```

## Workflow 2: OOMKilled After Events Expired

Question:

```text
Which historical Pods for this service had OOMKilled around this time?
```

Primary tables:

- `object_edges`
- `object_facts`
- `versions`

Query route:

```text
service -> Pods in time window -> object_facts(reason=OOMKilled)
```

Then reconstruct the Pod version before and after the termination.

## Workflow 3: Pod Evicted Or Rescheduled

Question:

```text
Did an affected Pod move Nodes or get evicted?
```

Signals:

- `object_edges(edge_type=pod_on_node)`
- `object_facts(reason=Evicted)`
- Pod deletion and recreation with new UID

Output should distinguish:

- same Pod assigned to a Node,
- deleted and recreated Pod with same name pattern,
- ReplicaSet created replacement Pod,
- eviction event mirrored before expiration.

## Workflow 4: Brief Deployment Misconfiguration

Question:

```text
Was the Deployment briefly configured incorrectly and then fixed?
```

Signals:

- `object_changes(change_family=spec)`
- rollout facts
- ReplicaSet hash changes
- Pod template image/resource/probe changes

Display:

```text
Deployment checkout-api changed at 10:02:45
spec.template.spec.containers[name=app].resources.limits.memory
  512Mi -> 384Mi
```

Then show the rollback or later correction.

## Workflow 5: Same-Node Noisy Neighbor

Question:

```text
Were affected Pods colocated with high-resource or unstable Pods?
```

Flow:

```text
affected Pods -> Nodes during time window -> other Pods on same Nodes
  -> resource request/limit facts
  -> restart/readiness/churn facts
```

Evidence examples:

- batch Pod scheduled shortly before spike,
- high memory limit on same Node,
- multiple same-node Pods restarted,
- Node pressure transitioned near the spike.

## Workflow 6: Current State Is Healthy

Question:

```text
Everything is normal now. What was different then?
```

Flow:

```text
topology at incident time
  vs latest topology
facts around incident time
  vs recent baseline facts
resource version diff
  incident version vs latest version
```

The product should explicitly show "then vs now".

## Workflow 7: Webhook Broke Deployments Briefly

Question:

```text
Why did creates/updates fail for several minutes even though the webhook is
healthy now?
```

Traditional investigation pain:

- admission webhook Events may be gone,
- webhook configuration may already be rolled back,
- cert-manager Certificate and Secret may have changed since the incident,
- APIService or ValidatingWebhookConfiguration status must be correlated by
  hand.

Kube-insight route:

```text
failed workload event
  -> ValidatingWebhookConfiguration / MutatingWebhookConfiguration
  -> webhook service
  -> EndpointSlice
  -> webhook Pods
  -> Certificate / CertificateRequest / Order / Challenge
  -> Secret metadata and keys
  -> exact config/certificate version diffs
```

Expected evidence:

```text
10:01:03 ValidatingWebhookConfiguration webhook.cert-manager.io caBundle changed
10:01:08 Certificate api-cert Ready=False reason=DoesNotExist
10:01:09 Secret api-tls had keys [tls.crt,tls.key] regenerated
10:02:12 Webhook Pod Ready=False
10:05:44 Certificate Ready=True and webhook Pod Ready=True
```

Advantage:

- Finds webhook service and cert-manager chain from stored edges.
- Shows exact before/after webhook and Certificate versions.
- Does not need expired Events to still exist in the cluster.

## Workflow 8: RBAC Change Caused Permission Loss

Question:

```text
Which RBAC change caused the controller to lose access?
```

Traditional investigation pain:

- `kubectl auth can-i` only answers current state,
- RoleBinding may have been fixed before investigation,
- ClusterRole aggregation and subject references are easy to miss.

Kube-insight route:

```text
controller Pod
  -> ServiceAccount
  -> RoleBinding / ClusterRoleBinding
  -> Role / ClusterRole
  -> rules diff
  -> Events and failed reconciliations
```

Expected evidence:

```text
09:58:20 Role api-reader removed verb=list from pods
09:58:24 RoleBinding api-reader-binding still points to Role api-reader
09:59:01 Controller Event Forbidden: cannot list pods
10:07:45 Role api-reader restored verb=list
```

Advantage:

- Shows historical permissions instead of only current permissions.
- Uses RoleBinding edges rather than scanning every RBAC object.
- Returns exact Role version diffs as proof.

## Workflow 9: CRD Or Controller Upgrade Regressed Custom Resources

Question:

```text
Did a CRD/schema/controller change break custom resources?
```

Traditional investigation pain:

- CRD status and schema are large,
- controller Events may be noisy or expired,
- custom resources vary by group and are often missed by hardcoded collectors.

Kube-insight route:

```text
CustomResourceDefinition
  -> discovered API resource
  -> custom resource versions
  -> controller Deployment / Pods
  -> Events and status facts
  -> CRD schema diff
```

Expected evidence:

```text
13:10:02 CRD widgets.example.com stored version changed v1beta1 -> v1
13:10:04 schema removed spec.endpoint
13:11:31 Widget api-widget status Ready=False reason=ValidationFailed
13:12:10 Controller Deployment image changed controller:v1.8.1 -> v1.9.0
```

Advantage:

- Discovery-backed collection can watch CRDs and custom resources.
- CRD definitions are mostly preserved; only truly sensitive fields are
  redacted.
- Dynamic resolver maps custom GVRs without adding code for every CRD.

## Workflow 10: Namespace-Scoped Collector Lost Visibility

Question:

```text
Were objects deleted, or did our collector lose namespace/RBAC visibility?
```

Traditional investigation pain:

- relist output cannot distinguish deletion from scope loss unless previous
  visibility is tracked,
- incorrect delete tombstones corrupt later history.

Kube-insight route:

```text
latest refs before relist
  -> relist namespace/resource scope
  -> missing in-scope objects => delete tombstone
  -> missing out-of-scope objects => unknown visibility
```

Expected evidence:

```text
pods/default/api-0 missing from default relist -> confirmed deleted
pods/other/api-1 outside current namespace scope -> unknown_visibility
```

Advantage:

- Avoids false delete history when permissions or namespace scope changes.
- Reports `reconciledDeleted` and `unknownVisibility` separately.
- Exposes per-GVR queue/backpressure to diagnose collector lag.

## Evidence Ranking

Initial scoring inputs:

- severity,
- time proximity to incident,
- object relationship distance from service,
- number of affected Pods,
- whether change happened before symptom,
- whether a rollback happened after symptom,
- whether multiple signals agree.

Example severity:

```text
100 OOMKilled / Evicted / Node NotReady
80  Node MemoryPressure / DiskPressure
70  Ready=False / CrashLoopBackOff
60  Deployment resource/image/probe change
40  restart count increase
20  colocated high-resource Pod
```

## Investigation Result Shape

```json
{
  "input": {
    "cluster": "staging",
    "namespace": "production",
    "service": "checkout-api",
    "from": "2026-05-11T10:05:00Z",
    "to": "2026-05-11T10:20:00Z"
  },
  "summary": "1 affected pod had OOMKilled after a deployment memory limit change.",
  "evidence": [
    {
      "ts": "2026-05-11T10:02:45Z",
      "severity": 60,
      "kind": "Deployment",
      "reason": "memory_limit_changed",
      "object_id": "..."
    }
  ],
  "topology": {
    "pods": [],
    "nodes": [],
    "workloads": []
  }
}
```

The API should return structured evidence. The UI can render timelines and
graphs from the same response.
