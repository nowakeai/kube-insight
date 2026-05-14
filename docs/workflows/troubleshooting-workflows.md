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
