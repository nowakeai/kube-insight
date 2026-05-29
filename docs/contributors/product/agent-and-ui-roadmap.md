# Agent And UI Roadmap

Audience: product and contributor planning. Use
[Built-in Web UI Agent Tutorial](../../users/tutorials/builtin-webui-agent.md) for a
hands-on user workflow.

Kube-insight is for human operators and for agents. The same evidence API should
power CLI, web, MCP, and skill integrations.

## CLI

The CLI remains the lowest-level operational interface:

- collect and watch resources,
- ingest offline bundles,
- search generic indexed evidence,
- investigate object and service evidence bundles,
- emit JSON for automation,
- run validation and benchmarks.

The CLI must expose primitives that agents can compose instead of scenario
commands that bake in one incident story:

```text
kube-insight query search "no endpoints" --from ... --to ...
kube-insight query object --kind Event --namespace flux-system --name ...
kube-insight query topology --kind ValidatingWebhookConfiguration --name ...
kube-insight query service checkout --namespace default --from ... --to ...
```

## Web And Chat

The first Web UI milestone is agent-first. The opening screen is a search-style
question box; after a question is submitted, the server runs an autonomous agent
that calls kube-insight tools and streams answers, tool progress, citations, and
artifacts back to the browser.

Initial web mode:

- agent chat home page and run view,
- server-managed sessions, runs, cancellation, and event streaming,
- autonomous Eino ADK ChatModelAgent with kube-insight read tools,
- assistant-ui based React chat surface,
- Markdown answers with evidence citations,
- Kubernetes resource artifact rendering,
- topology artifact rendering,
- resource and topology history travel artifacts.

Standalone evidence search, Service investigation pages, topology explorers,
diff workbenches, dashboards, and free-form SQL UI are later surfaces. They can
still exist as agent tools or artifacts during the first milestone.

Configuration shape:

```yaml
server:
  web:
    enabled: true
    listen: 127.0.0.1:8090
  chat:
    enabled: true
    provider: openai
    apiKeyEnv: OPENAI_API_KEY
    model: gpt-5.2
```

The built-in agent runtime is server-side. The primary runtime choice is
CloudWeGo Eino ADK; charmbracelet/fantasy is the fallback if Eino integration
complexity becomes disproportionate. The chat layer must call structured
investigation APIs as tools and cite object IDs, facts, changes, versions, and
diffs rather than inventing answers. See [Agent-First Web UI Design](agent-first-web-ui.md)
for the milestone design.

## API

Initial read API examples:

```text
GET /healthz
GET /api/v1/schema
POST /api/v1/sql
GET /api/v1/health?cluster=&status=&errorsOnly=&staleAfter=&limit=
GET /api/v1/history?cluster=&uid=&kind=&namespace=&name=&from=&to=&maxVersions=&maxObservations=&includeDocs=&diffs=
GET /api/v1/search?q=&kind=&namespace=&from=&to=
GET /api/v1/services/{namespace}/{name}/investigation?from=&to=
GET /api/v1/objects/{logical_id}/versions
GET /api/v1/objects/{logical_id}/versions/{version_id}
GET /api/v1/topology?kind=&namespace=&name=
GET /api/v1/facts?key=&value=&from=&to=
```

The first agent-facing API surface should stay deliberately small:

- `schema`: expose table, column, index, and join hints.
- `sql`: execute read-only SQL with row limits.
- `health`: expose collector coverage and staleness before agents make claims.
- `history`: expose one object's retained content versions, observation trail,
  and diffs without requiring agents to hand-write the joins.

The API can be served on its own, or through the embedded Web UI under the
same-origin `/api/v1/` path. The CLI command is:

```bash
kube-insight serve api --db kubeinsight.db --listen 127.0.0.1:8080
```

Initial write/admin API examples:

```text
POST /api/v1/ingest
POST /api/v1/discovery/resources
POST /api/v1/watch/resources
POST /api/v1/validation/poc
```

## MCP

The first MCP surface is a stdio server that mirrors the core agent API:

```bash
kube-insight serve mcp --db kubeinsight.db
```

Initial tools:

- `kube_insight_schema`: returns SQL schema, indexes, and join hints.
- `kube_insight_sql`: runs read-only SQL with `maxRows`.
- `kube_insight_health`: returns collector coverage and staleness.
- `kube_insight_history`: returns one object's content versions, observations,
  and optional diffs.

This keeps MCP thin while giving agents one safe structured path for historical
proof. Higher-level tools such as search, investigation, and topology can be
added after the SQL/RBAC contract is stable.

## Skill

The agent skill should provide:

- incident triage workflow prompts,
- common Kubernetes troubleshooting routes,
- examples of Service -> EndpointSlice -> Pod -> Node/Event investigation,
- guidance for using exact versions as proof.

The skill should never require direct database access; it should call CLI, API,
or MCP surfaces.
