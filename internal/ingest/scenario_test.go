package ingest

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"kube-insight/internal/core"
	"kube-insight/internal/storage"
	"kube-insight/internal/storage/sqlite"
)

func TestPipelineScenarioPodRestartOOMReadiness(t *testing.T) {
	input := `{
	  "apiVersion": "v1",
	  "kind": "List",
	  "items": [
	    {
	      "apiVersion": "v1",
	      "kind": "Pod",
	      "metadata": {"name": "api-1", "namespace": "default", "uid": "pod-uid", "resourceVersion": "10"},
	      "status": {
	        "phase": "Running",
	        "conditions": [{"type": "Ready", "status": "True"}],
	        "containerStatuses": [{"name": "api", "restartCount": 0}]
	      }
	    },
	    {
	      "apiVersion": "v1",
	      "kind": "Pod",
	      "metadata": {"name": "api-1", "namespace": "default", "uid": "pod-uid", "resourceVersion": "11"},
	      "status": {
	        "phase": "Running",
	        "conditions": [{"type": "Ready", "status": "False"}],
	        "containerStatuses": [{
	          "name": "api",
	          "restartCount": 3,
	          "lastState": {"terminated": {"reason": "OOMKilled"}},
	          "state": {"waiting": {"reason": "CrashLoopBackOff"}}
	        }]
	      }
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
	if summary.StoredObservations != 2 {
		t.Fatalf("stored = %d, want 2", summary.StoredObservations)
	}
	if !hasStoredFact(store.Facts, "pod_status.ready", "False") ||
		!hasStoredFact(store.Facts, "pod_status.last_reason", "OOMKilled") ||
		!hasStoredFact(store.Facts, "pod_status.restart_count", "3") {
		t.Fatalf("pod scenario facts missing: %#v", store.Facts)
	}
	if !hasStoredChange(store.Changes, "status.containerStatuses.api.lastState.reason", "OOMKilled") ||
		!hasStoredChange(store.Changes, "status.conditions.Ready", "False") {
		t.Fatalf("pod scenario changes missing: %#v", store.Changes)
	}
}

func TestSQLiteScenarioPodRestartOOMReadinessInvestigation(t *testing.T) {
	input := `{
	  "apiVersion": "v1",
	  "kind": "List",
	  "items": [
	    {
	      "apiVersion": "v1",
	      "kind": "Pod",
	      "metadata": {"name": "api-1", "namespace": "default", "uid": "pod-uid", "resourceVersion": "10"},
	      "status": {
	        "phase": "Running",
	        "conditions": [{"type": "Ready", "status": "True"}],
	        "containerStatuses": [{"name": "api", "restartCount": 0}]
	      }
	    },
	    {
	      "apiVersion": "v1",
	      "kind": "Pod",
	      "metadata": {"name": "api-1", "namespace": "default", "uid": "pod-uid", "resourceVersion": "11"},
	      "status": {
	        "phase": "Running",
	        "conditions": [{"type": "Ready", "status": "False"}],
	        "containerStatuses": [{
	          "name": "api",
	          "restartCount": 3,
	          "lastState": {"terminated": {"reason": "OOMKilled"}},
	          "state": {"waiting": {"reason": "CrashLoopBackOff"}}
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
	bundle, err := store.Investigate(context.Background(), sqlite.ObjectTarget{
		ClusterID: "local",
		Kind:      "Pod",
		Namespace: "default",
		Name:      "api-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hasStoredFact(bundle.Facts, "pod_status.last_reason", "OOMKilled") ||
		!hasStoredFact(bundle.Facts, "pod_status.restart_count", "3") {
		t.Fatalf("pod investigation facts missing: %#v", bundle.Facts)
	}
	if !hasStoredChange(bundle.Changes, "status.containerStatuses.api.lastState.reason", "OOMKilled") ||
		!hasStoredChange(bundle.Changes, "status.conditions.Ready", "False") {
		t.Fatalf("pod investigation changes missing: %#v", bundle.Changes)
	}
}

func TestPipelineScenarioNodePressureAndEndpointSliceReadiness(t *testing.T) {
	input := `{
	  "apiVersion": "v1",
	  "kind": "List",
	  "items": [
	    {
	      "apiVersion": "v1",
	      "kind": "Node",
	      "metadata": {"name": "node-a", "uid": "node-uid", "resourceVersion": "20"},
	      "status": {
	        "conditions": [
	          {"type": "Ready", "status": "False"},
	          {"type": "MemoryPressure", "status": "True"},
	          {"type": "DiskPressure", "status": "True"},
	          {"type": "PIDPressure", "status": "False"}
	        ]
	      }
	    },
	    {
	      "apiVersion": "discovery.k8s.io/v1",
	      "kind": "EndpointSlice",
	      "metadata": {
	        "name": "api-abc",
	        "namespace": "default",
	        "uid": "eps-uid",
	        "resourceVersion": "21",
	        "labels": {"kubernetes.io/service-name": "api"}
	      },
	      "endpoints": [{
	        "targetRef": {"kind": "Pod", "name": "api-1"},
	        "conditions": {"ready": false, "serving": true, "terminating": false}
	      }]
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
	if summary.StoredObservations != 2 {
		t.Fatalf("stored = %d, want 2", summary.StoredObservations)
	}
	for _, want := range []struct {
		key   string
		value string
	}{
		{"node_condition.MemoryPressure", "True"},
		{"node_condition.DiskPressure", "True"},
		{"endpoint.ready", "false"},
		{"endpoint.serving", "true"},
	} {
		if !hasStoredFact(store.Facts, want.key, want.value) {
			t.Fatalf("missing fact %s=%s in %#v", want.key, want.value, store.Facts)
		}
	}
	if !hasStoredEdge(store.Edges, "endpointslice_targets_pod", "local/pods/default/api-1") {
		t.Fatalf("endpointslice target edge missing: %#v", store.Edges)
	}
	if !hasStoredChange(store.Changes, "endpoints.api-1.conditions.ready", "false") {
		t.Fatalf("endpointslice readiness change missing: %#v", store.Changes)
	}
}

func TestSQLiteScenarioNodeAndEndpointSliceInvestigation(t *testing.T) {
	input := `{
	  "apiVersion": "v1",
	  "kind": "List",
	  "items": [
	    {
	      "apiVersion": "v1",
	      "kind": "Node",
	      "metadata": {"name": "node-a", "uid": "node-uid", "resourceVersion": "20"},
	      "status": {
	        "conditions": [
	          {"type": "Ready", "status": "False"},
	          {"type": "MemoryPressure", "status": "True"},
	          {"type": "DiskPressure", "status": "True"},
	          {"type": "PIDPressure", "status": "False"}
	        ]
	      }
	    },
	    {
	      "apiVersion": "discovery.k8s.io/v1",
	      "kind": "EndpointSlice",
	      "metadata": {
	        "name": "api-abc",
	        "namespace": "default",
	        "uid": "eps-uid",
	        "resourceVersion": "21",
	        "labels": {"kubernetes.io/service-name": "api"}
	      },
	      "endpoints": [{
	        "targetRef": {"kind": "Pod", "name": "api-1"},
	        "conditions": {"ready": false, "serving": true, "terminating": false}
	      }]
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
	node, err := store.Investigate(context.Background(), sqlite.ObjectTarget{
		ClusterID: "local",
		Kind:      "Node",
		Name:      "node-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hasStoredFact(node.Facts, "node_condition.MemoryPressure", "True") ||
		!hasStoredChange(node.Changes, "status.conditions.MemoryPressure", "True") {
		t.Fatalf("node investigation evidence missing: facts=%#v changes=%#v", node.Facts, node.Changes)
	}
	endpointSlice, err := store.Investigate(context.Background(), sqlite.ObjectTarget{
		ClusterID: "local",
		Kind:      "EndpointSlice",
		Namespace: "default",
		Name:      "api-abc",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hasStoredFact(endpointSlice.Facts, "endpoint.ready", "false") ||
		!hasStoredChange(endpointSlice.Changes, "endpoints.api-1.conditions.ready", "false") {
		t.Fatalf("endpointslice investigation evidence missing: facts=%#v changes=%#v", endpointSlice.Facts, endpointSlice.Changes)
	}
	topology, err := store.Topology(context.Background(), sqlite.ObjectTarget{
		ClusterID: "local",
		Kind:      "EndpointSlice",
		Namespace: "default",
		Name:      "api-abc",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hasTopologyEdge(topology.Edges, "endpointslice_targets_pod", "Pod", "default", "api-1") {
		t.Fatalf("endpointslice topology edge missing: %#v", topology.Edges)
	}
}

func TestPipelineScenarioEventMirrorAndRollup(t *testing.T) {
	input := eventScenarioInput()
	store := storage.NewMemoryStore()
	pipeline := DefaultPipeline(store)
	pipeline.Now = func() time.Time { return time.Unix(100, 0) }

	summary, err := pipeline.IngestJSON(context.Background(), []byte(input))
	if err != nil {
		t.Fatal(err)
	}
	if summary.StoredObservations != 3 {
		t.Fatalf("stored = %d, want 3", summary.StoredObservations)
	}
	for _, want := range []struct {
		key   string
		value string
	}{
		{"k8s_event.reason", "BackOff"},
		{"k8s_event.type", "Warning"},
		{"k8s_event.count", "5"},
	} {
		if !hasStoredFact(store.Facts, want.key, want.value) {
			t.Fatalf("missing event fact %s=%s in %#v", want.key, want.value, store.Facts)
		}
	}
	if !hasStoredChange(store.Changes, "count", "5") {
		t.Fatalf("event rollup count change missing: %#v", store.Changes)
	}
	if !hasStoredEdge(store.Edges, "event_regarding_object", "local/pods/default/api-1") {
		t.Fatalf("event regarding edge missing: %#v", store.Edges)
	}
}

func TestSQLiteScenarioEventMirrorInvestigation(t *testing.T) {
	store := openScenarioSQLite(t)
	defer store.Close()
	pipeline := DefaultPipeline(store)
	pipeline.Now = func() time.Time { return time.Unix(100, 0) }

	if _, err := pipeline.IngestJSON(context.Background(), []byte(eventScenarioInput())); err != nil {
		t.Fatal(err)
	}
	bundle, err := store.Investigate(context.Background(), sqlite.ObjectTarget{
		ClusterID: "local",
		Kind:      "Event",
		Namespace: "default",
		Name:      "api-backoff",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hasStoredFact(bundle.Facts, "k8s_event.count", "5") ||
		!hasStoredFact(bundle.Facts, "k8s_event.reason", "BackOff") {
		t.Fatalf("event investigation facts missing: %#v", bundle.Facts)
	}
	if !hasStoredChange(bundle.Changes, "count", "5") {
		t.Fatalf("event investigation count change missing: %#v", bundle.Changes)
	}
	topology, err := store.Topology(context.Background(), sqlite.ObjectTarget{
		ClusterID: "local",
		Kind:      "Event",
		Namespace: "default",
		Name:      "api-backoff",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hasTopologyEdge(topology.Edges, "event_regarding_object", "Pod", "default", "api-1") {
		t.Fatalf("event topology edge missing: %#v", topology.Edges)
	}
	stats, err := store.Stats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.EventMessageFingerprints != 1 || stats.EventFactRows < 8 {
		t.Fatalf("event stats = %#v", stats)
	}
}

func TestSQLiteGeneratedIncidentBundleFlow(t *testing.T) {
	input := `{
	  "apiVersion": "v1",
	  "kind": "List",
	  "items": [
	    {
	      "apiVersion": "v1",
	      "kind": "Service",
	      "metadata": {"name": "api", "namespace": "default", "uid": "svc-uid", "resourceVersion": "40"},
	      "spec": {"selector": {"app": "api"}}
	    },
	    {
	      "apiVersion": "v1",
	      "kind": "Node",
	      "metadata": {"name": "node-a", "uid": "node-uid", "resourceVersion": "41"},
	      "status": {
	        "conditions": [
	          {"type": "Ready", "status": "False"},
	          {"type": "MemoryPressure", "status": "True"}
	        ]
	      }
	    },
	    {
	      "apiVersion": "v1",
	      "kind": "Pod",
	      "metadata": {"name": "api-1", "namespace": "default", "uid": "pod-uid", "resourceVersion": "42"},
	      "spec": {"nodeName": "node-a"},
	      "status": {
	        "phase": "Running",
	        "conditions": [{"type": "Ready", "status": "True"}],
	        "containerStatuses": [{"name": "api", "restartCount": 0}]
	      }
	    },
	    {
	      "apiVersion": "v1",
	      "kind": "Pod",
	      "metadata": {"name": "api-1", "namespace": "default", "uid": "pod-uid", "resourceVersion": "43"},
	      "spec": {"nodeName": "node-a"},
	      "status": {
	        "phase": "Running",
	        "conditions": [{"type": "Ready", "status": "False"}],
	        "containerStatuses": [{
	          "name": "api",
	          "restartCount": 2,
	          "lastState": {"terminated": {"reason": "OOMKilled"}},
	          "state": {"waiting": {"reason": "CrashLoopBackOff"}}
	        }]
	      }
	    },
	    {
	      "apiVersion": "discovery.k8s.io/v1",
	      "kind": "EndpointSlice",
	      "metadata": {
	        "name": "api-abc",
	        "namespace": "default",
	        "uid": "eps-uid",
	        "resourceVersion": "44",
	        "labels": {"kubernetes.io/service-name": "api"}
	      },
	      "endpoints": [{
	        "targetRef": {"kind": "Pod", "name": "api-1"},
	        "conditions": {"ready": false, "serving": true, "terminating": false}
	      }]
	    },
	    {
	      "apiVersion": "events.k8s.io/v1",
	      "kind": "Event",
	      "metadata": {"name": "api-backoff", "namespace": "default", "uid": "event-uid", "resourceVersion": "45"},
	      "reason": "BackOff",
	      "type": "Warning",
	      "message": "Back-off restarting failed container api in pod api-1",
	      "count": 4,
	      "regarding": {"apiVersion": "v1", "kind": "Pod", "namespace": "default", "name": "api-1", "uid": "pod-uid"}
	    }
	  ]
	}`
	store := openScenarioSQLite(t)
	defer store.Close()
	pipeline := DefaultPipeline(store)
	pipeline.Now = func() time.Time { return time.Unix(100, 0) }

	summary, err := pipeline.IngestJSON(context.Background(), []byte(input))
	if err != nil {
		t.Fatal(err)
	}
	if summary.StoredObservations != 6 {
		t.Fatalf("stored = %d, want 6", summary.StoredObservations)
	}
	pod, err := store.Investigate(context.Background(), sqlite.ObjectTarget{
		ClusterID: "local",
		Kind:      "Pod",
		Namespace: "default",
		Name:      "api-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hasStoredFact(pod.Facts, "pod_status.last_reason", "OOMKilled") ||
		!hasStoredFact(pod.Facts, "pod_status.restart_count", "2") ||
		!hasStoredChange(pod.Changes, "status.conditions.Ready", "False") {
		t.Fatalf("pod bundle missing incident evidence: facts=%#v changes=%#v", pod.Facts, pod.Changes)
	}
	podTopology, err := store.Topology(context.Background(), sqlite.ObjectTarget{
		ClusterID: "local",
		Kind:      "Pod",
		Namespace: "default",
		Name:      "api-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []struct {
		edgeType string
		kind     string
		ns       string
		name     string
	}{
		{"pod_on_node", "Node", "", "node-a"},
		{"endpointslice_targets_pod", "Pod", "default", "api-1"},
		{"event_regarding_object", "Pod", "default", "api-1"},
	} {
		if !hasTopologyEdge(podTopology.Edges, want.edgeType, want.kind, want.ns, want.name) &&
			!hasTopologySourceEdge(podTopology.Edges, want.edgeType, want.kind, want.ns, want.name) {
			t.Fatalf("pod topology missing %s/%s/%s/%s in %#v", want.edgeType, want.kind, want.ns, want.name, podTopology.Edges)
		}
	}
	node, err := store.Investigate(context.Background(), sqlite.ObjectTarget{
		ClusterID: "local",
		Kind:      "Node",
		Name:      "node-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hasStoredFact(node.Facts, "node_condition.MemoryPressure", "True") {
		t.Fatalf("node pressure evidence missing: %#v", node.Facts)
	}
	event, err := store.Investigate(context.Background(), sqlite.ObjectTarget{
		ClusterID: "local",
		Kind:      "Event",
		Namespace: "default",
		Name:      "api-backoff",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hasStoredFact(event.Facts, "k8s_event.count", "4") ||
		!hasStoredChange(event.Changes, "count", "4") {
		t.Fatalf("event rollup evidence missing: facts=%#v changes=%#v", event.Facts, event.Changes)
	}
	service, err := store.InvestigateService(context.Background(), sqlite.ObjectTarget{
		ClusterID: "local",
		Namespace: "default",
		Name:      "api",
	})
	if err != nil {
		t.Fatal(err)
	}
	if service.Summary.EndpointSlices != 1 || service.Summary.Pods != 1 || service.Summary.Nodes != 1 || service.Summary.Events != 1 {
		t.Fatalf("service investigation summary = %#v", service.Summary)
	}
	if !serviceBundleHasFact(service, "pod_status.last_reason", "OOMKilled") ||
		!serviceBundleHasFact(service, "node_condition.MemoryPressure", "True") ||
		!serviceBundleHasFact(service, "k8s_event.count", "4") ||
		!serviceBundleHasChange(service, "endpoints.api-1.conditions.ready", "false") {
		t.Fatalf("service investigation missing cross-resource evidence: %#v", service)
	}
	if service.Service.Summary.Rank != 1 {
		t.Fatalf("service investigation ranking missing: %#v", service.Service.Summary)
	}
	if service.Service.Summary.Versions == 0 || len(service.Service.Versions) == 0 || service.Service.Versions[0].Document["kind"] != "Service" {
		t.Fatalf("service version evidence missing: %#v", service.Service.Versions)
	}
	if len(service.Objects) == 0 || service.Objects[0].Summary.Rank != 2 || service.Objects[0].Summary.EvidenceScore == 0 {
		t.Fatalf("ranked service objects missing: %#v", service.Objects)
	}
	if service.Objects[0].Summary.Versions == 0 || len(service.Objects[0].Versions) == 0 {
		t.Fatalf("ranked object version evidence missing: %#v", service.Objects[0])
	}
	podEvidence := serviceBundleObject(service, "Pod", "api-1")
	if podEvidence == nil || len(podEvidence.Versions) < 2 || len(podEvidence.VersionDiffs) == 0 {
		t.Fatalf("pod reconstructed versions/diffs missing: %#v", podEvidence)
	}
	if !versionDiffHasPath(podEvidence.VersionDiffs, "/status/containerStatuses") {
		t.Fatalf("pod version diff missing container status change: %#v", podEvidence.VersionDiffs)
	}
	windowed, err := store.InvestigateServiceWithOptions(context.Background(), sqlite.ObjectTarget{
		ClusterID: "local",
		Namespace: "default",
		Name:      "api",
	}, sqlite.InvestigationOptions{
		From: time.Unix(90, 0),
		To:   time.Unix(99, 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	if windowed.Summary.Objects != 1 || windowed.Summary.Facts != 0 || windowed.Summary.Changes != 0 || len(windowed.Topology) != 0 {
		t.Fatalf("windowed service investigation should exclude earlier evidence: %#v", windowed)
	}
}

func eventScenarioInput() string {
	return `{
	  "apiVersion": "v1",
	  "kind": "List",
	  "items": [
	    {
	      "apiVersion": "v1",
	      "kind": "Pod",
	      "metadata": {"name": "api-1", "namespace": "default", "uid": "pod-uid", "resourceVersion": "30"}
	    },
	    {
	      "apiVersion": "events.k8s.io/v1",
	      "kind": "Event",
	      "metadata": {"name": "api-backoff", "namespace": "default", "uid": "event-uid", "resourceVersion": "31"},
	      "reason": "BackOff",
	      "type": "Warning",
	      "message": "Back-off restarting failed container api in pod api-1",
	      "count": 1,
	      "regarding": {"apiVersion": "v1", "kind": "Pod", "namespace": "default", "name": "api-1", "uid": "pod-uid"}
	    },
	    {
	      "apiVersion": "events.k8s.io/v1",
	      "kind": "Event",
	      "metadata": {"name": "api-backoff", "namespace": "default", "uid": "event-uid", "resourceVersion": "32"},
	      "reason": "BackOff",
	      "type": "Warning",
	      "message": "Back-off restarting failed container api in pod api-1",
	      "count": 5,
	      "regarding": {"apiVersion": "v1", "kind": "Pod", "namespace": "default", "name": "api-1", "uid": "pod-uid"}
	    }
	  ]
	}`
}

func openScenarioSQLite(t *testing.T) *sqlite.Store {
	t.Helper()
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "kube-insight.db"))
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func hasStoredFact(facts []core.Fact, key, value string) bool {
	for _, fact := range facts {
		if fact.Key == key && fact.Value == value {
			return true
		}
	}
	return false
}

func hasStoredEdge(edges []core.Edge, edgeType, targetID string) bool {
	for _, edge := range edges {
		if edge.Type == edgeType && edge.TargetID == targetID {
			return true
		}
	}
	return false
}

func hasStoredChange(changes []core.Change, path, value string) bool {
	for _, change := range changes {
		if change.Path == path && change.New == value {
			return true
		}
	}
	return false
}

func hasTopologyEdge(edges []sqlite.TopologyEdge, edgeType, targetKind, targetNamespace, targetName string) bool {
	for _, edge := range edges {
		if edge.Type == edgeType &&
			edge.Target.Kind == targetKind &&
			edge.Target.Namespace == targetNamespace &&
			edge.Target.Name == targetName {
			return true
		}
	}
	return false
}

func hasTopologySourceEdge(edges []sqlite.TopologyEdge, edgeType, sourceKind, sourceNamespace, sourceName string) bool {
	for _, edge := range edges {
		if edge.Type == edgeType &&
			edge.Source.Kind == sourceKind &&
			edge.Source.Namespace == sourceNamespace &&
			edge.Source.Name == sourceName {
			return true
		}
	}
	return false
}

func serviceBundleHasFact(in sqlite.ServiceInvestigation, key, value string) bool {
	if hasStoredFact(in.Service.Facts, key, value) {
		return true
	}
	for _, bundle := range in.Objects {
		if hasStoredFact(bundle.Facts, key, value) {
			return true
		}
	}
	return false
}

func serviceBundleHasChange(in sqlite.ServiceInvestigation, path, value string) bool {
	if hasStoredChange(in.Service.Changes, path, value) {
		return true
	}
	for _, bundle := range in.Objects {
		if hasStoredChange(bundle.Changes, path, value) {
			return true
		}
	}
	return false
}

func serviceBundleObject(in sqlite.ServiceInvestigation, kind, name string) *sqlite.EvidenceBundle {
	if in.Service.Object.Kind == kind && in.Service.Object.Name == name {
		return &in.Service
	}
	for i := range in.Objects {
		if in.Objects[i].Object.Kind == kind && in.Objects[i].Object.Name == name {
			return &in.Objects[i]
		}
	}
	return nil
}

func versionDiffHasPath(diffs []sqlite.VersionDiff, path string) bool {
	for _, diff := range diffs {
		for _, change := range diff.Changes {
			if change.Path == path {
				return true
			}
		}
	}
	return false
}
