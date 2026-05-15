# Open Source Readiness Checklist

Use this checklist before pushing or tagging a public release.

## Repository

- [ ] `origin` points to `https://github.com/nowakeai/kube-insight.git`.
- [ ] `git status --short` is clean.
- [ ] `todos.md` is ignored and not present in history.
- [ ] Generated databases and validation output are ignored.
- [ ] License selection is confirmed and committed.
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
- [ ] GHCR image visibility is confirmed.
- [ ] Release notes mention breaking schema, storage, or CLI changes.
- [ ] The tag follows semantic versioning, for example `v0.1.0`.
