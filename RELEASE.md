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

Use semantic version tags:

```bash
git tag v0.1.0
git push origin v0.1.0
```

The default `release` workflow publishes both the default pure-Go build and the
chDB-enabled variant from `.goreleaser.yaml`:

- GitHub Release artifacts for default Linux, macOS, and Windows amd64 binaries,
  plus default Linux and macOS arm64 binaries, with no storage-backend suffix.
- chDB-enabled archives for Linux and macOS amd64/arm64. Each filename includes
  `chdb` and each archive includes the matching `libchdb.so` runtime plus
  `config/kube-insight.chdb.example.yaml`.
- `checksums.txt` covering all release archives.
- GHCR images on one package using multi-architecture tags:
  - `ghcr.io/nowakeai/kube-insight:<tag>`
  - `ghcr.io/nowakeai/kube-insight:latest`
  - `ghcr.io/nowakeai/kube-insight:<tag>-chdb`
  - `ghcr.io/nowakeai/kube-insight:latest-chdb`

The GitHub package must be public before release handoff. The Docker images carry
`org.opencontainers.image.source=https://github.com/nowakeai/kube-insight` so
GHCR links them to the public repository; confirm package visibility on the
GitHub package page after the first publish.

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
- Confirm GHCR package visibility.
- Confirm README badges resolve under `github.com/nowakeai/kube-insight`.
- Re-run history scans after any final documentation or fixture updates.
