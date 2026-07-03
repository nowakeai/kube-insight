# Open Source Readiness Checklist

Use this checklist before pushing or tagging a public release.

## Repository

- [ ] `origin` points to `https://github.com/nowakeai/kube-insight.git`.
- [ ] `git status --short` is clean.
- [ ] `todos.md` is ignored and not present in history.
- [ ] Generated databases and validation output are ignored.
- [ ] `LICENSE` is present and README badge matches it.
- [ ] Public security contact is configured.

## Community Files

- [ ] `README.md`
- [ ] `CONTRIBUTING.md`
- [ ] `SECURITY.md`
- [ ] `SUPPORT.md`
- [ ] `MAINTAINERS.md`
- [ ] `CODE_OF_CONDUCT.md`
- [ ] `RELEASE.md`
- [ ] `.github/ISSUE_TEMPLATE/*`
- [ ] `.github/pull_request_template.md`
- [ ] `.github/dependabot.yml`

## Validation

```bash
make test
make build
make validate
git diff --check
find cmd internal -name '*.go' -print0 | xargs -0 wc -l | awk '$2 != "total" && $1 > 800 {print}'
./scripts/open-source-check.sh
```

## Sensitive Data Scan

The project should not publish:

- kubeconfig credentials
- bearer tokens or cloud credentials
- generated SQLite databases
- unsanitized Kubernetes exports
- internal cluster names or private hostnames
- Secret payload values

Run the history scan after final docs and fixtures are committed, not before.

## Release

- [ ] CI is green.
- [ ] GoReleaser config passes a local check or release workflow dry-run.
- [ ] The merged `.goreleaser.yaml` stages each chDB `libchdb.so` runtime before
  publishing chDB-enabled archives and the default Docker image.
- [ ] Do not add Windows chDB artifacts until upstream chDB and `chdb-go` provide
  a working Windows runtime.
- [ ] GHCR package `ghcr.io/nowakeai/kube-insight` is public and includes
  multi-architecture `<tag>` and `latest` images with bundled chDB runtime.
- [ ] The binary/image tag follows semantic versioning, for example `v0.1.0`.

## Chart Release

- [ ] `make helm-release-check` passes.
- [ ] Chart-only changes use the `chart-release` workflow, not the GoReleaser
  `release` workflow.
- [ ] GHCR chart package `oci://ghcr.io/nowakeai/charts/kube-insight` is public
  and installable for the release version.
- [ ] Release notes mention breaking schema, storage, or CLI changes.
- [ ] The application chart tag uses `chart-v<version>`, for example
  `chart-v0.1.1`; the dedicated kagent Agent chart tag uses
  `chart-kube-insight-kagent-agent-v<version>`.
- [ ] Manual chart workflow runs set `chart` and `expected_chart_version` as
  guards when needed.
