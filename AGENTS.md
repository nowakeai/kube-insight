# kube-insight Agent Notes

This file is for agent-facing rules that should stay stable. Put CLI examples,
operational notes, and design details in `docs/`.

## Read First

- Product and architecture: `docs/README.md`
- Local usage and service mode: `docs/quickstart.md`
- Configuration, filters, and running modes: `docs/configuration/configuration.md`
- Processing config model: `docs/configuration/processing-model.md`
- Data model and schema intent: `docs/data/data-model.md`
- Storage efficiency plan: `docs/data/efficient-history-storage-v2.md`
- Agent SQL usage: `docs/workflows/agent-sql-cookbook.md`
- Development commands: `docs/dev/commands.md`

## Non-Negotiable Design Rules

- `api_resources` is the authoritative GVR/resource/scope mapping.
- Filters run before retained document hashing and storage.
- Destructive filters must record auditable decisions.
- Delete observations must not be dropped by change-discard filters.
- Resource-specific fast paths must still emit common outputs: retained
  versions, facts, edges, status changes, and change summaries.
- Facts and edges are the investigation candidate path; versions are proof.
- Storage implementations should satisfy `internal/storage.Store` where
  applicable.

## Engineering Rules

- Prefer mature libraries and ecosystem-standard packages over hand-rolled
  implementations. Do not reinvent protocol encoders, CLI parsers, metrics
  exposition, Kubernetes clients, SQL drivers, or UI primitives unless there is
  a clear product-specific reason.
- Keep code files at or below 800 lines. Split by responsibility before adding
  more behavior.
- Preserve user changes. Do not revert unrelated dirty worktree changes.
- Update docs alongside behavior changes.
- Prefer configuration, rule tables, and data-driven registries over hardcoded
  branching. Keep unavoidable built-in defaults centralized and documented so
  they can be overridden or moved to config later.
- Keep compression, delta logic, and retention policy above storage backends so
  SQLite/Postgres/Cockroach can share the same product semantics.

## Dependency Preferences

- Kubernetes access: prefer `client-go`.
- CLI: use Cobra/pflag patterns already in the repo.
- Logs/TUI styling: stay compatible with the Charm ecosystem.
- Metrics: use the official Prometheus Go client.
- SQLite: continue using the chosen pure-Go driver unless the storage design
  changes deliberately and is documented.

## Validation

Before handing off code changes, run the narrow relevant tests and, when
practical:

```bash
make test
make build
git diff --check
```

Also check the 800-line rule:

```bash
find cmd internal -name '*.go' -print0 | xargs -0 wc -l | awk '$2 != "total" && $1 > 800 {print}'
```
