# Agent Ecosystem Research

Status date: 2026-06-30

Audience: maintainers and contributors planning ecosystem integrations for
kube-insight.

## Purpose

kube-insight should integrate with cloud-native AIOps ecosystems as a
Kubernetes retained evidence specialist. The product role is not to replace
Kubernetes assistants, observability tools, runbooks, or agent runtimes. The
product role is to make historical Kubernetes object evidence, facts, changes,
topology, retained versions, and citations available to systems that already
live in Kubernetes, SRE, incident, observability, or ChatOps workflows.

The strategic message is:

```text
kagent asks. kube-insight proves.
```

The same message should generalize beyond kagent:

```text
Cloud-native agents coordinate. Observability systems measure. kube-insight
retains and proves what Kubernetes looked like when the incident happened.
```

## Selection Criteria

This research intentionally excludes broad generic agent frameworks from the
main support roadmap unless they become important to cloud-native AIOps users.
An ecosystem belongs on the main roadmap when it satisfies most of these
criteria:

- It is strongly connected to Kubernetes, SRE, incident response,
  observability, GitOps, or ChatOps.
- It has meaningful community, company, or foundation backing.
- It is active enough that users may encounter it in real production
  troubleshooting workflows.
- kube-insight has a clear complementary role: retained Kubernetes object
  evidence, not another live `kubectl`, metrics, or log reader.

General agent SDKs and orchestration frameworks such as Google ADK, OpenAI
Agents SDK, and LangGraph can still be useful for compatibility smoke tests,
but they are not cloud-native AIOps ecosystem priorities for this plan.

## kagent Findings

kagent is the highest-priority ecosystem for the next integration milestone
because it is Kubernetes-native, agent-focused, and explicitly built around
Kubernetes CRDs, MCP tools, skills, and A2A-facing agents.

Current official-doc findings:

- kagent agents are Kubernetes custom resources. Declarative agents specify
  model configuration, system messages, tools, and optional skills.
- kagent tool integration is the most direct fit for kube-insight. kagent
  supports MCP tools and can bring external MCP servers into agent toolsets.
- `RemoteMCPServer` and `MCPServer` references are used from
  `Agent.spec.declarative.tools`; an agent can select specific tool names from
  a server.
- kagent supports cross-namespace MCP tool references and `headersFrom` for
  passing per-tool-call headers from Kubernetes Secrets or ConfigMaps.
- kagent skills are capability descriptions and runtime guidance. The
  container-based skill flow packages `SKILL.md`, scripts, and resources into a
  container image, then references it from `spec.skills.refs`.
- kagent-created agents expose A2A. The documented A2A flow adds `a2aConfig`
  skill metadata to an Agent CR, exposes an agent card under the kagent
  controller A2A endpoint, and lets A2A clients invoke that kagent agent.
- The documented A2A skill metadata is not the same thing as a kube-insight
  executable skill. It describes capabilities for the agent card.

Implication for kube-insight:

- First integrate with kagent as a remote MCP tool server and as a kagent skill
  package/profile.
- A complete Helm chart is a prerequisite for a credible kagent integration
  story. kagent users should be able to install kube-insight into a cluster,
  expose the API/MCP service, configure RBAC and storage, and then reference the
  service from kagent examples without hand-written manifests.
- Use A2A as a later interoperability layer for delegating work to a
  kube-insight investigation agent, but do not claim that kagent Agent CRs can
  directly import arbitrary external A2A agents until that path is validated
  against the exact kagent version.

Recommended first kagent story:

```text
A kagent troubleshooting agent has Prometheus, Grafana, kubectl, GitOps, and
kube-insight tools. It uses kube-insight for retained object history and
evidence citations, then uses the other tools for live validation or metric/log
correlation.
```

## Protocol Findings

### MCP

MCP is the best current tool integration path.

For kube-insight, MCP should remain thin and evidence-oriented:

- `kube_insight_health` for coverage and cluster discovery.
- `kube_insight_schema` for backend-aware SQL and recipe discovery.
- `kube_insight_sql` for bounded read-only evidence queries.
- `kube_insight_search` for bounded symptom/fact search.
- `kube_insight_history` for retained versions, observations, and diffs.
- `kube_insight_topology` for relationships around a chosen root object.

MCP is where kagent and many non-kagent agent frameworks can consume
kube-insight today without waiting for a new agent-to-agent protocol endpoint.

### A2A

A2A is the best current agent-to-agent delegation story, but it is not a
replacement for MCP tools.

Official A2A 1.0.0 concepts map well to kube-insight's existing server-side
agent runtime:

| A2A concept | kube-insight fit |
| --- | --- |
| Agent Card | kube-insight investigation agent capability manifest |
| Message | user task or follow-up |
| Task | agent run |
| Context ID | agent session |
| Artifact | answer, SQL result artifact, topology artifact, proof bundle |
| Streaming | existing run event stream |
| Security schemes | future authn/authz-aware service mode |

The value of A2A for kube-insight is discoverable delegation:

```text
An orchestrating agent can discover that kube-insight is the specialist for
historical Kubernetes evidence and delegate the investigation branch to it.
```

The main caution is that A2A skills are capability metadata. They should not be
used as a hidden tool parameter schema or as a substitute for MCP tool
contracts.

## Kubernetes-Native Configuration Direction

Helm should be the first complete in-cluster installation path. After that,
kube-insight should move toward a Kubernetes operator-style control plane for
runtime configuration, similar in spirit to how Prometheus Operator turns
monitoring targets and rules into Kubernetes custom resources.

This direction fits the cloud-native AIOps ecosystem better than a config-file
only model:

- Platform teams can manage kube-insight behavior through GitOps.
- kagent and other Kubernetes-native agents can discover and reason about
  kube-insight configuration as Kubernetes resources.
- Resource-specific extraction rules can evolve without rebuilding the
  collector.
- Relationship extraction for CRDs can be owned by the teams that own those
  resources.
- Optional active probes can be declared, reviewed, RBAC-scoped, and audited.

Possible future CRDs:

| CRD | Purpose |
| --- | --- |
| `KubeInsight` | Top-level service instance: storage, server surfaces, retention, auth, and deployment mode. |
| `ResourceExtractionPolicy` | Per-GVR facts, status fields, relationships, change summaries, and retention profile selection. |
| `RelationshipPolicy` | Declarative owner, selector, reference, label, endpoint, or custom relationship extraction for CRDs. |
| `ActiveEvidenceProbe` | Controlled active collection such as HTTP GET, log sampling, or pod exec for targeted evidence. |
| `EvidenceRetentionPolicy` | Retention windows, minimum versions, filter-decision retention, and proof preservation rules. |
| `EvidenceFilterPolicy` | Redaction, destructive filtering, and normalization rules with auditable decisions. |

The first CRD milestone should stay declarative. It should expose the existing
resource profile, filter, extractor, retention, and relationship concepts as
Kubernetes APIs. Programmable probes such as HTTP GET, log collection, and pod
exec should come later because they need stronger safety boundaries.

Programmable or active collection must be treated as a privileged extension:

- It needs explicit RBAC and namespace/resource scoping.
- It must be disabled by default.
- It must record auditable collection decisions and command/probe metadata.
- It should run with strict timeouts, byte limits, concurrency limits, and
  allowlisted commands or probe types.
- It must keep kube-insight's core safety model: retained evidence and facts are
  proof; active probes are supplemental evidence with their own provenance.

This operator/CRD direction should complement, not replace, the existing
configuration model. YAML config remains useful for local and single-binary
runs. CRDs become the Kubernetes-native control plane for team deployments.

## Adjacent Cloud-Native AIOps Ecosystems To Track

These systems are not all direct competitors to kagent. They are useful future
integration targets because they are close to Kubernetes operations, SRE
investigations, observability, or incident response.

### HolmesGPT / Robusta

Fit:

- Open-source SRE investigation agent focused on production incidents.
- Documentation lists many data sources, including Kubernetes, Prometheus,
  Grafana, Loki, Tempo, ArgoCD, cloud providers, and MCP-based sources.
- Robusta is already used in Kubernetes alerting and incident workflows, which
  makes it a credible distribution path for retained Kubernetes evidence.

kube-insight path:

- Add kube-insight as an MCP data source or custom toolset.
- Position kube-insight as the missing retained Kubernetes object history layer
  next to HolmesGPT's metrics, logs, and cloud/service data sources.

Why track:

- It is close to the SRE use case and already has a broad data-source story.
  The integration could validate kube-insight's cross-tool investigation value.

### K8sGPT

Fit:

- Kubernetes-focused analyzer with CLI, operator, integrations, and gRPC serve
  mode.
- Strong Kubernetes troubleshooting mindshare, even though it is not the same
  category as a multi-agent runtime.

kube-insight path:

- Short-term: compare and document complementary roles.
- Medium-term: explore whether K8sGPT analyzer output can become another
  evidence signal, or whether kube-insight can provide retained evidence to a
  K8sGPT-facing assistant through MCP/HTTP.

Why track:

- K8sGPT has strong Kubernetes troubleshooting mindshare. The integration story
  should avoid a competitor framing and instead emphasize retained historical
  proof versus live analyzer findings.

### Botkube

Fit:

- ChatOps-oriented Kubernetes monitoring and assistant surface.
- Focuses on alerts, notifications, collaboration platforms, and Kubernetes
  troubleshooting from chat.

kube-insight path:

- Add links or examples showing how a ChatOps assistant can call kube-insight
  MCP or HTTP endpoints for historical evidence after an alert.

Why track:

- Botkube is a distribution channel for incident workflows. kube-insight can
  supply the "what changed before this alert" proof path.

### Kubernetes And OpenShift MCP Servers

Fit:

- Multiple projects expose live Kubernetes APIs through MCP.
- Red Hat documents a Kubernetes/OpenShift MCP server pattern for AI assistants
  and recommends least-privilege ServiceAccounts and optional read-only mode.

kube-insight path:

- Treat live Kubernetes MCP servers as complementary tools, not replacements.
- In examples, pair live Kubernetes MCP with kube-insight MCP:
  live server validates current state; kube-insight proves historical state.

Why track:

- This is likely to become the default shape of Kubernetes agent toolchains.
  kube-insight should be the retained-history tool in that bundle.

## Prioritization

| Priority | Ecosystem | First integration form | Reason |
| --- | --- | --- | --- |
| P0 | kagent | Helm install, remote MCP server, kagent Agent example, skill package/profile | Kubernetes-native community and direct user goal |
| P1 | HolmesGPT / Robusta | MCP or custom data-source spike | Direct SRE investigation adjacency |
| P1 | A2A protocol support | kube-insight A2A server | Protocol-level delegation to a specialist investigation agent |
| P2 | K8sGPT | complementarity research and possible adapter | Strong Kubernetes troubleshooting mindshare |
| P2 | Botkube | ChatOps workflow example | Real incident-response distribution path |
| P2 | Kubernetes/OpenShift MCP servers | paired live-state plus retained-history example | Common future shape for Kubernetes agent toolchains |

## Lower-Priority Compatibility Work

Generic agent frameworks are useful for proving that kube-insight's MCP and A2A
surfaces are portable, but they should not drive the cloud-native AIOps roadmap.

- Google ADK, OpenAI Agents SDK, and LangGraph can be used for occasional
  compatibility smoke tests.
- Any examples for those frameworks should live under generic MCP/A2A
  compatibility docs, not under cloud-native AIOps ecosystem positioning.
- Do not prioritize framework-specific SDK integrations until a real
  Kubernetes/SRE user workflow requires them.

## Product Positioning Rules

- Say "kube-insight integrates with kagent as a remote MCP tool server" for the
  first milestone.
- Say "kube-insight can provide a kagent skill package/profile that teaches
  agents how to use retained evidence efficiently."
- Say "A2A support makes kube-insight available as a delegatable Kubernetes
  evidence agent for A2A-capable systems."
- Do not say "kube-insight is a kagent Agent" unless kube-insight is actually
  deployed and reconciled through kagent's Agent CRD.
- Do not say "A2A skills are executable skills." In kagent examples,
  `a2aConfig.skills` is agent-card capability metadata.
- Do not position kube-insight against Prometheus, Grafana, Loki, kubectl,
  GitOps tools, K8sGPT, HolmesGPT, or Botkube. Position it as retained
  Kubernetes evidence that those tools can use.

## Open Questions

- What exact `RemoteMCPServer` transport and URL fields are required in the
  current kagent CRD version for a service-hosted kube-insight `/mcp` endpoint?
- Does kagent currently support importing or directly referencing external A2A
  agents, or only exposing kagent-managed agents through A2A?
- Should kube-insight package its external agent skill as a kagent
  container-based skill image, a Git-distributed skill, or both?
- What auth shape should the first kagent example use: unauthenticated local
  cluster service, bearer token through `headersFrom`, or mTLS through a
  gateway?
- How should kube-insight citations map to A2A artifacts and extensions so
  client agents preserve proof rather than summarizing it away?
- Which HolmesGPT/Robusta extension path is most appropriate: MCP source,
  custom toolset, or documented sidecar workflow?
- Should K8sGPT integration consume K8sGPT analyzer output as evidence, expose
  kube-insight as retained proof to K8sGPT-facing agents, or remain a
  complementary positioning story?
- Which configuration concepts should become CRDs first: top-level service
  instances, resource extraction policies, relationship policies, retention
  policies, or filters?
- What safety model is required before supporting active evidence probes such
  as HTTP GET, log sampling, or pod exec?

## Sources

- kagent MCP tool guide:
  <https://kagent.dev/docs/kagent/getting-started/first-mcp-tool>
- kagent tool concepts:
  <https://kagent.dev/docs/kagent/concepts/tools>
- kagent agent concepts:
  <https://kagent.dev/docs/kagent/concepts/agents>
- kagent skills example:
  <https://kagent.dev/docs/kagent/examples/skills>
- kagent A2A example:
  <https://kagent.dev/docs/kagent/examples/a2a-agents>
- A2A specification:
  <https://github.com/a2aproject/A2A/blob/main/docs/specification.md>
- HolmesGPT docs:
  <https://holmesgpt.dev/latest/>
- K8sGPT docs:
  <https://docs.k8sgpt.ai/>
- K8sGPT serve mode:
  <https://docs.k8sgpt.ai/reference/cli/serve-mode/>
- Botkube AI Assistant:
  <https://docs.botkube.io/plugins/ai-assistant/>
- Red Hat Kubernetes/OpenShift MCP server article:
  <https://developers.redhat.com/articles/2025/09/25/kubernetes-mcp-server-ai-powered-cluster-management>
