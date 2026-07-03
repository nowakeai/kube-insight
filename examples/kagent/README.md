# kagent Examples

These examples target kagent `kagent.dev/v1alpha2`, validated against kagent
chart `0.9.x`.

Install kube-insight with the Helm chart first. The examples assume:

- kube-insight release name: `kube-insight`
- kube-insight namespace: `kube-insight`
- kagent Agent namespace: `kagent`
- kube-insight Service URL:
  `http://kube-insight.kube-insight.svc.cluster.local:8090/mcp`

Adjust names and namespaces before applying the manifests.

If the kube-insight Helm chart already created kagent resources with
`kagent.enabled=true`, do not apply these default manifests with the same names.
Use either the Helm-managed path or this manual/GitOps path.

Apply the default kube-insight troubleshooting integration:

```bash
kubectl apply -k examples/kagent
```

This installs the kube-insight prompt library, registers kube-insight as a
`RemoteMCPServer`, and creates the troubleshooting Agent.

After applying the `RemoteMCPServer`, check that kagent accepted it and
discovered kube-insight tools, including `kube_insight_js`:

```bash
kubectl -n kagent get remotemcpserver kube-insight -o yaml
```

Before testing the Agent runtime, make sure the referenced kagent ModelConfig is
accepted and its provider Secret exists:

```bash
kubectl -n kagent get modelconfig
kubectl -n kagent get modelconfig default-model-config -o yaml
```

After applying the Agent, check that kagent accepted it and created a ready
runtime Deployment:

```bash
kubectl -n kagent get agent kube-insight-troubleshooter -o yaml
kubectl -n kagent get deploy,pod | grep kube-insight-troubleshooter
```

For a broader AIOps agent that combines kube-insight with kagent's Kubernetes,
Prometheus, Helm, shell, and other platform tools, review
`examples/kagent/aiops-fullstack-agent.yaml`. That example uses
`promptTemplate.dataSources` to include the kube-insight prompt library and
kagent's built-in prompt library.

Before applying the full-stack Agent, confirm your kagent tool server discovers
the referenced Kubernetes, Prometheus, Helm, and shell tools:

```bash
kubectl -n kagent get remotemcpserver kagent-tool-server -o yaml
```

If your environment does not expose one of those tool families, remove or edit
the corresponding `toolNames` block before applying the Agent.

If a Loki or Grafana MCP server is installed, apply or adapt
`examples/kagent/loki-remote-mcpserver.yaml`, then uncomment the optional Loki
tool block in the full-stack Agent and point it at the discovered tool names.

For manual testing through the kagent UI:

```bash
kubectl -n kagent port-forward svc/kagent-ui 8080:8080
```

Open `http://127.0.0.1:8080`, select the kube-insight troubleshooting Agent,
and ask a retained-history question such as:

```text
What changed in namespace <namespace> in the last 30 minutes? Start with
kube_insight_health and cite kube-insight evidence.
```
