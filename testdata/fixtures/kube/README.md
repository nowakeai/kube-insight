# Kubernetes Fixtures

These are small, sanitized fixture documents intended to be committed. They
cover common core resources, workload resources, networking/storage-adjacent
resources, a CRD plus a matching custom resource instance, cert-manager
certificate issuance resources, admission webhooks, and Event-to-resource
references. RBAC fixtures cover RoleBinding-to-Role, ClusterRoleBinding-to-
ClusterRole, and bound ServiceAccount/User/Group subjects. Additional fixtures
cover Pod/workload references to ServiceAccount, Secret, ConfigMap, PVC/PV,
StorageClass, Ingress backends, APIService/CRD webhook services, and Gateway
API parent/backend references.

Use generated data for volume tests:

```bash
go run ./cmd/kube-insight generate samples --fixtures testdata/fixtures/kube --output testdata/generated/kube-samples --clusters 3 --copies 100
```
