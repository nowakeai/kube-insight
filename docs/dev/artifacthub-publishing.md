# Artifact Hub Publishing

This note records the kube-insight Artifact Hub publishing flow.

## Helm Chart

The kube-insight application chart is published as an OCI Helm chart:

```text
oci://ghcr.io/nowakeai/charts/kube-insight
```

Artifact Hub repository kind:

```text
0 - Helm charts
```

Register or update the repository with an Artifact Hub API key:

```bash
curl -X POST 'https://artifacthub.io/api/v1/repositories/user' \
  -H "X-API-KEY-ID: ${ARTIFACTHUB_API_KEY_ID}" \
  -H "X-API-KEY-SECRET: ${ARTIFACTHUB_API_KEY_SECRET}" \
  -H 'Content-Type: application/json' \
  -d '{
    "kind": 0,
    "name": "kube-insight",
    "display_name": "kube-insight",
    "url": "oci://ghcr.io/nowakeai/charts/kube-insight"
  }'
```

For an existing repository, use:

```bash
curl -X PUT 'https://artifacthub.io/api/v1/repositories/user/kube-insight' \
  -H "X-API-KEY-ID: ${ARTIFACTHUB_API_KEY_ID}" \
  -H "X-API-KEY-SECRET: ${ARTIFACTHUB_API_KEY_SECRET}" \
  -H 'Content-Type: application/json' \
  -d '{
    "kind": 0,
    "name": "kube-insight",
    "display_name": "kube-insight",
    "url": "oci://ghcr.io/nowakeai/charts/kube-insight"
  }'
```

Validate the published chart:

```bash
helm show chart oci://ghcr.io/nowakeai/charts/kube-insight --version 0.1.1
```

## Verified Publisher

For OCI Helm repositories, Artifact Hub reads repository metadata from the
special `artifacthub.io` tag:

```bash
oras push \
  ghcr.io/nowakeai/charts/kube-insight:artifacthub.io \
  --config /dev/null:application/vnd.cncf.artifacthub.config.v1+yaml \
  artifacthub-repo.yml:application/vnd.cncf.artifacthub.repository-metadata.layer.v1.yaml
```

To enable verified publisher, first register the repository, then copy the
repository ID from Artifact Hub into `artifacthub-repo.yml`:

```yaml
repositoryID: <ARTIFACT_HUB_REPOSITORY_ID>
owners:
  - name: nowake.ai
    email: <MAINTAINER_EMAIL>
```

Do not commit personal Artifact Hub API keys, private emails, or local account
details.

## kagent Agent Chart

Artifact Hub has a `kagent` repository kind:

```text
28 - Kagent agents
```

kagent packages are tracked using the Helm tracker. Publish kube-insight
kagent-facing resources through the dedicated Agent chart, not as raw
`examples/kagent/*.yaml` files:

```bash
helm show chart oci://ghcr.io/nowakeai/charts/kube-insight-kagent-agent --version 0.1.0
```

The chart installs only kagent resources:

- kube-insight prompt library ConfigMap
- `RemoteMCPServer` pointing to kube-insight `/mcp`
- troubleshooting or full-stack AIOps `Agent`

Chart-only releases for this package use tags in this shape:

```text
chart-kube-insight-kagent-agent-v<version>
```

Register the dedicated kagent chart with Artifact Hub kind `28`:

```bash
curl -X POST 'https://artifacthub.io/api/v1/repositories/user' \
  -H "X-API-KEY-ID: ${ARTIFACTHUB_API_KEY_ID}" \
  -H "X-API-KEY-SECRET: ${ARTIFACTHUB_API_KEY_SECRET}" \
  -H 'Content-Type: application/json' \
  -d '{
    "kind": 28,
    "name": "kube-insight-kagent-agent",
    "display_name": "kube-insight kagent AIOps Agent",
    "url": "oci://ghcr.io/nowakeai/charts/kube-insight-kagent-agent"
  }'
```
