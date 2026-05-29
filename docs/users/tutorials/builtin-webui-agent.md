# Built-in Web UI Agent Tutorial

This tutorial walks through the shortest path from a release binary to the
built-in Web UI agent. Use this path when you want kube-insight itself to run
the agent loop and show answers, tool progress, artifacts, and citations in the
browser.

## Prerequisites

- A Kubernetes kubeconfig context that can list the resources you want to
  investigate.
- A kube-insight release binary.
- An OpenAI or OpenAI-compatible chat provider.

The built-in agent uses server-side credentials. Do not put provider API keys in
the browser.

## 1. Create A Config

Start from the example config and enable server-side chat:

```bash
cp config/kube-insight.example.yaml kube-insight.local.yaml
```

Set the `server.chat` block:

```yaml
server:
  chat:
    enabled: true
    provider: openai-compatible
    apiKeyEnv: OPENAI_API_KEY
    baseUrlEnv: OPENAI_BASE_URL
    model: gpt-5.2
    maxIterations: 32
```

Supported provider values are `openai` and `openai-compatible`. For the default
OpenAI endpoint, omit `baseUrlEnv` or leave the named environment variable
unset. For an OpenAI-compatible provider, set the base URL to the provider's
`/v1` endpoint.

## 2. Export Provider Credentials

The YAML names environment variables; the secret values live in the process
environment:

```bash
export OPENAI_API_KEY='...'
export OPENAI_BASE_URL='https://api.openai.com/v1'
```

The API reports whether the key and base URL variables are configured, but it
never returns secret values.

## 3. Start The Local App

Run the watcher, HTTP API, MCP service, metrics endpoint, and embedded Web UI on
one local listener:

```bash
./kube-insight --config kube-insight.local.yaml serve \
  --watch \
  --app \
  --metrics \
  --db kubeinsight.db \
  --listen 127.0.0.1:8090
```

Open <http://127.0.0.1:8090>.

`--app` serves the Web UI at `/`, API at `/api/v1/*`, Streamable HTTP MCP at
`/mcp`, legacy SSE at `/sse`, and metrics at `/metrics` when metrics are
enabled.

## 4. Check Server Status

Before trusting agent answers, confirm collection and chat are enabled:

```bash
curl -fsS http://127.0.0.1:8090/api/v1/server/info
curl -fsS 'http://127.0.0.1:8090/api/v1/health?errorsOnly=true&problemLimit=20'
```

In `server.info`, `chat.enabled` should be `true`, `apiKeyConfigured` should be
`true`, and `baseUrlConfigured` should match whether your provider needs a base
URL.

## 5. Ask A History-First Question

Use a question that requires retained history, not just current live state. Good
first prompts:

```text
Did the gcp cluster 2 node pool change in the last 3 days? Use evidence and cite the exact time window.
```

```text
Which namespace had the highest Pod count yesterday? Show how you computed it from retained history.
```

```text
Did any EndpointSlice for the checkout service become empty in the last 6 hours?
```

The agent should inspect coverage, resolve the cluster, use schema-aware SQL or
typed tools, and return cited evidence. Treat citations and artifacts as the
proof path; the natural-language answer is a summary.

## 6. Read The Result

When the run completes, check:

- The final answer states the cluster, object scope, and time range.
- Citations point to retained rows, objects, or generated artifacts.
- Tool progress shows bounded schema, SQL, search, history, topology, or service
  investigation calls rather than repeated broad scans.
- Any uncertainty is tied to collector coverage, stale streams, truncation, or
  missing evidence.

If the answer is weak, retry with a narrower cluster, namespace, object name, or
absolute time window.

## Common Problems

| Symptom | Check |
| --- | --- |
| Chat is disabled | `server.chat.enabled` is false or the server was started without the config you edited. |
| API key is not configured | The environment variable named by `server.chat.apiKeyEnv` is missing in the server process. |
| Provider call fails | Check `server.chat.provider`, `server.chat.model`, and `server.chat.baseUrlEnv`. |
| The agent cannot prove absence | Check collector health for the relevant resource streams and time range. |
| The Web UI loads but API calls fail | Use `serve --app` or make sure the Web UI proxy points at the API listener. |

## Next Steps

- [Quickstart](../getting-started/quickstart.md) covers CLI, service mode, API, and MCP basics.
- [Configuration](../../operators/configuration/configuration.md) documents the full
  `server.chat` schema.
- [Troubleshooting Workflows](../workflows/troubleshooting-workflows.md) gives
  investigation patterns you can adapt into Web UI prompts.
