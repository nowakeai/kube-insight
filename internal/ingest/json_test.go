package ingest

import (
	"context"
	"strings"
	"testing"
	"time"

	appconfig "kube-insight/internal/config"
	"kube-insight/internal/core"
	"kube-insight/internal/filter"
	"kube-insight/internal/kubeapi"
	"kube-insight/internal/storage"
)

func TestPipelineIngestsListJSON(t *testing.T) {
	input := `{
	  "apiVersion": "v1",
	  "kind": "List",
	  "items": [
	    {
	      "apiVersion": "v1",
	      "kind": "Pod",
	      "metadata": {"name": "api-1", "namespace": "default", "uid": "pod-uid", "resourceVersion": "10"},
	      "spec": {"nodeName": "node-a"},
	      "status": {"phase": "Running"}
	    },
	    {
	      "apiVersion": "v1",
	      "kind": "Secret",
	      "metadata": {"name": "db", "namespace": "default", "uid": "secret-uid"},
	      "data": {"password": "secret"}
	    },
	    {
	      "apiVersion": "coordination.k8s.io/v1",
	      "kind": "Lease",
	      "metadata": {"name": "node-a", "namespace": "kube-node-lease", "uid": "lease-uid"}
	    }
	  ]
	}`

	store := storage.NewMemoryStore()
	pipeline := DefaultPipeline(store)
	pipeline.Now = func() time.Time { return time.Unix(100, 0) }

	summary, err := pipeline.IngestJSON(context.Background(), []byte(input))
	if err != nil {
		t.Fatal(err)
	}
	if summary.Observations != 3 {
		t.Fatalf("observations = %d, want 3", summary.Observations)
	}
	if summary.StoredObservations != 2 {
		t.Fatalf("stored observations = %d, want 2", summary.StoredObservations)
	}
	if summary.ModifiedObservations != 2 {
		t.Fatalf("modified observations = %d, want 2", summary.ModifiedObservations)
	}
	if summary.DiscardedResources != 1 {
		t.Fatalf("discarded resources = %d, want 1", summary.DiscardedResources)
	}
	if summary.Facts != 2 {
		t.Fatalf("facts = %d, want 2", summary.Facts)
	}
	if summary.Edges != 1 {
		t.Fatalf("edges = %d, want 1", summary.Edges)
	}
	if _, ok := store.Observations[1].Object["data"]; ok {
		t.Fatal("secret payload was stored")
	}
	if len(store.RawLatest) != 2 {
		t.Fatalf("raw latest observations = %d, want 2", len(store.RawLatest))
	}
	rawSecret := store.RawLatest[1].Object
	if rawSecret["data"].(map[string]any)["password"] != "<redacted>" {
		t.Fatalf("raw latest secret should keep key with redacted value: %#v", rawSecret)
	}
	if len(store.FilterDecisions) != 11 {
		t.Fatalf("filter decisions = %d, want 11", len(store.FilterDecisions))
	}
	if !hasFilterDecision(store.FilterDecisions, "resource_version_removed", filter.KeepModified) {
		t.Fatalf("resourceVersion normalization decision missing: %#v", store.FilterDecisions)
	}
	if !hasFilterDecision(store.FilterDecisions, "secret_payload_removed", filter.KeepModified) {
		t.Fatalf("secret decision missing: %#v", store.FilterDecisions)
	}
	if !hasFilterDecision(store.FilterDecisions, "lease_skipped", filter.DiscardResource) {
		t.Fatalf("lease decision missing: %#v", store.FilterDecisions)
	}
}

func hasFilterDecision(decisions []storage.FilterDecisionRecord, reason string, outcome filter.Outcome) bool {
	for _, decision := range decisions {
		if decision.Decision.Reason == reason && decision.Decision.Outcome == outcome {
			return true
		}
	}
	return false
}

func TestPipelineKeepsDeletedObservationsDespiteDiscardChangeFilter(t *testing.T) {
	input := `{
	  "type": "DELETED",
	  "object": {
	    "apiVersion": "v1",
	    "kind": "Pod",
	    "metadata": {"name": "api-1", "namespace": "default", "uid": "pod-uid", "resourceVersion": "10"}
	  }
	}`

	store := storage.NewMemoryStore()
	pipeline := DefaultPipeline(store)
	pipeline.Filters = filter.Chain{discardChangeFilter{}}
	pipeline.FilterChains = FilterChains{}
	pipeline.Now = func() time.Time { return time.Unix(100, 0) }

	summary, err := pipeline.IngestJSON(context.Background(), []byte(input))
	if err != nil {
		t.Fatal(err)
	}
	if summary.StoredObservations != 1 {
		t.Fatalf("stored observations = %d, want 1", summary.StoredObservations)
	}
	if summary.DiscardedChanges != 0 {
		t.Fatalf("discarded changes = %d, want 0", summary.DiscardedChanges)
	}
	if len(store.Observations) != 1 || store.Observations[0].Type != core.ObservationDeleted {
		t.Fatalf("stored observation = %#v", store.Observations)
	}
	if len(store.FilterDecisions) != 1 || store.FilterDecisions[0].Decision.Outcome != filter.DiscardChange {
		t.Fatalf("filter decisions = %#v", store.FilterDecisions)
	}
	if summary.Changes != 1 {
		t.Fatalf("changes = %d, want 1", summary.Changes)
	}
}

func TestPipelineUsesConfiguredFilters(t *testing.T) {
	on := true
	off := false
	input := `{
	  "apiVersion": "v1",
	  "kind": "Pod",
	  "metadata": {"name": "api-1", "namespace": "default", "uid": "pod-uid", "resourceVersion": "10"},
	  "status": {"phase": "Running"}
	}`
	store := storage.NewMemoryStore()
	cfg := appconfig.Default()
	cfg.Processing.FilterChains = map[string][]string{"default": {"resource_version", "managed_fields"}}
	cfg.Processing.Filters = map[string]appconfig.ProcessingFilterConfig{
		"resource_version": {Type: "builtin", Enabled: &off, Action: "keep_modified", RemovePaths: []string{"/metadata/resourceVersion"}},
		"managed_fields":   {Type: "builtin", Enabled: &on, Action: "keep_modified", RemovePaths: []string{"/metadata/managedFields"}},
	}
	pipeline, err := PipelineForAppConfig(store, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pipeline.IngestJSON(context.Background(), []byte(input)); err != nil {
		t.Fatal(err)
	}
	metadata := store.Observations[0].Object["metadata"].(map[string]any)
	if metadata["resourceVersion"] != "10" {
		t.Fatalf("resourceVersion should be retained when filter is disabled: %#v", metadata)
	}
}

func TestPipelineUsesConfiguredRemovePaths(t *testing.T) {
	on := true
	input := `{
	  "apiVersion": "v1",
	  "kind": "Pod",
	  "metadata": {"name": "api-1", "namespace": "default", "uid": "pod-uid"},
	  "status": {"conditions": [{"type": "Ready", "status": "True", "lastHeartbeatTime": "2026-05-14T00:00:00Z"}]}
	}`
	store := storage.NewMemoryStore()
	cfg := appconfig.Default()
	cfg.Processing.FilterChains = map[string][]string{"default": {"status_condition_timestamps"}}
	cfg.Processing.Filters = map[string]appconfig.ProcessingFilterConfig{
		"status_condition_timestamps": {Type: "builtin", Enabled: &on, Action: "keep_modified", RemovePaths: []string{"/status/conditions/*/lastHeartbeatTime"}},
	}
	pipeline, err := PipelineForAppConfig(store, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pipeline.IngestJSON(context.Background(), []byte(input)); err != nil {
		t.Fatal(err)
	}
	condition := store.Observations[0].Object["status"].(map[string]any)["conditions"].([]any)[0].(map[string]any)
	if _, ok := condition["lastHeartbeatTime"]; ok {
		t.Fatalf("configured remove path did not run: %#v", condition)
	}
}

func TestPipelineForAppConfigUsesProfileFilterChain(t *testing.T) {
	input := `{
	  "apiVersion": "wgpolicyk8s.io/v1alpha2",
	  "kind": "PolicyReport",
	  "metadata": {"name": "policy", "namespace": "default", "uid": "policy-uid", "resourceVersion": "10"}
	}`
	cfg := appconfig.Default()
	cfg.ResourceProfiles.Rules = []appconfig.ResourceProfileRuleConfig{
		{Name: "policy_keep", Resources: []string{"policyreports.wgpolicyk8s.io"}, FilterChain: "none", ExtractorSet: "none"},
	}

	store := storage.NewMemoryStore()
	pipeline, err := PipelineForAppConfig(store, cfg)
	if err != nil {
		t.Fatal(err)
	}
	pipeline.Now = func() time.Time { return time.Unix(100, 0) }

	summary, err := pipeline.IngestJSON(context.Background(), []byte(input))
	if err != nil {
		t.Fatal(err)
	}
	if summary.StoredObservations != 1 || summary.DiscardedResources != 0 {
		t.Fatalf("summary = %#v", summary)
	}
	if len(store.FilterDecisions) != 0 {
		t.Fatalf("filter decisions = %#v", store.FilterDecisions)
	}
}

func TestDefaultEventProfileRemovesSeriesChurn(t *testing.T) {
	input := `{
	  "apiVersion": "events.k8s.io/v1",
	  "kind": "Event",
	  "metadata": {"name": "pod-warning.123", "namespace": "default", "uid": "event-uid", "resourceVersion": "10"},
	  "deprecatedCount": 3,
	  "deprecatedLastTimestamp": "2026-05-15T00:00:00Z",
	  "eventTime": "2026-05-15T00:00:00Z",
	  "series": {"count": 3, "lastObservedTime": "2026-05-15T00:00:00Z"},
	  "reason": "PolicyViolation",
	  "type": "Warning",
	  "note": "policy violation"
	}`

	store := storage.NewMemoryStore()
	pipeline, err := PipelineForAppConfig(store, appconfig.Default())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pipeline.IngestJSON(context.Background(), []byte(input)); err != nil {
		t.Fatal(err)
	}
	event := store.Observations[0].Object
	for _, key := range []string{"deprecatedCount", "deprecatedLastTimestamp", "eventTime", "series"} {
		if _, ok := event[key]; ok {
			t.Fatalf("event churn field %q should be removed: %#v", key, event)
		}
	}
}

func TestDefaultProfileNormalizesConditionSetChurn(t *testing.T) {
	input := `{
	  "apiVersion": "v1",
	  "kind": "Node",
	  "metadata": {"name": "node-a", "uid": "node-uid", "resourceVersion": "10", "generation": 3},
	  "status": {"conditions": [
	    {"type": "Ready", "status": "True", "lastHeartbeatTime": "2026-05-15T00:00:00Z", "lastTransitionTime": "2026-05-15T00:00:00Z"},
	    {"type": "DiskPressure", "status": "False", "lastUpdateTime": "2026-05-15T00:00:00Z"}
	  ]}
	}`

	store := storage.NewMemoryStore()
	pipeline, err := PipelineForAppConfig(store, appconfig.Default())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pipeline.IngestJSON(context.Background(), []byte(input)); err != nil {
		t.Fatal(err)
	}
	obj := store.Observations[0].Object
	metadata := obj["metadata"].(map[string]any)
	if _, ok := metadata["generation"]; ok {
		t.Fatalf("metadata.generation should be removed: %#v", metadata)
	}
	conditions := obj["status"].(map[string]any)["conditions"].([]any)
	first := conditions[0].(map[string]any)
	second := conditions[1].(map[string]any)
	if first["type"] != "DiskPressure" || second["type"] != "Ready" {
		t.Fatalf("conditions should be sorted by type: %#v", conditions)
	}
	for _, condition := range []map[string]any{first, second} {
		for _, field := range []string{"lastHeartbeatTime", "lastTransitionTime", "lastUpdateTime"} {
			if _, ok := condition[field]; ok {
				t.Fatalf("condition timestamp %q should be removed: %#v", field, condition)
			}
		}
	}
}

func TestDefaultProfileNormalizesClusterAutoscalerStatusConfigMap(t *testing.T) {
	input := `{
	  "apiVersion": "v1",
	  "kind": "ConfigMap",
	  "metadata": {
	    "name": "cluster-autoscaler-status",
	    "namespace": "kube-system",
	    "uid": "ca-status",
	    "resourceVersion": "10",
	    "annotations": {"cluster-autoscaler.kubernetes.io/last-updated": "2026-05-15 00:00:00 +0000 UTC"}
	  },
	  "data": {
	    "status": "time: 2026-05-15 00:00:00 +0000 UTC\nautoscalerStatus: Running\nnodeGroups:\n- name: z\n  health:\n    status: Healthy\n    lastProbeTime: \"2026-05-15T00:00:00Z\"\n- name: a\n  health:\n    status: Healthy\n    lastProbeTime: \"2026-05-15T00:00:00Z\"\n"
	  }
	}`

	store := storage.NewMemoryStore()
	pipeline, err := PipelineForAppConfig(store, appconfig.Default())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pipeline.IngestJSON(context.Background(), []byte(input)); err != nil {
		t.Fatal(err)
	}
	obj := store.Observations[0].Object
	metadata := obj["metadata"].(map[string]any)
	if _, ok := metadata["annotations"]; ok {
		t.Fatalf("autoscaler last-updated annotation should be removed: %#v", metadata)
	}
	status := obj["data"].(map[string]any)["status"].(string)
	if strings.Contains(status, "lastProbeTime") || strings.Contains(status, "time:") {
		t.Fatalf("autoscaler probe timestamps should be removed: %s", status)
	}
	if strings.Index(status, "name: a") > strings.Index(status, "name: z") {
		t.Fatalf("nodeGroups should be sorted by name: %s", status)
	}
}

func TestDefaultProfileNormalizesGKEWebhookHeartbeatConfigMap(t *testing.T) {
	input := `{
	  "apiVersion": "v1",
	  "kind": "ConfigMap",
	  "metadata": {
	    "name": "gke-common-webhook-heartbeat",
	    "namespace": "kube-system",
	    "uid": "gke-heartbeat",
	    "resourceVersion": "10"
	  },
	  "data": {
	    "1778818667837367803": "{\"hostname\":\"node-b\",\"version\":\"34.45.0-gke.3\"}",
	    "1778818686612998534": "{\"hostname\":\"node-a\",\"version\":\"34.45.0-gke.3\"}"
	  }
	}`

	store := storage.NewMemoryStore()
	pipeline, err := PipelineForAppConfig(store, appconfig.Default())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pipeline.IngestJSON(context.Background(), []byte(input)); err != nil {
		t.Fatal(err)
	}
	data := store.Observations[0].Object["data"].(map[string]any)
	if _, ok := data["1778818667837367803"]; ok {
		t.Fatalf("timestamp heartbeat key should be removed: %#v", data)
	}
	if data["node-a"] != "34.45.0-gke.3" || data["node-b"] != "34.45.0-gke.3" {
		t.Fatalf("hostname/version heartbeat evidence should be retained: %#v", data)
	}
}

func TestPipelineForAppConfigUsesExtractorResourceScope(t *testing.T) {
	input := `{
	  "apiVersion": "v1",
	  "kind": "Event",
	  "metadata": {"name": "event", "namespace": "default", "uid": "event-uid", "resourceVersion": "10"},
	  "reason": "Scheduled",
	  "message": "pod scheduled"
	}`
	cfg := appconfig.Default()

	store := storage.NewMemoryStore()
	pipeline, err := PipelineForAppConfig(store, cfg)
	if err != nil {
		t.Fatal(err)
	}
	pipeline.Now = func() time.Time { return time.Unix(100, 0) }

	summary, err := pipeline.IngestJSON(context.Background(), []byte(input))
	if err != nil {
		t.Fatal(err)
	}
	if summary.Facts != 0 || len(store.Facts) != 0 {
		t.Fatalf("facts = %#v summary=%#v", store.Facts, summary)
	}
}

func TestDecodeObjectsRejectsNonObjectItems(t *testing.T) {
	_, err := decodeObjects([]byte(`{"items":["bad"]}`))
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "items[0]") {
		t.Fatalf("error = %v", err)
	}
}

func TestPipelineRegistersCRDResourceMappingBeforeObjects(t *testing.T) {
	input := `{
	  "apiVersion": "v1",
	  "kind": "List",
	  "items": [
	    {
	      "apiVersion": "apiextensions.k8s.io/v1",
	      "kind": "CustomResourceDefinition",
	      "metadata": {"name": "indices.example.com", "uid": "crd-indices"},
	      "spec": {
	        "group": "example.com",
	        "names": {"kind": "Index", "plural": "indices"},
	        "scope": "Namespaced",
	        "versions": [{"name": "v1", "served": true, "storage": true}]
	      }
	    },
	    {
	      "apiVersion": "example.com/v1",
	      "kind": "Index",
	      "metadata": {"name": "search", "namespace": "default", "uid": "idx-search"}
	    }
	  ]
	}`

	store := storage.NewMemoryStore()
	pipeline := DefaultPipeline(store)
	pipeline.Now = func() time.Time { return time.Unix(100, 0) }

	if _, err := pipeline.IngestJSON(context.Background(), []byte(input)); err != nil {
		t.Fatal(err)
	}
	if len(store.Observations) != 2 {
		t.Fatalf("observations = %d, want 2", len(store.Observations))
	}
	if got := store.Observations[1].Ref.Resource; got != "indices" {
		t.Fatalf("custom resource = %q, want indices", got)
	}
}

func TestResourceNameKnownKubernetesKinds(t *testing.T) {
	resolver := kubeapi.NewResolver()
	tests := map[string]string{
		"ConfigMap":                "configmaps",
		"CronJob":                  "cronjobs",
		"CustomResourceDefinition": "customresourcedefinitions",
		"EndpointSlice":            "endpointslices",
		"HorizontalPodAutoscaler":  "horizontalpodautoscalers",
		"Ingress":                  "ingresses",
		"NetworkPolicy":            "networkpolicies",
		"PersistentVolumeClaim":    "persistentvolumeclaims",
		"ServiceAccount":           "serviceaccounts",
	}
	for kind, want := range tests {
		info, _ := resolver.ResolveGVK("", "", kind)
		if got := info.Resource; got != want {
			t.Fatalf("resource for kind %q = %q, want %q", kind, got, want)
		}
	}
}

type discardChangeFilter struct{}

func (discardChangeFilter) Name() string { return "test_discard_change_filter" }

func (discardChangeFilter) Apply(_ context.Context, obs core.Observation) (core.Observation, filter.Decision, error) {
	return obs, filter.Decision{Outcome: filter.DiscardChange, Reason: "test_change_discard"}, nil
}
