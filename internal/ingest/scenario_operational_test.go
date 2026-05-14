package ingest

import (
	"context"
	"testing"
	"time"

	"kube-insight/internal/storage/sqlite"
)

func TestSQLiteScenarioWebhookGitOpsFailureAfterRollback(t *testing.T) {
	input := `{
	  "apiVersion": "v1",
	  "kind": "List",
	  "items": [
	    {
	      "apiVersion": "v1",
	      "kind": "Service",
	      "metadata": {"name": "policy-webhook", "namespace": "policy-system", "uid": "svc-policy-webhook", "resourceVersion": "1"}
	    },
	    {
	      "apiVersion": "kustomize.toolkit.fluxcd.io/v1",
	      "kind": "Kustomization",
	      "metadata": {"name": "webapp", "namespace": "flux-system", "uid": "ks-webapp", "resourceVersion": "2"},
	      "status": {"conditions": [{"type": "Ready", "status": "False", "reason": "ReconciliationFailed"}]}
	    },
	    {
	      "type": "ADDED",
	      "object": {
	        "apiVersion": "admissionregistration.k8s.io/v1",
	        "kind": "ValidatingWebhookConfiguration",
	        "metadata": {"name": "policy-guard", "uid": "vwc-policy-guard", "resourceVersion": "3"},
	        "webhooks": [{
	          "name": "policy-guard.platform.example.com",
	          "failurePolicy": "Fail",
	          "clientConfig": {"service": {"name": "policy-webhook", "namespace": "policy-system"}}
	        }]
	      }
	    },
	    {
	      "apiVersion": "events.k8s.io/v1",
	      "kind": "Event",
	      "metadata": {"name": "webapp-admission-failed", "namespace": "flux-system", "uid": "event-webapp-admission", "resourceVersion": "4"},
	      "reason": "ReconciliationFailed",
	      "type": "Warning",
	      "message": "dry-run failed: failed calling webhook policy-guard.platform.example.com: no endpoints available for service policy-system/policy-webhook",
	      "count": 4,
	      "regarding": {"apiVersion": "kustomize.toolkit.fluxcd.io/v1", "kind": "Kustomization", "namespace": "flux-system", "name": "webapp", "uid": "ks-webapp"}
	    },
	    {
	      "type": "DELETED",
	      "object": {
	        "apiVersion": "admissionregistration.k8s.io/v1",
	        "kind": "ValidatingWebhookConfiguration",
	        "metadata": {"name": "policy-guard", "uid": "vwc-policy-guard", "resourceVersion": "5", "deletionTimestamp": "2026-05-14T10:08:00Z"},
	        "webhooks": [{
	          "name": "policy-guard.platform.example.com",
	          "failurePolicy": "Fail",
	          "clientConfig": {"service": {"name": "policy-webhook", "namespace": "policy-system"}}
	        }]
	      }
	    }
	  ]
	}`
	store := openScenarioSQLite(t)
	defer store.Close()
	pipeline := DefaultPipeline(store)
	pipeline.Now = func() time.Time { return time.Unix(100, 0) }

	if _, err := pipeline.IngestJSON(context.Background(), []byte(input)); err != nil {
		t.Fatal(err)
	}
	webhook, err := store.InvestigateWithOptions(context.Background(), sqlite.ObjectTarget{
		ClusterID: "local",
		Kind:      "ValidatingWebhookConfiguration",
		Name:      "policy-guard",
	}, sqlite.InvestigationOptions{MaxVersionsPerObject: 3})
	if err != nil {
		t.Fatal(err)
	}
	if !hasStoredFact(webhook.Facts, "admission_webhook.service", "policy-system/policy-webhook") ||
		!hasStoredFact(webhook.Facts, "admission_webhook.failure_policy", "Fail") {
		t.Fatalf("webhook facts missing: %#v", webhook.Facts)
	}
	if len(webhook.Versions) < 2 || !bundleHasVersionDiffPath(webhook, "/metadata/deletionTimestamp") {
		t.Fatalf("webhook rollback/delete proof missing: %#v", webhook.VersionDiffs)
	}
	topology, err := store.Topology(context.Background(), sqlite.ObjectTarget{
		ClusterID: "local",
		Kind:      "ValidatingWebhookConfiguration",
		Name:      "policy-guard",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hasTopologyEdge(topology.Edges, "webhook_uses_service", "Service", "policy-system", "policy-webhook") {
		t.Fatalf("webhook service edge missing: %#v", topology.Edges)
	}
	event, err := store.Investigate(context.Background(), sqlite.ObjectTarget{
		ClusterID: "local",
		Kind:      "Event",
		Namespace: "flux-system",
		Name:      "webapp-admission-failed",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hasStoredFact(event.Facts, "k8s_event.reason", "ReconciliationFailed") ||
		!hasStoredFact(event.Facts, "k8s_event.count", "4") {
		t.Fatalf("gitops event evidence missing: %#v", event.Facts)
	}
	eventTopology, err := store.Topology(context.Background(), sqlite.ObjectTarget{
		ClusterID: "local",
		Kind:      "Event",
		Namespace: "flux-system",
		Name:      "webapp-admission-failed",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hasTopologyEdge(eventTopology.Edges, "event_regarding_object", "Kustomization", "flux-system", "webapp") {
		t.Fatalf("event-to-gitops edge missing: %#v", eventTopology.Edges)
	}
}

func TestSQLiteScenarioRBACRollbackExplainsForbiddenEvent(t *testing.T) {
	input := `{
	  "apiVersion": "v1",
	  "kind": "List",
	  "items": [
	    {
	      "apiVersion": "v1",
	      "kind": "ServiceAccount",
	      "metadata": {"name": "source-controller", "namespace": "flux-system", "uid": "sa-source-controller", "resourceVersion": "10"}
	    },
	    {
	      "apiVersion": "rbac.authorization.k8s.io/v1",
	      "kind": "Role",
	      "metadata": {"name": "source-reader", "namespace": "flux-system", "uid": "role-source-reader", "resourceVersion": "11"},
	      "rules": [{"apiGroups": [""], "resources": ["pods"], "verbs": ["get", "list", "watch"]}]
	    },
	    {
	      "apiVersion": "rbac.authorization.k8s.io/v1",
	      "kind": "RoleBinding",
	      "metadata": {"name": "source-reader-binding", "namespace": "flux-system", "uid": "rb-source-reader", "resourceVersion": "12"},
	      "roleRef": {"apiGroup": "rbac.authorization.k8s.io", "kind": "Role", "name": "source-reader"},
	      "subjects": [{"kind": "ServiceAccount", "name": "source-controller", "namespace": "flux-system"}]
	    },
	    {
	      "apiVersion": "rbac.authorization.k8s.io/v1",
	      "kind": "Role",
	      "metadata": {"name": "source-reader", "namespace": "flux-system", "uid": "role-source-reader", "resourceVersion": "13"},
	      "rules": [{"apiGroups": [""], "resources": ["pods"], "verbs": ["get"]}]
	    },
	    {
	      "apiVersion": "events.k8s.io/v1",
	      "kind": "Event",
	      "metadata": {"name": "source-controller-forbidden", "namespace": "flux-system", "uid": "event-rbac-forbidden", "resourceVersion": "14"},
	      "reason": "FailedList",
	      "type": "Warning",
	      "message": "pods is forbidden: User system:serviceaccount:flux-system:source-controller cannot list resource pods",
	      "count": 2,
	      "regarding": {"apiVersion": "v1", "kind": "ServiceAccount", "namespace": "flux-system", "name": "source-controller", "uid": "sa-source-controller"}
	    }
	  ]
	}`
	store := openScenarioSQLite(t)
	defer store.Close()
	pipeline := DefaultPipeline(store)
	pipeline.Now = func() time.Time { return time.Unix(100, 0) }

	if _, err := pipeline.IngestJSON(context.Background(), []byte(input)); err != nil {
		t.Fatal(err)
	}
	roleBinding, err := store.Investigate(context.Background(), sqlite.ObjectTarget{
		ClusterID: "local",
		Kind:      "RoleBinding",
		Namespace: "flux-system",
		Name:      "source-reader-binding",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hasStoredFact(roleBinding.Facts, "rbac.role_ref", "Role/source-reader") ||
		!hasStoredFact(roleBinding.Facts, "rbac.subject", "ServiceAccount/flux-system/source-controller") {
		t.Fatalf("rbac binding facts missing: %#v", roleBinding.Facts)
	}
	topology, err := store.Topology(context.Background(), sqlite.ObjectTarget{
		ClusterID: "local",
		Kind:      "RoleBinding",
		Namespace: "flux-system",
		Name:      "source-reader-binding",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hasTopologyEdge(topology.Edges, "rbac_binding_grants_role", "Role", "flux-system", "source-reader") ||
		!hasTopologyEdge(topology.Edges, "rbac_binding_binds_subject", "ServiceAccount", "flux-system", "source-controller") {
		t.Fatalf("rbac topology missing: %#v", topology.Edges)
	}
	role, err := store.InvestigateWithOptions(context.Background(), sqlite.ObjectTarget{
		ClusterID: "local",
		Kind:      "Role",
		Namespace: "flux-system",
		Name:      "source-reader",
	}, sqlite.InvestigationOptions{MaxVersionsPerObject: 2})
	if err != nil {
		t.Fatal(err)
	}
	if !bundleHasVersionDiffPath(role, "/rules") {
		t.Fatalf("role rules diff missing: %#v", role.VersionDiffs)
	}
	event, err := store.Investigate(context.Background(), sqlite.ObjectTarget{
		ClusterID: "local",
		Kind:      "Event",
		Namespace: "flux-system",
		Name:      "source-controller-forbidden",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hasStoredFact(event.Facts, "k8s_event.reason", "FailedList") {
		t.Fatalf("forbidden event facts missing: %#v", event.Facts)
	}
}

func TestSQLiteScenarioCRDConversionWebhookRegression(t *testing.T) {
	input := `{
	  "apiVersion": "v1",
	  "kind": "List",
	  "items": [
	    {
	      "apiVersion": "v1",
	      "kind": "Service",
	      "metadata": {"name": "widgets-conversion", "namespace": "platform", "uid": "svc-widgets-conversion", "resourceVersion": "20"}
	    },
	    {
	      "apiVersion": "v1",
	      "kind": "Service",
	      "metadata": {"name": "widgets-conversion-fixed", "namespace": "platform", "uid": "svc-widgets-conversion-fixed", "resourceVersion": "21"}
	    },
	    {
	      "apiVersion": "apiextensions.k8s.io/v1",
	      "kind": "CustomResourceDefinition",
	      "metadata": {"name": "widgets.example.com", "uid": "crd-widgets", "resourceVersion": "22"},
	      "spec": {
	        "group": "example.com",
	        "names": {"kind": "Widget", "plural": "widgets"},
	        "conversion": {"strategy": "Webhook", "webhook": {"clientConfig": {"service": {"name": "widgets-conversion", "namespace": "platform"}}}}
	      }
	    },
	    {
	      "apiVersion": "example.com/v1",
	      "kind": "Widget",
	      "metadata": {"name": "checkout-widget", "namespace": "apps", "uid": "widget-checkout", "resourceVersion": "23"},
	      "status": {"conditions": [{"type": "Ready", "status": "False", "reason": "ConversionFailed"}]}
	    },
	    {
	      "apiVersion": "events.k8s.io/v1",
	      "kind": "Event",
	      "metadata": {"name": "widget-conversion-failed", "namespace": "apps", "uid": "event-widget-conversion", "resourceVersion": "24"},
	      "reason": "ConversionFailed",
	      "type": "Warning",
	      "message": "conversion webhook for widgets.example.com failed during dry-run",
	      "count": 3,
	      "regarding": {"apiVersion": "example.com/v1", "kind": "Widget", "namespace": "apps", "name": "checkout-widget", "uid": "widget-checkout"}
	    },
	    {
	      "apiVersion": "apiextensions.k8s.io/v1",
	      "kind": "CustomResourceDefinition",
	      "metadata": {"name": "widgets.example.com", "uid": "crd-widgets", "resourceVersion": "25"},
	      "spec": {
	        "group": "example.com",
	        "names": {"kind": "Widget", "plural": "widgets"},
	        "conversion": {"strategy": "Webhook", "webhook": {"clientConfig": {"service": {"name": "widgets-conversion-fixed", "namespace": "platform"}}}}
	      }
	    }
	  ]
	}`
	store := openScenarioSQLite(t)
	defer store.Close()
	pipeline := DefaultPipeline(store)
	pipeline.Now = func() time.Time { return time.Unix(100, 0) }

	if _, err := pipeline.IngestJSON(context.Background(), []byte(input)); err != nil {
		t.Fatal(err)
	}
	crd, err := store.InvestigateWithOptions(context.Background(), sqlite.ObjectTarget{
		ClusterID: "local",
		Kind:      "CustomResourceDefinition",
		Name:      "widgets.example.com",
	}, sqlite.InvestigationOptions{MaxVersionsPerObject: 2})
	if err != nil {
		t.Fatal(err)
	}
	if !hasStoredFact(crd.Facts, "crd_conversion_webhook.service", "platform/widgets-conversion") ||
		!hasStoredFact(crd.Facts, "crd_conversion_webhook.service", "platform/widgets-conversion-fixed") {
		t.Fatalf("crd conversion service facts missing: %#v", crd.Facts)
	}
	if !bundleHasVersionDiffPath(crd, "/spec/conversion/webhook/clientConfig/service/name") {
		t.Fatalf("crd conversion webhook diff missing: %#v", crd.VersionDiffs)
	}
	topology, err := store.Topology(context.Background(), sqlite.ObjectTarget{
		ClusterID: "local",
		Kind:      "CustomResourceDefinition",
		Name:      "widgets.example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hasTopologyEdge(topology.Edges, "crd_conversion_webhook_uses_service", "Service", "platform", "widgets-conversion-fixed") {
		t.Fatalf("crd conversion service edge missing: %#v", topology.Edges)
	}
	widget, err := store.Investigate(context.Background(), sqlite.ObjectTarget{
		ClusterID: "local",
		Kind:      "Widget",
		Namespace: "apps",
		Name:      "checkout-widget",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hasStoredFact(widget.Facts, "status_condition.Ready.reason", "ConversionFailed") {
		t.Fatalf("custom resource condition evidence missing: %#v", widget.Facts)
	}
	event, err := store.Investigate(context.Background(), sqlite.ObjectTarget{
		ClusterID: "local",
		Kind:      "Event",
		Namespace: "apps",
		Name:      "widget-conversion-failed",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hasStoredFact(event.Facts, "k8s_event.reason", "ConversionFailed") {
		t.Fatalf("conversion event facts missing: %#v", event.Facts)
	}
}

func TestSQLiteScenarioCertManagerCertificateRecoveredAfterSecretRotation(t *testing.T) {
	input := `{
	  "apiVersion": "v1",
	  "kind": "List",
	  "items": [
	    {
	      "apiVersion": "cert-manager.io/v1",
	      "kind": "ClusterIssuer",
	      "metadata": {"name": "letsencrypt-prod", "uid": "issuer-letsencrypt", "resourceVersion": "30"}
	    },
	    {
	      "apiVersion": "v1",
	      "kind": "Secret",
	      "metadata": {"name": "checkout-tls", "namespace": "default", "uid": "secret-checkout-tls", "resourceVersion": "31"},
	      "data": {"tls.crt": "redacted-by-filter", "tls.key": "redacted-by-filter"}
	    },
	    {
	      "apiVersion": "cert-manager.io/v1",
	      "kind": "Certificate",
	      "metadata": {"name": "checkout-cert", "namespace": "default", "uid": "cert-checkout", "resourceVersion": "32"},
	      "spec": {
	        "secretName": "checkout-tls",
	        "issuerRef": {"name": "letsencrypt-prod", "kind": "ClusterIssuer", "group": "cert-manager.io"}
	      },
	      "status": {"conditions": [{"type": "Ready", "status": "False", "reason": "DoesNotExist"}]}
	    },
	    {
	      "apiVersion": "events.k8s.io/v1",
	      "kind": "Event",
	      "metadata": {"name": "checkout-cert-failed", "namespace": "default", "uid": "event-cert-failed", "resourceVersion": "33"},
	      "reason": "DoesNotExist",
	      "type": "Warning",
	      "message": "Issuing certificate as Secret checkout-tls does not exist",
	      "count": 2,
	      "regarding": {"apiVersion": "cert-manager.io/v1", "kind": "Certificate", "namespace": "default", "name": "checkout-cert", "uid": "cert-checkout"}
	    },
	    {
	      "apiVersion": "cert-manager.io/v1",
	      "kind": "Certificate",
	      "metadata": {"name": "checkout-cert", "namespace": "default", "uid": "cert-checkout", "resourceVersion": "34"},
	      "spec": {
	        "secretName": "checkout-tls",
	        "issuerRef": {"name": "letsencrypt-prod", "kind": "ClusterIssuer", "group": "cert-manager.io"}
	      },
	      "status": {"conditions": [{"type": "Ready", "status": "True", "reason": "Ready"}]}
	    }
	  ]
	}`
	store := openScenarioSQLite(t)
	defer store.Close()
	pipeline := DefaultPipeline(store)
	pipeline.Now = func() time.Time { return time.Unix(100, 0) }

	if _, err := pipeline.IngestJSON(context.Background(), []byte(input)); err != nil {
		t.Fatal(err)
	}
	certificate, err := store.InvestigateWithOptions(context.Background(), sqlite.ObjectTarget{
		ClusterID: "local",
		Kind:      "Certificate",
		Namespace: "default",
		Name:      "checkout-cert",
	}, sqlite.InvestigationOptions{MaxVersionsPerObject: 2})
	if err != nil {
		t.Fatal(err)
	}
	if !hasStoredFact(certificate.Facts, "status_condition.Ready", "False") ||
		!hasStoredFact(certificate.Facts, "status_condition.Ready.reason", "DoesNotExist") ||
		!hasStoredFact(certificate.Facts, "status_condition.Ready", "True") {
		t.Fatalf("certificate condition evidence missing: %#v", certificate.Facts)
	}
	if !bundleHasVersionDiffPath(certificate, "/status/conditions") {
		t.Fatalf("certificate ready diff missing: %#v", certificate.VersionDiffs)
	}
	topology, err := store.Topology(context.Background(), sqlite.ObjectTarget{
		ClusterID: "local",
		Kind:      "Certificate",
		Namespace: "default",
		Name:      "checkout-cert",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hasTopologyEdge(topology.Edges, "certmanager_writes_secret", "Secret", "default", "checkout-tls") ||
		!hasTopologyEdge(topology.Edges, "certmanager_uses_issuer", "ClusterIssuer", "", "letsencrypt-prod") {
		t.Fatalf("cert-manager topology missing: %#v", topology.Edges)
	}
	event, err := store.Investigate(context.Background(), sqlite.ObjectTarget{
		ClusterID: "local",
		Kind:      "Event",
		Namespace: "default",
		Name:      "checkout-cert-failed",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hasStoredFact(event.Facts, "k8s_event.reason", "DoesNotExist") {
		t.Fatalf("certificate event evidence missing: %#v", event.Facts)
	}
}
