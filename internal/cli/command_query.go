package cli

import (
	"context"
	"fmt"
	"io"

	"kube-insight/internal/storage/sqlite"

	"github.com/spf13/cobra"
)

func investigateCommand(ctx context.Context, stdout io.Writer, state *cliState) *cobra.Command {
	cmd := objectQueryCommand(ctx, stdout, state)
	cmd.Use = "investigate"
	cmd.Short = "Build evidence bundles."
	cmd.AddCommand(investigateServiceCommand(ctx, stdout, state))
	return cmd
}

func queryCommand(ctx context.Context, stdout io.Writer, state *cliState) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "query",
		Short: "Query evidence, service investigations, and topology.",
	}
	object := objectQueryCommand(ctx, stdout, state)
	object.Use = "object"
	object.Short = "Query one object evidence bundle."
	cmd.AddCommand(object)
	cmd.AddCommand(investigateServiceCommand(ctx, stdout, state))
	cmd.AddCommand(topologyCommand(ctx, stdout, state))
	return cmd
}

func objectQueryCommand(ctx context.Context, stdout io.Writer, state *cliState) *cobra.Command {
	cmd := &cobra.Command{Use: "object", Short: "Query one object evidence bundle."}
	var target sqlite.ObjectTarget
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		rt, err := loadRuntimeConfig(cmd, state, "", false)
		if err != nil {
			return err
		}
		runCtx, _, err := runtimeContext(ctx, cmd.ErrOrStderr(), rt)
		if err != nil {
			return err
		}
		target.Namespace = namespaceForCommand(cmd, state, rt, target.Namespace)
		dbPath, err := requiredDBPath(cmd, state, rt, "investigate")
		if err != nil {
			return err
		}
		store, err := sqlite.Open(dbPath)
		if err != nil {
			return err
		}
		defer store.Close()
		bundle, err := store.Investigate(runCtx, target)
		if err != nil {
			return err
		}
		return writeJSON(stdout, bundle)
	}
	cmd.Flags().StringVar(&target.ClusterID, "cluster", "", "Cluster ID")
	cmd.Flags().StringVar(&target.UID, "uid", "", "Object UID")
	cmd.Flags().StringVar(&target.Kind, "kind", "", "Object kind")
	cmd.Flags().StringVar(&target.Name, "name", "", "Object name")
	return cmd
}

func investigateServiceCommand(ctx context.Context, stdout io.Writer, state *cliState) *cobra.Command {
	var target sqlite.ObjectTarget
	target.Kind = "Service"
	var from, to string
	var opts sqlite.InvestigationOptions
	cmd := &cobra.Command{
		Use:   "service NAME",
		Short: "Investigate a Service and related evidence.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				target.Name = args[0]
			}
			if target.Name == "" {
				return fmt.Errorf("investigate service requires a service name")
			}
			rt, err := loadRuntimeConfig(cmd, state, "", false)
			if err != nil {
				return err
			}
			runCtx, _, err := runtimeContext(ctx, cmd.ErrOrStderr(), rt)
			if err != nil {
				return err
			}
			target.Namespace = namespaceForCommand(cmd, state, rt, target.Namespace)
			if from != "" {
				opts.From, err = parseInvestigationTime(from)
				if err != nil {
					return fmt.Errorf("--from: %w", err)
				}
			}
			if to != "" {
				opts.To, err = parseInvestigationTime(to)
				if err != nil {
					return fmt.Errorf("--to: %w", err)
				}
			}
			if !opts.From.IsZero() && !opts.To.IsZero() && opts.From.After(opts.To) {
				return fmt.Errorf("--from must be before --to")
			}
			dbPath, err := requiredDBPath(cmd, state, rt, "investigate service")
			if err != nil {
				return err
			}
			store, err := sqlite.Open(dbPath)
			if err != nil {
				return err
			}
			defer store.Close()
			bundle, err := store.InvestigateServiceWithOptions(runCtx, target, opts)
			if err != nil {
				return err
			}
			return writeJSON(stdout, bundle)
		},
	}
	cmd.Flags().StringVar(&target.ClusterID, "cluster", "", "Cluster ID")
	cmd.Flags().StringVar(&target.Name, "name", "", "Service name")
	cmd.Flags().StringVar(&from, "from", "", "Start time, RFC3339 or YYYY-MM-DD")
	cmd.Flags().StringVar(&to, "to", "", "End time, RFC3339 or YYYY-MM-DD")
	cmd.Flags().IntVar(&opts.MaxEvidenceObjects, "max-evidence-objects", 0, "Maximum related evidence objects")
	cmd.Flags().IntVar(&opts.MaxVersionsPerObject, "max-versions-per-object", 0, "Maximum proof versions per object")
	return cmd
}

func topologyCommand(ctx context.Context, stdout io.Writer, state *cliState) *cobra.Command {
	var target sqlite.ObjectTarget
	cmd := &cobra.Command{
		Use:   "topology",
		Short: "Query object topology.",
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := loadRuntimeConfig(cmd, state, "", false)
			if err != nil {
				return err
			}
			runCtx, _, err := runtimeContext(ctx, cmd.ErrOrStderr(), rt)
			if err != nil {
				return err
			}
			target.Namespace = namespaceForCommand(cmd, state, rt, target.Namespace)
			dbPath, err := requiredDBPath(cmd, state, rt, "topology")
			if err != nil {
				return err
			}
			store, err := sqlite.Open(dbPath)
			if err != nil {
				return err
			}
			defer store.Close()
			graph, err := store.Topology(runCtx, target)
			if err != nil {
				return err
			}
			return writeJSON(stdout, graph)
		},
	}
	cmd.Flags().StringVar(&target.ClusterID, "cluster", "", "Cluster ID")
	cmd.Flags().StringVar(&target.UID, "uid", "", "Object UID")
	cmd.Flags().StringVar(&target.Kind, "kind", "", "Object kind")
	cmd.Flags().StringVar(&target.Name, "name", "", "Object name")
	return cmd
}
