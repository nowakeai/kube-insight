# kube-insight Web UI

This directory contains the React/Vite frontend that is embedded by
`kube-insight serve --app` and used by the Docker Compose development stack.

## Development

For normal frontend work, use the repository-level Compose environment so the
Web UI, API/watcher, metrics endpoint, and ClickHouse backend run together:

```bash
make dev-compose-up-detached
make dev-compose-ps
make dev-compose-logs-web
```

The Compose Web UI listens on `http://127.0.0.1:5173` by default and proxies:

- `/api/*` to `KUBE_INSIGHT_API_PROXY_TARGET` (default `http://watcher:8090`)
- `/healthz` to the watcher/app service
- `/metrics` to `KUBE_INSIGHT_METRICS_PROXY_TARGET` (default
  `http://watcher:8090`)

After dependency or Dockerfile changes, rebuild the web service:

```bash
make dev-compose-rebuild-web
```

## Local Checks

The repository `make test` and `make build` targets run the production frontend
build before Go tests/builds. Frontend-only checks can be run directly:

```bash
npm --prefix web ci
npm --prefix web run build
npm --prefix web run lint
npm --prefix web run test
```

## Runtime Shape

The first Web UI milestone is agent-first. The UI consumes the server-side agent
API for sessions, streamed runs, retry/cancel actions, evidence citations, and
typed Kubernetes artifacts. It should not require browser-side LLM credentials;
provider configuration lives on the kube-insight API server.
