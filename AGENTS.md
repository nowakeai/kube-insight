# kube-insight Agent Notes

This file is for agent-facing rules that should stay stable. Put CLI examples,
operational notes, and design details in `docs/`.

## Read First

- Product and architecture: `docs/README.md`
- Local usage and service mode: `docs/users/getting-started/quickstart.md`
- Configuration, filters, and running modes: `docs/operators/configuration/configuration.md`
- Processing config model: `docs/operators/configuration/processing-model.md`
- Data model and schema intent: `docs/operators/data/data-model.md`
- Storage efficiency plan: `docs/contributors/data/efficient-history-storage-v2.md`
- Agent SQL usage: `docs/users/workflows/agent-sql-cookbook.md`
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
- When stopping, handing off, or summarizing an incomplete thread, state both
  what should happen next and how you intend to do it.
- Keep code files at or below 800 lines. Split by responsibility before adding
  more behavior.
- Preserve user changes. Do not revert unrelated dirty worktree changes.
- Update docs alongside behavior changes.
- During development or testing, if you discover a durable workflow rule,
  constraint, validation prerequisite, or recurring pitfall, update this file
  or the appropriate `docs/` page in the same change. Do not leave durable
  process knowledge only in chat history or local scratch notes.
- When the user proposes conceptual agent/product direction, preserve the intent
  instead of turning examples into rigid bans. For external skill and black-box
  agent tests, model realistic user environments where kube-insight works
  alongside tools such as `kubectl`, port-forwarding, shell, Python, and cloud
  CLIs; the goal is to prove kube-insight improves AIOps with retained history
  and aggregation, not to exclude complementary tools.
- For Web UI or frontend development, use the Docker compose dev environment
  (`make dev-compose-up-detached`) instead of starting another host
  `kube-insight serve` process or a host Vite dev server. Compose owns the
  ClickHouse, watcher/API, metrics, and Web UI services; use the compose Web UI
  for Vite reloads and rebuild/recreate compose services after backend changes.
  Start a separate host `serve` only for pure backend tests or isolated smoke
  scripts, and stop it when the test is done.
- Prefer configuration, rule tables, and data-driven registries over hardcoded
  branching. Keep unavoidable built-in defaults centralized and documented so
  they can be overridden or moved to config later.
- Agent investigation efficiency is a product constraint. For broad symptom,
  health, topology, or recent-change prompts, prefer time-scoped exact facts,
  changes, and edges before raw JSON/document scans; pass absolute time bounds
  from client context for relative-time prompts; avoid repeated broad searches
  without kind/namespace/cluster/time filters.
- Optimize agent speed with prompt/tool contracts and real-case evaluation
  before adding large precomputed context caches. If a subagent such as the
  evidence condenser is used, pass artifact IDs/titles and concrete row or
  snippet excerpts so the secondary model remains evidence-bound.
- For built-in agent behavior fixes, prefer improving the prompt harness, tool
  descriptions, evaluation cases, and documented tool-use contracts before
  adding code-side hacks or hidden rewrites. Code guardrails are acceptable for
  safety, validation, or deterministic product semantics, but they should not
  teach the model the wrong workflow or silently compensate for avoidable prompt
  failures.
- When tuning built-in agent prompts, tool budgets, context replay, retry
  semantics, subagent behavior, or discovering generic agent usage lessons,
  carry durable guidance into `kube-insight-skill/SKILL.md` or
  `kube-insight-skill/references/` so users who do not use the built-in agent can
  still get the best kube-insight results.
- Agent answers should prefer human-readable cluster context/display names when
  available, while still including stable cluster IDs for exact evidence binding.
  Tool/SQL arguments remain UTC, but final user-facing timestamps should be
  rendered in the browser/client time zone when the Web UI provides it.
- Web UI retry is rewind semantics, not append semantics. A retry run must
  replace the retried run and truncate later runs in that branch in the session
  projection. Any change to session/run projection or retry UI must keep the
  retry branch tests passing.
- Keep compression, delta logic, and retention policy above storage backends so
  SQLite/chDB/ClickHouse and any SQL compatibility backends can share the same
  product semantics.
- Web UI retry actions must preserve checkpoint semantics. A retry must either
  call the retry endpoint or create a replacement run with `retryOfRunId`
  metadata when the original run has already been removed; it must not silently
  fall back to a plain appended message.
- Agent retention is server-owned and should run periodically from the API
  server. Do not prune unreferenced artifact events from in-progress runs;
  citations can arrive after artifacts during a live agent loop.
- Agent run lifecycle recovery is server-owned. Persisted `queued` or `running`
  runs from a previous API process should be transitioned with normal lifecycle
  events on startup; do not leave durable session state claiming work is active
  when no current process owns that execution.
- The built-in agent's private MCP client/server session must not idle-timeout
  under normal API server operation. The external `/mcp` HTTP surface may keep a
  bounded session timeout, but the in-process agent should not hold a stale MCP
  client that later fails tool calls after the LLM response succeeds.

## Dependency Preferences

- Prefer current stable versions of tools, GitHub Actions, Go modules, and
  libraries when they fit the project. Before adding or touching a dependency,
  check whether a newer compatible version exists instead of copying an older
  version from memory or stale examples.
- Kubernetes access: prefer `client-go`.
- CLI: use Cobra/pflag patterns already in the repo.
- Logs/TUI styling: stay compatible with the Charm ecosystem.
- Metrics: use the official Prometheus Go client.
- Local embedded storage: SQLite remains the pure-Go compatibility/test backend
  until the documented chDB migration is implemented; do not switch the local
  default to chDB without a real `chdb-go` adapter and validation.

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

After publishing a release, review the GitHub release page. Confirm release
notes, assets, checksums, package/image links, and install instructions render
correctly before handing off.
