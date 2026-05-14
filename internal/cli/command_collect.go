package cli

import (
	"context"
	"fmt"
	"io"
	"time"

	"kube-insight/internal/collector"
	"kube-insight/internal/samplegen"
	"kube-insight/internal/storage"
	"kube-insight/internal/storage/sqlite"

	"github.com/spf13/cobra"
)

func ingestCommand(ctx context.Context, stdout io.Writer, state *cliState) *cobra.Command {
	var file, dir string
	cmd := &cobra.Command{
		Use:   "ingest",
		Short: "Ingest Kubernetes JSON from a file or directory.",
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := loadRuntimeConfig(cmd, state, "", false)
			if err != nil {
				return err
			}
			if err := requireWriteRole(rt.Config, "ingest"); err != nil {
				return err
			}
			runCtx, _, err := runtimeContext(ctx, cmd.ErrOrStderr(), rt)
			if err != nil {
				return err
			}
			if (file == "") == (dir == "") {
				return fmt.Errorf("ingest requires exactly one of --file or --dir")
			}
			store := storage.Store(storage.NewMemoryStore())
			if dbPath := optionalDBPath(cmd, state, rt); dbPath != "" {
				sqliteStore, err := sqlite.Open(dbPath)
				if err != nil {
					return err
				}
				defer sqliteStore.Close()
				store = sqliteStore
			}
			summary, err := ingestInputs(runCtx, store, file, dir)
			if err != nil {
				return err
			}
			return writeJSON(stdout, summary)
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "", "JSON file to ingest")
	cmd.Flags().StringVarP(&dir, "dir", "d", "", "Directory of JSON files to ingest")
	return cmd
}

func collectCommand(ctx context.Context, stdout io.Writer, state *cliState) *cobra.Command {
	cmd := &cobra.Command{Use: "collect", Short: "Collect Kubernetes resources."}
	cmd.AddCommand(collectSamplesCommand(ctx, stdout, state))
	cmd.AddCommand(collectIngestCommand(ctx, stdout, state))
	return cmd
}

func collectSamplesCommand(ctx context.Context, stdout io.Writer, state *cliState) *cobra.Command {
	var flags collectFlags
	cmd := &cobra.Command{
		Use:   "samples",
		Short: "Collect sanitized Kubernetes sample JSON.",
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := loadRuntimeConfig(cmd, state, "", false)
			if err != nil {
				return err
			}
			if err := requireWriteRole(rt.Config, "collect samples"); err != nil {
				return err
			}
			runCtx, _, err := runtimeContext(ctx, cmd.ErrOrStderr(), rt)
			if err != nil {
				return err
			}
			opts, _, err := buildSampleOptions(runCtx, cmd, state, rt, flags)
			if err != nil {
				return err
			}
			var manifest collector.SampleManifest
			err = withKubeconfig(rt.Kubeconfig, func() error {
				var collectErr error
				manifest, collectErr = collector.CollectSamples(runCtx, opts)
				return collectErr
			})
			if err != nil {
				return err
			}
			return writeJSON(stdout, manifest)
		},
	}
	addCollectFlags(cmd, &flags)
	return cmd
}

func collectIngestCommand(ctx context.Context, stdout io.Writer, state *cliState) *cobra.Command {
	var flags collectFlags
	cmd := &cobra.Command{
		Use:   "ingest",
		Short: "Collect samples and ingest them into storage.",
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := loadRuntimeConfig(cmd, state, "", false)
			if err != nil {
				return err
			}
			if err := requireWriteRole(rt.Config, "collect ingest"); err != nil {
				return err
			}
			runCtx, _, err := runtimeContext(ctx, cmd.ErrOrStderr(), rt)
			if err != nil {
				return err
			}
			dbPath, err := requiredDBPath(cmd, state, rt, "collect ingest")
			if err != nil {
				return err
			}
			opts, _, err := buildSampleOptions(runCtx, cmd, state, rt, flags)
			if err != nil {
				return err
			}
			if opts.OutputDir == "" {
				opts.OutputDir = "testdata/generated/collect-ingest"
			}
			sqliteStore, err := sqlite.Open(dbPath)
			if err != nil {
				return err
			}
			defer sqliteStore.Close()

			var manifest collector.SampleManifest
			err = withKubeconfig(rt.Kubeconfig, func() error {
				var collectErr error
				manifest, collectErr = collector.CollectSamples(runCtx, opts)
				return collectErr
			})
			if err != nil {
				return err
			}
			summary, err := ingestInputs(runCtx, sqliteStore, "", opts.OutputDir)
			if err != nil {
				return err
			}
			return writeJSON(stdout, struct {
				Manifest collector.SampleManifest `json:"manifest"`
				Summary  any                      `json:"summary"`
			}{Manifest: manifest, Summary: summary})
		},
	}
	addCollectFlags(cmd, &flags)
	return cmd
}

func discoverCommand(ctx context.Context, stdout io.Writer, state *cliState) *cobra.Command {
	cmd := &cobra.Command{Use: "discover", Short: "Discover Kubernetes API metadata."}
	resources := &cobra.Command{
		Use:   "resources",
		Short: "Discover listable API resources and store them.",
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := loadRuntimeConfig(cmd, state, "", false)
			if err != nil {
				return err
			}
			if err := requireWriteRole(rt.Config, "discover resources"); err != nil {
				return err
			}
			runCtx, _, err := runtimeContext(ctx, cmd.ErrOrStderr(), rt)
			if err != nil {
				return err
			}
			dbPath, err := requiredDBPath(cmd, state, rt, "discover resources")
			if err != nil {
				return err
			}
			selection := collectionFromRuntime(cmd, state, rt)
			contexts, err := selectedContexts(runCtx, selection)
			if err != nil {
				return err
			}
			store, err := sqlite.Open(dbPath)
			if err != nil {
				return err
			}
			defer store.Close()

			type contextSummary struct {
				Context   string `json:"context"`
				Resources int    `json:"resources"`
				Error     string `json:"error,omitempty"`
			}
			out := struct {
				DiscoveredAt time.Time        `json:"discoveredAt"`
				Contexts     []contextSummary `json:"contexts"`
				Resources    int              `json:"resources"`
			}{DiscoveredAt: time.Now().UTC()}
			seen := map[string]collector.Resource{}
			err = withKubeconfig(rt.Kubeconfig, func() error {
				for _, kubeContext := range contexts {
					resources, discoverErr := discoverResources(runCtx, kubeContext, selection.UseClientGo)
					if discoverErr != nil {
						out.Contexts = append(out.Contexts, contextSummary{Context: kubeContext, Error: discoverErr.Error()})
						continue
					}
					for _, resource := range resources {
						seen[collectorResourceKey(resource)] = resource
					}
					out.Contexts = append(out.Contexts, contextSummary{Context: kubeContext, Resources: len(resources)})
				}
				return nil
			})
			if err != nil {
				return err
			}
			infos := resourcesToAPIInfos(resourcesFromMap(seen))
			if err := store.UpsertAPIResources(runCtx, infos, out.DiscoveredAt); err != nil {
				return err
			}
			out.Resources = len(infos)
			return writeJSON(stdout, out)
		},
	}
	cmd.AddCommand(resources)
	return cmd
}

func generateCommand(ctx context.Context, stdout io.Writer) *cobra.Command {
	cmd := &cobra.Command{Use: "generate", Short: "Generate deterministic test data."}
	var opts samplegen.Options
	samples := &cobra.Command{
		Use:   "samples",
		Short: "Generate Kubernetes sample JSON from fixtures.",
		RunE: func(cmd *cobra.Command, args []string) error {
			manifest, err := samplegen.Generate(ctx, opts)
			if err != nil {
				return err
			}
			return writeJSON(stdout, manifest)
		},
	}
	samples.Flags().StringVar(&opts.FixturesDir, "fixtures", "", "Fixture directory")
	samples.Flags().StringVarP(&opts.OutputDir, "output", "o", "", "Output directory")
	samples.Flags().IntVar(&opts.Clusters, "clusters", 0, "Number of synthetic clusters")
	samples.Flags().IntVar(&opts.Copies, "copies", 0, "Copies per fixture scenario")
	cmd.AddCommand(samples)
	return cmd
}

func devCommand(ctx context.Context, stdout io.Writer, state *cliState) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dev",
		Short: "Development, test-data, and validation tools.",
	}
	cmd.AddCommand(ingestCommand(ctx, stdout, state))
	cmd.AddCommand(collectCommand(ctx, stdout, state))
	cmd.AddCommand(discoverCommand(ctx, stdout, state))
	cmd.AddCommand(generateCommand(ctx, stdout))
	cmd.AddCommand(benchmarkCommand(ctx, stdout, state))
	cmd.AddCommand(validateCommand(ctx, stdout, state))
	return cmd
}

func addCollectFlags(cmd *cobra.Command, flags *collectFlags) {
	cmd.Flags().StringVarP(&flags.OutputDir, "output", "o", "", "Output directory")
	cmd.Flags().BoolVar(&flags.DiscoverResources, "discover-resources", false, "Discover listable API resources before collecting")
	cmd.Flags().BoolVar(&flags.UseDBResources, "use-db-resources", false, "Use API resources already stored in --db")
	cmd.Flags().StringArrayVar(&flags.Resources, "resource", nil, "Resource to collect, for example pods or deployments.apps; repeatable")
	cmd.Flags().IntVar(&flags.MaxItems, "max-items", 0, "Maximum items retained per resource")
}

func buildSampleOptions(ctx context.Context, cmd *cobra.Command, state *cliState, rt runtimeSettings, flags collectFlags) (collector.SampleOptions, string, error) {
	selection := collectionFromRuntime(cmd, state, rt)
	resources, err := resourcesFromConfig(flags.Resources, rt.Config)
	if err != nil {
		return collector.SampleOptions{}, "", err
	}
	resources = applyResourceExcludes(resources, rt.Config.Collection.Resources.Exclude)
	dbPath := optionalDBPath(cmd, state, rt)
	opts := collector.SampleOptions{
		OutputDir:         flags.OutputDir,
		Contexts:          selection.Contexts,
		AllContexts:       selection.AllContexts,
		Namespace:         selection.Namespace,
		DiscoverResources: flags.DiscoverResources || effectiveResourceAll(rt),
		UseClientGo:       selection.UseClientGo,
		Resources:         resources,
		MaxItems:          flags.MaxItems,
	}
	if len(opts.Resources) == 0 && !opts.DiscoverResources && len(rt.Config.Collection.Resources.Exclude) > 0 {
		opts.Resources = applyResourceExcludes(collector.DefaultResources(), rt.Config.Collection.Resources.Exclude)
	}
	if flags.UseDBResources && dbPath == "" {
		return opts, "", fmt.Errorf("--use-db-resources requires --db")
	}
	if dbPath == "" {
		return opts, dbPath, nil
	}
	sqliteStore, err := sqlite.Open(dbPath)
	if err != nil {
		return opts, "", err
	}
	defer sqliteStore.Close()
	if opts.DiscoverResources {
		contexts, err := collectContexts(ctx, opts)
		if err != nil {
			return opts, "", err
		}
		discovered, err := discoverAndStoreAPIResources(ctx, sqliteStore, contexts, opts.UseClientGo)
		if err != nil {
			return opts, "", err
		}
		discovered = applyResourceExcludes(discovered, rt.Config.Collection.Resources.Exclude)
		if len(resources) == 0 {
			opts.Resources = discovered
		} else {
			opts.Resources = enrichCollectorResources(resources, discovered)
		}
		opts.DiscoverResources = false
	}
	if flags.UseDBResources {
		infos, err := sqliteStore.APIResources(ctx)
		if err != nil {
			return opts, "", err
		}
		dbResources := applyResourceExcludes(apiInfosToResources(infos), rt.Config.Collection.Resources.Exclude)
		if len(dbResources) == 0 {
			return opts, "", fmt.Errorf("no listable API resources found in --db")
		}
		if len(resources) == 0 {
			opts.Resources = dbResources
		} else {
			opts.Resources = enrichCollectorResources(resources, dbResources)
		}
		opts.DiscoverResources = false
	}
	return opts, dbPath, nil
}
