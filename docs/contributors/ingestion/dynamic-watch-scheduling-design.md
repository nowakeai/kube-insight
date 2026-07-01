# Dynamic Watch Scheduling Design

Status date: 2026-07-01

Implementation status: Phase 2/3 baseline is implemented for writer-local
signals. The current scheduler scores resources by static profile priority,
queue wait time, recent watch events/bookmarks, latest object count, and retry
or watch error pressure. Query/incident hints and shared hint persistence remain
future work.

Audience: contributors designing the next watcher scheduling iteration after
static resource profiles, queued relists, and fixed watch rotation.

## Problem

Large Kubernetes clusters can expose hundreds of watchable GVRs. Keeping a
long-running WATCH stream open for every resource can overload the API server,
aggregated API services, kube-insight storage, and network path. Capping active
WATCH streams protects the cluster, but a static priority order can still leave
important resources queued during an incident.

The current short-term behavior is:

- all selected resources receive an initial LIST;
- resources beyond `collection.watch.maxConcurrentStreams` are marked queued;
- queued resources are periodically refreshed by LIST;
- active WATCH streams rotate after `collection.watch.streamRotationSeconds`.

This gives bounded freshness, but it does not decide which resource should get
the next WATCH slot based on current investigation value.

## Goals

- Keep Kubernetes API pressure bounded and predictable.
- Prefer real WATCH windows for resources most likely to help AIOps
  investigations right now.
- Keep the decision model explainable in health output and logs.
- Preserve deterministic static profiles as the base policy.
- Let operators disable dynamic behavior or pin critical resources.
- Avoid using LLMs in the scheduler. The scheduler should be deterministic and
  cheap.

## Non-Goals

- Do not replace resource profiles, retention policies, filters, or extractors.
- Do not let dynamic scheduling hide coverage gaps. Health must still show
  queued, relist, retry, and stale states.
- Do not run multiple independent watch owners for the same backend.
- Do not require CRD-based runtime configuration for the first implementation.

## Scheduling Model

Use a two-level policy:

1. **Static base class** from resource profiles.
2. **Dynamic score** from recent runtime signals.

The scheduler ranks candidates for a WATCH slot by:

```text
score = base_priority
      + incident_boost
      + query_heat_boost
      + freshness_boost
      + event_rate_boost
      + relationship_boost
      - cost_penalty
      - error_penalty
      - churn_penalty
```

The score is only used to choose the next slot and decide which active stream is
eligible to rotate out. The persisted resource profile remains the source of
truth for processing behavior.

## Signals

### Base Priority

Derived from resource profiles:

| profile priority | base score |
| --- | ---: |
| high | 100 |
| normal | 50 |
| low | 10 |
| disabled | not scheduled |

Operators can still force critical resources to high priority through
`resourceProfiles.rules`.

### Incident Boost

Short-lived boost for resources directly involved in an active investigation or
alert. Examples:

- user asks about a namespace, Service, Deployment, Node, Pod, Gateway, or GitOps
  object;
- agent tool call includes a resource/kind/namespace filter;
- an external alert maps to a Kubernetes object or owner.

Suggested score:

```text
+80 for exact GVR/object match
+50 for owner chain or topology neighbor
+30 for same namespace in the last 15 minutes
```

In all-in-one mode this can be implemented first as an in-memory hint handled by
the same process that owns the watcher:

```text
POST /api/v1/watch-scheduler/hints
{
  "scope": {"namespace": "payments", "kind": "Deployment", "name": "api"},
  "reason": "agent-query",
  "ttlSeconds": 900
}
```

The built-in agent and MCP tools can add hints when users ask scoped questions.
External clients can ignore this endpoint.

In split production, API/MCP/A2A readers must not keep hint state only in local
memory because the writer process owns watch scheduling. Readers should emit
hints to a shared hint sink or forward them to the writer. See
[Read/Write Separation](#readwrite-separation).

### Query Heat Boost

Boost resources recently queried through kube-insight APIs, MCP tools, or A2A
tasks.

Examples:

- `kube_insight_search` with `kind=Pod`;
- SQL query constrained to `api_resources.resource='services'`;
- service investigation for a Service should boost Services, EndpointSlices,
  Pods, workload controllers, Ingress/Gateway, and Events.

Suggested score:

```text
+min(40, 10 * recent_query_count)
decay half-life: 10 minutes
```

Implementation can start with lightweight counters keyed by
`cluster_id + group + version + resource`, not by raw SQL parsing. Tool handlers
can emit explicit usage signals when they know the resource scope. In
all-in-one mode these counters can be in memory; in split mode they need the
same hint sink described below.

### Freshness Boost

Queued resources become more important as their last LIST/watch age grows:

```text
+0 below target freshness
+20 when age > target freshness
+50 when age > 2 * target freshness
+80 when age > stale threshold
```

This prevents quiet but important resources from staying queued indefinitely,
even when they have low query heat.

### Event Rate Boost

Resources with useful recent changes deserve WATCH windows because event streams
capture exact transitions better than periodic LIST.

Use a bounded moving average:

```text
+0 for no recent events
+10 for low event rate
+30 for moderate event rate
+50 for high event rate
```

Avoid letting noisy resources monopolize the watch budget. Apply a churn penalty
when event rate exceeds a configured threshold.

### Relationship Boost

When one resource is important, related resources should temporarily move up.

Examples:

- Service -> EndpointSlices, Endpoints, Pods, owning workload controllers,
  Ingress/Gateway/HTTPRoute.
- Pod -> Node, owner ReplicaSet/Job/StatefulSet, PVCs, ConfigMaps, Secrets
  metadata, Events.
- GitOps object -> target workload, HelmRelease/Kustomization dependencies,
  source repositories.
- kagent Agent -> ToolServer, MCPServer/RemoteMCPServer, ModelConfig,
  ModelProviderConfig, related Secret metadata.

This should use existing facts and edges where available. If facts are missing,
fall back to conservative built-in relationship tables.

## Cost Model

Scheduling must consider cost, not only value:

| cost signal | effect |
| --- | --- |
| object count high | lower priority for full relist and watch unless boosted |
| recent API errors | avoid aggressive retries; use backoff |
| aggregated API group unstable | lower rotation frequency until stable |
| high storage write rate | prefer relist summary or longer rotation |
| secrets/configmaps large object counts | keep metadata filters, avoid raw churn |

Suggested penalty:

```text
cost_penalty = min(40, latest_objects / object_count_divisor)
error_penalty = 60 while retrying/list_error/watch_error backoff is active
churn_penalty = 30 when event rate exceeds high-churn threshold
```

## Slot Selection

The watcher should maintain a scheduler state per cluster:

```text
ResourceScheduleState:
  resource key
  static profile priority
  current status: watching | queued | retrying | stale | skipped
  last list/watch/bookmark time
  latest object count
  recent event count
  recent query count
  active incident hints
  next eligible time
  active since
  pinned mode
```

When a slot is free:

1. Recompute candidate scores for queued and retry-eligible resources.
2. Exclude disabled/skipped resources and resources still in backoff.
3. Choose the highest score.
4. Start WATCH from its latest known resourceVersion.
5. Emit a scheduling decision log with top factors.

When all slots are full:

1. Let streams run until `streamRotationSeconds` unless preemption is enabled.
2. At rotation time, decide whether the active resource should immediately
   reacquire the slot or yield.
3. Yield if a queued candidate beats it by `preemptionScoreMargin`.

First implementation should avoid hard preemption. Rotation-time replacement is
enough and easier to reason about.

## Validation Cases

The scheduler should keep these cases passing as the algorithm evolves:

| case | expected behavior |
| --- | --- |
| static priority | high-priority resources score above normal and low resources when no runtime signal exists |
| queued fairness | a low-priority resource that has waited long enough can overtake a fresh normal-priority resource |
| recent activity | resources with recent watch events or bookmarks receive a bounded boost and can win the next slot |
| object-count cost | very large resources receive a bounded cost penalty so they do not monopolize slots without other signals |
| retry/error pressure | resources with repeated watch errors or retries are temporarily deprioritized below healthier candidates |
| disabled profile | disabled resources remain unschedulable regardless of runtime signals |
| rotation handoff | when an active stream rotates out, the next slot goes to the highest current score, not simply the oldest waiter |

Current automated coverage lives in `internal/collector/watch_resources_test.go`
and exercises the score function, queued-wait overtaking, release handoff, and
race detection for the collector package.

## Read/Write Separation

Production deployments should normally run one writer and multiple read-only
API/MCP/Web/A2A instances. User queries happen on readers; watch scheduling
happens on the writer. Dynamic scheduling must preserve that boundary.

### Deployment Modes

| mode | query hint path | scheduler state |
| --- | --- | --- |
| all-in-one | local process memory | local process memory |
| split with shared SQL backend | readers write short-lived hint rows | writer polls/consumes hint rows |
| split with writer service endpoint | readers forward hints to writer | writer memory plus optional persistence |

The recommended production path is **shared SQL hint rows**. It works with
multiple readers, does not require readers to discover or trust a writer
endpoint, survives reader restarts, and keeps the writer as the only component
that mutates watch scheduling.

### Hint Sink

Add a small TTL-backed table in the shared backend:

```text
watch_scheduler_hints
- id
- cluster_id
- api_group
- api_version
- resource
- kind
- namespace
- name
- reason
- weight
- source
- created_at
- expires_at
```

Readers append hints when they have structured scope. The writer periodically
loads unexpired hints and folds them into scores. Readers never directly change
watch state.

For SQL backends, inserts should be cheap and bounded:

- reject hints with TTL above a configured maximum;
- coalesce duplicate hints in memory on the writer;
- cap total active hints per cluster/reason/source;
- prune expired hints from the writer maintenance loop.

SQLite all-in-one can use memory first; split SQLite should not be a production
target. ClickHouse/chDB can store hints as append-only rows with TTL-style
cleanup or periodic deletes.

### Query Heat

Query heat should follow the same producer/consumer model:

1. API/MCP/A2A handlers emit structured usage events when they know the resource
   scope.
2. Writer aggregates usage events into decayed counters.
3. Scheduler uses the counters during rotation-time selection.

Avoid parsing arbitrary SQL in the first version. Resource-scoped tools can emit
high-quality signals:

- `kube_insight_search(kind=Pod)` -> Pods;
- `kube_insight_history(kind=Deployment)` -> Deployments;
- `kube_insight_topology(kind=Service)` -> Services plus related topology;
- `kube_insight_service_investigation` -> Services, EndpointSlices, Pods,
  workload controllers, Ingress/Gateway, Events;
- kagent troubleshooting prompts -> kagent, observability, workload, and RBAC
  resources touched by the tool call.

### Writer Consumption

The writer should treat external hints as advisory:

- expired hints are ignored;
- hints cannot schedule disabled resources;
- hints cannot bypass retry/backoff safety;
- hints cannot exceed configured boost caps;
- hint source and reason are surfaced in logs/health for explainability.

The writer can poll every `scoreIntervalSeconds` rather than stream hint
updates. Scheduler decisions do not need sub-second latency.

### Failure Semantics

If the hint sink is unavailable:

- readers should continue serving queries;
- writer should fall back to static profiles, freshness, relist, and rotation;
- health should include a scheduler warning only if hint consumption repeatedly
  fails;
- no query should fail just because a scheduling hint cannot be recorded.

This keeps query availability independent from watcher optimization.

## Pinning

Some resources should never rotate out unless they error or the process stops.
This is optional and should be conservative.

Proposed config:

```yaml
collection:
  watch:
    dynamicScheduling:
      enabled: false
      pinnedResources:
        - pods
        - nodes
        - events.events.k8s.io
```

Pinned resources:

- count against `maxConcurrentStreams`;
- stay in WATCH after they get a slot;
- still record errors and retry normally;
- should be few by default.

For the first dynamic release, prefer no pinned defaults unless staging shows
rotation harms high-value Pod/Node workflows.

## Configuration

Proposed staged config:

```yaml
collection:
  watch:
    maxConcurrentStreams: 64
    queuedRelistIntervalSeconds: 300
    streamRotationSeconds: 900
    dynamicScheduling:
      enabled: false
      scoreIntervalSeconds: 30
      queryHeatTtlSeconds: 900
      incidentHintTtlSeconds: 900
      targetFreshnessSeconds: 300
      preemptionScoreMargin: 50
      maxPreemptionsPerMinute: 4
      pinnedResources: []
      protectedResources:
        - pods
        - nodes
```

`protectedResources` means hard preemption should not stop these streams early.
Normal rotation can still happen unless they are also pinned.

## Observability

Add scheduler evidence to health and metrics:

- current score;
- top score reasons;
- active/queued duration;
- last scheduling decision;
- relist count while queued;
- rotation count;
- preemption count;
- next eligible time.

Example health row:

```text
resource=vmservicescrapes.operator.victoriametrics.com status=queued score=142
reasons=base:high,freshness:50,query_heat:20,cost:-8 age_seconds=312
```

Metrics:

```text
kube_insight_watch_scheduler_score{resource="pods"} 180
kube_insight_watch_scheduler_rotations_total{resource="pods"} 12
kube_insight_watch_scheduler_preemptions_total{resource="pods"} 0
kube_insight_watch_scheduler_hints{reason="agent-query"} 3
```

## Persistence

Initial score-only implementation can keep computed scheduler state in writer
memory. This avoids schema churn and is acceptable because static profiles, LIST
snapshots, and health offsets remain durable.

Hints from user queries are different: all-in-one mode may keep them in memory,
but split production needs a shared hint sink so readers can influence the
writer safely.

Persist only the minimal cross-process inputs:

- schedule hints for multi-process API/writer setups;
- query heat across restarts;
- decision audit history.

If persisted, store bounded rows with TTL and no environment-specific values.

## Rollout Plan

### Phase 1: Explainable Rotation

Already partly implemented:

- static priority profiles;
- queued relist;
- fixed stream rotation;
- health status for queued/watching.

Add:

- rotation counts and last rotation time in health;
- docs clarifying rotation is not intelligence yet.

### Phase 2: Score-Only Scheduler

Implement scoring but do not change behavior yet.

- compute score for each resource;
- expose score and reasons in logs/health;
- compare score rankings against staging incidents and smoke cases.

This phase is safe because it only observes. The baseline score function is now
available in the collector and is logged when a watch worker starts.

### Phase 3: Rotation-Time Selection

Use score to choose who receives the next slot after a rotation.

- no hard preemption;
- no dynamic API hints required;
- respect backoff and protected resources.

This is the first production-useful dynamic scheduler.

The baseline implementation now uses score-based slot acquisition instead of a
plain semaphore. When a WATCH slot is free, the highest-scoring queued request
enters first; ties keep FIFO order. It does not yet use query heat or incident
hints.

### Phase 4: Query and Incident Hints

Let APIs, MCP tools, A2A tasks, and the built-in agent emit short-lived hints.

- scoped user questions boost related resources;
- service investigation boosts topology neighbors;
- kagent troubleshooting boosts kagent and observability CRDs.

In split deployments, this phase requires the shared hint sink. Readers append
hints, and the writer consumes them during scoring. Do not implement reader-only
in-memory query heat as the production path.

### Phase 5: Controlled Preemption

Allow a high-scoring queued resource to take a slot before the current stream's
rotation deadline.

Guardrails:

- preemption disabled by default;
- per-minute cap;
- score margin;
- protected resources;
- no preemption during retry/backoff storms.

## Open Questions

- Should Pods and Nodes be pinned by default, or only protected from early
  preemption?
- How should SQL query heat be inferred without brittle SQL parsing?
- Should service investigation hints be emitted by the API layer or the MCP tool
  wrapper?
- What are safe default score weights for clusters with very large Secrets or
  ConfigMaps?
- Should dynamic scheduling be disabled automatically when API server latency or
  retry rates rise?

## Recommended First Implementation

Implement Phase 2 and Phase 3 next:

1. Add a small scheduler state object in `internal/collector`.
2. Compute scores from static priority, freshness, latest object count, retry
   state, and recent event count.
3. Log score decisions at rotation boundaries.
4. Use score to pick the next WATCH slot after rotation.
5. Keep dynamic scheduling disabled by default until staging validates the
   ranking.

Then implement Phase 4 as a read/write-safe extension:

1. Add the shared `watch_scheduler_hints` sink for SQL-compatible backends.
2. Add a narrow append-only API for structured hints.
3. Let API/MCP/A2A readers write hints without requiring direct writer access.
4. Let the writer poll unexpired hints and fold them into scores.
5. Keep query success independent from hint write success.

This gives kube-insight a clear path from deterministic rotation to adaptive
watch coverage without coupling the collector to LLM behavior or external agent
systems.
