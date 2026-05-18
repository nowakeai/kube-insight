package metrics

import (
	"context"
	"time"

	"kube-insight/internal/storage"
	"kube-insight/internal/storage/clickhouse"

	"github.com/prometheus/client_golang/prometheus"
)

type clickHouseMetricsStore interface {
	Close() error
	Stats(context.Context) (clickhouse.Stats, error)
	ResourceHealth(context.Context, storage.ResourceHealthOptions) (storage.ResourceHealthReport, error)
}

type ClickHouseCollector struct {
	open func(context.Context) (clickHouseMetricsStore, error)

	clusters                *prometheus.Desc
	apiResources            *prometheus.Desc
	objects                 *prometheus.Desc
	observations            *prometheus.Desc
	versions                *prometheus.Desc
	facts                   *prometheus.Desc
	edges                   *prometheus.Desc
	changes                 *prometheus.Desc
	latestRows              *prometheus.Desc
	ingestionOffsets        *prometheus.Desc
	offsetLagSeconds        *prometheus.Desc
	storageBytes            *prometheus.Desc
	storageParts            *prometheus.Desc
	storagePartAgeSeconds   *prometheus.Desc
	storageCompressionRatio *prometheus.Desc
	storageCompressedPerRow *prometheus.Desc
	resourceStreams         *prometheus.Desc
	resourceStreamsByStatus *prometheus.Desc
	scrapeUnixSeconds       *prometheus.Desc
}

func NewClickHouseCollector(opts ServerOptions) *ClickHouseCollector {
	open := opts.OpenClickHouseStore
	if open == nil {
		endpoint := opts.ClickHouseEndpoint
		options := opts.ClickHouseOptions
		open = func(context.Context) (clickHouseMetricsStore, error) {
			return clickhouse.NewHTTPStore(endpoint, options)
		}
	}
	return &ClickHouseCollector{
		open: open,

		clusters:                newDesc("clusters", "Stored Kubernetes clusters.", nil),
		apiResources:            newDesc("api_resources", "Discovered Kubernetes API resources.", nil),
		objects:                 newDesc("objects", "Stored Kubernetes objects by state.", []string{"state"}),
		observations:            newDesc("observations", "Object observations by content change state.", []string{"content_changed"}),
		versions:                newDesc("versions", "Retained object content versions.", nil),
		facts:                   newDesc("facts", "Extracted fact rows.", nil),
		edges:                   newDesc("edges", "Extracted topology/reference edge rows.", nil),
		changes:                 newDesc("changes", "Extracted change summary rows.", nil),
		latestRows:              newDesc("latest_rows", "Rows in the latest object index.", nil),
		ingestionOffsets:        newDesc("ingestion_offsets", "Tracked ingestion offset rows.", nil),
		offsetLagSeconds:        newDesc("offset_lag_seconds", "Seconds since the latest ingestion offset update.", nil),
		storageBytes:            newDesc("storage_bytes", "Storage bytes by kind.", []string{"kind"}),
		storageParts:            newDesc("storage_parts", "ClickHouse active and inactive data parts by kind.", []string{"kind"}),
		storagePartAgeSeconds:   newDesc("storage_part_age_seconds", "Age in seconds of the oldest inactive ClickHouse part by kind.", []string{"kind"}),
		storageCompressionRatio: newDesc("storage_compression_ratio", "ClickHouse uncompressed bytes divided by compressed bytes.", nil),
		storageCompressedPerRow: newDesc("storage_compressed_bytes_per_row", "ClickHouse compressed bytes per active business-table row.", nil),
		resourceStreams:         newDesc("resource_streams", "Resource stream health summary by state.", []string{"state"}),
		resourceStreamsByStatus: newDesc("resource_streams_by_status", "Resource streams by last status.", []string{"status"}),
		scrapeUnixSeconds:       newDesc("metrics_scrape_unix_seconds", "Unix timestamp of this metrics scrape.", nil),
	}
}

func (c *ClickHouseCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, desc := range c.descs() {
		ch <- desc
	}
}

func (c *ClickHouseCollector) Collect(ch chan<- prometheus.Metric) {
	store, err := c.open(context.Background())
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
	health, err := store.ResourceHealth(context.Background(), storage.ResourceHealthOptions{})
	if err != nil {
		ch <- prometheus.NewInvalidMetric(c.resourceStreams, err)
		return
	}
	c.collectStats(ch, stats)
	c.collectResourceHealth(ch, health)
	ch <- gauge(c.scrapeUnixSeconds, float64(time.Now().Unix()))
}

func (c *ClickHouseCollector) collectStats(ch chan<- prometheus.Metric, stats clickhouse.Stats) {
	ch <- gauge(c.clusters, float64(stats.Clusters))
	ch <- gauge(c.apiResources, float64(stats.APIResources))
	ch <- gauge(c.objects, float64(stats.Objects), "active")
	ch <- gauge(c.objects, float64(stats.DeletedObjects), "deleted")
	ch <- gauge(c.observations, float64(stats.ContentChangedObservations), "true")
	ch <- gauge(c.observations, float64(stats.UnchangedObservations), "false")
	ch <- gauge(c.versions, float64(stats.Versions))
	ch <- gauge(c.facts, float64(stats.Facts))
	ch <- gauge(c.edges, float64(stats.Edges))
	ch <- gauge(c.changes, float64(stats.Changes))
	ch <- gauge(c.latestRows, float64(stats.LatestRows))
	ch <- gauge(c.ingestionOffsets, float64(stats.IngestionOffsets))
	ch <- gauge(c.offsetLagSeconds, float64(stats.OffsetLagMS)/1000)
	ch <- gauge(c.storageBytes, float64(stats.RawBytes), "raw_documents")
	ch <- gauge(c.storageBytes, float64(stats.StoredBytes), "stored_documents")
	ch <- gauge(c.storageBytes, float64(stats.CompressedBytes), "clickhouse_compressed")
	ch <- gauge(c.storageBytes, float64(stats.UncompressedBytes), "clickhouse_uncompressed")
	ch <- gauge(c.storageBytes, float64(stats.ProofCompressedBytes), "clickhouse_proof_compressed")
	ch <- gauge(c.storageBytes, float64(stats.DerivedCompressedBytes), "clickhouse_derived_compressed")
	for _, footprint := range stats.Footprint {
		label := "clickhouse_" + footprint.Database + "_" + footprint.State
		ch <- gauge(c.storageBytes, float64(footprint.BytesOnDisk), label+"_bytes_on_disk")
		ch <- gauge(c.storageParts, float64(footprint.Parts), label+"_parts")
		if footprint.State == "inactive" {
			ch <- gauge(c.storagePartAgeSeconds, float64(footprint.OldestInactiveAgeSeconds), label+"_oldest_inactive")
		}
	}
	ch <- gauge(c.storageCompressionRatio, stats.CompressionRatio)
	ch <- gauge(c.storageCompressedPerRow, stats.CompressedBytesPerRow)
	for _, part := range stats.TableParts {
		ch <- gauge(c.storageBytes, float64(part.CompressedBytes), "clickhouse_table_"+part.Table+"_compressed")
		ch <- gauge(c.storageBytes, float64(part.UncompressedBytes), "clickhouse_table_"+part.Table+"_uncompressed")
	}
}

func (c *ClickHouseCollector) collectResourceHealth(ch chan<- prometheus.Metric, health storage.ResourceHealthReport) {
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

func (c *ClickHouseCollector) descs() []*prometheus.Desc {
	return []*prometheus.Desc{
		c.clusters,
		c.apiResources,
		c.objects,
		c.observations,
		c.versions,
		c.facts,
		c.edges,
		c.changes,
		c.latestRows,
		c.ingestionOffsets,
		c.offsetLagSeconds,
		c.storageBytes,
		c.storageParts,
		c.storagePartAgeSeconds,
		c.storageCompressionRatio,
		c.storageCompressedPerRow,
		c.resourceStreams,
		c.resourceStreamsByStatus,
		c.scrapeUnixSeconds,
	}
}
