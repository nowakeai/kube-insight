# kube-insight Helm Chart

This chart installs kube-insight as a Kubernetes retained-evidence collector and
agent-facing service.

The default chart install uses embedded chDB storage on a PVC. Release
container images are chDB-capable by default, while standalone binary archives
keep chDB in a separate artifact to stay small. Use external ClickHouse for
shared team storage. SQLite is available only for tests, demos, and temporary
runs; do not use SQLite for long-running retained-history deployments.

## Install From OCI Registry

Use the published chart for normal installs:

```bash
helm install kube-insight oci://ghcr.io/nowakeai/charts/kube-insight \
  --namespace kube-insight \
  --create-namespace \
  --version <CHART_VERSION>
```

Use the local `charts/kube-insight` path only when installing from a source
checkout.

## Default: chDB-backed Install

Use the default install when you want an embedded ClickHouse-compatible store on
the release PVC from a source checkout:

```bash
helm install kube-insight charts/kube-insight \
  --namespace kube-insight \
  --create-namespace
```

chDB mode serves Web UI, API, MCP, and collection from the same Service. Chart
metrics are disabled for chDB in this version because the Prometheus metrics
collector currently supports SQLite and ClickHouse HTTP metrics.

The default chDB configuration opens 4 embedded sessions for concurrent API,
Web UI, MCP, and agent queries.

The default retention window is 180 days, about 6 months.

## Shared Team Storage: ClickHouse-backed Install

Provide an external ClickHouse DSN through a Secret:

```bash
kubectl create namespace kube-insight --dry-run=client -o yaml | kubectl apply -f -

kubectl -n kube-insight create secret generic kube-insight-clickhouse \
  --from-literal=dsn='http://clickhouse.example:8123/?user=kube_insight&password=...'

helm install kube-insight charts/kube-insight \
  --namespace kube-insight \
  --create-namespace \
  -f charts/kube-insight/values-clickhouse.yaml
```

The release:

- watches Kubernetes resources with the release ServiceAccount,
- stores retained evidence in ClickHouse,
- serves Web UI, HTTP API, Streamable HTTP MCP, and Prometheus metrics on one
  Service port.

## Explicit SQLite Test Or Temporary Install

Use SQLite only for tests, local demos, temporary evaluation, or temporary
single-cluster runs. It is not a long-running storage mode:

```bash
helm install kube-insight charts/kube-insight \
  --namespace kube-insight \
  --create-namespace \
  --set storage.driver=sqlite \
  --set image.tag='<VERSION>'
```

Use `mode=api` for a read-only API/MCP/Web UI instance and `mode=writer` for a
collector-only instance.

## kagent Integration

If kagent is already installed, the chart can create the kube-insight kagent
integration resources for you:

Prerequisites:

- kagent CRDs are installed in the cluster;
- `kagent.namespace` already exists (`kagent` by default);
- create an Agent only after the referenced kagent `ModelConfig` exists.

```bash
helm install kube-insight charts/kube-insight \
  --namespace kube-insight \
  --create-namespace \
  --set kagent.enabled=true
```

This renders resources in `kagent.namespace` (`kagent` by default):

- `ConfigMap/kube-insight-prompts`, labeled as a kagent prompt library;
- `RemoteMCPServer/kube-insight`, pointing to the kube-insight in-cluster
  `/mcp` endpoint.

The default MCP URL is derived from the Helm release Service:

```text
http://<release-name>.<release-namespace>.svc.cluster.local:8090/mcp
```

Override it when kube-insight is exposed through another Service or gateway:

```bash
helm upgrade kube-insight charts/kube-insight \
  --namespace kube-insight \
  --reuse-values \
  --set kagent.enabled=true \
  --set kagent.remoteMCPServer.url='http://kube-insight.example/mcp'
```

Optionally create a basic retained-evidence troubleshooting Agent:

```bash
helm upgrade kube-insight charts/kube-insight \
  --namespace kube-insight \
  --reuse-values \
  --set kagent.enabled=true \
  --set kagent.agent.create=true \
  --set kagent.agent.modelConfig='<MODEL_CONFIG_NAME>'
```

`kagent.enabled` requires kagent CRDs to exist and cannot be used with
`mode=writer`, because writer mode does not expose the MCP Service.
