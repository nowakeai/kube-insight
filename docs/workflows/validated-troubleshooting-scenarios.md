# Validated Troubleshooting Scenarios

These scenarios are acceptance examples, not product-specific query logic.
Kube-insight should not contain a "webhook incident" branch or an "RBAC incident"
branch. The goal is to prove that generic historical evidence, facts,
relationships, search, topology, and version diffs are strong enough for humans
and agents to investigate these incidents better than live `kubectl` and raw API
server reads.

The current runnable coverage lives in `internal/ingest/scenario_test.go` and
`internal/ingest/scenario_operational_test.go`.

## 1. Webhook Broke GitOps, Then Disappeared

Story:

A platform component briefly created a `ValidatingWebhookConfiguration` with
`failurePolicy=Fail`. Flux attempted a dry-run apply for a `Kustomization`, the
API server called the webhook, and the webhook service had no endpoints. The
GitOps controller raised an alert. By the time operations looked, the webhook
had already been deleted and Kubernetes Events were no longer visible in the
cluster.

Kube-insight evidence:

- historical `ValidatingWebhookConfiguration` versions,
- webhook facts: name, `failurePolicy`, and referenced service,
- topology edge from webhook to Service,
- Event facts for the failed GitOps reconciliation,
- Event edge to the affected `Kustomization`,
- version diff proving when the webhook was deleted.

Runnable test:

```text
TestSQLiteScenarioWebhookGitOpsFailureAfterRollback
```

## 2. RBAC Rollback Hid A Permission Outage

Story:

A controller lost `list pods` permission for several minutes because a Role was
edited incorrectly. The Role was fixed before the investigation, so
`kubectl auth can-i` only shows the current healthy state.

Kube-insight evidence:

- RoleBinding facts for `roleRef` and bound ServiceAccount subject,
- topology edges from RoleBinding to Role and ServiceAccount,
- Role version diff showing the dropped verb,
- Event facts for the historical forbidden list failure.

Runnable test:

```text
TestSQLiteScenarioRBACRollbackExplainsForbiddenEvent
```

## 3. CRD Conversion Webhook Broke Custom Resources

Story:

A CRD conversion webhook was changed during a controller upgrade. Custom
resources started failing dry-run and reconciliation. Later the webhook service
was corrected, and the visible current CRD looks healthy.

Kube-insight evidence:

- CRD facts for conversion webhook service references,
- topology edge from CRD to conversion Service,
- CRD version diff showing the webhook service change,
- custom resource status condition reason,
- Event facts pointing at the failed custom resource.

Runnable test:

```text
TestSQLiteScenarioCRDConversionWebhookRegression
```

## 4. cert-manager Certificate Recovered After Secret Rotation

Story:

A TLS Secret was missing or rotated incorrectly, causing a cert-manager
Certificate to be `Ready=False`. cert-manager later repaired the Secret and the
Certificate became ready. The current Secret and Certificate are healthy.

Kube-insight evidence:

- Certificate status condition facts and reasons,
- Certificate edges to Secret and ClusterIssuer,
- Event facts for issuance failure,
- version diff proving `Ready=False -> Ready=True`.

Runnable test:

```text
TestSQLiteScenarioCertManagerCertificateRecoveredAfterSecretRotation
```

## 5. Service Spike With Expired Events

Story:

A service had a short spike. Current Pods are healthy, but during the window a
Pod became unready, restarted, and recorded `OOMKilled`; an Event existed at the
time but may have expired from Kubernetes.

Kube-insight evidence:

- Service -> EndpointSlice -> Pod -> Node topology,
- Pod restart, readiness, and last termination facts,
- Node pressure facts,
- Event rollup facts,
- reconstructed Pod versions and diffs.

Runnable tests:

```text
TestSQLiteGeneratedIncidentBundleFlow
TestSQLiteScenarioEventMirrorInvestigation
```

## 6. EndpointSlice Rollout Blackhole

Story:

A rollout changed endpoint readiness and temporarily removed serving endpoints.
By the time the incident is reviewed, the Service routes normally.

Kube-insight evidence:

- EndpointSlice readiness facts,
- EndpointSlice -> Pod edge,
- readiness change rows,
- service investigation bundle with EndpointSlice and Pod proof versions.

Runnable tests:

```text
TestPipelineScenarioNodePressureAndEndpointSliceReadiness
TestSQLiteScenarioNodeAndEndpointSliceInvestigation
```
