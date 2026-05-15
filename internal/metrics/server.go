package metrics

import (
	"context"
	"errors"
	"net/http"
	"time"

	"kube-insight/internal/storage/sqlite"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type ServerOptions struct {
	DBPath string
}

type Server struct {
	mux *http.ServeMux
}

func NewServer(opts ServerOptions) (*Server, error) {
	if opts.DBPath == "" {
		return nil, errors.New("metrics server requires a sqlite database path")
	}
	registry := prometheus.NewRegistry()
	registry.MustRegister(NewCollector(opts.DBPath))

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.Handle("GET /metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))
	return &Server{mux: mux}, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func ListenAndServe(ctx context.Context, listen string, opts ServerOptions) error {
	if listen == "" {
		listen = "127.0.0.1:9090"
	}
	handler, err := NewServer(opts)
	if err != nil {
		return err
	}
	server := &http.Server{
		Addr:              listen,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}
	done := make(chan error, 1)
	go func() {
		done <- server.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return err
		}
		err := <-done
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

type Collector struct {
	dbPath string

	clusters                *prometheus.Desc
	apiResources            *prometheus.Desc
	objects                 *prometheus.Desc
	observations            *prometheus.Desc
	versions                *prometheus.Desc
	blobs                   *prometheus.Desc
	facts                   *prometheus.Desc
	edges                   *prometheus.Desc
	changes                 *prometheus.Desc
	latestRows              *prometheus.Desc
	filterDecisions         *prometheus.Desc
	filterRedactedFields    *prometheus.Desc
	filterRemovedFields     *prometheus.Desc
	secretPayloadRemovals   *prometheus.Desc
	secretPayloadViolations *prometheus.Desc
	ingestionOffsets        *prometheus.Desc
	offsetLagSeconds        *prometheus.Desc
	storageBytes            *prometheus.Desc
	versionsByProfile       *prometheus.Desc
	apiResourcesByProfile   *prometheus.Desc
	resourceStreams         *prometheus.Desc
	resourceStreamsByStatus *prometheus.Desc
	filterDecisionsByResult *prometheus.Desc
	scrapeUnixSeconds       *prometheus.Desc
}

func NewCollector(dbPath string) *Collector {
	return &Collector{
		dbPath: dbPath,

		clusters:                newDesc("clusters", "Stored Kubernetes clusters.", nil),
		apiResources:            newDesc("api_resources", "Discovered Kubernetes API resources.", nil),
		objects:                 newDesc("objects", "Stored Kubernetes objects by state.", []string{"state"}),
		observations:            newDesc("observations", "Object observations by content change state.", []string{"content_changed"}),
		versions:                newDesc("versions", "Retained object content versions.", nil),
		blobs:                   newDesc("blobs", "Retained JSON blobs.", nil),
		facts:                   newDesc("facts", "Extracted fact rows.", nil),
		edges:                   newDesc("edges", "Extracted topology/reference edge rows.", nil),
		changes:                 newDesc("changes", "Extracted change summary rows.", nil),
		latestRows:              newDesc("latest_rows", "Rows in the latest object index.", nil),
		filterDecisions:         newDesc("filter_decisions", "Filter decisions by destructive state.", []string{"destructive"}),
		filterRedactedFields:    newDesc("filter_redacted_fields", "Fields redacted by filters.", nil),
		filterRemovedFields:     newDesc("filter_removed_fields", "Fields removed by filters.", nil),
		secretPayloadRemovals:   newDesc("secret_payload_removals", "Secret payload removal decisions.", nil),
		secretPayloadViolations: newDesc("secret_payload_violations", "Latest Secret JSON documents still containing data/stringData.", nil),
		ingestionOffsets:        newDesc("ingestion_offsets", "Tracked ingestion offset rows.", nil),
		offsetLagSeconds:        newDesc("offset_lag_seconds", "Seconds since the latest ingestion offset update.", nil),
		storageBytes:            newDesc("storage_bytes", "Storage bytes by kind.", []string{"kind"}),
		versionsByProfile:       newDesc("versions_by_profile", "Retained versions by processing profile.", []string{"profile"}),
		apiResourcesByProfile:   newDesc("api_resources_by_profile", "API resources by processing profile.", []string{"profile"}),
		resourceStreams:         newDesc("resource_streams", "Resource stream health summary by state.", []string{"state"}),
		resourceStreamsByStatus: newDesc("resource_streams_by_status", "Resource streams by last status.", []string{"status"}),
		filterDecisionsByResult: newDesc("filter_decisions_by_result", "Filter decisions by filter, outcome, and reason.", []string{"filter", "outcome", "reason"}),
		scrapeUnixSeconds:       newDesc("metrics_scrape_unix_seconds", "Unix timestamp of this metrics scrape.", nil),
	}
}

func newDesc(name string, help string, labels []string) *prometheus.Desc {
	return prometheus.NewDesc("kube_insight_"+name, help, labels, nil)
}

func (c *Collector) Describe(ch chan<- *prometheus.Desc) {
	for _, desc := range c.descs() {
		ch <- desc
	}
}

func (c *Collector) Collect(ch chan<- prometheus.Metric) {
	store, err := sqlite.OpenReadOnly(c.dbPath)
	if err != nil {
		ch <- prometheus.NewInvalidMetric(c.clusters, err)
		return
	}
	defer store.Close()

	stats, err := store.Stats(context.Background())
	if err != nil {
		ch <- prometheus.NewInvalidMetric(c.clusters, err)
		return
	}
	health, err := store.ResourceHealth(context.Background(), sqlite.ResourceHealthOptions{})
	if err != nil {
		ch <- prometheus.NewInvalidMetric(c.resourceStreams, err)
		return
	}
	filterRows, err := store.FilterDecisionMetrics(context.Background())
	if err != nil {
		ch <- prometheus.NewInvalidMetric(c.filterDecisionsByResult, err)
		return
	}

	c.collectStats(ch, stats)
	c.collectProfileStats(ch, stats)
	c.collectResourceHealth(ch, health)
	c.collectFilterDecisionMetrics(ch, filterRows)
	ch <- gauge(c.scrapeUnixSeconds, float64(time.Now().Unix()))
}

func (c *Collector) collectStats(ch chan<- prometheus.Metric, stats sqlite.Stats) {
	ch <- gauge(c.clusters, float64(stats.Clusters))
	ch <- gauge(c.apiResources, float64(stats.APIResources))
	ch <- gauge(c.objects, float64(stats.Objects-stats.DeletedObjects), "active")
	ch <- gauge(c.objects, float64(stats.DeletedObjects), "deleted")
	ch <- gauge(c.observations, float64(stats.Observations-stats.UnchangedObservations), "true")
	ch <- gauge(c.observations, float64(stats.UnchangedObservations), "false")
	ch <- gauge(c.versions, float64(stats.Versions))
	ch <- gauge(c.blobs, float64(stats.Blobs))
	ch <- gauge(c.facts, float64(stats.Facts))
	ch <- gauge(c.edges, float64(stats.Edges))
	ch <- gauge(c.changes, float64(stats.Changes))
	ch <- gauge(c.latestRows, float64(stats.LatestRows))
	ch <- gauge(c.filterDecisions, float64(stats.FilterDecisions-stats.DestructiveFilterDecisions), "false")
	ch <- gauge(c.filterDecisions, float64(stats.DestructiveFilterDecisions), "true")
	ch <- gauge(c.filterRedactedFields, float64(stats.RedactedFields))
	ch <- gauge(c.filterRemovedFields, float64(stats.RemovedFields))
	ch <- gauge(c.secretPayloadRemovals, float64(stats.SecretPayloadRemovals))
	ch <- gauge(c.secretPayloadViolations, float64(stats.SecretPayloadViolations))
	ch <- gauge(c.ingestionOffsets, float64(stats.IngestionOffsets))
	ch <- gauge(c.offsetLagSeconds, float64(stats.OffsetLagMS)/1000)
	ch <- gauge(c.storageBytes, float64(stats.RawBytes), "raw_documents")
	ch <- gauge(c.storageBytes, float64(stats.BlobBytes), "blobs")
	ch <- gauge(c.storageBytes, float64(stats.DatabaseBytes), "database_file")
	ch <- gauge(c.storageBytes, float64(stats.WALBytes), "wal_file")
	ch <- gauge(c.storageBytes, float64(stats.LiveBytes), "live_pages")
	ch <- gauge(c.storageBytes, float64(stats.SlackBytes), "slack_pages")
	ch <- gauge(c.storageBytes, float64(stats.TableBytes), "tables")
	ch <- gauge(c.storageBytes, float64(stats.IndexBytes), "indexes")
}

func (c *Collector) collectProfileStats(ch chan<- prometheus.Metric, stats sqlite.Stats) {
	for profile, count := range stats.StoredVersionsByProfile {
		ch <- gauge(c.versionsByProfile, float64(count), profile)
	}
	for profile, count := range stats.APIResourcesByProfile {
		ch <- gauge(c.apiResourcesByProfile, float64(count), profile)
	}
}

func (c *Collector) collectResourceHealth(ch chan<- prometheus.Metric, health sqlite.ResourceHealthReport) {
	ch <- gauge(c.resourceStreams, float64(health.Summary.Healthy), "healthy")
	ch <- gauge(c.resourceStreams, float64(health.Summary.Unstable), "unstable")
	ch <- gauge(c.resourceStreams, float64(health.Summary.Errors), "errors")
	ch <- gauge(c.resourceStreams, float64(health.Summary.Stale), "stale")
	ch <- gauge(c.resourceStreams, float64(health.Summary.NotStarted), "not_started")
	ch <- gauge(c.resourceStreams, float64(health.Summary.Queued), "queued")
	for status, count := range health.ByStatus {
		ch <- gauge(c.resourceStreamsByStatus, float64(count), status)
	}
}

func (c *Collector) collectFilterDecisionMetrics(ch chan<- prometheus.Metric, rows []sqlite.FilterDecisionMetric) {
	for _, row := range rows {
		ch <- gauge(c.filterDecisionsByResult, float64(row.Count), row.FilterName, row.Outcome, row.Reason)
	}
}

func (c *Collector) descs() []*prometheus.Desc {
	return []*prometheus.Desc{
		c.clusters,
		c.apiResources,
		c.objects,
		c.observations,
		c.versions,
		c.blobs,
		c.facts,
		c.edges,
		c.changes,
		c.latestRows,
		c.filterDecisions,
		c.filterRedactedFields,
		c.filterRemovedFields,
		c.secretPayloadRemovals,
		c.secretPayloadViolations,
		c.ingestionOffsets,
		c.offsetLagSeconds,
		c.storageBytes,
		c.versionsByProfile,
		c.apiResourcesByProfile,
		c.resourceStreams,
		c.resourceStreamsByStatus,
		c.filterDecisionsByResult,
		c.scrapeUnixSeconds,
	}
}

func gauge(desc *prometheus.Desc, value float64, labels ...string) prometheus.Metric {
	return prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, value, labels...)
}
