package collector

import (
	"context"
	"errors"
	"testing"
	"time"

	"kube-insight/internal/ingest"
)

func TestAggregateWatchResourcesSummaryUsesInitialListWorkers(t *testing.T) {
	pod := Resource{Name: "pods", Version: "v1", Resource: "pods", Kind: "Pod", Namespaced: true, Verbs: []string{"list", "watch"}}
	service := Resource{Name: "services", Version: "v1", Resource: "services", Kind: "Service", Namespaced: true, Verbs: []string{"list", "watch"}}
	summary := WatchResourcesSummary{
		Context:       "ctx",
		ClusterID:     "cluster",
		Resources:     2,
		Concurrency:   1,
		MaxQueueDepth: 1,
		Workers: []WatchWorkerSummary{
			{
				Resource: pod,
				Summary: &WatchSummary{
					Resource:          pod,
					Listed:            2,
					Stored:            2,
					ReconciledDeleted: 1,
					Ingest:            ingest.Summary{Observations: 2, StoredObservations: 2, Facts: 1},
				},
			},
			{
				Resource:    service,
				Queued:      true,
				QueueWaitMS: 12,
				Summary: &WatchSummary{
					Resource: service,
					Listed:   1,
					Stored:   1,
					Ingest:   ingest.Summary{Observations: 1, StoredObservations: 1, Edges: 1},
				},
			},
		},
	}

	aggregateWatchResourcesSummary(&summary)

	if summary.Completed != 2 || summary.Errors != 0 {
		t.Fatalf("completed/errors = %d/%d", summary.Completed, summary.Errors)
	}
	if summary.Listed != 3 || summary.Stored != 3 || summary.ReconciledDeleted != 1 {
		t.Fatalf("aggregate counts listed=%d stored=%d deleted=%d", summary.Listed, summary.Stored, summary.ReconciledDeleted)
	}
	if summary.Ingest.Observations != 3 || summary.Ingest.StoredObservations != 3 || summary.Ingest.Facts != 1 || summary.Ingest.Edges != 1 {
		t.Fatalf("ingest aggregate = %#v", summary.Ingest)
	}
	if summary.BackpressureEvents != 1 || summary.QueueWaitMS != 12 || len(summary.ResourceQueue) != 2 {
		t.Fatalf("queue aggregate backpressure=%d wait=%f queue=%#v", summary.BackpressureEvents, summary.QueueWaitMS, summary.ResourceQueue)
	}
}

func TestMergeWatchSummaryPreservesInitialListAndAddsStream(t *testing.T) {
	base := &WatchSummary{
		Listed:          5,
		Stored:          5,
		ResourceVersion: "10",
		Ingest:          ingest.Summary{Observations: 5, StoredObservations: 5},
	}
	stream := WatchSummary{
		Events:          2,
		Stored:          2,
		Bookmarks:       1,
		ResourceVersion: "12",
		Ingest:          ingest.Summary{Observations: 2, StoredObservations: 2, Changes: 1},
	}

	mergeWatchSummary(base, stream)

	if base.Listed != 5 || base.Events != 2 || base.Stored != 7 || base.Bookmarks != 1 {
		t.Fatalf("merged counts = %#v", base)
	}
	if base.ResourceVersion != "12" {
		t.Fatalf("resourceVersion = %q", base.ResourceVersion)
	}
	if base.Ingest.Observations != 7 || base.Ingest.StoredObservations != 7 || base.Ingest.Changes != 1 {
		t.Fatalf("merged ingest = %#v", base.Ingest)
	}
}

func TestWatchTimeoutSecondsUsesRemainingDeadline(t *testing.T) {
	deadline := time.Now().Add(1500 * time.Millisecond)
	seconds := watchTimeoutSeconds(deadline, 30*time.Second)
	if seconds == nil || *seconds < 1 || *seconds > 2 {
		t.Fatalf("timeout seconds = %v", seconds)
	}
	if watchTimeoutSeconds(time.Time{}, 0) != nil {
		t.Fatal("zero timeout should not set watch timeout seconds")
	}
}

func TestGracefulWatchStop(t *testing.T) {
	if !isGracefulWatchStop(errWatchClosed, time.Now().Add(time.Second)) {
		t.Fatal("bounded watch close should be graceful")
	}
	if isGracefulWatchStop(errWatchClosed, time.Time{}) {
		t.Fatal("unbounded watch close should be retried")
	}
	if !isGracefulWatchStop(context.Canceled, time.Time{}) {
		t.Fatal("context cancellation should be graceful")
	}
	if isGracefulWatchStop(errors.New("stream reset"), time.Now().Add(time.Second)) {
		t.Fatal("real stream error should not be graceful")
	}
}

func TestTransientWatchStreamErrorClassification(t *testing.T) {
	transient := []error{
		errWatchClosed,
		errors.New(`an error on the server ("unable to decode an event from the watch stream: stream error: stream ID 441; INTERNAL_ERROR; received from peer") has prevented the request from succeeding`),
		errors.New("http2: client connection lost"),
		errors.New("connection reset by peer"),
	}
	for _, err := range transient {
		if !isTransientWatchStreamError(err) {
			t.Fatalf("error should be transient: %v", err)
		}
	}
	if isTransientWatchStreamError(errors.New("forbidden: User cannot watch pods")) {
		t.Fatal("authorization errors should not be transient")
	}
	if isTransientWatchStreamError(errors.New("resource version too old")) {
		t.Fatal("expired resource versions are handled by relist logic")
	}
}

func TestNeedsResourceDiscoveryForExactCLIResources(t *testing.T) {
	if !needsResourceDiscovery([]Resource{{Name: "pods"}}) {
		t.Fatal("bare exact resource should need discovery metadata")
	}
	full := Resource{
		Name:       "pods",
		Version:    "v1",
		Resource:   "pods",
		Kind:       "Pod",
		Namespaced: true,
		Verbs:      []string{"list", "watch"},
	}
	if needsResourceDiscovery([]Resource{full}) {
		t.Fatal("fully resolved resource should not need discovery")
	}
}

func TestEnrichWatchResourcesMatchesQualifiedNames(t *testing.T) {
	base := []Resource{{Name: "apiservices.apiregistration.k8s.io"}}
	metadata := []Resource{{
		Name:       "apiservices.apiregistration.k8s.io",
		Group:      "apiregistration.k8s.io",
		Version:    "v1",
		Resource:   "apiservices",
		Kind:       "APIService",
		Namespaced: false,
		Verbs:      []string{"list", "watch"},
	}}

	enriched := enrichWatchResources(base, metadata)

	if enriched[0].Resource != "apiservices" || enriched[0].Kind != "APIService" || enriched[0].Namespaced {
		t.Fatalf("enriched resource = %#v", enriched[0])
	}
}
