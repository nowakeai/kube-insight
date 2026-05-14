package cli

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"

	"kube-insight/internal/benchmark"
	"kube-insight/internal/collector"
	"kube-insight/internal/storage/sqlite"

	"github.com/spf13/cobra"
)

func benchmarkCommand(ctx context.Context, stdout io.Writer, state *cliState) *cobra.Command {
	cmd := &cobra.Command{Use: "benchmark", Short: "Run local and watch benchmarks."}
	cmd.AddCommand(benchmarkLocalCommand(ctx, stdout, state))
	cmd.AddCommand(benchmarkWatchCommand(ctx, stdout, state))
	return cmd
}

func benchmarkLocalCommand(ctx context.Context, stdout io.Writer, state *cliState) *cobra.Command {
	var opts benchmark.Options
	cmd := &cobra.Command{
		Use:   "local",
		Short: "Benchmark generated local ingestion and queries.",
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := loadRuntimeConfig(cmd, state, "", false)
			if err != nil {
				return err
			}
			opts.DBPath = dbPathForOptions(cmd, state, rt, opts.DBPath)
			runCtx, _, err := runtimeContext(ctx, cmd.ErrOrStderr(), rt)
			if err != nil {
				return err
			}
			report, err := benchmark.RunLocal(runCtx, opts)
			if err != nil {
				return err
			}
			return writeJSON(stdout, report)
		},
	}
	cmd.Flags().StringVar(&opts.FixturesDir, "fixtures", "", "Fixture directory")
	cmd.Flags().StringVarP(&opts.OutputDir, "output", "o", "", "Output directory")
	cmd.Flags().IntVar(&opts.Clusters, "clusters", 0, "Number of synthetic clusters")
	cmd.Flags().IntVar(&opts.Copies, "copies", 0, "Copies per fixture scenario")
	cmd.Flags().IntVar(&opts.QueryRuns, "query-runs", 0, "Query repetitions")
	return cmd
}

func benchmarkWatchCommand(ctx context.Context, stdout io.Writer, state *cliState) *cobra.Command {
	var opts benchmark.WatchOptions
	var resources []string
	cmd := &cobra.Command{
		Use:   "watch",
		Short: "Benchmark Kubernetes watch ingestion.",
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := loadRuntimeConfig(cmd, state, "", false)
			if err != nil {
				return err
			}
			runCtx, _, err := runtimeContext(ctx, cmd.ErrOrStderr(), rt)
			if err != nil {
				return err
			}
			selection := collectionFromRuntime(cmd, state, rt)
			opts.Context = singleContext(selection)
			opts.Namespace = selection.Namespace
			opts.Concurrency = selection.Concurrency
			opts.DBPath, err = requiredDBPath(cmd, state, rt, "benchmark watch")
			if err != nil {
				return err
			}
			opts.DiscoverResources = opts.DiscoverResources || effectiveResourceAll(rt)
			opts.Resources, err = resourcesFromConfig(resources, rt.Config)
			if err != nil {
				return err
			}
			opts.Resources = applyResourceExcludes(opts.Resources, rt.Config.Collection.Resources.Exclude)
			if len(opts.Resources) == 0 && !opts.DiscoverResources {
				return fmt.Errorf("benchmark watch requires --resource or --discover-resources")
			}
			var report benchmark.Report
			err = withKubeconfig(rt.Kubeconfig, func() error {
				var runErr error
				report, runErr = benchmark.RunWatch(runCtx, opts)
				return runErr
			})
			if err != nil {
				return err
			}
			return writeJSON(stdout, report)
		},
	}
	cmd.Flags().StringArrayVar(&resources, "resource", nil, "Resource to watch; repeatable")
	cmd.Flags().BoolVar(&opts.DiscoverResources, "discover-resources", false, "Discover watchable resources")
	cmd.Flags().DurationVar(&opts.Duration, "duration", 0, "Watch duration")
	cmd.Flags().DurationVar(&opts.Duration, "timeout", 0, "Alias for --duration")
	cmd.Flags().IntVar(&opts.MaxRetries, "retries", -1, "Maximum watch retries; -1 uses command default")
	cmd.Flags().IntVar(&opts.MaxEvents, "max-events", 0, "Stop after N events")
	return cmd
}

func validateCommand(ctx context.Context, stdout io.Writer, state *cliState) *cobra.Command {
	cmd := &cobra.Command{Use: "validate", Short: "Run acceptance validations."}
	var opts benchmark.ValidationOptions
	poc := &cobra.Command{
		Use:   "poc",
		Short: "Run PoC acceptance validation.",
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := loadRuntimeConfig(cmd, state, "", false)
			if err != nil {
				return err
			}
			runCtx, _, err := runtimeContext(ctx, cmd.ErrOrStderr(), rt)
			if err != nil {
				return err
			}
			opts.DBPath = dbPathForOptions(cmd, state, rt, opts.DBPath)
			applyValidationConfig(cmd, rt.Config, &opts)
			report, err := benchmark.RunPoCValidation(runCtx, opts)
			if err != nil {
				return err
			}
			if err := writeJSON(stdout, report); err != nil {
				return err
			}
			if !report.Passed {
				return fmt.Errorf("poc validation failed")
			}
			return nil
		},
	}
	poc.Flags().StringVar(&opts.FixturesDir, "fixtures", "", "Fixture directory")
	poc.Flags().StringVarP(&opts.OutputDir, "output", "o", "", "Output directory")
	poc.Flags().IntVar(&opts.Clusters, "clusters", 0, "Number of synthetic clusters")
	poc.Flags().IntVar(&opts.Copies, "copies", 0, "Copies per fixture scenario")
	poc.Flags().IntVar(&opts.QueryRuns, "query-runs", 0, "Query repetitions")
	poc.Flags().Float64Var(&opts.MaxStoredToRawRatio, "max-stored-to-raw-ratio", 0, "Maximum stored/raw ratio")
	poc.Flags().Float64Var(&opts.MaxLatestLookupP95MS, "max-latest-lookup-p95-ms", 0, "Maximum latest lookup p95 latency")
	poc.Flags().Float64Var(&opts.MaxHistoricalGetP95MS, "max-historical-get-p95-ms", 0, "Maximum historical get p95 latency")
	poc.Flags().Float64Var(&opts.MaxServiceInvestigationP95MS, "max-service-investigation-p95-ms", 0, "Maximum service investigation p95 latency")
	poc.Flags().IntVar(&opts.MinServiceVersions, "min-service-versions", 0, "Minimum service versions")
	poc.Flags().IntVar(&opts.MinServiceDiffs, "min-service-diffs", 0, "Minimum service diffs")
	cmd.AddCommand(poc)
	return cmd
}

func watchCommand(ctx context.Context, stdout, stderr io.Writer, state *cliState) *cobra.Command {
	var opts collector.WatchResourcesOptions
	cmd := &cobra.Command{
		Use:   "watch [RESOURCE_PATTERN ...]",
		Short: "Watch Kubernetes resources into storage.",
		Long: strings.TrimSpace(`
Watch Kubernetes resources into storage.

Without resource patterns, kube-insight discovers and watches all watchable
resources. Positional patterns can be resource names such as pods or
deployments.apps, or group/version wildcards such as v1/* and apps/v1/*.

If --db is not set by flag, environment, or config, watch writes to
./kubeinsight.db.

By default, watch uses the current kubeconfig context and runs until it is
interrupted. Use --all-contexts to watch every configured context.
`),
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWatchResourcesCommand(ctx, stdout, stderr, state, cmd, args, opts, "watch")
		},
	}
	cmd.Flags().IntVar(&opts.MaxEvents, "max-events", 0, "Stop after N events")
	cmd.Flags().IntVar(&opts.MaxRetries, "retries", -1, "Maximum watch retries; -1 retries forever")
	cmd.Flags().DurationVar(&opts.Timeout, "timeout", 0, "Watch timeout; 0 runs until interrupted")
	cmd.AddCommand(watchResourceCommand(ctx, stdout, stderr, state))
	cmd.AddCommand(watchResourcesCommand(ctx, stdout, stderr, state))
	return cmd
}

func watchResourceCommand(ctx context.Context, stdout, stderr io.Writer, state *cliState) *cobra.Command {
	var resource string
	var opts collector.WatchOptions
	cmd := &cobra.Command{
		Use:    "resource",
		Short:  "Watch one resource.",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := loadRuntimeConfig(cmd, state, "", false)
			if err != nil {
				return err
			}
			if err := requireWriteRole(rt.Config, "watch resource"); err != nil {
				return err
			}
			selection := collectionFromRuntime(cmd, state, rt)
			if !selection.UseClientGo {
				return fmt.Errorf("watch resource requires --client-go or collection.useClientGo=true")
			}
			resources, err := collector.ParseResources([]string{resource})
			if err != nil {
				return err
			}
			if len(resources) != 1 {
				return fmt.Errorf("watch resource requires exactly one resource")
			}
			dbPath, err := watchDBPath(ctx, cmd, state, rt, selection)
			if err != nil {
				return err
			}
			runCtx, logger, err := runtimeContext(ctx, stderr, rt)
			if err != nil {
				return err
			}
			opts.Logf = newWatchLogf(logger)
			opts.Logf("using db", "db", dbPath)
			sqliteStore, err := sqlite.Open(dbPath)
			if err != nil {
				return err
			}
			defer sqliteStore.Close()
			opts.Context = singleContext(selection)
			opts.Namespace = selection.Namespace
			opts.Resource = resources[0]
			opts.Store = sqliteStore
			var summary collector.WatchSummary
			err = withKubeconfig(rt.Kubeconfig, func() error {
				var watchErr error
				summary, watchErr = collector.WatchResourceClientGo(runCtx, opts)
				return watchErr
			})
			if err != nil {
				return err
			}
			return writeJSON(stdout, summary)
		},
	}
	cmd.Flags().StringVar(&resource, "resource", "", "Resource to watch")
	cmd.Flags().IntVar(&opts.MaxEvents, "max-events", 0, "Stop after N events")
	cmd.Flags().IntVar(&opts.MaxRetries, "retries", -1, "Maximum watch retries; -1 retries forever")
	cmd.Flags().DurationVar(&opts.Timeout, "timeout", 0, "Watch timeout; 0 runs until interrupted")
	return cmd
}

func watchResourcesCommand(ctx context.Context, stdout, stderr io.Writer, state *cliState) *cobra.Command {
	var opts collector.WatchResourcesOptions
	var resources []string
	cmd := &cobra.Command{
		Use:    "resources",
		Short:  "Watch several resources.",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWatchResourcesCommand(ctx, stdout, stderr, state, cmd, resources, opts, "watch resources")
		},
	}
	cmd.Flags().StringArrayVar(&resources, "resource", nil, "Resource to watch; repeatable")
	cmd.Flags().BoolVar(&opts.DiscoverResources, "discover-resources", false, "Discover watchable resources")
	cmd.Flags().IntVar(&opts.MaxEvents, "max-events", 0, "Stop after N events")
	cmd.Flags().IntVar(&opts.MaxRetries, "retries", -1, "Maximum watch retries; -1 retries forever")
	cmd.Flags().DurationVar(&opts.Timeout, "timeout", 0, "Watch timeout; 0 runs until interrupted")
	return cmd
}

type watchContextResult struct {
	Context string                           `json:"context"`
	DBPath  string                           `json:"dbPath"`
	Summary *collector.WatchResourcesSummary `json:"summary,omitempty"`
	Error   string                           `json:"error,omitempty"`
}

type watchContextsSummary struct {
	Contexts  int                  `json:"contexts"`
	Completed int                  `json:"completed"`
	Errors    int                  `json:"errors"`
	Results   []watchContextResult `json:"results"`
}

func runWatchResourcesCommand(ctx context.Context, stdout, stderr io.Writer, state *cliState, cmd *cobra.Command, resourceArgs []string, opts collector.WatchResourcesOptions, commandName string) error {
	rt, err := loadRuntimeConfig(cmd, state, "", false)
	if err != nil {
		return err
	}
	if err := requireWriteRole(rt.Config, commandName); err != nil {
		return err
	}
	selection := collectionFromRuntime(cmd, state, rt)
	selection.UseClientGo = true
	opts.Namespace = selection.Namespace
	opts.Concurrency = selection.Concurrency
	runCtx, logger, err := runtimeContext(ctx, stderr, rt)
	if err != nil {
		return err
	}
	opts.Logf = newWatchLogf(logger)
	return withKubeconfig(rt.Kubeconfig, func() error {
		contexts, err := selectedContexts(runCtx, selection)
		if err != nil {
			return err
		}
		if len(contexts) == 0 {
			return fmt.Errorf("watch found no kubeconfig contexts")
		}
		if selection.AllContexts {
			opts.Logf("selected all contexts", "count", len(contexts))
		} else {
			opts.Logf("selected context", "context", contexts[0])
		}
		if len(contexts) == 1 {
			summary, _, err := runWatchContext(runCtx, cmd, state, rt, selection, contexts[0], resourceArgs, opts, nil, "")
			if err != nil {
				return err
			}
			return writeJSON(stdout, summary)
		}
		summary, err := runWatchContexts(runCtx, cmd, state, rt, selection, contexts, resourceArgs, opts)
		if writeErr := writeJSON(stdout, summary); writeErr != nil {
			return writeErr
		}
		if err != nil && ctx.Err() == nil {
			return err
		}
		return nil
	})
}

func runWatchContexts(ctx context.Context, cmd *cobra.Command, state *cliState, rt runtimeSettings, selection collectionSettings, contexts []string, resourceArgs []string, opts collector.WatchResourcesOptions) (watchContextsSummary, error) {
	out := watchContextsSummary{
		Contexts: len(contexts),
		Results:  make([]watchContextResult, len(contexts)),
	}
	var sharedStore *sqlite.Store
	sharedDBPath := optionalDBPath(cmd, state, rt)
	if sharedDBPath == "" {
		sharedDBPath = defaultSQLiteDBPath
	}
	var err error
	sharedStore, err = sqlite.Open(sharedDBPath)
	if err != nil {
		return out, err
	}
	defer sharedStore.Close()
	opts.Logf("using shared db", "db", sharedDBPath)

	var wg sync.WaitGroup
	for i, kubeContext := range contexts {
		i := i
		kubeContext := kubeContext
		wg.Add(1)
		go func() {
			defer wg.Done()
			summary, dbPath, err := runWatchContext(ctx, cmd, state, rt, selection, kubeContext, resourceArgs, opts, sharedStore, sharedDBPath)
			result := watchContextResult{Context: kubeContext, DBPath: dbPath}
			if err != nil {
				result.Error = err.Error()
				opts.Logf("context error", "context", kubeContext, "error", err)
			} else {
				result.Summary = &summary
			}
			out.Results[i] = result
		}()
	}
	wg.Wait()
	for _, result := range out.Results {
		if result.Error != "" {
			out.Errors++
			continue
		}
		out.Completed++
	}
	if out.Errors > 0 {
		return out, fmt.Errorf("watch failed in %d context(s)", out.Errors)
	}
	return out, nil
}

func runWatchContext(ctx context.Context, cmd *cobra.Command, state *cliState, rt runtimeSettings, selection collectionSettings, kubeContext string, resourceArgs []string, opts collector.WatchResourcesOptions, sharedStore *sqlite.Store, sharedDBPath string) (collector.WatchResourcesSummary, string, error) {
	selection.Contexts = []string{kubeContext}
	selection.AllContexts = false
	opts.Context = kubeContext
	if len(resourceArgs) == 0 {
		opts.Logf("no resource patterns supplied; discovering all watchable resources", "context", kubeContext)
	} else {
		opts.Logf("resolving resource patterns", "context", kubeContext, "patterns", strings.Join(normalizeResourceArgs(resourceArgs), ","))
	}
	resources, discoverResources, err := watchResourcesFromArgs(ctx, selection, resourceArgs)
	if err != nil {
		return collector.WatchResourcesSummary{}, "", err
	}
	opts.Resources = applyResourceExcludes(resources, rt.Config.Collection.Resources.Exclude)
	opts.DiscoverResources = discoverResources

	dbPath := sharedDBPath
	if dbPath == "" {
		dbPath = watchDBPathForContext(cmd, state, rt, kubeContext)
	}
	opts.Logf("using db", "context", kubeContext, "db", dbPath)
	if sharedStore != nil {
		opts.Store = sharedStore
		summary, err := collector.WatchResourcesClientGo(ctx, opts)
		return summary, dbPath, err
	}

	sqliteStore, err := sqlite.Open(dbPath)
	if err != nil {
		return collector.WatchResourcesSummary{}, dbPath, err
	}
	defer sqliteStore.Close()
	opts.Store = sqliteStore
	summary, err := collector.WatchResourcesClientGo(ctx, opts)
	return summary, dbPath, err
}

func newWatchLogf(logger *slog.Logger) collector.WatchLogFunc {
	if logger == nil {
		return func(string, ...any) {}
	}
	logger = logger.With("component", "watch")
	return func(message string, args ...any) {
		logger.Info(message, args...)
	}
}

func watchDBPath(ctx context.Context, cmd *cobra.Command, state *cliState, rt runtimeSettings, selection collectionSettings) (string, error) {
	if dbPath := optionalDBPath(cmd, state, rt); dbPath != "" {
		return dbPath, nil
	}
	contextName := singleContext(selection)
	if contextName == "" {
		err := withKubeconfig(rt.Kubeconfig, func() error {
			var currentErr error
			contextName, currentErr = currentContext(ctx, true)
			return currentErr
		})
		if err != nil {
			return "", err
		}
	}
	return watchDBPathForContext(cmd, state, rt, contextName), nil
}

func watchDBPathForContext(cmd *cobra.Command, state *cliState, rt runtimeSettings, contextName string) string {
	if dbPath := optionalDBPath(cmd, state, rt); dbPath != "" {
		return dbPath
	}
	return defaultWatchDBPath(contextName)
}

func defaultWatchDBPath(contextName string) string {
	return defaultSQLiteDBPath
}

func watchResourcesFromArgs(ctx context.Context, selection collectionSettings, values []string) ([]collector.Resource, bool, error) {
	values = normalizeResourceArgs(values)
	if len(values) == 0 {
		return nil, true, nil
	}
	var exact []string
	var patterns []string
	for _, value := range values {
		if isResourcePattern(value) {
			patterns = append(patterns, value)
			continue
		}
		exact = append(exact, value)
	}
	var resources []collector.Resource
	if len(exact) > 0 {
		var err error
		resources, err = collector.ParseResources(exact)
		if err != nil {
			return nil, false, err
		}
	}
	if len(patterns) == 0 {
		return resources, false, nil
	}
	discovered, err := discoverResources(ctx, singleContext(selection), true)
	if err != nil {
		return nil, false, err
	}
	matched := matchResourcePatterns(discovered, patterns)
	if len(matched) == 0 {
		return nil, false, fmt.Errorf("no resources match %s", strings.Join(patterns, ", "))
	}
	return mergeCollectorResources(resources, matched), false, nil
}

func normalizeResourceArgs(values []string) []string {
	var out []string
	for _, value := range values {
		for _, item := range strings.Split(value, ",") {
			item = strings.TrimSpace(item)
			if item != "" {
				out = append(out, item)
			}
		}
	}
	return out
}

func isResourcePattern(value string) bool {
	return strings.Contains(value, "/") || strings.Contains(value, "*")
}

func matchResourcePatterns(resources []collector.Resource, patterns []string) []collector.Resource {
	var out []collector.Resource
	for _, resource := range resources {
		for _, pattern := range patterns {
			if resourceMatchesPattern(resource, pattern) {
				out = append(out, resource)
				break
			}
		}
	}
	return out
}

func resourceMatchesPattern(resource collector.Resource, pattern string) bool {
	parts := strings.Split(pattern, "/")
	var groupPattern, versionPattern, resourcePattern string
	switch len(parts) {
	case 2:
		groupPattern = ""
		versionPattern = parts[0]
		resourcePattern = parts[1]
	case 3:
		groupPattern = parts[0]
		versionPattern = parts[1]
		resourcePattern = parts[2]
	default:
		return false
	}
	return segmentMatches(groupPattern, resource.Group) &&
		segmentMatches(versionPattern, resource.Version) &&
		(segmentMatches(resourcePattern, resource.Resource) || segmentMatches(resourcePattern, resource.Name))
}

func segmentMatches(pattern, value string) bool {
	return pattern == "*" || pattern == value
}
