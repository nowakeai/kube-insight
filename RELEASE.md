# Release Process

Releases are tag-driven and use GoReleaser.

## Prerequisites

- `main` is green in CI.
- `make test`, `make build`, and `make validate` pass locally.
- Release notes are reviewed.
- The repository license is committed.
- Sensitive-data scans have been run against the current tree and reachable
  history.

## Versioning

Use semantic version tags for binary and container image releases:

```bash
git tag v0.1.0
git push origin v0.1.0
```

The default `release` workflow builds categorized GitHub Release notes with
`mikepenz/release-changelog-builder-action`, appends an artifact summary, and
then passes those notes to GoReleaser with `--release-notes`. The same workflow
publishes binary archives and the chDB-capable container image:

- GitHub Release artifacts for default Linux, macOS, and Windows amd64 binaries,
  plus default Linux and macOS arm64 binaries, with no storage-backend suffix.
- chDB-enabled archives for Linux and macOS amd64/arm64. Each filename includes
  `chdb` and each archive includes the matching `libchdb.so` runtime plus
  `config/kube-insight.chdb.example.yaml`.
- `checksums.txt` covering all release archives.
- GHCR images on one package using multi-architecture tags:
  - `ghcr.io/nowakeai/kube-insight:<tag>`
  - `ghcr.io/nowakeai/kube-insight:latest`

The GitHub image package must be public before release handoff. The Docker image
carries `org.opencontainers.image.source=https://github.com/nowakeai/kube-insight`
so GHCR links it to the public repository; confirm image package visibility on
the GitHub package page after the first publish.

## chDB-Enabled Variant

The chDB-enabled release variant is built with `-tags chdb` and still supports
SQLite and ClickHouse drivers. It is currently scoped to Linux and macOS
amd64/arm64 because those are the official chDB `libchdb` runtime assets staged
by the release workflow. Windows chDB archives are not published until `chdb-go`
and upstream chDB provide a working Windows runtime.

Local checks:

```bash
make build-chdb
make build-chdb-image
make release-chdb-check
```

`make release-chdb-check` stages the Linux and macOS amd64/arm64 chDB runtimes
under `build/chdb-runtime/` and validates the merged `.goreleaser.yaml`.
`make build-chdb-image` stages `bin/kube-insight-chdb` and the local amd64
`libchdb.so` into `dist/chdb-image/`, then builds a local single-architecture
`kube-insight-chdb:local` image.

The release container image is chDB-capable by default and is built from
`docker/chdb.Dockerfile`. The smaller default binary archives do not link chDB;
use the `chdb` archives when a standalone chDB-capable binary is required.

## Helm Chart

Chart releases are independent from binary and container image releases. Use the
`chart-release` workflow when only chart templates, values, examples, or install
docs changed.

Tag-based chart release:

```bash
git tag chart-v0.1.1
git push origin chart-v0.1.1
```

The `chart-release` workflow packages `charts/kube-insight` and pushes it as an
OCI chart to `oci://ghcr.io/nowakeai/charts/kube-insight`. For `chart-v*` tags,
the tag version must match `Chart.yaml.version`. `Chart.yaml` is the source of
truth for both chart `version` and `appVersion`; update it in the chart change
before tagging. For manual `workflow_dispatch` releases, optionally provide
`expected_chart_version` or `expected_app_version` as guards against publishing
the wrong checkout.

Local chart release check:

```bash
make helm-release-check
```

Install a published chart with:

```bash
helm install kube-insight oci://ghcr.io/nowakeai/charts/kube-insight \
  --namespace kube-insight \
  --create-namespace \
  --version <version>
```

## Release Notes

Tag releases use commit-mode changelog generation. Conventional commit prefixes
are grouped into Features, Performance, Fixes, Documentation, CI and
Maintenance, and Other Changes. Review the generated GitHub Release body after
publish to confirm user-facing changes and artifact links are clear.

## Snapshot Checks

If GoReleaser is installed locally:

```bash
goreleaser check
goreleaser release --snapshot --clean
```

If it is not installed, rely on the GitHub Actions release workflow after CI is
green.

## Before Public Release

- Confirm the public security contact.
- Confirm GHCR image package visibility after binary/image releases.
- Confirm the OCI Helm chart package is visible and installable after chart
  releases.
- Confirm README badges resolve under `github.com/nowakeai/kube-insight`.
- Re-run history scans after any final documentation or fixture updates.
