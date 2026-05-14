package cli

import (
	"context"
	"fmt"
	"io"
	"sort"
	"time"

	"kube-insight/internal/collector"
	"kube-insight/internal/storage/sqlite"

	"github.com/spf13/cobra"
)

func dbCommand(ctx context.Context, stdout io.Writer, state *cliState) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "db",
		Short: "Manage kube-insight storage.",
	}
	cmd.AddCommand(dbClustersCommand(ctx, stdout, state))
	cmd.AddCommand(dbCompactCommand(ctx, stdout, state))
	cmd.AddCommand(dbResourcesCommand(ctx, stdout, state))
	return cmd
}

func dbCompactCommand(ctx context.Context, stdout io.Writer, state *cliState) *cobra.Command {
	var pruneUnchanged bool
	cmd := &cobra.Command{
		Use:   "compact",
		Short: "Compact SQLite storage after migrations and deletes.",
		RunE: func(cmd *cobra.Command, args []string) error {
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
			return writeJSON(stdout, report)
		},
	}
	cmd.Flags().BoolVar(&pruneUnchanged, "prune-unchanged", false, "Prune duplicate retained versions after object observations are backfilled")
	return cmd
}

func dbClustersCommand(ctx context.Context, stdout io.Writer, state *cliState) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "clusters",
		Short: "List and clean stored clusters.",
		RunE: func(cmd *cobra.Command, args []string) error {
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
			return writeJSON(stdout, clusters)
		},
	}
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
	cmd := &cobra.Command{
		Use:   "health",
		Short: "Inspect per-resource watch and ingestion health.",
		RunE: func(cmd *cobra.Command, args []string) error {
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
			report, err := store.ResourceHealth(runCtx, opts)
			if err != nil {
				return err
			}
			return writeJSON(stdout, report)
		},
	}
	cmd.Flags().StringVar(&opts.ClusterID, "cluster", "", "Cluster ID")
	cmd.Flags().StringVar(&opts.Status, "status", "", "Filter by status, such as listed, bookmark, watch_error, list_error, or not_started")
	cmd.Flags().BoolVar(&opts.ErrorsOnly, "errors-only", false, "Show only resources with watch/list errors")
	cmd.Flags().DurationVar(&opts.StaleAfter, "stale-after", 0, "Mark rows stale when updated longer ago than this duration")
	cmd.Flags().IntVar(&opts.Limit, "limit", 100, "Maximum rows to return; 0 means no limit")
	return cmd
}

func dbResourcesCoverageCommand(ctx context.Context, stdout io.Writer, state *cliState) *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:   "coverage",
		Short: "Compare live API discovery with stored API resource metadata.",
		RunE: func(cmd *cobra.Command, args []string) error {
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
			return writeJSON(stdout, report)
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 50, "Maximum missing and extra resources to include per context; 0 means counts only")
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
