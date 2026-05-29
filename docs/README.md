# kube-insight Documentation

`kube-insight` is a Kubernetes historical evidence and troubleshooting system.

It records Kubernetes resource history, reconstructs historical topology, and
extracts compact troubleshooting facts so engineers can investigate incidents
after the live cluster has already returned to a healthy state.

## Product Statement

```text
kube-insight answers what Kubernetes looked like when a problem happened,
which objects were related, what changed nearby, and what evidence still exists
after Kubernetes Events and live state have moved on.
```

## Start Here

Read these in order when evaluating or using kube-insight:

1. [Product Brief](users/getting-started/product-brief.md)
2. [Quickstart](users/getting-started/quickstart.md)
3. [Built-in Web UI Agent Tutorial](users/tutorials/builtin-webui-agent.md)
4. [External Agent Skill Tutorial](users/tutorials/external-agent-skill.md)
5. [Configuration](operators/configuration/configuration.md)
6. [Storage Modes And Performance](users/reference/storage-mode-comparison.md)
7. [Troubleshooting Workflows](users/workflows/troubleshooting-workflows.md)
8. [Security, Privacy, And Retention](operators/security/security-retention.md)

## User Guides And Tutorials

- [Built-in Web UI Agent Tutorial](users/tutorials/builtin-webui-agent.md): configure
  the server-side LLM provider, start `serve --app`, open the browser UI, and
  run a history-first investigation.
- [External Agent Skill Tutorial](users/tutorials/external-agent-skill.md): connect
  Codex, Claude, or another external agent to kube-insight through MCP or the
  read-only HTTP API.
- [Agent SQL Cookbook](users/workflows/agent-sql-cookbook.md): schema-first SQL
  patterns for agents and operators.
- [Real-World Troubleshooting Cases](users/workflows/real-world-cases.md): example
  incident questions and the evidence kube-insight should return.
- [Validated Troubleshooting Scenarios](users/workflows/validated-troubleshooting-scenarios.md):
  tested scenarios and expected investigation signals.

## Operator Reference

Use these when configuring or operating kube-insight:

- [Configuration](operators/configuration/configuration.md)
- [Processing Configuration Model](operators/configuration/processing-model.md)
- [Data Model](operators/data/data-model.md)
- [Facts Catalog](operators/data/facts-catalog.md)
- [Ingestion And Extraction](operators/ingestion/ingestion-and-extraction.md)
- [Security, Privacy, And Retention](operators/security/security-retention.md)
- [Storage Modes And Performance](users/reference/storage-mode-comparison.md)

## Contributor And Design References

These documents are not required for normal use. They are for contributors,
operators evaluating internals, and maintainers who need the system design,
roadmap history, or unresolved design context:

- [Roadmap](contributors/roadmap/roadmap.md)
- [System Architecture](contributors/architecture/system-architecture.md)
- [Technology Stack](contributors/architecture/technology-stack.md)
- [Storage, Index, And Query Design](contributors/data/storage-index-query.md)
- [Efficient History Storage V2](contributors/data/efficient-history-storage-v2.md)
- [Facts Catalog](operators/data/facts-catalog.md)
- [Multi Backend Roadmap](contributors/data/multi-backend-roadmap.md)
- [Ingestion And Extraction](operators/ingestion/ingestion-and-extraction.md)
- [Global Watcher Design](contributors/ingestion/global-watcher-design.md)
- [Validated Troubleshooting Scenarios](users/workflows/validated-troubleshooting-scenarios.md)
- [Agent And UI Roadmap](contributors/product/agent-and-ui-roadmap.md)
- [Agent-First Web UI Design](contributors/product/agent-first-web-ui.md)
- [Kubernetes RBAC Inheritance](contributors/security/kubernetes-rbac-inheritance.md)
- [Agent SQL RBAC Filtering](contributors/security/agent-rbac-sql-filtering.md)
- [Backend Strategy](maintainers/research/backend-strategy.md)
- [Storage Cost And Compression Notes](maintainers/research/storage-cost-and-compression-notes.md)
- [Roadmap And Open Questions](contributors/roadmap/roadmap-open-questions.md)

## Research Archive

Historical design and storage research from the DiffStor and KubeChronicle phase
is kept under [archive/research](archive/research/). These documents are useful
background for maintainers, but they are not user tutorials and may describe
older product names or discarded approaches:

- [Backend Comparison: SQLite+Zstd vs TimescaleDB](archive/research/backend-comparison.md)
- [DiffStor Deep Research Notes](archive/research/diffstor-deep-research.md)
- [DiffStor PoC Plan](archive/research/diffstor-poc-plan.md)
- [Reverse Delta Query And Indexing](archive/research/diffstor-query-indexing.md)
- [KubeChronicle Product Design](archive/research/kubechronicle-product-design.md)
- [KubeChronicle Storage, Index, And Query Design](archive/research/kubechronicle-storage-index-query-design.md)

## nowake.ai Docs Site Source Contract

Some documents in this repository are rendered by the nowake.ai website docs
site through its explicit source manifest. Keep those source documents suitable
for both this repository and the website renderer:

- Keep the project repository as the source of truth; do not add website-only
  Starlight frontmatter here.
- Start each public source document with exactly one top-level `#` heading. The
  website sync step removes that heading and injects site frontmatter from its
  manifest.
- Use relative links for other repository docs. The website sync step rewrites
  manifest-listed links to `/docs/...` and leaves other repository links as
  GitHub source links.
- Keep public docs task-oriented: problem, prerequisites, commands or config,
  expected output, common failure signals, current limits, and next links.
- Keep development checklists, closeout notes, and raw research notes outside
  the public source manifest unless they are rewritten for users.

## Development Notes

Development-stage checklists, closeout notes, validation plans, and local
workflow records live under `docs/dev/` so user-facing docs stay focused.

- [Development Commands](dev/commands.md)
- [Background Tasks](dev/background-tasks.md)
- [Agent Evaluation](dev/agent-evaluation.md)
- [ClickHouse Local Workflow](dev/clickhouse-local-workflow.md)
- [MVP Dev Checklist](dev/mvp-dev-checklist.md)
- [MVP PR Summary](dev/mvp-pr-summary.md)
- [PoC And Benchmark Plan](dev/poc-and-benchmark-plan.md)
- [Test Acceptance Plan](dev/test-acceptance-plan.md)
- [ClickHouse MVP Closeout](dev/clickhouse-mvp-closeout.md)
- [Agent-First Web UI Checklist](dev/agent-first-web-ui-checklist.md)
- [Open Source Readiness](dev/open-source-readiness.md)

## Community And Release

- [Contributing](../CONTRIBUTING.md)
- [Security Policy](../SECURITY.md)
- [Support](../SUPPORT.md)
- [Maintainers](../MAINTAINERS.md)
- [Code Of Conduct](../CODE_OF_CONDUCT.md)
- [Release Process](../RELEASE.md)

## Directory Layout

Public docs are organized first by audience, then by category:

| Directory | Audience | Contents |
| --- | --- |
| `users/getting-started/` | Users and evaluators | Product brief and quickstart. |
| `users/tutorials/` | Users | Built-in Web UI agent and external agent skill tutorials. |
| `users/workflows/` | Users and agents | Troubleshooting workflows, SQL cookbook, and real-world cases. |
| `users/reference/` | Users and evaluators | User-facing benchmark and storage-mode tradeoffs. |
| `operators/configuration/` | Operators | Runtime configuration and processing model. |
| `operators/data/` | Operators and advanced users | Data model and facts catalog. |
| `operators/ingestion/` | Operators | Ingestion behavior and extraction outputs. |
| `operators/security/` | Operators | Current security, privacy, and retention guidance. |
| `contributors/` | Contributors | Architecture, internal data design, ingestion design, product design, roadmap, and future security design. |
| `maintainers/research/` | Maintainers | Current backend and storage-cost research notes. |
| `archive/research/` | Maintainers and historians | Historical research using older product names or discarded approaches. |
| `dev/` | Contributors | Development commands, local workflows, PR checklists, validation plans, and closeout notes. |
| `../kube-insight-skill/` | External agents | Agent skill instructions and backend-detection rules for MCP/CLI use. |
