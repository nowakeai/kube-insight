# Product Brief

## Product Definition

`kube-insight` is a historical evidence store for Kubernetes clusters.

It continuously records Kubernetes resource versions, historical topology, and
high-value troubleshooting facts. Its job is to answer operational questions
that current `kubectl`, expired Kubernetes Events, metrics, logs, and GitOps
history cannot answer alone.

## Primary Use Case

A service had a small latency or error-rate spike several hours ago.

Current state is healthy. No alert fired. The affected Pods may have restarted,
moved, or been replaced. Kubernetes Events may already be expired.

The engineer wants to know:

- Did any related Pod get OOMKilled, evicted, restarted, or become unready?
- Did the Pod move to another Node?
- Did a Deployment, ReplicaSet, image, probe, env var, or resource limit change?
- Did the Node enter memory, disk, PID, or readiness pressure?
- Were other high-resource Pods colocated on the same Node?
- What exact resource versions prove the answer?

## Users

### Platform Engineers

They debug workloads and cluster behavior. They need historical Kubernetes state,
topology, and evidence around incidents.

### SRE / Observability Engineers

They start from telemetry symptoms and need Kubernetes context around a time
window.

### Compliance / Governance Engineers

They need to reconstruct historical resource state, ownership, placement, and
configuration.

## Product Principles

### Full History Is The Source Of Truth

Every retained resource version should be reconstructable. Derived facts and
topology can be rebuilt; raw history cannot.

### Incident Queries Should Not Scan All JSON

Common troubleshooting paths must use compact relationship and fact indexes.
Historical JSON reconstruction is reserved for evidence drill-down and diff.

### Kubernetes-Aware, Backend-Flexible

The product is Kubernetes-specific at the domain layer, but storage backends
should remain replaceable.

### Evidence First, Not Automatic Blame

The product should rank and explain evidence. It should avoid claiming a root
cause without enough signal.

## Non-Goals

- Replace metrics, logs, traces, or alerting.
- Become a generic schemaless JSON warehouse.
- Index every scalar field in every historical JSON document by default.
- Store Secret payloads by default.
- Require PostgreSQL as the only backend during early PoC.

## Product Surface

CLI:

```bash
kube-insight collect --context staging --out data/kube-samples.json
kube-insight ingest --context staging --cluster staging
kube-insight investigate service checkout-api \
  --namespace production \
  --from 2026-05-11T10:05:00Z \
  --to 2026-05-11T10:20:00Z
kube-insight topology service checkout-api --at 2026-05-11T10:10:00Z
kube-insight diff deployment checkout-api --from-version 120 --to-version 125
```

API:

```text
GET /clusters
GET /objects
GET /objects/{id}/latest
GET /objects/{id}/versions
GET /objects/{id}/versions/{version_id}
GET /objects/{id}/diff?from=&to=
POST /investigations
GET /investigations/{id}
GET /topology?root=&at=
GET /facts?object_id=&from=&to=
```

UI:

- Cluster timeline.
- Namespace/service/workload explorer.
- Investigation result view.
- Topology-at-time graph.
- Evidence timeline.
- Resource version diff.
- Storage and index strategy report.
