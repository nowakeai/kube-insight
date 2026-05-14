# Agent And UI Roadmap

Kube-insight is for human operators and for agents. The same evidence API should
power CLI, web, MCP, and skill integrations.

## CLI

The CLI remains the lowest-level operational interface:

- collect and watch resources,
- ingest offline bundles,
- investigate service incidents,
- emit JSON for automation,
- run validation and benchmarks.

## Web And Chat

Planned web mode:

- timeline view,
- Service-centric topology graph,
- evidence ranking table,
- exact version and diff panel,
- chat panel backed by OpenAI when `OPENAI_API_KEY` is available.

Configuration shape:

```yaml
server:
  web:
    enabled: true
    listen: 127.0.0.1:8081
  chat:
    enabled: true
    openaiApiKeyEnv: OPENAI_API_KEY
    model: gpt-5.2
```

The chat layer must call structured investigation APIs first. It should cite
object IDs, facts, changes, versions, and diffs rather than inventing answers.

## API

Initial read API examples:

```text
GET /api/v1/services/{namespace}/{name}/investigation?from=&to=
GET /api/v1/objects/{logical_id}/versions
GET /api/v1/objects/{logical_id}/versions/{version_id}
GET /api/v1/topology?kind=&namespace=&name=
GET /api/v1/facts?key=&value=&from=&to=
```

Initial write/admin API examples:

```text
POST /api/v1/ingest
POST /api/v1/discovery/resources
POST /api/v1/watch/resources
POST /api/v1/validation/poc
```

## MCP

MCP tools should expose investigation primitives:

- `kube_insight_investigate_service`
- `kube_insight_get_topology`
- `kube_insight_get_versions`
- `kube_insight_diff_versions`
- `kube_insight_query_facts`

Responses should be compact by default and include handles for retrieving full
version JSON only when needed.

## Skill

The agent skill should provide:

- incident triage workflow prompts,
- common Kubernetes troubleshooting routes,
- examples of Service -> EndpointSlice -> Pod -> Node/Event investigation,
- guidance for using exact versions as proof.

The skill should never require direct database access; it should call CLI, API,
or MCP surfaces.
