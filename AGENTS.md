# kube-insight Agent Notes

## Current State

The repository now has a minimal Go framework plus design docs.

Current code is still intentionally dependency-light for the core pipeline:

- local storage uses the pure-Go SQLite driver `modernc.org/sqlite`,
- Kubernetes discovery/list support uses `client-go`,
- standard-library CLI dispatch and core package boundaries remain in place.

This keeps the first skeleton buildable without CGO or system SQLite libraries
while keeping Kubernetes integration behind collector boundaries.

## Project Layout

```text
cmd/kube-insight/
  CLI entrypoint.

internal/cli/
  Standard-library CLI dispatcher.

internal/core/
  Shared domain types: observations, resource refs, facts, edges, changes,
  stored versions.

internal/filter/
  Configurable ingestion filters. Filters return auditable decisions such as
  keep, keep_modified, discard_change, and discard_resource.

internal/extractor/
  Resource-specific evidence extractors. Initial skeleton covers Pod, Node,
  Event, and EndpointSlice.

internal/storage/
  Storage interfaces, an in-memory implementation for early tests, and a
  SQLite implementation for local PoC ingestion.

internal/ingest/
  Dependency-free JSON ingestion pipeline for Kubernetes single-object JSON and
  List JSON samples. It runs filters, extractors, and a Store implementation.

internal/storage/sql/schema.sql
  Initial SQL schema draft aligned with docs/data/data-model.md.

internal/storage/sqlite/
  SQLite Store implementation. It bootstraps schema.sql and currently writes
  full JSON versions, latest index rows, facts, edges, and changes.

internal/collector/
  Kubernetes sample collection. The default path still supports kubectl, and
  `--client-go` enables native client-go discovery/list collection.
```

## Design Commitments

- `api_resources` is the authoritative GVR/resource/scope mapping.
- Filters run before retained document hashing and storage.
- Destructive filters must record an auditable decision.
- Delete observations must not be dropped by change-discard filters.
- Resource-specific fast paths still emit the common logical outputs:
  retained versions, facts, edges, status changes, and change summaries.
- Facts and edges are the investigation candidate path; versions are proof.
- Storage implementations should satisfy `internal/storage.Store`.

## Development Commands

Preferred checks:

```bash
go test ./...
go test ./internal/filter ./internal/extractor ./internal/cli
```

Current runnable CLI:

```bash
go run ./cmd/kube-insight version
go run ./cmd/kube-insight ingest --file sample.json
go run ./cmd/kube-insight ingest --dir testdata/kube-samples --db kube-insight.db
go run ./cmd/kube-insight discover resources --db kube-insight.db --context staging
go run ./cmd/kube-insight discover resources --client-go --db kube-insight.db --context staging
go run ./cmd/kube-insight collect samples --all-contexts --discover-resources --db kube-insight.db --output testdata/generated/kube-samples
go run ./cmd/kube-insight collect ingest --context staging --discover-resources --db kube-insight.db --resource pods --max-items 10 --output testdata/generated/live-samples
go run ./cmd/kube-insight collect ingest --client-go --context staging --discover-resources --db kube-insight.db --resource pods --max-items 10 --output testdata/generated/live-samples
go run ./cmd/kube-insight watch resource --client-go --context staging --db kube-insight.db --resource pods --max-events 1 --timeout 30s --retries 3
go run ./cmd/kube-insight watch resources --client-go --context staging --db kube-insight.db --resource pods --resource services --concurrency 2 --max-events 0 --timeout 30s --retries 3
go run ./cmd/kube-insight generate samples --fixtures testdata/fixtures/kube --output testdata/generated/kube-samples --clusters 3 --copies 100
go run ./cmd/kube-insight investigate --db kube-insight.db --kind Pod --namespace default --name sample-pod
go run ./cmd/kube-insight investigate service api --namespace default --db kube-insight.db --from 2026-05-14T10:00:00Z --to 2026-05-14T11:00:00Z --max-evidence-objects 5 --max-versions-per-object 3
go run ./cmd/kube-insight topology --db kube-insight.db --kind Pod --namespace default --name sample-pod
go run ./cmd/kube-insight benchmark local --fixtures testdata/fixtures/kube --output testdata/generated/benchmark-samples --db kube-insight-benchmark.db --clusters 3 --copies 100 --query-runs 25
go run ./cmd/kube-insight benchmark watch --context staging --db kube-insight-watch-benchmark.db --resource pods --resource services --duration 30s --concurrency 2 --retries 3
go run ./cmd/kube-insight validate poc --fixtures testdata/fixtures/kube --output testdata/generated/poc-validation --db kube-insight-poc-validation.db --clusters 1 --copies 2 --query-runs 3
./scripts/poc-demo.sh
```

Formatting:

```bash
gofmt -w cmd internal
```

## Next Implementation Steps

1. Expand filter decision metrics with policy-profile override details where
   custom policy changes the default processing profile.
2. Add stricter generated-data scenarios for permission-loss and namespace
   scope changes.
3. Add optional JSON compaction/compression strategy benchmarks beyond the
   current full-identity SQLite PoC.
4. Begin hardening live cluster watch tests against real kubeconfig contexts.

## Dependency Guidance

When adding dependencies:

- prefer `client-go` for Kubernetes interactions,
- choose SQLite driver deliberately and document the tradeoff,
- keep compression and delta logic above storage backends,
- avoid adding web/UI dependencies until the core PoC passes local scenarios.

## Documentation

Update docs alongside behavior changes:

- architecture: `docs/architecture/`
- data model and storage: `docs/data/`
- ingestion and extractors: `docs/ingestion/`
- benchmark and acceptance: `docs/validation/`
