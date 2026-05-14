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
4. [Data Model](data/data-model.md)
5. [Storage, Index, And Query Design](data/storage-index-query.md)
6. [Ingestion And Extraction](ingestion/ingestion-and-extraction.md)
7. [Global Watcher Design](ingestion/global-watcher-design.md)
8. [Troubleshooting Workflows](workflows/troubleshooting-workflows.md)
9. [Security, Privacy, And Retention](security/security-retention.md)
10. [Kubernetes RBAC Inheritance](security/kubernetes-rbac-inheritance.md)
11. [Backend Strategy](research/backend-strategy.md)
12. [PoC And Benchmark Plan](validation/poc-and-benchmark-plan.md)
13. [Test Acceptance Plan](validation/test-acceptance-plan.md)
14. [Roadmap And Open Questions](roadmap/roadmap-open-questions.md)

## Categories

| Category | Contents |
| --- | --- |
| `requirements/` | Product definition, target users, core use cases, MVP boundary. |
| `architecture/` | System-level components, deployment shape, and technology stack. |
| `data/` | Domain data model, storage layout, indexes, and query paths. |
| `ingestion/` | Kubernetes discovery, global watches, normalization, relationships, facts. |
| `workflows/` | Incident investigation flows and expected evidence output. |
| `security/` | Redaction, retention, authorization, and Kubernetes RBAC inheritance. |
| `research/` | Backend strategy and supporting research. |
| `validation/` | PoC, benchmark, testing, and acceptance plans. |
| `roadmap/` | Product roadmap, implementation phases, and unresolved questions. |

## Research Archive

Historical design and storage research from the DiffStor and KubeChronicle phase
is kept under [research/archive](research/archive/). These documents are useful
background, but the main kube-insight design docs above are the current source
of truth.
