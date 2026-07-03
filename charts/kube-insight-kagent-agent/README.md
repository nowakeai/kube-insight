# kube-insight kagent Agent Chart

This chart installs kagent resources that connect kagent to an existing
kube-insight deployment.

It does not install kube-insight itself. Install kube-insight first:

```bash
helm install kube-insight oci://ghcr.io/nowakeai/charts/kube-insight \
  --namespace kube-insight \
  --create-namespace
```

Then install the kagent Agent package:

```bash
helm install kube-insight-kagent-agent \
  oci://ghcr.io/nowakeai/charts/kube-insight-kagent-agent \
  --namespace kagent \
  --version <CHART_VERSION> \
  --set agent.modelConfig='<MODEL_CONFIG_NAME>'
```

Prerequisites:

- kagent CRDs are installed.
- The `kagent` namespace exists.
- kube-insight is reachable from kagent through the configured MCP URL.
- The referenced kagent `ModelConfig` exists.

## Profiles

The default profile is `troubleshooter`. It creates:

- a kube-insight prompt library ConfigMap;
- a `RemoteMCPServer` pointing to kube-insight `/mcp`;
- a kagent troubleshooting `Agent` that uses kube-insight retained-evidence
  tools.

For a broader AIOps agent that also references kagent's Kubernetes,
Prometheus, Helm, and shell tools:

```bash
helm upgrade kube-insight-kagent-agent \
  oci://ghcr.io/nowakeai/charts/kube-insight-kagent-agent \
  --namespace kagent \
  --reuse-values \
  --set agent.profile=aiops-fullstack \
  --set agent.name=kube-insight-aiops-fullstack \
  --set agent.description='Full-stack AIOps agent combining kube-insight retained evidence with live platform tools.'
```

Before using `aiops-fullstack`, confirm your kagent tool server exposes the
referenced Kubernetes, Prometheus, Helm, and shell tools:

```bash
kubectl -n kagent get remotemcpserver kagent-tool-server -o yaml
```

## Values

Important values:

| Value | Default | Description |
| --- | --- | --- |
| `remoteMCPServer.url` | `http://kube-insight.kube-insight.svc.cluster.local:8090/mcp` | kube-insight MCP endpoint. |
| `agent.modelConfig` | `default-model-config` | kagent ModelConfig used by the Agent. |
| `agent.profile` | `troubleshooter` | `troubleshooter` or `aiops-fullstack`. |
| `agent.a2aConfig` | `false` | Adds an empty `a2aConfig` block for kagent-managed A2A exposure. |
| `lokiRemoteMCPServer.create` | `false` | Optionally registers a Loki MCP server. |
| `agent.loki.enabled` | `false` | Adds Loki tools to the Agent. |

Do not install this chart with the same resource names as the kube-insight
application chart's `kagent.enabled=true` resources unless Helm ownership is
coordinated. Prefer one owner for the prompt library, `RemoteMCPServer`, and
Agent resources.
