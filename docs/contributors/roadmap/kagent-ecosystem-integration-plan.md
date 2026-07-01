# kagent Ecosystem Integration Plan

Status date: 2026-07-01

Audience: maintainers and contributors implementing the next small release.

Related research:
[Agent Ecosystem Research](../product/agent-ecosystem-research.md).

## Release Theme

```text
kube-insight for cloud-native agents: A2A, kagent, and Helm.
```

The release should make kube-insight installable in Kubernetes, usable from
kagent, and directly callable as an A2A-compatible Kubernetes evidence agent.

## Goals

- Let a kagent Agent use kube-insight as a remote MCP tool server.
- Implement kube-insight A2A support for whole-investigation delegation.
- Ship a production-usable kube-insight Helm chart so users can install the
  collector, API, Web UI, MCP surface, metrics, RBAC, storage configuration, and
  kagent-facing Service without hand-written manifests.
- Teach kagent agents how to use kube-insight evidence efficiently through a
  skill package/profile.
- Publish a demo workflow that shows kagent coordinating kube-insight with
  monitoring, GitOps, or live Kubernetes tools.
- Create a community-friendly story that can be shared with kagent users and
  maintainers.

## Non-Goals

- Do not make kube-insight a general-purpose agent runtime.
- Do not replace Prometheus, Grafana, Loki, kubectl, ArgoCD, Flux, K8sGPT, or
  HolmesGPT.
- Do not add cluster-write or remediation behavior to kube-insight for this
  release.
- Do not claim that kagent can directly import arbitrary external A2A agents
  until that has been validated with the target kagent version.
- Do not treat kagent `a2aConfig.skills` metadata as executable skill logic.
- Do not implement operator-style CRD configuration in this release. Keep CRDs
  as a future runtime control-plane direction.

## Integration Architecture

The installation path:

```text
helm install kube-insight
  -> ServiceAccount / RBAC
  -> kube-insight Deployment or split writer/API Deployments
  -> ConfigMap and Secret references
  -> Service exposing Web UI, API, MCP, and metrics
  -> optional PVC or external ClickHouse configuration
```

The first supported path:

```text
kagent Agent
  -> RemoteMCPServer / MCPServer reference
  -> kube-insight Helm-created Service /mcp
  -> kube-insight API/storage
  -> retained versions, observations, facts, changes, edges, citations
```

The complementary tool bundle should be documented as:

```text
Prometheus/Grafana/Loki: metrics and logs
kubectl or Kubernetes MCP: live current state
GitOps tools: desired state and sync history
kube-insight: retained Kubernetes object history and proof
```

The A2A path:

```text
A2A-capable orchestrator
  -> kube-insight Agent Card
  -> message/send or message/stream
  -> kube-insight agent run
  -> artifacts and citations
```

## Deliverables

## Implementation Progress

Status date: 2026-07-01

- [x] Prepared a staging development environment with kagent core installed and
  ready. The environment is for validation only; do not record staging cluster
  identifiers, internal addresses, or unrelated cluster state in this repo.
- [x] Helm chart implementation and smoke validation in `charts/kube-insight/`.
- [x] kagent MCP examples and troubleshooting agent example.
- [x] kagent-oriented skill profile/reference.
- [x] A2A endpoint baseline implementation.
- [x] A2A user documentation.
- [x] A2A streaming and baseline artifact/citation projection.
- [x] End-to-end external A2A client validation.
- [x] Release-scope development complete.

### Current Phase Results

- kagent core is available for integration testing with its built-in tool server
  accepted.
- The Helm chart has been added with all-in-one, API-only, writer-only,
  default chDB, explicit SQLite test/temporary mode, and external ClickHouse
  rendering paths. Product guidance is ClickHouse or chDB first for
  long-running installs; SQLite is only for tests, demos, and temporary runs.
- Local chart validation passed with `make helm-lint` and Kubernetes
  client-side dry-run for default, writer, and ClickHouse modes.
- Staging chart smoke passed for the all-in-one SQLite mode with a fresh PVC:
  the Pod reached Ready, `/api/v1/server/info` returned the expected enabled
  API/MCP/Web UI/metrics/watch surfaces, `/metrics` exposed kube-insight
  metrics, and `/mcp` returned the expected MCP session error for a raw GET
  without a session.
- The smoke found and fixed two install-path issues:
  - client-go collection now falls back to Kubernetes in-cluster config when no
    kubeconfig current-context exists inside a Pod;
  - SQLite opens its connection with a busy timeout in the DSN so API and watch
    components can bootstrap the same all-in-one SQLite database without an
    immediate `SQLITE_BUSY` failure.
  - the chart mounts a writable `/tmp` and sets `TMPDIR=/tmp`, which is required
    for SQLite temporary files while keeping the root filesystem read-only.
- A follow-up smoke with default collection concurrency observed no
  `SQLITE_BUSY`, SQLite I/O, or watch retry errors during the initial validation
  window.
- kagent `kagent.dev/v1alpha2` CRDs were validated against the live development
  environment. Generic examples were added under `examples/kagent/`:
  `RemoteMCPServer` points to the kube-insight Helm Service MCP endpoint, and
  the troubleshooting `Agent` references the discovered kube-insight MCP tools.
- kagent RemoteMCPServer discovery smoke passed with a temporary object: kagent
  accepted the server and discovered the 8 runtime MCP tools
  (`kube_insight_health`, `kube_insight_schema`, `kube_insight_sql`,
  `kube_insight_js`, `kube_insight_search`, `kube_insight_history`,
  `kube_insight_topology`, and `kube_insight_service_investigation`). MCP
  prompts are not listed as tools by this kagent version, so the Agent example
  only pins discovered runtime tools.
- kagent Agent runtime smoke passed after wiring a ready model provider in the
  validation environment. The manual troubleshooting Agent reached Ready and
  now pins the 8 discovered kube-insight MCP tools, including
  `kube_insight_js`.
- chDB-backed staging smoke passed with a chDB-enabled kube-insight image and
  PVC-backed `/data/kubeinsight.chdb`. The API, MCP, Web UI, and watcher run
  against `storage.driver=chdb`; chart metrics are disabled for chDB in this
  iteration because the metrics collector currently supports SQLite and
  ClickHouse HTTP metrics only.
- Loki MCP was validated through a kagent `RemoteMCPServer` using the
  lightweight Loki MCP server shape. kagent discovered `loki_query`,
  `loki_label_names`, and `loki_label_values`, and the full-stack AIOps Agent
  can include those tools when the Loki MCP server is installed.
- Prometheus-compatible metrics validation in staging uses VictoriaMetrics, not
  a vanilla Prometheus Service. kagent's built-in Prometheus tools default to a
  local Prometheus URL unless the tool call provides `prometheus_url`, so the
  kube-insight AIOps prompt guidance must tell the agent to pass the
  environment's Prometheus-compatible metrics endpoint explicitly. A smoke
  query against `up` returned a successful VictoriaMetrics response.
- The selected kagent prompt integration is a ConfigMap prompt library labeled
  `kagent.dev/prompt-library: "true"` and included through
  `promptTemplate.dataSources`. This keeps kube-insight's validated prompt
  guidance reusable and ecosystem-visible without relying on MCP prompt
  discovery.
- Health-output tuning was added after staging validation showed agents can
  overreact to a large queued count. In kube-insight health, `queued` means the
  initial LIST snapshot completed and the resource stream is waiting for a
  watch stream slot; queued alone is not an unhealthy signal. Treat retrying,
  errors, stale, skipped, and not_started streams as coverage gaps. The MCP
  health formatter now includes this note directly in tool output, and kagent
  prompt-library guidance mirrors it.
- Short-term watch freshness tuning now prioritizes AIOps-critical resources in
  the built-in profiles and periodically relists queued resources with
  `collection.watch.queuedRelistIntervalSeconds`. This improves freshness on
  CRD-heavy clusters without increasing the number of long-lived WATCH streams.
  A simplified watch rotation mechanism was also added with
  `collection.watch.streamRotationSeconds`, so active watch streams periodically
  release their slots and queued resources can receive real watch windows.
  Dynamic priority adjustment design is captured in
  [Dynamic Watch Scheduling](../ingestion/dynamic-watch-scheduling-design.md).
  The first implementation now replaces plain semaphore acquisition with
  score-based slot selection using writer-local signals; query/incident hints
  and the shared hint sink remain follow-up work.
- Score-based watch slot selection was validated in the staging environment
  with chDB storage, 64 active watch slots, 60-second queued relists, and
  60-second stream rotation. Watch logs showed slot acquisition with
  `scheduleScore` values, active stream rotation, and queued relists. The
  health API reported 255 resources, 64 healthy streams, 191 queued streams,
  `complete=true`, `unstable=0`, `errors=0`, `stale=0`, `retrying=0`, and no
  queued resource older than 120 seconds in the validation window.
- Automated scheduler validation cases now cover static priority ordering,
  queued-wait fairness, recent activity boosts, object-count cost penalties,
  retry/error penalties, disabled profiles, and rotation handoff. Collector
  package repeat tests and race tests passed for the scheduler path.
- chDB SQL smoke found that schema-exposed logical tables such as `facts`
  failed unless callers qualified the database name. The chDB store now selects
  the configured database after schema initialization, so agent SQL can use the
  same naked table names shown by `kube_insight_schema`.
- kagent-oriented skill guidance was added in
  `kube-insight-skill/references/kagent.md`, and the user-facing integration
  tutorial was added at `docs/users/tutorials/kagent-integration.md`.
- A2A baseline endpoints were added to the API/app listener:
  `/.well-known/agent-card.json`, `POST /message:send`, `GET /tasks/{task_id}`,
  `GET /tasks`, and `POST /tasks/{task_id}:cancel`. The implementation maps
  A2A task IDs to kube-insight agent run IDs and context IDs to agent session
  IDs, and returns final answers as text artifacts.
- A2A streaming was added with `POST /message:stream` and
  `GET /tasks/{task_id}:subscribe`, using Server-Sent Events task snapshots
  backed by the existing kube-insight agent run event stream.
- Baseline A2A artifact/citation projection was added: final answers, retained
  kube-insight artifacts, artifact metadata, and citation targets are returned
  with task artifacts.
- A2A user documentation was added at
  `docs/users/tutorials/a2a-integration.md`.
- External A2A HTTP smoke passed against a temporary local API server:
  Agent Card discovery, non-streaming `message:send`, task lookup, and
  streaming `message:stream` all returned valid protocol-shaped responses. The
  temporary server intentionally had no LLM runner configured, so the smoke
  validated the expected failed-task path without requiring provider secrets.
- Release-scope development is complete. Remaining validation before promotion
  is environment-dependent: run a kagent Agent-level retained-history query in
  an environment with a ready `ModelConfig` and provider Secret. Richer A2A
  proof-bundle shaping is a follow-up unless a concrete external client needs
  it.

### 1. Helm Chart

Add a first-class kube-insight Helm chart, for example under
`charts/kube-insight/`.

The chart should support:

- service modes:
  - all-in-one local/team service: `serve --watch --app --metrics`,
  - API-only reader: `serve --app --metrics` with collection disabled,
  - writer-only collector for split deployments when needed;
- storage modes:
  - external ClickHouse DSN from a Secret for team, kagent, and central history
    installs,
  - chDB as a preferred local-first storage direction once the chart can package
    a chDB-enabled in-cluster runtime,
  - SQLite with PVC only for tests, local demos, and temporary single-cluster
    evidence runs;
- server surfaces:
  - Web UI at `/`,
  - read API at `/api/v1/*`,
  - Streamable HTTP MCP at `/mcp`,
  - metrics endpoint when enabled;
- Kubernetes access:
  - ServiceAccount creation,
  - least-privilege ClusterRole/Role templates for watched resources,
  - configurable namespace-scoped or cluster-scoped watch mode,
  - clear extra RBAC hooks for CRDs and custom resources;
- configuration:
  - generated `ConfigMap` from chart values,
  - Secret references for LLM provider keys and ClickHouse DSN,
  - resource filters, destructive filter audit behavior, retention settings,
    and watched resource lists;
- operations:
  - probes,
  - resource requests and limits,
  - pod security context,
  - labels/annotations,
  - node selectors, tolerations, affinity,
  - optional ingress,
  - optional ServiceMonitor or documented Prometheus scrape annotations.

Validation:

- `helm lint` passes.
- `helm template` output is committed or snapshotted in tests where practical.
- A kind/minikube smoke install starts kube-insight and reaches `/healthz`.
- MCP is reachable at the Helm-created Service path.
- The chart does not contain private hostnames, credentials, or environment
  specific paths.
- Documentation leads with the ClickHouse-backed team/kagent install, then
  documents SQLite as a test/demo/temporary mode that is not intended for
  long-running retained history.

### 2. kagent MCP Example

Add `examples/kagent/remote-mcpserver.yaml` after validating the current kagent
CRD schema.

The example should:

- Register the Helm-created kube-insight service-hosted `/mcp` endpoint.
- Use Streamable HTTP when supported by the target kagent version.
- Include namespace and service-DNS placeholders.
- Include optional auth header wiring with `headersFrom`.
- Avoid hardcoded private hostnames, tokens, or cluster names.

Validation:

- Install kube-insight with the Helm chart, then apply the resource to a kagent
  test cluster.
- Confirm kagent discovers the kube-insight MCP tools.
- Confirm `kube_insight_health` and `kube_insight_schema` are callable.

### 3. kagent Troubleshooting Agent Example

Add `examples/kagent/kube-insight-troubleshooting-agent.yaml`.

The example Agent should:

- Reference the kube-insight MCP server.
- Include only selected kube-insight tools needed for first workflows.
- Use a system message that routes historical claims through kube-insight.
- Tell the agent to use live Kubernetes or monitoring tools for current-state
  validation when available.
- Require evidence citations for final answers.

Minimum tool set:

- `kube_insight_health`
- `kube_insight_schema`
- `kube_insight_sql`
- `kube_insight_js`
- `kube_insight_search`
- `kube_insight_history`
- `kube_insight_topology`
- `kube_insight_service_investigation`

Validation:

- Ask a recent-change question with explicit namespace and time bounds.
- Ask a service-impact question that requires Service, EndpointSlice, Pod, and
  Event evidence.
- Confirm the answer names the evidence source and does not rely only on live
  Kubernetes state.

### 4. kagent User Tutorial

Add `docs/users/tutorials/kagent-integration.md`.

The tutorial should cover:

- Installing kube-insight with the Helm chart.
- Choosing ClickHouse for team/kagent history, chDB when in-cluster packaging is
  available, and SQLite only for tests, demos, or temporary runs.
- Registering kube-insight with kagent as an MCP tool server.
- Creating a kagent troubleshooting agent.
- Running three copyable investigations:
  - alert follow-up: "what changed in this namespace in the last 30 minutes?"
  - rollout incident: "why did this Service lose ready endpoints after deploy?"
  - GitOps incident: "what objects changed after the sync?"
- How to read kube-insight citations and when to validate current state with
  live tools.

Audience rule:

- This is user-facing and should live under `docs/users/tutorials/`.
- It should not link to contributor-only design docs except under a clearly
  labeled "Design background" section.

### 5. kagent Skill Package/Profile

Add a kagent-oriented skill profile without weakening the existing external
agent skill.

Preferred first shape:

- `kube-insight-skill/references/kagent.md` for kagent-specific behavior.
- Optional `kube-insight-skill/profiles/kagent/SKILL.md` only if kagent's
  container-based skill loader benefits from a separate entrypoint.

The skill guidance should say:

- Start with coverage and freshness.
- Read schema before SQL.
- Prefer facts, changes, edges, topology, and retained history before raw docs.
- Bound queries by cluster, namespace, kind, and absolute time.
- Use kube-insight for historical proof, and use live tools for current state.
- Preserve evidence IDs, artifact IDs, query labels, and timestamps in final
  answers.

Validation:

- Package the skill as a kagent container-based skill only after local file
  loading is validated in a test kagent agent.
- Ask the agent what skill it has and confirm it can describe kube-insight's
  evidence workflow.

### 6. Demo And Community Artifact

Create a short reproducible demo:

```text
Investigate Kubernetes incidents with kagent + kube-insight
```

The demo should show:

- kagent receiving the operator's natural-language question.
- kube-insight returning retained evidence.
- optional Prometheus/Grafana/Kubernetes tools validating the live situation.
- a final answer with citations to kube-insight evidence.

Publishable assets:

- README section or docs callout: "Works with kagent".
- A concise blog/demo script.
- A GitHub discussion, issue, or example PR proposal for the kagent community
  after the local demo is validated.

### 7. kube-insight A2A Endpoint

Implement the smallest useful A2A server surface:

- Agent Card endpoint. Done.
- Non-streaming message send mapped to a kube-insight agent run. Done.
- Streaming message send mapped to existing run events. Done.
- Task status mapped from run lifecycle. Done.
- Artifacts mapped from final answer, retained kube-insight artifacts, and
  citation metadata. Done.
- Follow-up: improve specialized SQL/table, topology, and proof-bundle
  presentation if external-client validation needs it.
- Context ID mapped to session ID. Done.

Initial capability metadata:

- `historical-kubernetes-investigation`
- `recent-change-analysis`
- `topology-evidence`
- `service-impact-investigation`
- `retained-object-history`

Validation:

- Use an A2A client to fetch the agent card.
- Send a bounded incident question.
- Confirm task completion, artifacts, and evidence citations survive the A2A
  response.
- Confirm streaming event order is stable.

### 8. A2A Documentation

Add user-facing docs after the endpoint works. Done in
`docs/users/tutorials/a2a-integration.md`.

The docs should distinguish:

- MCP: agents use kube-insight as a tool server.
- A2A: agents delegate a whole investigation branch to kube-insight.

Avoid saying that kagent consumes kube-insight A2A directly unless the exact
path has been tested.

### 9. Adjacent Ecosystem Notes

Keep adjacent ecosystem notes lightweight in this release:

- HolmesGPT / Robusta: explore kube-insight as a custom data source or MCP
  source.
- K8sGPT: document complementary roles or prototype a retained-evidence
  adapter.
- Botkube: show a ChatOps workflow where an alert is followed by kube-insight
  historical evidence.
- Kubernetes/OpenShift MCP servers: pair live current-state tools with
  kube-insight retained-history tools.

## Acceptance Criteria

The release is ready when:

- `helm lint` and a fresh-cluster Helm smoke install pass.
- The Helm chart exposes a stable Service path for Web UI, API, MCP, and
  metrics.
- kagent can list and call kube-insight MCP tools from an Agent CR.
- A kagent troubleshooting agent answers at least one retained-history question
  with kube-insight evidence citations.
- A2A agent card discovery and one bounded A2A investigation request work
  against the Helm-installed service or a local service process.
- The user tutorial works on a fresh local or test-cluster setup starting from
  Helm install.
- The skill/profile is loaded by a kagent agent or the docs clearly mark it as
  a repository-local profile pending package validation.
- No examples contain private hostnames, credentials, or environment-specific
  paths.
- The public positioning says kube-insight complements observability and live
  Kubernetes tools.
- Narrow tests, `git diff --check`, and relevant docs link checks pass.

## Implementation Notes

- Keep the existing MCP tool contracts stable. kagent integration should not
  require a separate kagent-only API.
- Treat the Helm chart as the canonical in-cluster install path for ecosystem
  integrations. Raw manifests may exist as generated examples, but they should
  not become a second source of truth.
- Treat Helm as installation and CRDs as a future runtime control plane. Do not
  overload Helm values with every future extraction or relationship rule once a
  Kubernetes-native policy API exists.
- Keep chart values generic. Do not bake in local domains, private registries,
  personal model providers, or cluster-specific resource lists.
- Prefer examples and skill guidance before code-side hacks. If an agent uses
  kube-insight poorly, first improve tool descriptions, tutorial prompts, and
  skill instructions.
- Do not add hidden query rewrites to compensate for vague agent prompts unless
  the rewrite enforces deterministic product semantics or safety.
- If auth is included, use generic placeholders and Kubernetes Secret examples.
- If a live kagent test reveals a durable workflow rule, update this plan, the
  tutorial, or `AGENTS.md` in the same change.

## Suggested Task Breakdown

1. Design chart values for all-in-one, API-only, writer-only, SQLite PVC, and
   external ClickHouse modes.
2. Implement `charts/kube-insight/` with RBAC, Service, Deployment, ConfigMap,
   Secret references, probes, metrics, and optional ingress.
3. Run `helm lint`, `helm template`, and a fresh-cluster smoke install. Done:
   local render checks and staging all-in-one smoke pass.
4. Validate current kagent CRD fields for external MCP servers. Done for
   `kagent.dev/v1alpha2`.
5. Create minimal Helm-backed kube-insight Service and RemoteMCPServer examples.
   Done in `examples/kagent/remote-mcpserver.yaml`.
6. Create kagent Agent example with kube-insight tools. Done in
   `examples/kagent/troubleshooting-agent.yaml`.
7. Run local/test-cluster smoke against `kube_insight_health` and
   `kube_insight_schema`.
8. Write kagent tutorial. Done in
   `docs/users/tutorials/kagent-integration.md`.
9. Add kagent skill profile/reference. Done in
   `kube-insight-skill/references/kagent.md`.
10. Run an end-to-end retained-history demo. CRD/tool-discovery, Agent
   acceptance, runtime readiness, and manual retained-evidence query smoke
   passed in the validation environment.
11. Add README/docs "Works with kagent" and Helm install entries.
12. Implement A2A agent card, non-streaming send, streaming send, task status,
   final-answer artifacts, retained artifact/citation projection, task list,
   subscription, and cancellation. Done.
13. Add A2A user docs. Done in
   `docs/users/tutorials/a2a-integration.md`.
14. Validate with an external A2A client and refine proof-bundle shaping only if
   the client integration needs it.

## Risks

- kagent CRD schemas may change. Pin examples to a tested kagent version.
- Helm chart scope can sprawl into production-operator work. Keep the first
  chart complete for installation and service exposure, but defer advanced
  multi-cluster automation and backup/restore jobs unless needed for the demo.
- kagent docs and examples may imply A2A exposure for kagent-managed agents, not
  consumption of arbitrary external A2A agents. Validate before wording.
- MCP tool descriptions may be too generic for kagent agents to use
  efficiently. The skill profile and examples must provide concrete evidence
  workflow rules.
- Auth propagation between kagent, kube-insight, and future Kubernetes RBAC
  filtering needs explicit design. Keep the first example simple, then harden.
- A2A scope can grow quickly. Keep the release baseline to agent card
  discovery, non-streaming message send, streaming task snapshots, task status,
  and baseline artifact/citation projection unless external-client validation
  proves richer proof-bundle shaping is required.
- Community promotion before a working demo would create confusion. Publish the
  kagent story only after a reproducible smoke passes.

## Follow-Up Research

- Validate kagent external A2A consumption, if any.
- Research whether kube-insight's built-in Web UI/A2A agent should optionally
  derive its LLM provider configuration from a kagent `ModelConfig`. This is not
  in the current release scope: today the built-in agent uses `server.chat`,
  while kagent-created Agents use kagent `ModelConfig`. Evaluate a lightweight
  shared-Secret documentation path first, then a real `ModelConfig` adapter only
  after confirming the kagent CRD schema and Secret key conventions are stable.
- Decide whether the chart should later manage ClickHouse itself or continue to
  assume an external ClickHouse service for team deployments.
- Future release only: design the CRD/operator control plane for resource extraction,
  relationship extraction, filters, retention, and optional active evidence
  probes.
- Compare kagent skill packaging options for repository-based versus
  container-based distribution.
- Explore a HolmesGPT custom toolset or MCP data-source spike.
- Monitor Kubernetes/OpenShift MCP server patterns for least-privilege,
  read-only live-state validation examples.
