# Contributing to kube-insight

Thanks for helping improve kube-insight. This project is still in a local-first
PoC phase, so small focused changes are easier to review than broad rewrites.

## Development Setup

```bash
make build
make test
make validate
```

The repository enforces a few project rules:

- Keep Go files under 800 lines.
- Preserve storage semantics across backends.
- Run filters before retained document hashing and storage.
- Keep destructive filters auditable.
- Do not drop delete observations in change-discard paths.
- Update docs with behavior changes.

## Pull Request Checklist

- Explain the user-visible behavior change.
- Include focused tests for storage, extraction, CLI, API, or MCP behavior.
- Run `make test`.
- Run `make build` for CLI changes.
- Run `make validate` when ingestion, storage, or query behavior changes.
- Run `git diff --check`.

## Commit Style

Use short imperative commit messages:

```text
Add retained event message previews
Tune fact indexes for agent queries
Cover delete observation timestamps
```

## Sensitive Data

Do not commit kubeconfig files, generated SQLite databases, cluster exports, or
real Kubernetes object payloads that have not gone through the sanitizer.

Before opening a PR that touches fixtures or documentation examples, scan for:

- access tokens and private keys
- kubeconfig credentials
- internal cluster names
- private IPs and hostnames
- Secret payload values

Generated output belongs under `testdata/generated/`, which is ignored.
