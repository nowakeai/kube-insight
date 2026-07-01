package collector

import (
	"context"
	"errors"
	"testing"
	"time"

	"kube-insight/internal/core"
	"kube-insight/internal/extractor"
	"kube-insight/internal/ingest"
	"kube-insight/internal/resourceprofile"
	"kube-insight/internal/storage"
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

func TestWatchStreamRunTimeoutUsesRotationAndDeadline(t *testing.T) {
	if got := watchStreamRunTimeout(time.Time{}, 5*time.Second); got != 5*time.Second {
		t.Fatalf("rotation timeout = %v", got)
	}
	if got := watchStreamRunTimeout(time.Now().Add(50*time.Millisecond), time.Second); got <= 0 || got > time.Second {
		t.Fatalf("deadline-bounded timeout = %v", got)
	}
	if got := watchStreamRunTimeout(time.Time{}, 0); got != 0 {
		t.Fatalf("unbounded timeout = %v", got)
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

func TestUpsertConfiguredClusterUsesExplicitClusterID(t *testing.T) {
	store := &clusterMetadataStore{}
	if err := upsertConfiguredCluster(context.Background(), store, "k8s-explicit", ""); err != nil {
		t.Fatal(err)
	}
	if store.cluster.Name != "k8s-explicit" {
		t.Fatalf("cluster metadata = %#v", store.cluster)
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

func TestAcquireWatchStreamSlotRefreshesQueuedWorker(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	sem := make(chan struct{}, 1)
	sem <- struct{}{}
	queued := 0
	refreshed := 0

	release, ok := acquireWatchStreamSlot(ctx, sem, func() {
		queued++
	}, func() {
		refreshed++
		if refreshed == 2 {
			<-sem
		}
	}, time.Millisecond)
	if !ok {
		t.Fatal("expected slot")
	}
	defer release()
	if queued != 1 {
		t.Fatalf("queued callbacks = %d", queued)
	}
	if refreshed < 2 {
		t.Fatalf("refresh callbacks = %d", refreshed)
	}
}

func TestWatchStreamSchedulerSelectsHighestScore(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	scores := map[string]int{"low": 10, "high": 100}
	scheduler := newWatchStreamScheduler(1, func(key string, _ time.Duration) int {
		return scores[key]
	})
	firstRelease, ok := scheduler.acquire(ctx, "first", nil, nil, 0)
	if !ok {
		t.Fatal("expected first slot")
	}
	defer firstRelease()

	acquired := make(chan string, 2)
	go func() {
		release, ok := scheduler.acquire(ctx, "low", nil, nil, 0)
		if ok {
			defer release()
			acquired <- "low"
		}
	}()
	go func() {
		release, ok := scheduler.acquire(ctx, "high", nil, nil, 0)
		if ok {
			defer release()
			acquired <- "high"
		}
	}()
	time.Sleep(20 * time.Millisecond)
	firstRelease()
	firstRelease = func() {}

	select {
	case got := <-acquired:
		if got != "high" {
			t.Fatalf("first acquired = %q", got)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func TestWatchScheduleScoreCases(t *testing.T) {
	rules := []resourceprofile.Rule{
		{Resources: []string{"pods"}, Profile: resourceprofile.Profile{Priority: "high"}},
		{Resources: []string{"jobs"}, Profile: resourceprofile.Profile{Priority: "low"}},
		{Resources: []string{"events"}, Disabled: true},
	}
	pods := Resource{Name: "pods", Version: "v1", Resource: "pods", Kind: "Pod", Namespaced: true}
	services := Resource{Name: "services", Version: "v1", Resource: "services", Kind: "Service", Namespaced: true}
	jobs := Resource{Name: "jobs", Version: "v1", Resource: "jobs", Kind: "Job", Namespaced: true}
	events := Resource{Name: "events", Version: "v1", Resource: "events", Kind: "Event", Namespaced: true}

	cases := []struct {
		name      string
		resource  Resource
		summary   *WatchSummary
		queuedFor time.Duration
		want      int
	}{
		{
			name:     "high priority base",
			resource: pods,
			want:     100,
		},
		{
			name:     "normal priority base",
			resource: services,
			want:     50,
		},
		{
			name:     "low priority base",
			resource: jobs,
			want:     10,
		},
		{
			name:      "queued low priority catches normal after waiting",
			resource:  jobs,
			queuedFor: 3 * time.Minute,
			want:      70,
		},
		{
			name:     "recent events and bookmarks boost active resource",
			resource: services,
			summary:  &WatchSummary{Events: 8, Bookmarks: 5},
			want:     100,
		},
		{
			name:     "large object count applies bounded cost penalty",
			resource: pods,
			summary:  &WatchSummary{LatestObjects: 10000},
			want:     60,
		},
		{
			name:     "watch errors and retries apply bounded error penalty",
			resource: pods,
			summary:  &WatchSummary{WatchErrors: 3, Retries: 4},
			want:     20,
		},
		{
			name:     "disabled resource is not schedulable",
			resource: events,
			want:     -100000,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := watchScheduleScore(tc.resource, tc.summary, rules, tc.queuedFor)
			if got != tc.want {
				t.Fatalf("score = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestWatchStreamSchedulerQueuedWaitCanOvertakeLowerBasePriority(t *testing.T) {
	now := time.Now()
	scheduler := newWatchStreamScheduler(1, func(key string, queuedFor time.Duration) int {
		switch key {
		case "old-low":
			return 10 + min(80, int(queuedFor/time.Minute)*20)
		case "new-normal":
			return 50
		default:
			return 0
		}
	})
	oldLow := scheduler.register("old-low")
	newNormal := scheduler.register("new-normal")

	scheduler.mu.Lock()
	oldLow.queuedAt = now.Add(-3 * time.Minute)
	newNormal.queuedAt = now
	best := scheduler.bestLocked(now)
	scheduler.mu.Unlock()

	if best == nil || best.key != "old-low" {
		t.Fatalf("best request = %#v, want old-low", best)
	}
}

func TestWatchStreamSchedulerReleaseSelectsNextHighestScore(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	scores := map[string]int{"first": 90, "normal": 50, "active": 100, "errored": 20}
	scheduler := newWatchStreamScheduler(1, func(key string, _ time.Duration) int {
		return scores[key]
	})
	firstRelease, ok := scheduler.acquire(ctx, "first", nil, nil, 0)
	if !ok {
		t.Fatal("expected first slot")
	}
	defer firstRelease()

	acquired := make(chan string, 3)
	for _, key := range []string{"normal", "active", "errored"} {
		key := key
		go func() {
			release, ok := scheduler.acquire(ctx, key, nil, nil, 0)
			if ok {
				defer release()
				acquired <- key
			}
		}()
	}
	time.Sleep(20 * time.Millisecond)
	firstRelease()
	firstRelease = func() {}

	select {
	case got := <-acquired:
		if got != "active" {
			t.Fatalf("first acquired after release = %q", got)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
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

type clusterMetadataStore struct {
	cluster storage.ClusterRecord
}

func (s *clusterMetadataStore) PutObservation(context.Context, core.Observation, extractor.Evidence) error {
	return nil
}

func (s *clusterMetadataStore) GetFacts(context.Context, string) ([]core.Fact, error) {
	return nil, nil
}

func (s *clusterMetadataStore) GetEdges(context.Context, string) ([]core.Edge, error) {
	return nil, nil
}

func (s *clusterMetadataStore) UpsertCluster(_ context.Context, cluster storage.ClusterRecord) error {
	s.cluster = cluster
	return nil
}
