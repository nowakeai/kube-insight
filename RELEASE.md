# Release Process

Releases are tag-driven and use GoReleaser.

## Prerequisites

- `main` is green in CI.
- `make test`, `make build`, and `make validate` pass locally.
- Release notes are reviewed.
- The repository license has been selected and committed.
- Sensitive-data scans have been run against the current tree and reachable
  history.

## Versioning

Use semantic version tags:

```bash
git tag v0.1.0
git push origin v0.1.0
```

The `release` workflow publishes:

- GitHub Release artifacts for Linux, macOS, and Windows.
- checksums.txt
- GHCR images:
  - `ghcr.io/nowakeai/kube-insight:<tag>`
  - `ghcr.io/nowakeai/kube-insight:latest`

## Snapshot Checks

If GoReleaser is installed locally:

```bash
goreleaser check
goreleaser release --snapshot --clean
```

If it is not installed, rely on the GitHub Actions release workflow after CI is
green.

## Before Public Release

- Confirm and commit the project license.
- Confirm the public security contact.
- Confirm GHCR package visibility.
- Confirm README badges resolve under `github.com/nowakeai/kube-insight`.
- Re-run history scans after any final documentation or fixture updates.
