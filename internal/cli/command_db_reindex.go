package cli

import (
	"context"
	"encoding/json"
	"io"

	"kube-insight/internal/core"
	"kube-insight/internal/extractor"
	"kube-insight/internal/ingest"
	"kube-insight/internal/kubeapi"
	"kube-insight/internal/resourceprofile"
	"kube-insight/internal/storage/sqlite"

	"github.com/spf13/cobra"
)

func dbReindexCommand(ctx context.Context, stdout io.Writer, state *cliState) *cobra.Command {
	var yes bool
	var batchObjects int
	var output string
	cmd := &cobra.Command{
		Use:   "reindex",
		Short: "Rebuild derived facts, edges, and changes from retained JSON.",
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
			rules := rt.Config.ProfileRules()
			store, err := sqlite.OpenWithOptions(dbCommandPath(cmd, state, rt), sqlite.Options{ProfileRules: rules})
			if err != nil {
				return err
			}
			defer store.Close()

			registry, err := ingest.ExtractorRegistryFromProcessing(rt.Config.Processing)
			if err != nil {
				return err
			}
			resolver, err := reindexResolver(runCtx, store)
			if err != nil {
				return err
			}
			report, err := store.ReindexEvidence(runCtx, sqlite.ReindexOptions{
				DryRun:       !yes,
				BatchObjects: batchObjects,
				Evidence: func(ctx context.Context, version sqlite.BackfillVersion) (extractor.Evidence, error) {
					var object map[string]any
					if err := json.Unmarshal(version.Document, &object); err != nil {
						return extractor.Evidence{}, err
					}
					resolver.RegisterFromObject(object)
					extractor.RegisterAPIObject(object)
					obs := reindexObservation(version, object)
					profile := resourceprofile.Select(kubeapi.ResourceInfo{
						Group:      obs.Ref.Group,
						Version:    obs.Ref.Version,
						Resource:   obs.Ref.Resource,
						Kind:       obs.Ref.Kind,
						Namespaced: obs.Ref.Namespace != "",
					}, rules)
					return registry.ExtractSet(extractor.WithResolver(ctx, resolver), obs, profile.ExtractorSet)
				},
			})
			if err != nil {
				return err
			}
			if output == outputJSON {
				return writeJSON(stdout, report)
			}
			return writeReindexReportTable(stdout, report)
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "Apply changes; without --yes this is a dry run")
	cmd.Flags().IntVar(&batchObjects, "batch-objects", 500, "Objects per reindex transaction")
	addOutputFlag(cmd, &output, outputTable)
	return cmd
}

func reindexResolver(ctx context.Context, store *sqlite.Store) (*kubeapi.Resolver, error) {
	resolver := kubeapi.NewResolver()
	resources, err := store.APIResources(ctx)
	if err != nil {
		return nil, err
	}
	for _, resource := range resources {
		resolver.Register(resource)
	}
	return resolver, nil
}

func reindexObservation(version sqlite.BackfillVersion, object map[string]any) core.Observation {
	typ := version.ObservationType
	if typ == "" {
		typ = core.ObservationModified
	}
	return core.Observation{
		Type:            typ,
		ObservedAt:      version.ObservedAt,
		ResourceVersion: version.ResourceVersion,
		Ref:             version.Ref,
		Object:          object,
	}
}

func writeReindexReportTable(stdout io.Writer, report sqlite.ReindexReport) error {
	mode := "apply"
	deleteLabel := "deleted"
	insertLabel := "inserted"
	if report.DryRun {
		mode = "dry-run"
		deleteLabel = "to delete"
		insertLabel = "to insert"
	}
	return writeSection(stdout, "Evidence reindex", []string{"Metric", "Value"}, [][]string{
		{"Mode", mode},
		{"Batches", humanCount(report.Batches)},
		{"Objects scanned", humanCount(report.Objects)},
		{"Versions scanned", humanCount(report.VersionsScanned)},
		{"Facts " + deleteLabel, humanCount(report.FactsDeleted)},
		{"Facts " + insertLabel, humanCount(report.FactsInserted)},
		{"Changes " + deleteLabel, humanCount(report.ChangesDeleted)},
		{"Changes " + insertLabel, humanCount(report.ChangesInserted)},
		{"Edges " + deleteLabel, humanCount(report.EdgesDeleted)},
		{"Edges " + insertLabel, humanCount(report.EdgesInserted)},
		{"Duration", humanDuration(report.StartedAt, report.FinishedAt)},
	})
}
