# Processing Configuration Model

This document defines the normalized configuration model for resource
classification, filtering, extraction, and retention.

## Naming

Configuration naming is intentionally regular:

- YAML keys use `lowerCamelCase`: `resourceProfiles`, `filterChain`,
  `extractorSet`, `retentionPolicy`, `maxAgeSeconds`.
- Named profiles, policies, filter chains, extractor sets, filters, and
  extractors use `lower_snake_case`:
  `pod_fast_path`, `secret_metadata_only`, `events_short_window`.
- CLI flags use `kebab-case`: `--filter-decision-max-age`.
- Environment variables use `KUBE_INSIGHT_` plus upper snake case for the YAML
  path: `KUBE_INSIGHT_STORAGE_SQLITE_PATH`.
- SQLite table and column names use `snake_case`.
- API and command JSON fields use `lowerCamelCase`.
- Kubernetes resource patterns keep Kubernetes-native lowercase names:
  `pods`, `deployments.apps`, `apps/v1/deployments`, `v1/pods`.

Use quoted YAML strings for glob patterns:

```yaml
resources:
  - "apps/v1/*"
  - "*.reports.kyverno.io"
```

## Resource Patterns

Every resource selector uses the same syntax:

| Pattern | Meaning |
| --- | --- |
| `pods` | Any resource named `pods` |
| `pods.v1` | Core v1 Pods |
| `deployments.apps` | Deployments in the apps API group |
| `v1/pods` | Core v1 Pods |
| `apps/v1/deployments` | apps/v1 Deployments |
| `apps/v1/*` | Any apps/v1 resource |
| `*.reports.kyverno.io` | Any matching Kyverno report resource |

`v1/*` means core API v1 only. It does not mean every API group at version
`v1`.

## Single Classification Point

`resourceProfiles.rules` is the only place that should classify Kubernetes
resources for product behavior.

Each rule maps resources to a named profile and selects reusable policies:

```yaml
resourceProfiles:
  defaults:
    enabled: true
    filterChain: default
    extractorSet: generic
    retentionPolicy: standard
    compactionStrategy: full_json
    priority: normal
    maxEventBuffer: 256
  rules:
    - name: event_rollup
      resources: [events.events.k8s.io]
      filterChain: default
      extractorSet: event
      retentionPolicy: events_short_window
      compactionStrategy: rollup
      priority: high
      maxEventBuffer: 2048

    - name: low_signal_report
      enabled: false
      resources:
        - "*policyreports.wgpolicyk8s.io"
        - "*ephemeralreports.reports.kyverno.io"
      filterChain: report_skip
      extractorSet: none
      retentionPolicy: derived_short_window
      compactionStrategy: skip_or_downsample
      priority: low
```

The selected profile is stored in `resource_processing_profiles` during API
resource discovery. Ingest then uses the profile to choose the filter chain and
extractor set for every observation.

## Component Libraries

Filters and extractors are reusable components. They do not decide which
resource is important; profiles do that.

`processing.filterChains` defines named ordered chains:

```yaml
processing:
  filterChains:
    default:
      - managed_fields
      - resource_version
      - metadata_generation
      - status_condition_set
      - leader_election_configmap
      - cluster_autoscaler_status
      - gke_webhook_heartbeat
    secret_metadata_only:
      - managed_fields
      - resource_version
      - secret_metadata_only
    report_skip:
      - report_skip
    none: []
```

`processing.filters` defines the components used by those chains:

```yaml
processing:
  filters:
    managed_fields:
      type: builtin
      action: keep_modified
      removePaths: [/metadata/managedFields]
    secret_metadata_only:
      type: builtin
      action: keep_modified
      keepSecretKeys: true
      removePaths: [/data, /stringData]
      guard:
        resources: [secrets]
    report_skip:
      type: builtin
      action: discard_resource
```

`guard` is a defensive constraint on a component. It should prevent an unsafe
component from running on the wrong resource, but it should not become another
business classification layer.

Filter and extractor guards support the same resource/kind glob matching plus
optional object scoping:

```yaml
guard:
  resources: [configmaps]
  namespaces: [kube-system]
  names: [cluster-autoscaler-status]
```

Extractor sets follow the same shape:

```yaml
processing:
  extractorSets:
    generic: [reference]
    pod: [reference, pod]
    node: [reference, node]
    event: [reference, event]
    endpointslice: [reference, endpointslice]
    none: []
  extractors:
    reference:
      type: builtin
    pod:
      type: builtin
      guard:
        resources: [pods]
    event:
      type: builtin
      guard:
        resources: [events.events.k8s.io]
```

## Retention Policies

Retention policy selection belongs to the resource profile. Retention policy
definitions belong under storage:

```yaml
storage:
  retention:
    enabled: true
    filterDecisionMaxAgeSeconds: 604800
    policies:
      standard:
        minVersionsPerObject: 1
      events_short_window:
        maxAgeSeconds: 604800
        minVersionsPerObject: 1
      crd_long_window:
        maxAgeSeconds: 7776000
        minVersionsPerObject: 2
```

This keeps resource importance in one place:

```yaml
resourceProfiles:
  rules:
    - name: crd_definition
      resources: [customresourcedefinitions.apiextensions.k8s.io]
      retentionPolicy: crd_long_window
```

## Execution Flow

1. Discovery lists API resources and resolves each resource through
   `resourceProfiles.rules`.
2. The selected profile is persisted in `resource_processing_profiles`.
3. Watch/list collects observations.
4. Ingest selects the profile for each observation.
5. The profile's `filterChain` runs before hashing and storage.
6. The profile's `extractorSet` emits facts, edges, and changes.
7. Retention applies named policies selected by profile.

The important invariant is that resource classification is profile-driven.
Filters, extractors, retention policies, and storage strategies are reusable
building blocks selected by the profile.
