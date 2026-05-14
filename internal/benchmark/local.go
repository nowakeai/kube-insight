package benchmark

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"kube-insight/internal/collector"
	"kube-insight/internal/ingest"
	"kube-insight/internal/logging"
	"kube-insight/internal/samplegen"
	"kube-insight/internal/storage/sqlite"
)

type Options struct {
	FixturesDir string
	OutputDir   string
	DBPath      string
	Clusters    int
	Copies      int
	QueryRuns   int
}

type WatchOptions struct {
	Context           string
	DBPath            string
	Resources         []collector.Resource
	DiscoverResources bool
	Namespace         string
	Duration          time.Duration
	Concurrency       int
	MaxRetries        int
	MaxEvents         int
}

type Report struct {
	Dataset                        string                           `json:"dataset"`
	DurationSeconds                float64                          `json:"duration_seconds"`
	Objects                        int64                            `json:"objects"`
	Versions                       int64                            `json:"versions"`
	DeletedObjects                 int64                            `json:"deleted_objects"`
	UnknownVisibilityObjects       int64                            `json:"unknown_visibility_objects"`
	RelistConfirmedDeletes         int64                            `json:"relist_confirmed_deletes"`
	RawBytes                       int64                            `json:"raw_bytes"`
	StoredBytes                    int64                            `json:"stored_bytes"`
	IndexBytes                     int64                            `json:"index_bytes"`
	BlobBytes                      int64                            `json:"blob_bytes"`
	FactBytes                      int64                            `json:"fact_bytes"`
	EdgeBytes                      int64                            `json:"edge_bytes"`
	CompressionRatio               float64                          `json:"compression_ratio"`
	WatchEventsPerSecond           float64                          `json:"watch_events_per_second"`
	StoredVersionsPerSecond        float64                          `json:"stored_versions_per_second"`
	StoredVersionsByProfile        map[string]int64                 `json:"stored_versions_by_profile"`
	APIResourcesByProfile          map[string]int64                 `json:"api_resources_by_profile"`
	ProcessingProfiles             int64                            `json:"processing_profiles"`
	DisabledProcessingProfiles     int64                            `json:"disabled_processing_profiles"`
	WatchRestarts                  int64                            `json:"watch_restarts"`
	StaleRelistCount               int64                            `json:"stale_relist_count"`
	PeriodicRelistCount            int64                            `json:"periodic_relist_count"`
	OffsetLagMS                    int64                            `json:"offset_lag_ms"`
	MaxQueueDepth                  int                              `json:"max_queue_depth"`
	BackpressureEvents             int                              `json:"backpressure_events"`
	QueueWaitMS                    float64                          `json:"queue_wait_ms"`
	WatchResourceQueue             []collector.WatchResourceQueue   `json:"watch_resource_queue,omitempty"`
	FilterKept                     int64                            `json:"filter_kept"`
	FilterModified                 int64                            `json:"filter_modified"`
	FilterDiscardedChange          int64                            `json:"filter_discarded_change"`
	FilterDiscardedResource        int64                            `json:"filter_discarded_resource"`
	PodHeartbeatDiscardRatio       float64                          `json:"pod_heartbeat_discard_ratio"`
	PodMissedTransitionCount       int64                            `json:"pod_missed_transition_count"`
	PodStatusChangeRows            int64                            `json:"pod_status_change_rows"`
	NodeConditionFactRows          int64                            `json:"node_condition_fact_rows"`
	NodeRetainedBytes              int64                            `json:"node_retained_bytes"`
	EventRollupRatio               float64                          `json:"event_rollup_ratio"`
	EventMessageFingerprints       int64                            `json:"event_message_fingerprints"`
	EventFactRows                  int64                            `json:"event_fact_rows"`
	EndpointSliceEdgeRowsPerChange float64                          `json:"endpointslice_edge_rows_per_change"`
	EndpointSliceReadinessFactRows int64                            `json:"endpointslice_readiness_fact_rows"`
	LeaseSkippedCount              int64                            `json:"lease_skipped_count"`
	RedactedFields                 int64                            `json:"redacted_fields"`
	SecretPayloadViolations        int64                            `json:"secret_payload_violations"`
	VacuumMS                       float64                          `json:"vacuum_ms"`
	BytesReclaimed                 int64                            `json:"bytes_reclaimed"`
	WriteMS                        float64                          `json:"write_ms"`
	LatestLookupMS                 float64                          `json:"latest_lookup_ms"`
	LatestLookupP50MS              float64                          `json:"latest_lookup_p50_ms"`
	LatestLookupP95MS              float64                          `json:"latest_lookup_p95_ms"`
	HistoricalGetMS                float64                          `json:"historical_get_ms"`
	HistoricalGetP50MS             float64                          `json:"historical_get_p50_ms"`
	HistoricalGetP95MS             float64                          `json:"historical_get_p95_ms"`
	ServiceInvestigationMS         float64                          `json:"service_investigation_ms"`
	ServiceInvestigationP50MS      float64                          `json:"service_investigation_p50_ms"`
	ServiceInvestigationP95MS      float64                          `json:"service_investigation_p95_ms"`
	ServiceInvestigationVersions   int                              `json:"service_investigation_versions"`
	ServiceInvestigationDiffs      int                              `json:"service_investigation_diffs"`
	FactQueryMS                    float64                          `json:"fact_query_ms"`
	FactQueryP50MS                 float64                          `json:"fact_query_p50_ms"`
	FactQueryP95MS                 float64                          `json:"fact_query_p95_ms"`
	TopologyQueryMS                float64                          `json:"topology_query_ms"`
	TopologyQueryP50MS             float64                          `json:"topology_query_p50_ms"`
	TopologyQueryP95MS             float64                          `json:"topology_query_p95_ms"`
	QueryRuns                      int                              `json:"query_runs"`
	Generated                      samplegen.Manifest               `json:"generated"`
	Ingest                         ingest.Summary                   `json:"ingest"`
	Counts                         sqlite.Stats                     `json:"counts"`
	Watch                          *collector.WatchResourcesSummary `json:"watch,omitempty"`
}

func RunLocal(ctx context.Context, opts Options) (Report, error) {
	if opts.OutputDir == "" {
		opts.OutputDir = filepath.Join("testdata", "generated", "benchmark-samples")
	}
	if opts.DBPath == "" {
		opts.DBPath = filepath.Join(opts.OutputDir, "kube-insight-benchmark.db")
	}
	if opts.QueryRuns <= 0 {
		opts.QueryRuns = 25
	}
	if err := cleanBenchmarkOutput(opts.OutputDir); err != nil {
		return Report{}, err
	}
	if err := os.MkdirAll(filepath.Dir(opts.DBPath), 0o755); err != nil {
		return Report{}, err
	}
	removeSQLiteFiles(opts.DBPath)

	start := time.Now()
	manifest, err := samplegen.Generate(ctx, samplegen.Options{
		FixturesDir: opts.FixturesDir,
		OutputDir:   opts.OutputDir,
		Clusters:    opts.Clusters,
		Copies:      opts.Copies,
	})
	if err != nil {
		return Report{}, err
	}

	store, err := sqlite.Open(opts.DBPath)
	if err != nil {
		return Report{}, err
	}
	ingestStart := time.Now()
	summary, err := ingestDir(ctx, store, opts.OutputDir)
	writeMS := millisSince(ingestStart)
	if err != nil {
		_ = store.Close()
		return Report{}, err
	}
	counts, err := store.Stats(ctx)
	if err != nil {
		_ = store.Close()
		return Report{}, err
	}
	latencies := measureQueryLatencies(ctx, store, opts.QueryRuns)
	if err := store.Close(); err != nil {
		return Report{}, err
	}

	rawSampleBytes, err := jsonBytes(opts.OutputDir)
	if err != nil {
		return Report{}, err
	}
	storedBytes := sqliteBytes(opts.DBPath)
	duration := time.Since(start).Seconds()
	report := Report{
		Dataset:                        "generated",
		DurationSeconds:                duration,
		Objects:                        counts.Objects,
		Versions:                       counts.Versions,
		DeletedObjects:                 counts.DeletedObjects,
		UnknownVisibilityObjects:       0,
		RawBytes:                       rawSampleBytes,
		StoredBytes:                    storedBytes,
		IndexBytes:                     counts.IndexBytes,
		BlobBytes:                      counts.BlobBytes,
		FactBytes:                      counts.FactBytes,
		EdgeBytes:                      counts.EdgeBytes,
		StoredVersionsByProfile:        counts.StoredVersionsByProfile,
		APIResourcesByProfile:          counts.APIResourcesByProfile,
		ProcessingProfiles:             counts.ProcessingProfiles,
		DisabledProcessingProfiles:     counts.DisabledProcessingProfiles,
		FilterKept:                     int64(summary.StoredObservations),
		FilterModified:                 int64(summary.ModifiedObservations),
		FilterDiscardedChange:          int64(summary.DiscardedChanges),
		FilterDiscardedResource:        int64(summary.DiscardedResources),
		PodStatusChangeRows:            counts.PodStatusChangeRows,
		NodeConditionFactRows:          counts.NodeConditionFactRows,
		EventMessageFingerprints:       counts.EventMessageFingerprints,
		EventFactRows:                  counts.EventFactRows,
		EndpointSliceReadinessFactRows: counts.EndpointSliceReadinessFactRows,
		RedactedFields:                 counts.RedactedFields,
		SecretPayloadViolations:        counts.SecretPayloadViolations,
		WriteMS:                        writeMS,
		LatestLookupMS:                 latencies.latestLookup.P50MS,
		LatestLookupP50MS:              latencies.latestLookup.P50MS,
		LatestLookupP95MS:              latencies.latestLookup.P95MS,
		HistoricalGetMS:                latencies.historicalGet.P50MS,
		HistoricalGetP50MS:             latencies.historicalGet.P50MS,
		HistoricalGetP95MS:             latencies.historicalGet.P95MS,
		ServiceInvestigationMS:         latencies.investigation.P50MS,
		ServiceInvestigationP50MS:      latencies.investigation.P50MS,
		ServiceInvestigationP95MS:      latencies.investigation.P95MS,
		ServiceInvestigationVersions:   latencies.investigationVersions,
		ServiceInvestigationDiffs:      latencies.investigationDiffs,
		FactQueryMS:                    latencies.factQuery.P50MS,
		FactQueryP50MS:                 latencies.factQuery.P50MS,
		FactQueryP95MS:                 latencies.factQuery.P95MS,
		TopologyQueryMS:                latencies.topology.P50MS,
		TopologyQueryP50MS:             latencies.topology.P50MS,
		TopologyQueryP95MS:             latencies.topology.P95MS,
		QueryRuns:                      opts.QueryRuns,
		Generated:                      manifest,
		Ingest:                         summary,
		Counts:                         counts,
	}
	if duration > 0 {
		report.StoredVersionsPerSecond = float64(counts.Versions) / duration
	}
	if storedBytes > 0 {
		report.CompressionRatio = float64(rawSampleBytes) / float64(storedBytes)
	}
	if counts.EndpointSliceChangeRows > 0 {
		report.EndpointSliceEdgeRowsPerChange = float64(counts.EndpointSliceEdgeRows) / float64(counts.EndpointSliceChangeRows)
	}
	return report, nil
}

func RunWatch(ctx context.Context, opts WatchOptions) (Report, error) {
	if opts.DBPath == "" {
		opts.DBPath = filepath.Join("testdata", "generated", "benchmark-watch", "kube-insight-watch.db")
	}
	if opts.Duration <= 0 {
		opts.Duration = 30 * time.Second
	}
	if opts.MaxRetries < 0 {
		opts.MaxRetries = 3
	}
	if err := os.MkdirAll(filepath.Dir(opts.DBPath), 0o755); err != nil {
		return Report{}, err
	}
	removeSQLiteFiles(opts.DBPath)

	store, err := sqlite.Open(opts.DBPath)
	if err != nil {
		return Report{}, err
	}
	start := time.Now()
	watchLogger := logging.FromContext(ctx).With("component", "watch")
	watchSummary, err := collector.WatchResourcesClientGo(ctx, collector.WatchResourcesOptions{
		Context:           opts.Context,
		Resources:         opts.Resources,
		DiscoverResources: opts.DiscoverResources,
		Namespace:         opts.Namespace,
		Store:             store,
		MaxEvents:         opts.MaxEvents,
		Timeout:           opts.Duration,
		Concurrency:       opts.Concurrency,
		MaxRetries:        opts.MaxRetries,
		Logf: func(message string, args ...any) {
			watchLogger.Info(message, args...)
		},
	})
	duration := time.Since(start).Seconds()
	if err != nil {
		_ = store.Close()
		return Report{}, err
	}
	counts, err := store.Stats(ctx)
	if err != nil {
		_ = store.Close()
		return Report{}, err
	}
	if err := store.Close(); err != nil {
		return Report{}, err
	}
	storedBytes := sqliteBytes(opts.DBPath)
	report := Report{
		Dataset:                        "watch",
		DurationSeconds:                duration,
		Objects:                        counts.Objects,
		Versions:                       counts.Versions,
		DeletedObjects:                 counts.DeletedObjects,
		UnknownVisibilityObjects:       int64(watchSummary.UnknownVisibility),
		RelistConfirmedDeletes:         int64(watchSummary.ReconciledDeleted),
		StoredBytes:                    storedBytes,
		IndexBytes:                     counts.IndexBytes,
		BlobBytes:                      counts.BlobBytes,
		FactBytes:                      counts.FactBytes,
		EdgeBytes:                      counts.EdgeBytes,
		StoredVersionsByProfile:        counts.StoredVersionsByProfile,
		APIResourcesByProfile:          counts.APIResourcesByProfile,
		ProcessingProfiles:             counts.ProcessingProfiles,
		DisabledProcessingProfiles:     counts.DisabledProcessingProfiles,
		WatchRestarts:                  int64(watchSummary.Retries + watchSummary.WatchErrors),
		StaleRelistCount:               int64(watchSummary.Relists),
		PeriodicRelistCount:            int64(watchSummary.Resources),
		OffsetLagMS:                    counts.OffsetLagMS,
		MaxQueueDepth:                  watchSummary.MaxQueueDepth,
		BackpressureEvents:             watchSummary.BackpressureEvents,
		QueueWaitMS:                    watchSummary.QueueWaitMS,
		WatchResourceQueue:             watchSummary.ResourceQueue,
		FilterKept:                     int64(watchSummary.Ingest.StoredObservations),
		FilterModified:                 int64(watchSummary.Ingest.ModifiedObservations),
		FilterDiscardedChange:          int64(watchSummary.Ingest.DiscardedChanges),
		FilterDiscardedResource:        int64(watchSummary.Ingest.DiscardedResources),
		PodStatusChangeRows:            counts.PodStatusChangeRows,
		NodeConditionFactRows:          counts.NodeConditionFactRows,
		EventMessageFingerprints:       counts.EventMessageFingerprints,
		EventFactRows:                  counts.EventFactRows,
		EndpointSliceReadinessFactRows: counts.EndpointSliceReadinessFactRows,
		LeaseSkippedCount:              int64(watchSummary.Ingest.DiscardedResources),
		RedactedFields:                 counts.RedactedFields,
		SecretPayloadViolations:        counts.SecretPayloadViolations,
		Ingest:                         watchSummary.Ingest,
		Counts:                         counts,
		Watch:                          &watchSummary,
	}
	if duration > 0 {
		report.WatchEventsPerSecond = float64(watchSummary.Events) / duration
		report.StoredVersionsPerSecond = float64(watchSummary.Stored) / duration
	}
	if storedBytes > 0 {
		report.CompressionRatio = float64(counts.RawBytes) / float64(storedBytes)
	}
	if counts.EndpointSliceChangeRows > 0 {
		report.EndpointSliceEdgeRowsPerChange = float64(counts.EndpointSliceEdgeRows) / float64(counts.EndpointSliceChangeRows)
	}
	return report, nil
}

type queryLatencies struct {
	latestLookup          LatencySummary
	historicalGet         LatencySummary
	investigation         LatencySummary
	investigationVersions int
	investigationDiffs    int
	factQuery             LatencySummary
	topology              LatencySummary
}

type LatencySummary struct {
	Count int     `json:"count"`
	P50MS float64 `json:"p50_ms"`
	P95MS float64 `json:"p95_ms"`
	MinMS float64 `json:"min_ms"`
	MaxMS float64 `json:"max_ms"`
}

func measureQueryLatencies(ctx context.Context, store *sqlite.Store, runs int) queryLatencies {
	target, err := store.LatestObjectTarget(ctx, "Pod")
	if err != nil {
		return queryLatencies{}
	}
	serviceTarget, serviceErr := store.LatestObjectTarget(ctx, "Service")
	if runs <= 0 {
		runs = 1
	}
	var out queryLatencies
	var object sqlite.ObjectRecord
	var dbObjectID int64
	latestLookup := make([]float64, 0, runs)
	historicalGet := make([]float64, 0, runs)
	factQuery := make([]float64, 0, runs)
	topologyQuery := make([]float64, 0, runs)
	investigationQuery := make([]float64, 0, runs)
	for i := 0; i < runs; i++ {
		latestLookup = append(latestLookup, timed(func() {
			object, dbObjectID, _ = store.FindObject(ctx, target)
		}))
		if dbObjectID != 0 {
			historicalGet = append(historicalGet, timed(func() {
				_, _ = store.LatestDocument(ctx, dbObjectID)
			}))
		}
		if object.LogicalID != "" {
			factQuery = append(factQuery, timed(func() {
				_, _ = store.GetFacts(ctx, object.LogicalID)
			}))
		}
		topologyQuery = append(topologyQuery, timed(func() {
			_, _ = store.Topology(ctx, target)
		}))
		investigationQuery = append(investigationQuery, timed(func() {
			if serviceErr == nil {
				service, err := store.InvestigateService(ctx, serviceTarget)
				if err == nil && service.Summary.Versions > out.investigationVersions {
					out.investigationVersions = service.Summary.Versions
				}
				if err == nil && service.Summary.VersionDiffs > out.investigationDiffs {
					out.investigationDiffs = service.Summary.VersionDiffs
				}
				return
			}
			_, _ = store.Investigate(ctx, target)
		}))
	}
	out.latestLookup = summarizeLatency(latestLookup)
	out.historicalGet = summarizeLatency(historicalGet)
	out.factQuery = summarizeLatency(factQuery)
	out.topology = summarizeLatency(topologyQuery)
	out.investigation = summarizeLatency(investigationQuery)
	return out
}

func summarizeLatency(samples []float64) LatencySummary {
	if len(samples) == 0 {
		return LatencySummary{}
	}
	sorted := append([]float64(nil), samples...)
	sort.Float64s(sorted)
	return LatencySummary{
		Count: len(sorted),
		P50MS: percentile(sorted, 50),
		P95MS: percentile(sorted, 95),
		MinMS: sorted[0],
		MaxMS: sorted[len(sorted)-1],
	}
}

func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	if p <= 0 {
		return sorted[0]
	}
	if p >= 100 {
		return sorted[len(sorted)-1]
	}
	index := int(math.Ceil((p/100)*float64(len(sorted)))) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(sorted) {
		index = len(sorted) - 1
	}
	return sorted[index]
}

func timed(fn func()) float64 {
	start := time.Now()
	fn()
	return millisSince(start)
}

func millisSince(start time.Time) float64 {
	return float64(time.Since(start).Microseconds()) / 1000
}

func ingestDir(ctx context.Context, store *sqlite.Store, dir string) (ingest.Summary, error) {
	files, err := jsonFiles(dir)
	if err != nil {
		return ingest.Summary{}, err
	}
	var total ingest.Summary
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			return total, err
		}
		pipeline := ingest.DefaultPipeline(store)
		pipeline.ClusterID = clusterIDForPath(dir, path)
		summary, err := pipeline.IngestJSON(ctx, data)
		if err != nil {
			return total, fmt.Errorf("%s: %w", path, err)
		}
		total.Observations += summary.Observations
		total.StoredObservations += summary.StoredObservations
		total.ModifiedObservations += summary.ModifiedObservations
		total.DiscardedChanges += summary.DiscardedChanges
		total.DiscardedResources += summary.DiscardedResources
		total.Facts += summary.Facts
		total.Edges += summary.Edges
		total.Changes += summary.Changes
	}
	return total, nil
}

func jsonFiles(dir string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || filepath.Base(path) == "manifest.json" || filepath.Ext(path) != ".json" {
			return nil
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	if len(files) == 0 {
		return nil, fmt.Errorf("no JSON files found in %s", dir)
	}
	return files, nil
}

func clusterIDForPath(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return "local"
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) > 1 && parts[0] != "" {
		return parts[0]
	}
	return "local"
}

func jsonBytes(dir string) (int64, error) {
	files, err := jsonFiles(dir)
	if err != nil {
		return 0, err
	}
	var total int64
	for _, path := range files {
		info, err := os.Stat(path)
		if err != nil {
			return 0, err
		}
		total += info.Size()
	}
	return total, nil
}

func sqliteBytes(path string) int64 {
	var total int64
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if info, err := os.Stat(path + suffix); err == nil {
			total += info.Size()
		}
	}
	return total
}

func removeSQLiteFiles(path string) {
	for _, suffix := range []string{"", "-wal", "-shm"} {
		_ = os.Remove(path + suffix)
	}
}

func cleanBenchmarkOutput(dir string) error {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if name != "manifest.json" && !strings.HasPrefix(name, "cluster-gen-") {
			continue
		}
		if err := os.RemoveAll(filepath.Join(dir, name)); err != nil {
			return err
		}
	}
	return nil
}
