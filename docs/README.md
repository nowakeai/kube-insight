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

## Main Design Docs

Read these in order:

1. [Product Brief](requirements/product-brief.md)
2. [System Architecture](architecture/system-architecture.md)
3. [Technology Stack](architecture/technology-stack.md)
4. [Quickstart](quickstart.md)
5. [Data Model](data/data-model.md)
6. [Storage, Index, And Query Design](data/storage-index-query.md)
7. [Efficient History Storage V2](data/efficient-history-storage-v2.md)
8. [Agent SQL Cookbook](workflows/agent-sql-cookbook.md)
9. [kube-insight Agent Skill](agent/kube-insight-skill/SKILL.md)
10. [Insight vs kubectl Benchmark](validation/insight-vs-kubectl-benchmark.md)
11. [Real-World Troubleshooting Cases](workflows/real-world-cases.md)
12. [Ingestion And Extraction](ingestion/ingestion-and-extraction.md)
13. [Global Watcher Design](ingestion/global-watcher-design.md)
14. [Troubleshooting Workflows](workflows/troubleshooting-workflows.md)
15. [Validated Troubleshooting Scenarios](workflows/validated-troubleshooting-scenarios.md)
16. [Configuration](configuration/configuration.md)
17. [Development Commands](dev/commands.md)
18. [ClickHouse Local Workflow](dev/clickhouse-local-workflow.md)
19. [MVP Dev Checklist](dev/mvp-dev-checklist.md)
20. [Agent And UI Roadmap](product/agent-and-ui-roadmap.md)
21. [Security, Privacy, And Retention](security/security-retention.md)
22. [Kubernetes RBAC Inheritance](security/kubernetes-rbac-inheritance.md)
23. [Agent SQL RBAC Filtering](security/agent-rbac-sql-filtering.md)
24. [Open Source Readiness](project/open-source-readiness.md)
25. [Backend Strategy](research/backend-strategy.md)
26. [Storage Cost And Compression Notes](research/storage-cost-and-compression-notes.md)
27. [Multi Backend Roadmap](data/multi-backend-roadmap.md)
28. [PoC And Benchmark Plan](validation/poc-and-benchmark-plan.md)
29. [ClickHouse MVP Closeout](validation/clickhouse-mvp-closeout.md)
30. [Test Acceptance Plan](validation/test-acceptance-plan.md)
31. [Roadmap And Open Questions](roadmap/roadmap-open-questions.md)

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
| `dev/` | Development commands and local verification workflow. |
| `agent/` | Agent skill instructions and backend-detection rules for MCP/CLI use. |
| `product/` | Human UI, agent, API, MCP, and skill roadmap. |
| `ingestion/` | Kubernetes discovery, global watches, normalization, relationships, facts. |
| `workflows/` | Incident investigation flows and expected evidence output. |
| `security/` | Redaction, retention, authorization, and Kubernetes RBAC inheritance. |
| `project/` | Open-source readiness and release hygiene. |
| `research/` | Backend strategy and supporting research. |
| `validation/` | PoC, benchmark, testing, and acceptance plans. |
| `roadmap/` | Product roadmap, implementation phases, and unresolved questions. |

## Research Archive

Historical design and storage research from the DiffStor and KubeChronicle phase
is kept under [research/archive](research/archive/). These documents are useful
background, but the main kube-insight design docs above are the current source
of truth.
