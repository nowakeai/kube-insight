package extractor

import (
	"context"
	"testing"
	"time"

	"kube-insight/internal/core"
	"kube-insight/internal/kubeapi"
)

func TestPodExtractorEmitsPlacementFactAndEdge(t *testing.T) {
	obs := core.Observation{
		Type:       core.ObservationModified,
		ObservedAt: time.Unix(10, 0),
		Ref: core.ResourceRef{
			ClusterID: "c1",
			Resource:  "pods",
			Kind:      "Pod",
			Namespace: "default",
			Name:      "api-1",
			UID:       "pod-uid",
		},
		Object: map[string]any{
			"status": map[string]any{"phase": "Running"},
			"spec":   map[string]any{"nodeName": "node-a"},
		},
	}

	evidence, err := PodExtractor{}.Extract(context.Background(), obs)
	if err != nil {
		t.Fatal(err)
	}
	if len(evidence.Facts) != 2 {
		t.Fatalf("facts = %d, want 2", len(evidence.Facts))
	}
	if len(evidence.Edges) != 1 {
		t.Fatalf("edges = %d, want 1", len(evidence.Edges))
	}
	if evidence.Edges[0].Type != "pod_on_node" {
		t.Fatalf("edge type = %s", evidence.Edges[0].Type)
	}
}

func TestPodExtractorEmitsRestartOOMAndReadinessTransitions(t *testing.T) {
	obs := core.Observation{
		Type:       core.ObservationModified,
		ObservedAt: time.Unix(10, 0),
		Ref: core.ResourceRef{
			ClusterID: "c1",
			Resource:  "pods",
			Kind:      "Pod",
			Namespace: "default",
			Name:      "api-1",
			UID:       "pod-uid",
		},
		Object: map[string]any{
			"status": map[string]any{
				"phase": "Running",
				"conditions": []any{
					map[string]any{"type": "Ready", "status": "False"},
				},
				"containerStatuses": []any{
					map[string]any{
						"name":         "api",
						"restartCount": 3,
						"lastState": map[string]any{
							"terminated": map[string]any{"reason": "OOMKilled"},
						},
						"state": map[string]any{
							"waiting": map[string]any{"reason": "CrashLoopBackOff"},
						},
					},
				},
			},
		},
	}

	evidence, err := PodExtractor{}.Extract(context.Background(), obs)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []struct {
		key   string
		value string
	}{
		{"pod_status.ready", "False"},
		{"pod_status.restart_count", "3"},
		{"pod_status.reason", "CrashLoopBackOff"},
		{"pod_status.last_reason", "OOMKilled"},
	} {
		if !hasFact(evidence.Facts, want.key, want.value) {
			t.Fatalf("missing fact %s=%s in %#v", want.key, want.value, evidence.Facts)
		}
	}
	if !hasChange(evidence.Changes, "status.conditions.Ready", "False") ||
		!hasChange(evidence.Changes, "status.containerStatuses.api.restartCount", "3") ||
		!hasChange(evidence.Changes, "status.containerStatuses.api.lastState.reason", "OOMKilled") {
		t.Fatalf("expected pod transition changes, got %#v", evidence.Changes)
	}
}

func TestNodeExtractorEmitsConditionFacts(t *testing.T) {
	obs := core.Observation{
		ObservedAt: time.Unix(10, 0),
		Ref:        core.ResourceRef{ClusterID: "c1", Resource: "nodes", Kind: "Node", Name: "node-a", UID: "node-uid"},
		Object: map[string]any{
			"status": map[string]any{
				"conditions": []any{
					map[string]any{"type": "MemoryPressure", "status": "True"},
				},
			},
		},
	}

	evidence, err := NodeExtractor{}.Extract(context.Background(), obs)
	if err != nil {
		t.Fatal(err)
	}
	if len(evidence.Facts) != 1 {
		t.Fatalf("facts = %d, want 1", len(evidence.Facts))
	}
	if evidence.Facts[0].Key != "node_condition.MemoryPressure" {
		t.Fatalf("fact key = %s", evidence.Facts[0].Key)
	}
	if len(evidence.Changes) != 1 {
		t.Fatalf("changes = %d, want 1", len(evidence.Changes))
	}
}

func TestNodeExtractorEmitsPressureAndReadyTransitions(t *testing.T) {
	obs := core.Observation{
		ObservedAt: time.Unix(10, 0),
		Ref:        core.ResourceRef{ClusterID: "c1", Resource: "nodes", Kind: "Node", Name: "node-a", UID: "node-uid"},
		Object: map[string]any{
			"status": map[string]any{
				"conditions": []any{
					map[string]any{"type": "Ready", "status": "False"},
					map[string]any{"type": "MemoryPressure", "status": "True"},
					map[string]any{"type": "DiskPressure", "status": "True"},
					map[string]any{"type": "PIDPressure", "status": "False"},
				},
			},
		},
	}

	evidence, err := NodeExtractor{}.Extract(context.Background(), obs)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []struct {
		key   string
		value string
	}{
		{"node_condition.Ready", "False"},
		{"node_condition.MemoryPressure", "True"},
		{"node_condition.DiskPressure", "True"},
		{"node_condition.PIDPressure", "False"},
	} {
		if !hasFact(evidence.Facts, want.key, want.value) {
			t.Fatalf("missing fact %s=%s in %#v", want.key, want.value, evidence.Facts)
		}
	}
	if !hasChange(evidence.Changes, "status.conditions.MemoryPressure", "True") {
		t.Fatalf("memory pressure change missing: %#v", evidence.Changes)
	}
}

func TestEventExtractorFingerprintsMessage(t *testing.T) {
	obs := core.Observation{
		ObservedAt: time.Unix(10, 0),
		Ref:        core.ResourceRef{ClusterID: "c1", Resource: "events", Kind: "Event", Name: "event-a", UID: "event-uid"},
		Object: map[string]any{
			"reason":              "BackOff",
			"type":                "Warning",
			"message":             "Back-off restarting failed container app",
			"count":               3,
			"reportingController": "kubelet",
			"reportingInstance":   "node-a",
		},
	}

	evidence, err := EventExtractor{}.Extract(context.Background(), obs)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []struct {
		key   string
		value string
	}{
		{"k8s_event.message_preview", "Back-off restarting failed container app"},
		{"k8s_event.reporting_controller", "kubelet"},
		{"k8s_event.reporting_instance", "node-a"},
	} {
		if !hasFact(evidence.Facts, want.key, want.value) {
			t.Fatalf("missing fact %s=%s in %#v", want.key, want.value, evidence.Facts)
		}
	}
}

func TestEventExtractorCapturesEventsV1Fields(t *testing.T) {
	obs := core.Observation{
		ObservedAt: time.Unix(10, 0),
		Ref:        core.ResourceRef{ClusterID: "c1", Resource: "events", Kind: "Event", Name: "event-a", UID: "event-uid"},
		Object: map[string]any{
			"reason":              "PolicyViolation",
			"type":                "Warning",
			"note":                "admission denied by validating-node-p4sa-audience",
			"action":              "ValidatingAdmissionPolicyDenied",
			"reportingController": "validatingadmissionpolicy",
			"series": map[string]any{
				"count": 7,
			},
		},
	}

	evidence, err := EventExtractor{}.Extract(context.Background(), obs)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []struct {
		key   string
		value string
	}{
		{"k8s_event.message_preview", "admission denied by validating-node-p4sa-audience"},
		{"k8s_event.action", "ValidatingAdmissionPolicyDenied"},
		{"k8s_event.reporting_controller", "validatingadmissionpolicy"},
		{"k8s_event.series_count", "7"},
	} {
		if !hasFact(evidence.Facts, want.key, want.value) {
			t.Fatalf("missing fact %s=%s in %#v", want.key, want.value, evidence.Facts)
		}
	}
	if !hasChange(evidence.Changes, "series.count", "7") {
		t.Fatalf("series count change missing: %#v", evidence.Changes)
	}
}

func TestEventExtractorEmitsObjectReferenceEdges(t *testing.T) {
	obs := core.Observation{
		ObservedAt: time.Unix(10, 0),
		Ref: core.ResourceRef{
			ClusterID: "c1",
			Resource:  "events",
			Kind:      "Event",
			Namespace: "default",
			Name:      "event-a",
			UID:       "event-uid",
		},
		Object: map[string]any{
			"regarding": map[string]any{
				"apiVersion": "cert-manager.io/v1",
				"kind":       "Certificate",
				"namespace":  "default",
				"name":       "api-cert",
			},
		},
	}

	evidence, err := EventExtractor{}.Extract(context.Background(), obs)
	if err != nil {
		t.Fatal(err)
	}
	if len(evidence.Edges) != 1 {
		t.Fatalf("edges = %d, want 1", len(evidence.Edges))
	}
	if evidence.Edges[0].Type != "event_regarding_object" || evidence.Edges[0].TargetID != "c1/cert-manager.io/certificates/default/api-cert" {
		t.Fatalf("edge = %#v", evidence.Edges[0])
	}
}

func TestEventExtractorUsesContextResolverForDiscoveredResources(t *testing.T) {
	resolver := kubeapi.NewResolver()
	resolver.Register(kubeapi.ResourceInfo{
		Group:      "example.com",
		Version:    "v1",
		Resource:   "indices",
		Kind:       "Index",
		Namespaced: true,
	})
	obs := core.Observation{
		ObservedAt: time.Unix(10, 0),
		Ref: core.ResourceRef{
			ClusterID: "c1",
			Resource:  "events",
			Kind:      "Event",
			Namespace: "default",
			Name:      "event-a",
			UID:       "event-uid",
		},
		Object: map[string]any{
			"regarding": map[string]any{
				"apiVersion": "example.com/v1",
				"kind":       "Index",
				"name":       "search",
			},
		},
	}

	evidence, err := EventExtractor{}.Extract(WithResolver(context.Background(), resolver), obs)
	if err != nil {
		t.Fatal(err)
	}
	if !hasEdge(evidence.Edges, "event_regarding_object", "c1/example.com/indices/default/search") {
		t.Fatalf("resolver-backed edge missing: %#v", evidence.Edges)
	}
}

func TestReferenceExtractorEmitsCertManagerAndWebhookEdges(t *testing.T) {
	cert := core.Observation{
		ObservedAt: time.Unix(10, 0),
		Ref: core.ResourceRef{
			ClusterID: "c1",
			Group:     "cert-manager.io",
			Version:   "v1",
			Resource:  "certificates",
			Kind:      "Certificate",
			Namespace: "default",
			Name:      "api-cert",
			UID:       "cert-uid",
		},
		Object: map[string]any{
			"spec": map[string]any{
				"secretName": "api-tls",
				"issuerRef": map[string]any{
					"group": "cert-manager.io",
					"kind":  "Issuer",
					"name":  "app-issuer",
				},
			},
		},
	}
	certEvidence, err := ReferenceExtractor{}.Extract(context.Background(), cert)
	if err != nil {
		t.Fatal(err)
	}
	if !hasEdge(certEvidence.Edges, "certmanager_uses_issuer", "c1/cert-manager.io/issuers/default/app-issuer") {
		t.Fatalf("issuer edge missing: %#v", certEvidence.Edges)
	}
	if !hasEdge(certEvidence.Edges, "certmanager_writes_secret", "c1/secrets/default/api-tls") {
		t.Fatalf("secret edge missing: %#v", certEvidence.Edges)
	}

	webhook := core.Observation{
		ObservedAt: time.Unix(10, 0),
		Ref: core.ResourceRef{
			ClusterID: "c1",
			Group:     "admissionregistration.k8s.io",
			Version:   "v1",
			Resource:  "validatingwebhookconfigurations",
			Kind:      "ValidatingWebhookConfiguration",
			Name:      "cert-manager-webhook",
		},
		Object: map[string]any{
			"webhooks": []any{
				map[string]any{
					"clientConfig": map[string]any{
						"service": map[string]any{"namespace": "cert-manager", "name": "cert-manager-webhook"},
					},
				},
			},
		},
	}
	webhookEvidence, err := ReferenceExtractor{}.Extract(context.Background(), webhook)
	if err != nil {
		t.Fatal(err)
	}
	if !hasEdge(webhookEvidence.Edges, "webhook_uses_service", "c1/services/cert-manager/cert-manager-webhook") {
		t.Fatalf("webhook edge missing: %#v", webhookEvidence.Edges)
	}
}

func TestReferenceExtractorUsesContextResolverForOwnerReferences(t *testing.T) {
	resolver := kubeapi.NewResolver()
	resolver.Register(kubeapi.ResourceInfo{
		Group:      "example.com",
		Version:    "v1",
		Resource:   "indices",
		Kind:       "Index",
		Namespaced: true,
	})
	obs := core.Observation{
		ObservedAt: time.Unix(10, 0),
		Ref: core.ResourceRef{
			ClusterID: "c1",
			Group:     "example.com",
			Version:   "v1",
			Resource:  "widgets",
			Kind:      "Widget",
			Namespace: "default",
			Name:      "api-widget",
			UID:       "widget-uid",
		},
		Object: map[string]any{
			"metadata": map[string]any{
				"ownerReferences": []any{
					map[string]any{
						"apiVersion": "example.com/v1",
						"kind":       "Index",
						"name":       "search",
					},
				},
			},
		},
	}

	evidence, err := ReferenceExtractor{}.Extract(WithResolver(context.Background(), resolver), obs)
	if err != nil {
		t.Fatal(err)
	}
	if !hasEdge(evidence.Edges, "owned_by", "c1/example.com/indices/default/search") {
		t.Fatalf("resolver-backed owner edge missing: %#v", evidence.Edges)
	}
}

func TestReferenceExtractorEmitsRBACBindingEdges(t *testing.T) {
	obs := core.Observation{
		ObservedAt: time.Unix(10, 0),
		Ref: core.ResourceRef{
			ClusterID: "c1",
			Group:     "rbac.authorization.k8s.io",
			Version:   "v1",
			Resource:  "rolebindings",
			Kind:      "RoleBinding",
			Namespace: "default",
			Name:      "api-reader-binding",
			UID:       "rb-uid",
		},
		Object: map[string]any{
			"roleRef": map[string]any{
				"apiGroup": "rbac.authorization.k8s.io",
				"kind":     "Role",
				"name":     "api-reader",
			},
			"subjects": []any{
				map[string]any{
					"kind":      "ServiceAccount",
					"name":      "api",
					"namespace": "default",
				},
				map[string]any{
					"kind": "Group",
					"name": "developers",
				},
			},
		},
	}

	evidence, err := ReferenceExtractor{}.Extract(context.Background(), obs)
	if err != nil {
		t.Fatal(err)
	}
	if !hasEdge(evidence.Edges, "rbac_binding_grants_role", "c1/rbac.authorization.k8s.io/roles/default/api-reader") {
		t.Fatalf("role edge missing: %#v", evidence.Edges)
	}
	if !hasEdge(evidence.Edges, "rbac_binding_binds_subject", "c1/serviceaccounts/default/api") {
		t.Fatalf("service account edge missing: %#v", evidence.Edges)
	}
	if !hasEdge(evidence.Edges, "rbac_binding_binds_subject", "c1/rbac.authorization.k8s.io/groups/developers") {
		t.Fatalf("group edge missing: %#v", evidence.Edges)
	}
}

func TestReferenceExtractorEmitsPodAndWorkloadTemplateRefs(t *testing.T) {
	pod := core.Observation{
		ObservedAt: time.Unix(10, 0),
		Ref: core.ResourceRef{
			ClusterID: "c1",
			Resource:  "pods",
			Kind:      "Pod",
			Namespace: "default",
			Name:      "api-0",
			UID:       "pod-uid",
		},
		Object: map[string]any{
			"spec": map[string]any{
				"serviceAccountName": "api",
				"imagePullSecrets":   []any{map[string]any{"name": "registry"}},
				"volumes": []any{
					map[string]any{"secret": map[string]any{"secretName": "api-secret"}},
					map[string]any{"configMap": map[string]any{"name": "api-config"}},
					map[string]any{"persistentVolumeClaim": map[string]any{"claimName": "api-data"}},
				},
				"containers": []any{
					map[string]any{
						"envFrom": []any{map[string]any{"configMapRef": map[string]any{"name": "api-config"}}},
						"env": []any{
							map[string]any{"valueFrom": map[string]any{"secretKeyRef": map[string]any{"name": "api-secret"}}},
						},
					},
				},
			},
		},
	}
	evidence, err := ReferenceExtractor{}.Extract(context.Background(), pod)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []struct {
		typ    string
		target string
	}{
		{"pod_uses_serviceaccount", "c1/serviceaccounts/default/api"},
		{"pod_uses_secret", "c1/secrets/default/registry"},
		{"pod_uses_secret", "c1/secrets/default/api-secret"},
		{"pod_uses_configmap", "c1/configmaps/default/api-config"},
		{"pod_uses_pvc", "c1/persistentvolumeclaims/default/api-data"},
	} {
		if !hasEdge(evidence.Edges, want.typ, want.target) {
			t.Fatalf("missing %s -> %s in %#v", want.typ, want.target, evidence.Edges)
		}
	}

	deploy := core.Observation{
		ObservedAt: time.Unix(10, 0),
		Ref: core.ResourceRef{
			ClusterID: "c1",
			Group:     "apps",
			Version:   "v1",
			Resource:  "deployments",
			Kind:      "Deployment",
			Namespace: "default",
			Name:      "api",
		},
		Object: map[string]any{
			"spec": map[string]any{
				"template": map[string]any{
					"spec": map[string]any{
						"serviceAccountName": "api",
						"containers": []any{
							map[string]any{"envFrom": []any{map[string]any{"secretRef": map[string]any{"name": "api-secret"}}}},
						},
					},
				},
			},
		},
	}
	evidence, err = ReferenceExtractor{}.Extract(context.Background(), deploy)
	if err != nil {
		t.Fatal(err)
	}
	if !hasEdge(evidence.Edges, "workload_template_uses_serviceaccount", "c1/serviceaccounts/default/api") {
		t.Fatalf("serviceaccount edge missing: %#v", evidence.Edges)
	}
	if !hasEdge(evidence.Edges, "workload_template_uses_secret", "c1/secrets/default/api-secret") {
		t.Fatalf("secret edge missing: %#v", evidence.Edges)
	}
}

func TestReferenceExtractorEmitsTrafficStorageAndGatewayRefs(t *testing.T) {
	cases := []struct {
		name string
		obs  core.Observation
		typ  string
		dst  string
	}{
		{
			name: "hpa",
			obs: core.Observation{
				ObservedAt: time.Unix(10, 0),
				Ref:        core.ResourceRef{ClusterID: "c1", Group: "autoscaling", Version: "v2", Resource: "horizontalpodautoscalers", Kind: "HorizontalPodAutoscaler", Namespace: "default", Name: "api"},
				Object:     map[string]any{"spec": map[string]any{"scaleTargetRef": map[string]any{"apiVersion": "apps/v1", "kind": "Deployment", "name": "api"}}},
			},
			typ: "hpa_scales_target",
			dst: "c1/apps/deployments/default/api",
		},
		{
			name: "ingress",
			obs: core.Observation{
				ObservedAt: time.Unix(10, 0),
				Ref:        core.ResourceRef{ClusterID: "c1", Group: "networking.k8s.io", Version: "v1", Resource: "ingresses", Kind: "Ingress", Namespace: "default", Name: "api"},
				Object: map[string]any{
					"spec": map[string]any{
						"rules": []any{
							map[string]any{
								"http": map[string]any{
									"paths": []any{
										map[string]any{
											"backend": map[string]any{
												"service": map[string]any{"name": "api"},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			typ: "ingress_routes_to_service",
			dst: "c1/services/default/api",
		},
		{
			name: "pvc",
			obs: core.Observation{
				ObservedAt: time.Unix(10, 0),
				Ref:        core.ResourceRef{ClusterID: "c1", Resource: "persistentvolumeclaims", Kind: "PersistentVolumeClaim", Namespace: "default", Name: "api-data"},
				Object:     map[string]any{"spec": map[string]any{"volumeName": "pv-api-data", "storageClassName": "fast"}},
			},
			typ: "pvc_bound_to_pv",
			dst: "c1/persistentvolumes/pv-api-data",
		},
		{
			name: "gateway",
			obs: core.Observation{
				ObservedAt: time.Unix(10, 0),
				Ref:        core.ResourceRef{ClusterID: "c1", Group: "gateway.networking.k8s.io", Version: "v1", Resource: "httproutes", Kind: "HTTPRoute", Namespace: "default", Name: "api"},
				Object: map[string]any{"spec": map[string]any{
					"parentRefs": []any{map[string]any{"name": "api-gateway"}},
					"rules":      []any{map[string]any{"backendRefs": []any{map[string]any{"name": "api"}}}},
				}},
			},
			typ: "gateway_route_attaches_to_parent",
			dst: "c1/gateway.networking.k8s.io/gateways/default/api-gateway",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			evidence, err := ReferenceExtractor{}.Extract(context.Background(), tc.obs)
			if err != nil {
				t.Fatal(err)
			}
			if !hasEdge(evidence.Edges, tc.typ, tc.dst) {
				t.Fatalf("missing %s -> %s in %#v", tc.typ, tc.dst, evidence.Edges)
			}
		})
	}
}

func hasEdge(edges []core.Edge, edgeType, targetID string) bool {
	for _, edge := range edges {
		if edge.Type == edgeType && edge.TargetID == targetID {
			return true
		}
	}
	return false
}

func hasFact(facts []core.Fact, key, value string) bool {
	for _, fact := range facts {
		if fact.Key == key && fact.Value == value {
			return true
		}
	}
	return false
}

func hasChange(changes []core.Change, path, value string) bool {
	for _, change := range changes {
		if change.Path == path && change.New == value {
			return true
		}
	}
	return false
}

func TestEndpointSliceExtractorEmitsServiceAndPodEdges(t *testing.T) {
	obs := core.Observation{
		ObservedAt: time.Unix(10, 0),
		Ref: core.ResourceRef{
			ClusterID: "c1",
			Group:     "discovery.k8s.io",
			Version:   "v1",
			Resource:  "endpointslices",
			Kind:      "EndpointSlice",
			Namespace: "default",
			Name:      "api-abc",
			UID:       "eps-uid",
		},
		Object: map[string]any{
			"metadata": map[string]any{
				"labels": map[string]any{"kubernetes.io/service-name": "api"},
			},
			"endpoints": []any{
				map[string]any{
					"targetRef":  map[string]any{"kind": "Pod", "name": "api-1"},
					"conditions": map[string]any{"ready": true},
				},
			},
		},
	}

	evidence, err := EndpointSliceExtractor{}.Extract(context.Background(), obs)
	if err != nil {
		t.Fatal(err)
	}
	if len(evidence.Edges) != 2 {
		t.Fatalf("edges = %d, want 2", len(evidence.Edges))
	}
	if evidence.Edges[0].Type != "endpointslice_for_service" {
		t.Fatalf("edge[0] = %#v", evidence.Edges[0])
	}
	if evidence.Edges[1].Type != "endpointslice_targets_pod" {
		t.Fatalf("edge[1] = %#v", evidence.Edges[1])
	}
}

func TestEndpointSliceExtractorEmitsReadinessAndMembershipChanges(t *testing.T) {
	obs := core.Observation{
		ObservedAt: time.Unix(10, 0),
		Ref: core.ResourceRef{
			ClusterID: "c1",
			Group:     "discovery.k8s.io",
			Version:   "v1",
			Resource:  "endpointslices",
			Kind:      "EndpointSlice",
			Namespace: "default",
			Name:      "api-abc",
			UID:       "eps-uid",
		},
		Object: map[string]any{
			"metadata": map[string]any{
				"labels": map[string]any{"kubernetes.io/service-name": "api"},
			},
			"endpoints": []any{
				map[string]any{
					"targetRef": map[string]any{"kind": "Pod", "name": "api-1"},
					"conditions": map[string]any{
						"ready":       false,
						"serving":     true,
						"terminating": false,
					},
				},
			},
		},
	}

	evidence, err := EndpointSliceExtractor{}.Extract(context.Background(), obs)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []struct {
		key   string
		value string
	}{
		{"endpoint.ready", "false"},
		{"endpoint.serving", "true"},
		{"endpoint.terminating", "false"},
	} {
		if !hasFact(evidence.Facts, want.key, want.value) {
			t.Fatalf("missing fact %s=%s in %#v", want.key, want.value, evidence.Facts)
		}
	}
	if !hasChange(evidence.Changes, "endpoints.targetRef", "Pod/api-1") ||
		!hasChange(evidence.Changes, "endpoints.api-1.conditions.ready", "false") {
		t.Fatalf("expected endpointslice changes, got %#v", evidence.Changes)
	}
}
