# External Agent Skill Tutorial

This tutorial shows how to use `kube-insight-skill/` with an external agent such
as Codex, Claude, a custom MCP client, or another assistant runtime. Use this
path when the agent runs outside kube-insight and uses kube-insight as an
evidence source.

The skill is different from the built-in Web UI agent:

- The built-in agent runs inside the kube-insight API server and is configured
  with `server.chat`.
- The external skill is an instruction package for another agent. That agent
  brings its own model, shell, filesystem, MCP client, and data tools.

## Prerequisites

- A running kube-insight service with API and MCP enabled.
- An external agent runtime that can load local skill instructions or read a
  project-provided skill directory.
- Optional but recommended: shell tools such as `jq`, Python, DuckDB, or
  notebooks for large aggregations.

## 1. Start kube-insight

For local SQLite:

```bash
./kube-insight serve --watch --app --db kubeinsight.db
```

For a ClickHouse-backed team service, start from the ClickHouse example config:

```bash
export KUBE_INSIGHT_CLICKHOUSE_DSN='http://127.0.0.1:8123/?user=kube_insight&password=...'
./kube-insight --config config/kube-insight.clickhouse.example.yaml serve \
  --watch \
  --app \
  --metrics
```

The app listener exposes:

- API: `http://127.0.0.1:8090/api/v1/*`
- MCP: `http://127.0.0.1:8090/mcp`
- Web UI: `http://127.0.0.1:8090/`

## 2. Verify The Evidence Service

```bash
curl -fsS http://127.0.0.1:8090/healthz
curl -fsS http://127.0.0.1:8090/api/v1/server/info
curl -fsS 'http://127.0.0.1:8090/api/v1/health?detail=full&limit=20'
```

The agent should treat stale, missing, retrying, or errored collector streams as
evidence quality signals.

## 3. Make The Skill Available

Point your external agent at the repository's skill directory:

```text
kube-insight-skill/
```

The entrypoint is:

```text
kube-insight-skill/SKILL.md
```

If your agent has a personal skill/plugin directory, copy or symlink
`kube-insight-skill/` into that directory. Keep the `references/` directory next
to `SKILL.md`; it contains query patterns and task recipes used by the skill.

## 4. Prefer Streamable HTTP MCP

Configure the external agent's MCP client with:

```json
{
  "mcpServers": {
    "kube-insight": {
      "type": "streamable-http",
      "url": "http://127.0.0.1:8090/mcp"
    }
  }
}
```

Use stdio MCP only when the agent runtime cannot use remote MCP:

```bash
./kube-insight serve mcp --db kubeinsight.db
```

## 5. Fallback To HTTP API

If the agent cannot use MCP, it can use the HTTP API:

```bash
curl -fsS http://127.0.0.1:8090/api/v1/schema
curl -fsS -X POST http://127.0.0.1:8090/api/v1/sql \
  -H 'content-type: application/json' \
  -d '{"sql":"select 1","maxRows":1}'
```

The SQL API is read-only. Agents should call schema first because SQLite and
ClickHouse-compatible backends expose different table names and timestamp
shapes.

## 6. Give The Agent A Strong First Task

Use tasks that need retained history or aggregation:

```text
Use kube-insight to determine whether gcp cluster 2 had Node pool membership or capacity changes in the last 3 days. Start with coverage and cite exact evidence.
```

```text
Use kube-insight to find the namespace with the highest Pod count yesterday. Use retained history, not only current kubectl state.
```

```text
Use kube-insight to check whether the checkout Service had any empty EndpointSlices during the incident window.
```

The expected agent loop is:

1. Check coverage and discover clusters.
2. Read schema and backend notes.
3. Run bounded SQL or typed MCP tools.
4. Export larger intermediate rows to files when useful.
5. Compute with Python, jq, DuckDB, or another local tool when the task is an
   aggregation.
6. Answer with cluster ID, object identity, timestamps, row counts, artifacts,
   and citations.

## What The Skill Should Avoid

- Do not answer from memory or raw intuition.
- Do not guess table names before reading schema.
- Do not treat fuzzy names such as `gcp2` as stable cluster IDs.
- Do not use broad search for recipe-shaped aggregation questions.
- Do not raise built-in JavaScript row/query caps to hide a poor plan.
- Do not use kube-insight as a replacement for live operational actions; use it
  for retained evidence and read-only historical investigation.

## Next Steps

- [kube-insight Agent Skill](../../kube-insight-skill/SKILL.md) is the full
  agent-facing instruction file.
- [Agent SQL Cookbook](../workflows/agent-sql-cookbook.md) has backend-specific
  SQL patterns.
- [Built-in Web UI Agent Tutorial](builtin-webui-agent.md) covers the
  server-owned agent path.
