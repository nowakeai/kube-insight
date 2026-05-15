package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"time"

	"kube-insight/internal/collector"
	"kube-insight/internal/core"
	"kube-insight/internal/ingest"
	"kube-insight/internal/kubeapi"
	"kube-insight/internal/resourceprofile"
	"kube-insight/internal/storage"
	"kube-insight/internal/storage/sqlite"

	"github.com/spf13/cobra"
)

func dbCommand(ctx context.Context, stdout io.Writer, state *cliState) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "db",
		Short: "Manage kube-insight storage.",
	}
	cmd.AddCommand(dbBackfillCommand(ctx, stdout, state))
	cmd.AddCommand(dbClustersCommand(ctx, stdout, state))
	cmd.AddCommand(dbCompactCommand(ctx, stdout, state))
	cmd.AddCommand(dbRetentionCommand(ctx, stdout, state))
	cmd.AddCommand(dbResourcesCommand(ctx, stdout, state))
	return cmd
}

func dbBackfillCommand(ctx context.Context, stdout io.Writer, state *cliState) *cobra.Command {
	var yes bool
	var batchObjects int
	var output string
	cmd := &cobra.Command{
		Use:   "backfill",
		Short: "Reprocess retained history with the current filter configuration.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateOutputFormat(output); err != nil {
				return err
			}
			rt, err := loadRuntimeConfig(cmd, state, "", false)
			if err != nil {
				return err
			}
			runCtx, _, err := runtimeContext(ctx, cmd.ErrOrStderr(), rt)
			if err != nil {
				return err
			}
			store, err := sqlite.Open(dbCommandPath(cmd, state, rt))
			if err != nil {
				return err
			}
			defer store.Close()
			chains, err := ingest.FilterChainsFromProcessing(rt.Config.Processing)
			if err != nil {
				return err
			}
			rules := rt.Config.ProfileRules()
			report, err := store.BackfillRetainedHistory(runCtx, sqlite.BackfillOptions{
				DryRun:       !yes,
				BatchObjects: batchObjects,
				Normalize: func(ctx context.Context, version sqlite.BackfillVersion) ([]byte, error) {
					var object map[string]any
					if err := json.Unmarshal(version.Document, &object); err != nil {
						return nil, err
					}
					profile := resourceprofile.Select(kubeapi.ResourceInfo{
						Group:      version.Ref.Group,
						Version:    version.Ref.Version,
						Resource:   version.Ref.Resource,
						Kind:       version.Ref.Kind,
						Namespaced: version.Ref.Namespace != "",
					}, rules)
					obs := versionToObservation(version, object)
					chain := chains.Select(profile.FilterChain)
					if chain == nil {
						return json.Marshal(obs.Object)
					}
					filtered, _, err := chain.Apply(ctx, obs)
					if err != nil {
						return nil, err
					}
					return json.Marshal(filtered.Object)
				},
			})
			if err != nil {
				return err
			}
			if output == outputJSON {
				return writeJSON(stdout, report)
			}
			return writeBackfillReportTable(stdout, report)
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "Apply changes; without --yes this is a dry run")
	cmd.Flags().IntVar(&batchObjects, "batch-objects", 500, "Objects per backfill transaction")
	addOutputFlag(cmd, &output, outputTable)
	return cmd
}

func versionToObservation(version sqlite.BackfillVersion, object map[string]any) core.Observation {
	return core.Observation{
		Type:            core.ObservationModified,
		ObservedAt:      version.ObservedAt,
		ResourceVersion: version.ResourceVersion,
		Ref:             version.Ref,
		Object:          object,
	}
}

func dbCompactCommand(ctx context.Context, stdout io.Writer, state *cliState) *cobra.Command {
	var pruneUnchanged bool
	var output string
	cmd := &cobra.Command{
		Use:   "compact",
		Short: "Compact SQLite storage after migrations and deletes.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateOutputFormat(output); err != nil {
				return err
			}
			rt, err := loadRuntimeConfig(cmd, state, "", false)
			if err != nil {
				return err
			}
			runCtx, _, err := runtimeContext(ctx, cmd.ErrOrStderr(), rt)
			if err != nil {
				return err
			}
			store, err := sqlite.Open(dbCommandPath(cmd, state, rt))
			if err != nil {
				return err
			}
			defer store.Close()
			report, err := store.Compact(runCtx, sqlite.CompactOptions{PruneUnchanged: pruneUnchanged})
			if err != nil {
				return err
			}
			if output == outputJSON {
				return writeJSON(stdout, report)
			}
			return writeCompactReportTable(stdout, report)
		},
	}
	cmd.Flags().BoolVar(&pruneUnchanged, "prune-unchanged", false, "Prune duplicate retained versions after object observations are backfilled")
	addOutputFlag(cmd, &output, outputTable)
	return cmd
}

func dbRetentionCommand(ctx context.Context, stdout io.Writer, state *cliState) *cobra.Command {
	var maxAge time.Duration
	var filterDecisionMaxAge time.Duration
	var profiles []string
	var resources []string
	var minVersions int
	var yes bool
	var output string
	cmd := &cobra.Command{
		Use:   "retention",
		Short: "Apply configured or explicit history retention.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateOutputFormat(output); err != nil {
				return err
			}
			rt, err := loadRuntimeConfig(cmd, state, "", false)
			if err != nil {
				return err
			}
			retention := rt.Config.Storage.Retention
			if maxAge == 0 && retention.MaxAgeSeconds > 0 {
				maxAge = time.Duration(retention.MaxAgeSeconds) * time.Second
			}
			if filterDecisionMaxAge == 0 && retention.FilterDecisionMaxAgeSeconds > 0 {
				filterDecisionMaxAge = time.Duration(retention.FilterDecisionMaxAgeSeconds) * time.Second
			}
			if minVersions == 0 {
				minVersions = retention.MinVersionsPerObject
			}
			rules := retentionPolicyRulesFromConfig(retention.Policies)
			if len(profiles) > 0 || len(resources) > 0 {
				rules = append(rules, sqlite.RetentionRuleOptions{
					Name:                 "cli",
					Profiles:             profiles,
					Resources:            resources,
					MaxAge:               maxAge,
					MinVersionsPerObject: minVersions,
				})
				maxAge = 0
			}
			if maxAge <= 0 && filterDecisionMaxAge <= 0 && !hasCLIRetentionRules(rules) {
				return fmt.Errorf("retention requires --max-age, --filter-decision-max-age, or storage.retention config")
			}
			if !yes {
				return fmt.Errorf("refusing to apply retention without --yes")
			}
			runCtx, _, err := runtimeContext(ctx, cmd.ErrOrStderr(), rt)
			if err != nil {
				return err
			}
			runCtx, cancel := context.WithTimeout(runCtx, 5*time.Second)
			defer cancel()
			store, err := sqlite.OpenReadOnly(dbCommandPath(cmd, state, rt))
			if err != nil {
				return err
			}
			defer store.Close()
			report, err := store.ApplyRetention(runCtx, sqlite.RetentionOptions{
				MaxAge:               maxAge,
				MinVersionsPerObject: minVersions,
				FilterDecisionMaxAge: filterDecisionMaxAge,
				Rules:                rules,
				SkipWhenDatabaseBusy: rt.Config.Storage.Maintenance.SkipWhenDatabaseBusy,
			})
			if err != nil {
				return err
			}
			if output == outputJSON {
				return writeJSON(stdout, report)
			}
			return writeRetentionReportTable(stdout, report)
		},
	}
	cmd.Flags().DurationVar(&maxAge, "max-age", 0, "Delete historical versions older than this duration while keeping each object's latest/min versions")
	cmd.Flags().DurationVar(&filterDecisionMaxAge, "filter-decision-max-age", 0, "Delete filter decision audit rows older than this duration")
	cmd.Flags().StringArrayVar(&profiles, "profile", nil, "Apply --max-age only to this processing profile; repeatable")
	cmd.Flags().StringArrayVar(&resources, "resource", nil, "Apply --max-age only to this resource, resource.group, or group/version/resource; repeatable")
	cmd.Flags().IntVar(&minVersions, "min-versions-per-object", 0, "Minimum versions to keep per object; 0 uses config")
	cmd.Flags().BoolVar(&yes, "yes", false, "Confirm destructive retention deletes")
	addOutputFlag(cmd, &output, outputTable)
	return cmd
}

func hasCLIRetentionRules(rules []sqlite.RetentionRuleOptions) bool {
	for _, rule := range rules {
		if rule.MaxAge > 0 {
			return true
		}
	}
	return false
}

func dbClustersCommand(ctx context.Context, stdout io.Writer, state *cliState) *cobra.Command {
	var output string
	cmd := &cobra.Command{
		Use:   "clusters",
		Short: "List and clean stored clusters.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateOutputFormat(output); err != nil {
				return err
			}
			rt, err := loadRuntimeConfig(cmd, state, "", false)
			if err != nil {
				return err
			}
			runCtx, _, err := runtimeContext(ctx, cmd.ErrOrStderr(), rt)
			if err != nil {
				return err
			}
			store, err := sqlite.Open(dbCommandPath(cmd, state, rt))
			if err != nil {
				return err
			}
			defer store.Close()
			clusters, err := store.ListClusters(runCtx)
			if err != nil {
				return err
			}
			if output == outputJSON {
				return writeJSON(stdout, clusters)
			}
			return writeClustersTable(stdout, clusters)
		},
	}
	addOutputFlag(cmd, &output, outputTable)
	cmd.AddCommand(dbClusterDeleteCommand(ctx, stdout, state))
	return cmd
}

func dbClusterDeleteCommand(ctx context.Context, stdout io.Writer, state *cliState) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete CLUSTER_ID",
		Short: "Delete one stored cluster and its history.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !yes {
				return fmt.Errorf("refusing to delete cluster %q without --yes", args[0])
			}
			rt, err := loadRuntimeConfig(cmd, state, "", false)
			if err != nil {
				return err
			}
			runCtx, _, err := runtimeContext(ctx, cmd.ErrOrStderr(), rt)
			if err != nil {
				return err
			}
			store, err := sqlite.Open(dbCommandPath(cmd, state, rt))
			if err != nil {
				return err
			}
			defer store.Close()
			deleted, err := store.DeleteCluster(runCtx, args[0])
			if err != nil {
				return err
			}
			return writeJSON(stdout, map[string]any{
				"deleted": true,
				"cluster": deleted,
			})
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "Confirm deletion")
	return cmd
}

func dbCommandPath(cmd *cobra.Command, state *cliState, rt runtimeSettings) string {
	if dbPath := optionalDBPath(cmd, state, rt); dbPath != "" {
		return dbPath
	}
	return defaultSQLiteDBPath
}

func dbResourcesCommand(ctx context.Context, stdout io.Writer, state *cliState) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "resources",
		Short: "Inspect stored Kubernetes API resource metadata.",
	}
	cmd.AddCommand(dbResourcesCoverageCommand(ctx, stdout, state))
	cmd.AddCommand(dbResourcesHealthCommand(ctx, stdout, state))
	return cmd
}

func dbResourcesHealthCommand(ctx context.Context, stdout io.Writer, state *cliState) *cobra.Command {
	var opts sqlite.ResourceHealthOptions
	var output string
	var includeSkipped bool
	cmd := &cobra.Command{
		Use:   "health",
		Short: "Inspect per-resource watch and ingestion health.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateOutputFormat(output); err != nil {
				return err
			}
			rt, err := loadRuntimeConfig(cmd, state, "", false)
			if err != nil {
				return err
			}
			runCtx, _, err := runtimeContext(ctx, cmd.ErrOrStderr(), rt)
			if err != nil {
				return err
			}
			opts.ExcludeResources = rt.Config.Collection.Resources.Exclude
			opts.IncludeExcluded = includeSkipped
			store, err := sqlite.Open(dbCommandPath(cmd, state, rt))
			if err != nil {
				return err
			}
			defer store.Close()
			report, err := store.ResourceHealth(runCtx, opts)
			if err != nil {
				return err
			}
			if output == outputJSON {
				return writeJSON(stdout, report)
			}
			return writeResourceHealthTable(stdout, report)
		},
	}
	cmd.Flags().StringVar(&opts.ClusterID, "cluster", "", "Cluster ID")
	cmd.Flags().StringVar(&opts.Status, "status", "", "Filter by status, such as listed, bookmark, retrying, watch_error, list_error, or not_started")
	cmd.Flags().BoolVar(&opts.ErrorsOnly, "errors-only", false, "Show only resources with watch/list errors")
	cmd.Flags().BoolVar(&includeSkipped, "include-skipped", false, "Include resources skipped by collection.resources.exclude")
	cmd.Flags().DurationVar(&opts.StaleAfter, "stale-after", 0, "Mark rows stale when updated longer ago than this duration")
	cmd.Flags().IntVar(&opts.Limit, "limit", 100, "Maximum rows to return; 0 means no limit")
	addOutputFlag(cmd, &output, outputTable)
	return cmd
}

func dbResourcesCoverageCommand(ctx context.Context, stdout io.Writer, state *cliState) *cobra.Command {
	var limit int
	var output string
	cmd := &cobra.Command{
		Use:   "coverage",
		Short: "Compare live API discovery with stored API resource metadata.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateOutputFormat(output); err != nil {
				return err
			}
			rt, err := loadRuntimeConfig(cmd, state, "", false)
			if err != nil {
				return err
			}
			runCtx, _, err := runtimeContext(ctx, cmd.ErrOrStderr(), rt)
			if err != nil {
				return err
			}
			store, err := sqlite.Open(dbCommandPath(cmd, state, rt))
			if err != nil {
				return err
			}
			defer store.Close()
			apiInfos, err := store.APIResources(runCtx)
			if err != nil {
				return err
			}
			dbResources := apiInfosToResources(apiInfos)
			selection := collectionFromRuntime(cmd, state, rt)
			contexts, err := selectedContexts(runCtx, selection)
			if err != nil {
				return err
			}
			report := dbResourceCoverageReport{
				CheckedAt:   time.Now().UTC(),
				DBResources: len(dbResources),
			}
			err = withKubeconfig(rt.Kubeconfig, func() error {
				for _, kubeContext := range contexts {
					liveResources, err := discoverResources(runCtx, kubeContext, selection.UseClientGo)
					if err != nil {
						return err
					}
					contextReport := compareResourceCoverage(kubeContext, liveResources, dbResources)
					limitResourceCoverage(&contextReport, limit)
					report.Contexts = append(report.Contexts, contextReport)
				}
				return nil
			})
			if err != nil {
				return err
			}
			if output == outputJSON {
				return writeJSON(stdout, report)
			}
			return writeResourceCoverageTable(stdout, report)
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 50, "Maximum missing and extra resources to include per context; 0 means counts only")
	addOutputFlag(cmd, &output, outputTable)
	return cmd
}

type dbResourceCoverageReport struct {
	CheckedAt   time.Time                 `json:"checkedAt"`
	DBResources int                       `json:"dbResources"`
	Contexts    []dbResourceCoverageEntry `json:"contexts"`
}

type dbResourceCoverageEntry struct {
	Context       string               `json:"context"`
	LiveResources int                  `json:"liveResources"`
	Matched       int                  `json:"matched"`
	MissingCount  int                  `json:"missingCount"`
	ExtraCount    int                  `json:"extraCount"`
	Missing       []collector.Resource `json:"missing,omitempty"`
	Extra         []collector.Resource `json:"extra,omitempty"`
}

func compareResourceCoverage(kubeContext string, liveResources, dbResources []collector.Resource) dbResourceCoverageEntry {
	liveByKey := resourceCoverageMap(liveResources)
	dbByKey := resourceCoverageMap(dbResources)
	out := dbResourceCoverageEntry{
		Context:       kubeContext,
		LiveResources: len(liveByKey),
	}
	for key, liveResource := range liveByKey {
		if _, ok := dbByKey[key]; ok {
			out.Matched++
			continue
		}
		out.Missing = append(out.Missing, liveResource)
	}
	for key, dbResource := range dbByKey {
		if _, ok := liveByKey[key]; ok {
			continue
		}
		out.Extra = append(out.Extra, dbResource)
	}
	sortResourcesByCoverageKey(out.Missing)
	sortResourcesByCoverageKey(out.Extra)
	out.MissingCount = len(out.Missing)
	out.ExtraCount = len(out.Extra)
	return out
}

func limitResourceCoverage(report *dbResourceCoverageEntry, limit int) {
	if limit < 0 {
		return
	}
	if limit == 0 {
		report.Missing = nil
		report.Extra = nil
		return
	}
	if len(report.Missing) > limit {
		report.Missing = report.Missing[:limit]
	}
	if len(report.Extra) > limit {
		report.Extra = report.Extra[:limit]
	}
}

func resourceCoverageMap(resources []collector.Resource) map[string]collector.Resource {
	out := make(map[string]collector.Resource, len(resources))
	for _, resource := range resources {
		key := resourceCoverageKey(resource)
		if key == "" {
			continue
		}
		out[key] = resource
	}
	return out
}

func sortResourcesByCoverageKey(resources []collector.Resource) {
	sort.Slice(resources, func(i, j int) bool {
		return resourceCoverageKey(resources[i]) < resourceCoverageKey(resources[j])
	})
}

func resourceCoverageKey(resource collector.Resource) string {
	if resource.Resource == "" || resource.Version == "" {
		return ""
	}
	return resource.Group + "\x00" + resource.Version + "\x00" + resource.Resource
}

func writeCompactReportTable(stdout io.Writer, report sqlite.CompactReport) error {
	if err := writeSection(stdout, "Storage compaction", []string{"Metric", "Before", "After", "Delta"}, [][]string{
		{"Database files", humanBytes(report.BytesBefore), humanBytes(report.BytesAfter), humanBytes(report.BytesReclaimed) + " reclaimed"},
		{"Raw documents", humanBytes(report.StatsBefore.RawBytes), humanBytes(report.StatsAfter.RawBytes), humanBytes(report.StatsAfter.RawBytes - report.StatsBefore.RawBytes)},
		{"Tables", humanBytes(report.StatsBefore.TableBytes), humanBytes(report.StatsAfter.TableBytes), humanBytes(report.StatsAfter.TableBytes - report.StatsBefore.TableBytes)},
		{"Indexes", humanBytes(report.StatsBefore.IndexBytes), humanBytes(report.StatsAfter.IndexBytes), humanBytes(report.StatsAfter.IndexBytes - report.StatsBefore.IndexBytes)},
		{"Duration", humanDuration(report.StartedAt, report.FinishedAt), "", ""},
	}); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(stdout); err != nil {
		return err
	}
	rows := [][]string{
		{"Objects", humanCount(report.StatsAfter.Objects)},
		{"Deleted objects", humanCount(report.StatsAfter.DeletedObjects)},
		{"Versions", humanCount(report.StatsAfter.Versions)},
		{"Blobs", humanCount(report.StatsAfter.Blobs)},
		{"Facts", humanCount(report.StatsAfter.Facts)},
		{"Edges", humanCount(report.StatsAfter.Edges)},
		{"Changes", humanCount(report.StatsAfter.Changes)},
		{"Filter decisions", humanCount(report.StatsAfter.FilterDecisions)},
		{"Filter decision rollups", humanCount(report.StatsAfter.FilterDecisionRollups)},
		{"Filter decision rollup events", humanCount(report.StatsAfter.FilterDecisionRollupEvents)},
	}
	if report.Pruned != nil {
		rows = append(rows, []string{"Pruned unchanged versions", humanCount(report.Pruned.Versions)})
	}
	if report.FilterRollup != nil {
		rows = append(rows, []string{"Rolled up filter decisions", humanCount(report.FilterRollup.RowsRolledUp)})
	}
	if report.DeletedRawLatestRows > 0 {
		rows = append(rows, []string{"Deleted raw latest rows", humanCount(report.DeletedRawLatestRows)})
	}
	return writeSection(stdout, "Rows after compaction", []string{"Data", "Rows"}, rows)
}

func writeBackfillReportTable(stdout io.Writer, report sqlite.BackfillReport) error {
	if err := writeSection(stdout, "History backfill", []string{"Metric", "Value"}, [][]string{
		{"Mode", map[bool]string{true: "dry-run", false: "apply"}[report.DryRun]},
		{"Batches", humanCount(report.Batches)},
		{"Objects scanned", humanCount(report.Objects)},
		{"Versions scanned", humanCount(report.VersionsScanned)},
		{"Versions normalized", humanCount(report.VersionsUpdated)},
		{"Versions pruned", humanCount(report.VersionsPruned)},
		{"Database files before", humanBytes(report.BytesBefore)},
		{"Database files after", humanBytes(report.BytesAfter)},
		{"Reclaim estimate", humanBytes(report.BytesReclaimedEstimate)},
		{"Duration", humanDuration(report.StartedAt, report.FinishedAt)},
	}); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(stdout); err != nil {
		return err
	}
	return writeSection(stdout, "Changed rows", []string{"Data", "Rows"}, [][]string{
		{"Observations repointed", humanCount(report.ObservationsRepointed)},
		{"Facts deleted", humanCount(report.FactsDeleted)},
		{"Changes deleted", humanCount(report.ChangesDeleted)},
		{"Edges deleted", humanCount(report.EdgesDeleted)},
		{"Blobs deleted", humanCount(report.BlobsDeleted)},
	})
}

func writeRetentionReportTable(stdout io.Writer, report sqlite.RetentionReport) error {
	if err := writeSection(stdout, "Storage retention", []string{"Metric", "Value"}, [][]string{
		{"Cutoff", humanTime(report.Cutoff)},
		{"Filter decision cutoff", humanTime(report.FilterDecisionCutoff)},
		{"Database files before", humanBytes(report.BytesBefore)},
		{"Database files after", humanBytes(report.BytesAfter)},
		{"Reclaimed estimate", humanBytes(report.BytesReclaimedEstimate)},
		{"Duration", humanDuration(report.StartedAt, report.FinishedAt)},
		{"Skipped", boolText(report.Skipped)},
		{"Reason", report.Reason},
	}); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(stdout); err != nil {
		return err
	}
	if err := writeSection(stdout, "Deleted rows", []string{"Data", "Rows"}, [][]string{
		{"Versions", humanCount(report.Versions)},
		{"Observations", humanCount(report.Observations)},
		{"Facts", humanCount(report.Facts)},
		{"Changes", humanCount(report.Changes)},
		{"Edges", humanCount(report.Edges)},
		{"Blobs", humanCount(report.Blobs)},
		{"Filter decisions", humanCount(report.FilterDecisions)},
	}); err != nil {
		return err
	}
	if len(report.Rules) == 0 {
		return nil
	}
	if _, err := fmt.Fprintln(stdout); err != nil {
		return err
	}
	rows := make([][]string, 0, len(report.Rules))
	for _, rule := range report.Rules {
		rows = append(rows, []string{
			rule.Name,
			humanTime(rule.Cutoff),
			humanCount(rule.Versions),
			shortText(joinStrings(rule.Profiles), 32),
			shortText(joinStrings(rule.Resources), 48),
		})
	}
	return writeSection(stdout, "Retention rules", []string{"Rule", "Cutoff", "Selected", "Profiles", "Resources"}, rows)
}

func writeClustersTable(stdout io.Writer, clusters []storage.ClusterRecord) error {
	rows := make([][]string, 0, len(clusters))
	for _, cluster := range clusters {
		rows = append(rows, []string{
			shortText(cluster.Name, 40),
			shortText(cluster.Source, 24),
			humanTime(cluster.CreatedAt),
			humanCount(cluster.Objects),
			humanCount(cluster.Versions),
			humanCount(cluster.Latest),
		})
	}
	return writeTable(stdout, []string{"Cluster", "Source", "Created", "Objects", "Versions", "Latest"}, rows)
}

func writeResourceHealthTable(stdout io.Writer, report sqlite.ResourceHealthReport) error {
	summary := report.Summary
	if err := writeSection(stdout, "Resource health summary", []string{"Resources", "Healthy", "Queued", "Unstable", "Errors", "Stale", "Not Started", "Skipped", "Complete"}, [][]string{{
		humanCount(int64(summary.Resources)),
		humanCount(int64(summary.Healthy)),
		humanCount(int64(summary.Queued)),
		humanCount(int64(summary.Unstable)),
		humanCount(int64(summary.Errors)),
		humanCount(int64(summary.Stale)),
		humanCount(int64(summary.NotStarted)),
		humanCount(int64(summary.Skipped)),
		boolText(summary.Complete),
	}}); err != nil {
		return err
	}
	if len(report.Summary.Warnings) > 0 {
		if _, err := fmt.Fprintf(stdout, "Warnings: %s\n", joinWarnings(report.Summary.Warnings)); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(stdout); err != nil {
		return err
	}
	rows := make([][]string, 0, len(report.Resources))
	for _, record := range report.Resources {
		rows = append(rows, []string{
			shortText(record.ClusterID, 24),
			compactResourceName(record.Group, record.Version, record.Resource),
			record.Namespace,
			record.Status,
			shortText(record.Error, 80),
			humanOptionalTime(record.UpdatedAt),
			humanCount(int64(record.LatestObjects)),
		})
	}
	return writeSection(stdout, "Resources", []string{"Cluster", "Resource", "Namespace", "Status", "Error", "Updated", "Latest"}, rows)
}

func writeResourceCoverageTable(stdout io.Writer, report dbResourceCoverageReport) error {
	rows := make([][]string, 0, len(report.Contexts))
	for _, contextReport := range report.Contexts {
		rows = append(rows, []string{
			contextReport.Context,
			humanCount(int64(contextReport.LiveResources)),
			humanCount(int64(report.DBResources)),
			humanCount(int64(contextReport.Matched)),
			humanCount(int64(contextReport.MissingCount)),
			humanCount(int64(contextReport.ExtraCount)),
		})
	}
	if err := writeSection(stdout, "Resource coverage", []string{"Context", "Live", "Stored", "Matched", "Missing", "Extra"}, rows); err != nil {
		return err
	}
	for _, contextReport := range report.Contexts {
		if len(contextReport.Missing) == 0 && len(contextReport.Extra) == 0 {
			continue
		}
		if _, err := fmt.Fprintf(stdout, "\n%s details\n", contextReport.Context); err != nil {
			return err
		}
		detailRows := make([][]string, 0, len(contextReport.Missing)+len(contextReport.Extra))
		for _, resource := range contextReport.Missing {
			detailRows = append(detailRows, []string{"missing", compactResourceName(resource.Group, resource.Version, resource.Resource), resource.Kind})
		}
		for _, resource := range contextReport.Extra {
			detailRows = append(detailRows, []string{"extra", compactResourceName(resource.Group, resource.Version, resource.Resource), resource.Kind})
		}
		if err := writeTable(stdout, []string{"Type", "Resource", "Kind"}, detailRows); err != nil {
			return err
		}
	}
	return nil
}

func joinWarnings(warnings []string) string {
	return joinStrings(warnings)
}

func joinStrings(values []string) string {
	out := ""
	for i, value := range values {
		if i > 0 {
			out += "; "
		}
		out += value
	}
	return out
}
