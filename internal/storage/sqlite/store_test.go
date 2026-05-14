package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"kube-insight/internal/core"
	"kube-insight/internal/extractor"
	"kube-insight/internal/filter"
	"kube-insight/internal/kubeapi"
	"kube-insight/internal/storage"
)

func TestStorePersistsObservationEvidence(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "kube-insight.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	obs := core.Observation{
		Type:            core.ObservationModified,
		ObservedAt:      time.Unix(10, 0),
		ResourceVersion: "42",
		Ref: core.ResourceRef{
			ClusterID: "c1",
			Group:     "",
			Version:   "v1",
			Resource:  "pods",
			Kind:      "Pod",
			Namespace: "default",
			Name:      "api-1",
			UID:       "pod-uid",
		},
		Object: map[string]any{
			"apiVersion": "v1",
			"kind":       "Pod",
			"metadata": map[string]any{
				"name":            "api-1",
				"namespace":       "default",
				"uid":             "pod-uid",
				"resourceVersion": "42",
			},
			"spec":   map[string]any{"nodeName": "node-a"},
			"status": map[string]any{"phase": "Running"},
		},
	}
	evidence := extractor.Evidence{
		Facts: []core.Fact{{
			Time:     obs.ObservedAt,
			ObjectID: "c1/pod-uid",
			Key:      "pod_status.phase",
			Value:    "Running",
			Severity: 10,
		}},
		Edges: []core.Edge{{
			Type:      "pod_on_node",
			SourceID:  "c1/pod-uid",
			TargetID:  "c1/nodes/node-a",
			ValidFrom: obs.ObservedAt,
		}},
		Changes: []core.Change{{
			Time:     obs.ObservedAt,
			ObjectID: "c1/pod-uid",
			Family:   "status",
			Path:     "status.phase",
			Op:       "replace",
			New:      "Running",
			Severity: 10,
		}},
	}

	if err := store.PutObservation(context.Background(), obs, evidence); err != nil {
		t.Fatal(err)
	}

	facts, err := store.GetFacts(context.Background(), "c1/pod-uid")
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 1 {
		t.Fatalf("facts = %d, want 1", len(facts))
	}
	if facts[0].Key != "pod_status.phase" || facts[0].Value != "Running" {
		t.Fatalf("fact = %#v", facts[0])
	}

	edges, err := store.GetEdges(context.Background(), "c1/pod-uid")
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 1 {
		t.Fatalf("edges = %d, want 1", len(edges))
	}
	if edges[0].Type != "pod_on_node" || edges[0].TargetID != "c1/nodes/node-a" {
		t.Fatalf("edge = %#v", edges[0])
	}

	bundle, err := store.Investigate(context.Background(), ObjectTarget{
		ClusterID: "c1",
		Kind:      "Pod",
		Namespace: "default",
		Name:      "api-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if bundle.Object.LogicalID != "c1/pod-uid" {
		t.Fatalf("logical id = %s", bundle.Object.LogicalID)
	}
	if bundle.Summary.Facts != 1 || bundle.Summary.Edges != 1 || bundle.Summary.Changes != 1 {
		t.Fatalf("summary = %#v", bundle.Summary)
	}
	if bundle.Latest["kind"] != "Pod" {
		t.Fatalf("latest = %#v", bundle.Latest)
	}

	topology, err := store.Topology(context.Background(), ObjectTarget{
		ClusterID: "c1",
		Kind:      "Pod",
		Namespace: "default",
		Name:      "api-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if topology.Summary.Nodes != 2 || topology.Summary.Edges != 1 {
		t.Fatalf("topology summary = %#v", topology.Summary)
	}
	if topology.Edges[0].Type != "pod_on_node" || topology.Edges[0].Direction != "out" {
		t.Fatalf("topology edge = %#v", topology.Edges[0])
	}
}

func TestListAndDeleteClusters(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "kube-insight.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if err := store.UpsertCluster(context.Background(), storage.ClusterRecord{
		Name:   "k8s-cluster-uid",
		UID:    "cluster-uid",
		Source: "dev-context https://127.0.0.1:6443",
	}); err != nil {
		t.Fatal(err)
	}
	obs := core.Observation{
		Type:       core.ObservationModified,
		ObservedAt: time.Unix(10, 0),
		Ref: core.ResourceRef{
			ClusterID: "k8s-cluster-uid",
			Version:   "v1",
			Resource:  "pods",
			Kind:      "Pod",
			Namespace: "default",
			Name:      "api",
			UID:       "pod-uid",
		},
		Object: map[string]any{
			"apiVersion": "v1",
			"kind":       "Pod",
			"metadata": map[string]any{
				"name":      "api",
				"namespace": "default",
				"uid":       "pod-uid",
			},
		},
	}
	if err := store.PutObservation(context.Background(), obs, extractor.Evidence{}); err != nil {
		t.Fatal(err)
	}

	clusters, err := store.ListClusters(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(clusters) != 1 || clusters[0].Name != "k8s-cluster-uid" || clusters[0].UID != "cluster-uid" || clusters[0].Objects != 1 || clusters[0].Versions != 1 {
		t.Fatalf("clusters = %#v", clusters)
	}

	deleted, err := store.DeleteCluster(context.Background(), "k8s-cluster-uid")
	if err != nil {
		t.Fatal(err)
	}
	if deleted.Name != "k8s-cluster-uid" || deleted.Objects != 1 || deleted.Versions != 1 {
		t.Fatalf("deleted = %#v", deleted)
	}
	clusters, err = store.ListClusters(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(clusters) != 0 {
		t.Fatalf("clusters after delete = %#v", clusters)
	}
}

func TestStorePersistsGroupedEdgeTarget(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "kube-insight.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	obs := core.Observation{
		Type:       core.ObservationModified,
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
			"apiVersion": "cert-manager.io/v1",
			"kind":       "Certificate",
			"metadata": map[string]any{
				"name":      "api-cert",
				"namespace": "default",
				"uid":       "cert-uid",
			},
		},
	}
	evidence := extractor.Evidence{
		Edges: []core.Edge{{
			Type:      "certmanager_uses_issuer",
			SourceID:  "c1/cert-uid",
			TargetID:  "c1/cert-manager.io/issuers/default/app-issuer",
			ValidFrom: obs.ObservedAt,
		}},
	}

	if err := store.PutObservation(context.Background(), obs, evidence); err != nil {
		t.Fatal(err)
	}
	edges, err := store.GetEdges(context.Background(), "c1/cert-uid")
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 1 {
		t.Fatalf("edges = %d, want 1", len(edges))
	}
	if edges[0].TargetID != "c1/cert-manager.io/issuers/default/app-issuer" {
		t.Fatalf("target id = %s", edges[0].TargetID)
	}

	topology, err := store.Topology(context.Background(), ObjectTarget{
		ClusterID: "c1",
		Kind:      "Certificate",
		Namespace: "default",
		Name:      "api-cert",
	})
	if err != nil {
		t.Fatal(err)
	}
	if topology.Summary.Nodes != 2 || topology.Edges[0].Target.LogicalID != "c1/cert-manager.io/issuers/default/app-issuer" {
		t.Fatalf("topology = %#v", topology)
	}
}

func TestStorePersistsRBACGroupSubjectWithSlash(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "kube-insight.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	obs := core.Observation{
		Type:       core.ObservationModified,
		ObservedAt: time.Unix(10, 0),
		Ref: core.ResourceRef{
			ClusterID: "c1",
			Group:     "rbac.authorization.k8s.io",
			Version:   "v1",
			Resource:  "clusterrolebindings",
			Kind:      "ClusterRoleBinding",
			Name:      "admins",
			UID:       "binding-uid",
		},
		Object: map[string]any{
			"apiVersion": "rbac.authorization.k8s.io/v1",
			"kind":       "ClusterRoleBinding",
			"metadata": map[string]any{
				"name": "admins",
				"uid":  "binding-uid",
			},
		},
	}
	groupName := "keycloakoidc_group://Rancher Admin"
	evidence := extractor.Evidence{Edges: []core.Edge{{
		Type:      "rbac_binding_binds_subject",
		SourceID:  "c1/binding-uid",
		TargetID:  "c1/rbac.authorization.k8s.io/groups/" + groupName,
		ValidFrom: obs.ObservedAt,
	}}}

	if err := store.PutObservation(context.Background(), obs, evidence); err != nil {
		t.Fatal(err)
	}
	topology, err := store.Topology(context.Background(), ObjectTarget{ClusterID: "c1", Kind: "ClusterRoleBinding", Name: "admins"})
	if err != nil {
		t.Fatal(err)
	}
	if len(topology.Edges) != 1 || topology.Edges[0].Target.Kind != "Group" || topology.Edges[0].Target.Name != groupName {
		t.Fatalf("topology = %#v", topology.Edges)
	}
}

func TestStorePersistsAndUsesDiscoveredAPIResources(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "kube-insight.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	resources := []kubeapi.ResourceInfo{{
		Group:      "example.com",
		Version:    "v1",
		Resource:   "indices",
		Kind:       "Index",
		Namespaced: true,
		Verbs:      []string{"get", "list", "watch"},
	}}
	if err := store.UpsertAPIResources(context.Background(), resources, time.Unix(10, 0)); err != nil {
		t.Fatal(err)
	}
	got, err := store.APIResources(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, resource := range got {
		if resource.Group == "example.com" && resource.Resource == "indices" && resource.Kind == "Index" && resource.Namespaced {
			found = true
		}
	}
	if !found {
		t.Fatalf("discovered resource missing: %#v", got)
	}

	obs := core.Observation{
		Type:       core.ObservationModified,
		ObservedAt: time.Unix(20, 0),
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
			"apiVersion": "example.com/v1",
			"kind":       "Widget",
			"metadata": map[string]any{
				"name":      "api-widget",
				"namespace": "default",
				"uid":       "widget-uid",
			},
		},
	}
	evidence := extractor.Evidence{Edges: []core.Edge{{
		Type:      "references_index",
		SourceID:  "c1/widget-uid",
		TargetID:  "c1/example.com/indices/default/search",
		ValidFrom: obs.ObservedAt,
	}}}
	if err := store.PutObservation(context.Background(), obs, evidence); err != nil {
		t.Fatal(err)
	}
	edges, err := store.GetEdges(context.Background(), "c1/widget-uid")
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 1 || edges[0].TargetID != "c1/example.com/indices/default/search" {
		t.Fatalf("edges = %#v", edges)
	}
	topology, err := store.Topology(context.Background(), ObjectTarget{
		ClusterID: "c1",
		Kind:      "Widget",
		Namespace: "default",
		Name:      "api-widget",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(topology.Edges) != 1 ||
		topology.Edges[0].Target.Group != "example.com" ||
		topology.Edges[0].Target.Resource != "indices" ||
		topology.Edges[0].Target.Kind != "Index" ||
		topology.Edges[0].Target.Namespace != "default" {
		t.Fatalf("topology edge did not use discovered target resource: %#v", topology.Edges)
	}
}

func TestStoreUpsertsIngestionOffsetWithEmptyNamespace(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "kube-insight.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	offset := storage.IngestionOffset{
		ClusterID: "c1",
		Resource: kubeapi.ResourceInfo{
			Group:      "",
			Version:    "v1",
			Resource:   "pods",
			Kind:       "Pod",
			Namespaced: true,
			Verbs:      []string{"list", "watch"},
		},
		ResourceVersion: "10",
		Event:           storage.OffsetEventList,
		Status:          "listed",
		At:              time.Unix(10, 0),
	}
	if err := store.UpsertIngestionOffset(context.Background(), offset); err != nil {
		t.Fatal(err)
	}
	offset.ResourceVersion = "11"
	offset.Event = storage.OffsetEventBookmark
	offset.Status = "bookmark"
	offset.At = time.Unix(11, 0)
	if err := store.UpsertIngestionOffset(context.Background(), offset); err != nil {
		t.Fatal(err)
	}

	stats, err := store.Stats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.IngestionOffsets != 1 {
		t.Fatalf("offset rows = %d, want 1", stats.IngestionOffsets)
	}
}

func TestStoreResourceHealthReportsOffsetsAndNotStartedResources(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "kube-insight.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	podResource := kubeapi.ResourceInfo{
		Version:    "v1",
		Resource:   "pods",
		Kind:       "Pod",
		Namespaced: true,
		Verbs:      []string{"list", "watch"},
	}
	if err := store.UpsertIngestionOffset(context.Background(), storage.IngestionOffset{
		ClusterID:       "c1",
		Resource:        podResource,
		ResourceVersion: "10",
		Event:           storage.OffsetEventWatch,
		Status:          "watch_error",
		Error:           "stream reset",
		At:              time.Unix(10, 0),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertAPIResources(context.Background(), []kubeapi.ResourceInfo{{
		Version:    "v1",
		Resource:   "services",
		Kind:       "Service",
		Namespaced: true,
		Verbs:      []string{"list", "watch"},
	}}, time.Unix(10, 0)); err != nil {
		t.Fatal(err)
	}

	health, err := store.ResourceHealth(context.Background(), ResourceHealthOptions{ClusterID: "c1"})
	if err != nil {
		t.Fatal(err)
	}
	if health.Summary.Resources != 2 || health.Summary.Errors != 1 || health.Summary.NotStarted != 1 {
		t.Fatalf("summary = %#v", health.Summary)
	}
	if health.Summary.Complete || len(health.Summary.Warnings) != 2 {
		t.Fatalf("summary warnings = %#v", health.Summary)
	}
	if health.ByStatus["watch_error"] != 1 || health.ByStatus["not_started"] != 1 {
		t.Fatalf("by status = %#v", health.ByStatus)
	}

	errorsOnly, err := store.ResourceHealth(context.Background(), ResourceHealthOptions{ClusterID: "c1", ErrorsOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(errorsOnly.Resources) != 1 || errorsOnly.Resources[0].Error != "stream reset" {
		t.Fatalf("errors only = %#v", errorsOnly.Resources)
	}
}

func TestStoreLatestResourceRefsExcludesDeletedAndAllowsReappearance(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "kube-insight.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ref := core.ResourceRef{ClusterID: "c1", Version: "v1", Resource: "pods", Kind: "Pod", Namespace: "default", Name: "api", UID: "pod-uid"}
	obj := map[string]any{
		"apiVersion": "v1",
		"kind":       "Pod",
		"metadata": map[string]any{
			"name":      "api",
			"namespace": "default",
			"uid":       "pod-uid",
		},
	}
	if err := store.PutObservation(context.Background(), core.Observation{Type: core.ObservationModified, ObservedAt: time.Unix(10, 0), Ref: ref, Object: obj}, extractor.Evidence{}); err != nil {
		t.Fatal(err)
	}
	info := kubeapi.ResourceInfo{Version: "v1", Resource: "pods", Kind: "Pod", Namespaced: true}
	refs, err := store.LatestResourceRefs(context.Background(), "c1", info, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 {
		t.Fatalf("refs = %#v", refs)
	}
	if err := store.PutObservation(context.Background(), core.Observation{Type: core.ObservationDeleted, ObservedAt: time.Unix(11, 0), Ref: ref, Object: obj}, extractor.Evidence{}); err != nil {
		t.Fatal(err)
	}
	refs, err = store.LatestResourceRefs(context.Background(), "c1", info, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 0 {
		t.Fatalf("deleted refs = %#v", refs)
	}
	if err := store.PutObservation(context.Background(), core.Observation{Type: core.ObservationModified, ObservedAt: time.Unix(12, 0), Ref: ref, Object: obj}, extractor.Evidence{}); err != nil {
		t.Fatal(err)
	}
	refs, err = store.LatestResourceRefs(context.Background(), "c1", info, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 {
		t.Fatalf("reappeared refs = %#v", refs)
	}
}

func TestStoreSkipsUnchangedContentVersions(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "kube-insight.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ref := core.ResourceRef{ClusterID: "c1", Version: "v1", Resource: "configmaps", Kind: "ConfigMap", Namespace: "default", Name: "settings", UID: "cm-uid"}
	obj := map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": map[string]any{
			"name":      "settings",
			"namespace": "default",
			"uid":       "cm-uid",
		},
		"data": map[string]any{"mode": "prod"},
	}
	evidence := extractor.Evidence{
		Facts: []core.Fact{{
			Time:     time.Unix(10, 0),
			ObjectID: "c1/cm-uid",
			Key:      "config.mode",
			Value:    "prod",
		}},
		Changes: []core.Change{{
			Time:     time.Unix(10, 0),
			ObjectID: "c1/cm-uid",
			Family:   "config",
			Path:     "data.mode",
			Op:       "replace",
			New:      "prod",
		}},
	}
	if err := store.PutObservation(context.Background(), core.Observation{Type: core.ObservationModified, ObservedAt: time.Unix(10, 0), ResourceVersion: "10", Ref: ref, Object: obj}, evidence); err != nil {
		t.Fatal(err)
	}
	if err := store.PutObservation(context.Background(), core.Observation{Type: core.ObservationModified, ObservedAt: time.Unix(20, 0), ResourceVersion: "11", Ref: ref, Object: obj}, evidence); err != nil {
		t.Fatal(err)
	}

	var versions, observations, unchangedObservations, observationVersions, facts, changes int
	err = store.db.QueryRow(`select count(*) from versions`).Scan(&versions)
	if err != nil {
		t.Fatal(err)
	}
	err = store.db.QueryRow(`select count(*) from object_observations`).Scan(&observations)
	if err != nil {
		t.Fatal(err)
	}
	err = store.db.QueryRow(`select count(*) from object_observations where not content_changed`).Scan(&unchangedObservations)
	if err != nil {
		t.Fatal(err)
	}
	err = store.db.QueryRow(`select count(distinct version_id) from object_observations`).Scan(&observationVersions)
	if err != nil {
		t.Fatal(err)
	}
	err = store.db.QueryRow(`select count(*) from object_facts`).Scan(&facts)
	if err != nil {
		t.Fatal(err)
	}
	err = store.db.QueryRow(`select count(*) from object_changes`).Scan(&changes)
	if err != nil {
		t.Fatal(err)
	}
	if versions != 1 || facts != 1 || changes != 1 {
		t.Fatalf("versions/facts/changes = %d/%d/%d, want 1/1/1", versions, facts, changes)
	}
	if observations != 2 || unchangedObservations != 1 || observationVersions != 1 {
		t.Fatalf("observations/unchanged/version_refs = %d/%d/%d, want 2/1/1", observations, unchangedObservations, observationVersions)
	}
	var lastSeen, latestObserved int64
	if err := store.db.QueryRow(`select last_seen_at from objects where uid = 'cm-uid'`).Scan(&lastSeen); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow(`select observed_at from latest_index`).Scan(&latestObserved); err != nil {
		t.Fatal(err)
	}
	if lastSeen != time.Unix(20, 0).UnixMilli() || latestObserved != time.Unix(20, 0).UnixMilli() {
		t.Fatalf("lastSeen/latestObserved = %d/%d", lastSeen, latestObserved)
	}
}

func TestStoreStatsCountsFilterRedactionMetrics(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "kube-insight.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	obs := core.Observation{
		Type:       core.ObservationModified,
		ObservedAt: time.Unix(10, 0),
		Ref: core.ResourceRef{
			ClusterID: "c1",
			Version:   "v1",
			Resource:  "secrets",
			Kind:      "Secret",
			Namespace: "default",
			Name:      "db",
			UID:       "secret-uid",
		},
		Object: map[string]any{
			"apiVersion": "v1",
			"kind":       "Secret",
			"metadata": map[string]any{
				"name":      "db",
				"namespace": "default",
				"uid":       "secret-uid",
			},
		},
	}
	decisions := []filter.Decision{{
		Outcome: filter.KeepModified,
		Reason:  "secret_payload_removed",
		Meta: map[string]any{
			"filter":               "secret_redaction_filter",
			"redactedFields":       2,
			"removedFields":        2,
			"secretPayloadRemoved": true,
		},
	}}
	if err := store.PutObservation(context.Background(), obs, extractor.Evidence{}); err != nil {
		t.Fatal(err)
	}
	if err := store.PutFilterDecisions(context.Background(), obs, decisions); err != nil {
		t.Fatal(err)
	}
	stats, err := store.Stats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.RedactedFields != 2 || stats.RemovedFields != 2 || stats.SecretPayloadRemovals != 1 || stats.SecretPayloadViolations != 0 {
		t.Fatalf("stats = %#v", stats)
	}
}

func TestStorePersistsOnlyUsefulFilterDecisions(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "kube-insight.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	obs := core.Observation{
		Type:       core.ObservationModified,
		ObservedAt: time.Unix(10, 0),
		Ref: core.ResourceRef{
			ClusterID: "c1",
			Version:   "v1",
			Resource:  "configmaps",
			Kind:      "ConfigMap",
			Namespace: "default",
			Name:      "settings",
			UID:       "cm-uid",
		},
	}
	decisions := []filter.Decision{
		{Outcome: filter.Keep, Reason: "not_secret", Meta: map[string]any{"filter": "secret_redaction_filter"}},
		{Outcome: filter.KeepModified, Reason: "managed_fields_removed", Meta: map[string]any{"filter": "metadata_normalization_filter"}},
		{Outcome: filter.KeepModified, Reason: "secret_payload_removed", Meta: map[string]any{"filter": "secret_redaction_filter", "redactedFields": 1}},
		{Outcome: filter.DiscardResource, Reason: "lease_resource", Meta: map[string]any{"filter": "lease_skip_filter"}},
	}
	if err := store.PutFilterDecisions(context.Background(), obs, decisions); err != nil {
		t.Fatal(err)
	}
	var rows int
	if err := store.db.QueryRow(`select count(*) from filter_decisions`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 2 {
		t.Fatalf("filter decision rows = %d, want 2", rows)
	}
}

func TestStoreStatsCountsProcessingProfiles(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "kube-insight.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	resources := []kubeapi.ResourceInfo{
		{Version: "v1", Resource: "pods", Kind: "Pod", Namespaced: true, Verbs: []string{"list", "watch"}},
		{Version: "v1", Resource: "secrets", Kind: "Secret", Namespaced: true, Verbs: []string{"list", "watch"}},
		{Group: "coordination.k8s.io", Version: "v1", Resource: "leases", Kind: "Lease", Namespaced: true, Verbs: []string{"list", "watch"}},
	}
	if err := store.UpsertAPIResources(context.Background(), resources, time.Unix(10, 0)); err != nil {
		t.Fatal(err)
	}
	podObs := core.Observation{
		Type:       core.ObservationModified,
		ObservedAt: time.Unix(11, 0),
		Ref: core.ResourceRef{
			ClusterID: "c1",
			Version:   "v1",
			Resource:  "pods",
			Kind:      "Pod",
			Namespace: "default",
			Name:      "api",
			UID:       "pod-uid",
		},
		Object: map[string]any{
			"apiVersion": "v1",
			"kind":       "Pod",
			"metadata": map[string]any{
				"name":      "api",
				"namespace": "default",
				"uid":       "pod-uid",
			},
		},
	}
	if err := store.PutObservation(context.Background(), podObs, extractor.Evidence{}); err != nil {
		t.Fatal(err)
	}

	stats, err := store.Stats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.ProcessingProfiles != 3 || stats.DisabledProcessingProfiles != 1 {
		t.Fatalf("profile counts = %#v", stats)
	}
	if stats.APIResourcesByProfile["pod_fast_path"] != 1 || stats.APIResourcesByProfile["secret_metadata_only"] != 1 || stats.APIResourcesByProfile["lease_skip_or_downsample"] != 1 {
		t.Fatalf("api resources by profile = %#v", stats.APIResourcesByProfile)
	}
	if stats.StoredVersionsByProfile["pod_fast_path"] != 1 {
		t.Fatalf("stored versions by profile = %#v", stats.StoredVersionsByProfile)
	}
}
