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

## User Documentation

Read these in order when evaluating or using kube-insight:

1. [Product Brief](requirements/product-brief.md)
2. [Quickstart](quickstart.md)
3. [Configuration](configuration/configuration.md)
4. [Data Model](data/data-model.md)
5. [Storage Modes And Performance](validation/storage-mode-comparison.md)
6. [Agent SQL Cookbook](workflows/agent-sql-cookbook.md)
7. [kube-insight Agent Skill](../kube-insight-skill/SKILL.md)
8. [Real-World Troubleshooting Cases](workflows/real-world-cases.md)
9. [Troubleshooting Workflows](workflows/troubleshooting-workflows.md)
10. [Security, Privacy, And Retention](security/security-retention.md)

## Design References

These documents explain the system shape and current implementation direction:

- [Roadmap](roadmap/roadmap.md)
- [System Architecture](architecture/system-architecture.md)
- [Technology Stack](architecture/technology-stack.md)
- [Storage, Index, And Query Design](data/storage-index-query.md)
- [Efficient History Storage V2](data/efficient-history-storage-v2.md)
- [Facts Catalog](data/facts-catalog.md)
- [Multi Backend Roadmap](data/multi-backend-roadmap.md)
- [Ingestion And Extraction](ingestion/ingestion-and-extraction.md)
- [Global Watcher Design](ingestion/global-watcher-design.md)
- [Validated Troubleshooting Scenarios](workflows/validated-troubleshooting-scenarios.md)
- [Agent And UI Roadmap](product/agent-and-ui-roadmap.md)
- [Agent-First Web UI Design](product/agent-first-web-ui.md)
- [Kubernetes RBAC Inheritance](security/kubernetes-rbac-inheritance.md)
- [Agent SQL RBAC Filtering](security/agent-rbac-sql-filtering.md)
- [Backend Strategy](research/backend-strategy.md)
- [Storage Cost And Compression Notes](research/storage-cost-and-compression-notes.md)
- [Roadmap And Open Questions](roadmap/roadmap-open-questions.md)

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

## Categories

| Category | Contents |
| --- | --- |
| `requirements/` | Product definition, target users, core use cases, MVP boundary. |
| `architecture/` | System-level components, deployment shape, and technology stack. |
| `data/` | Domain data model, storage layout, indexes, and query paths. |
| `configuration/` | YAML configuration, filters, extractors, and plugin shape. |
| `dev/` | Development commands, local workflows, PR checklists, validation plans, and closeout notes. |
| `agent/` | Agent skill instructions and backend-detection rules for MCP/CLI use. |
| `product/` | Human UI, agent, API, MCP, and skill roadmap. |
| `ingestion/` | Kubernetes discovery, global watches, normalization, relationships, facts. |
| `workflows/` | Incident investigation flows and expected evidence output. |
| `security/` | Redaction, retention, authorization, and Kubernetes RBAC inheritance. |
| `research/` | Backend strategy and supporting research. |
| `validation/` | User-facing benchmark, performance, and storage-mode validation summaries. |
| `roadmap/` | Product roadmap, implementation phases, and unresolved questions. |

## Research Archive

Historical design and storage research from the DiffStor and KubeChronicle phase
is kept under [research/archive](research/archive/). These documents are useful
background, but the main kube-insight design docs above are the current source
of truth.
