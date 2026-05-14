package cli

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

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
		Short: "Query stored Kubernetes evidence.",
	}
	object := objectQueryCommand(ctx, stdout, state)
	object.Use = "object"
	object.Short = "Query one object evidence bundle."
	cmd.AddCommand(sqlSchemaCommand(ctx, stdout, state))
	cmd.AddCommand(sqlQueryCommand(ctx, stdout, state))
	cmd.AddCommand(object)
	cmd.AddCommand(historyCommand(ctx, stdout, state))
	cmd.AddCommand(investigateServiceCommand(ctx, stdout, state))
	cmd.AddCommand(searchEvidenceCommand(ctx, stdout, state))
	cmd.AddCommand(topologyCommand(ctx, stdout, state))
	return cmd
}

func objectQueryCommand(ctx context.Context, stdout io.Writer, state *cliState) *cobra.Command {
	cmd := &cobra.Command{Use: "object", Short: "Query one object evidence bundle."}
	var target sqlite.ObjectTarget
	var from, to string
	var opts sqlite.InvestigationOptions
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
		dbPath, err := requiredDBPath(cmd, state, rt, "investigate")
		if err != nil {
			return err
		}
		store, err := sqlite.Open(dbPath)
		if err != nil {
			return err
		}
		defer store.Close()
		bundle, err := store.InvestigateWithOptions(runCtx, target, opts)
		if err != nil {
			return err
		}
		return writeJSON(stdout, bundle)
	}
	cmd.Flags().StringVar(&target.ClusterID, "cluster", "", "Cluster ID")
	cmd.Flags().StringVar(&target.UID, "uid", "", "Object UID")
	cmd.Flags().StringVar(&target.Kind, "kind", "", "Object kind")
	cmd.Flags().StringVar(&target.Name, "name", "", "Object name")
	cmd.Flags().StringVar(&from, "from", "", "Start time, RFC3339 or YYYY-MM-DD")
	cmd.Flags().StringVar(&to, "to", "", "End time, RFC3339 or YYYY-MM-DD")
	cmd.Flags().IntVar(&opts.MaxVersionsPerObject, "max-versions-per-object", 0, "Maximum proof versions")
	return cmd
}

func historyCommand(ctx context.Context, stdout io.Writer, state *cliState) *cobra.Command {
	var target sqlite.ObjectTarget
	var from, to string
	var opts sqlite.ObjectHistoryOptions
	cmd := &cobra.Command{
		Use:     "history",
		Aliases: []string{"observations"},
		Short:   "Query object content versions and observation history.",
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
			dbPath, err := requiredDBPath(cmd, state, rt, "query history")
			if err != nil {
				return err
			}
			store, err := sqlite.OpenReadOnly(dbPath)
			if err != nil {
				return err
			}
			defer store.Close()
			history, err := store.ObjectHistory(runCtx, target, opts)
			if err != nil {
				return err
			}
			return writeJSON(stdout, history)
		},
	}
	opts.IncludeDiffs = true
	cmd.Flags().StringVar(&target.ClusterID, "cluster", "", "Cluster ID")
	cmd.Flags().StringVar(&target.UID, "uid", "", "Object UID")
	cmd.Flags().StringVar(&target.Kind, "kind", "", "Object kind")
	cmd.Flags().StringVar(&target.Name, "name", "", "Object name")
	cmd.Flags().StringVar(&from, "from", "", "Start time, RFC3339 or YYYY-MM-DD")
	cmd.Flags().StringVar(&to, "to", "", "End time, RFC3339 or YYYY-MM-DD")
	cmd.Flags().IntVar(&opts.MaxVersions, "max-versions", 50, "Maximum retained content versions")
	cmd.Flags().IntVar(&opts.MaxObservations, "max-observations", 100, "Maximum observation rows")
	cmd.Flags().BoolVar(&opts.IncludeDocs, "include-docs", false, "Include full retained JSON documents")
	cmd.Flags().BoolVar(&opts.IncludeDiffs, "diffs", true, "Include diffs between returned versions")
	return cmd
}

func searchEvidenceCommand(ctx context.Context, stdout io.Writer, state *cliState) *cobra.Command {
	opts := sqlite.EvidenceSearchOptions{
		IncludeHealth:    true,
		HealthStaleAfter: 10 * time.Minute,
	}
	var from, to string
	cmd := &cobra.Command{
		Use:     "search [terms...]",
		Aliases: []string{"find"},
		Short:   "Search indexed evidence and return lightweight matches.",
		Args:    cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if opts.Query == "" {
				opts.Query = strings.Join(args, " ")
			}
			rt, err := loadRuntimeConfig(cmd, state, "", false)
			if err != nil {
				return err
			}
			runCtx, _, err := runtimeContext(ctx, cmd.ErrOrStderr(), rt)
			if err != nil {
				return err
			}
			opts.Namespace = namespaceForCommand(cmd, state, rt, opts.Namespace)
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
			dbPath, err := requiredDBPath(cmd, state, rt, "query search")
			if err != nil {
				return err
			}
			store, err := sqlite.Open(dbPath)
			if err != nil {
				return err
			}
			defer store.Close()
			result, err := store.SearchEvidence(runCtx, opts)
			if err != nil {
				return err
			}
			return writeJSON(stdout, result)
		},
	}
	cmd.Flags().StringVarP(&opts.Query, "query", "q", "", "Search text; defaults to positional terms")
	cmd.Flags().StringVar(&opts.ClusterID, "cluster", "", "Cluster ID")
	cmd.Flags().StringVar(&opts.Kind, "kind", "", "Object kind")
	cmd.Flags().StringVar(&from, "from", "", "Start time, RFC3339 or YYYY-MM-DD")
	cmd.Flags().StringVar(&to, "to", "", "End time, RFC3339 or YYYY-MM-DD")
	cmd.Flags().IntVar(&opts.Limit, "limit", 20, "Maximum matched objects")
	cmd.Flags().IntVar(&opts.MaxVersionsPerObject, "max-versions-per-object", 0, "Maximum proof versions per matched object")
	cmd.Flags().BoolVar(&opts.IncludeBundles, "include-bundles", false, "Include full evidence bundles; large output")
	cmd.Flags().BoolVar(&opts.IncludeHealth, "include-health", true, "Include resource coverage and watch health summary")
	cmd.Flags().DurationVar(&opts.HealthStaleAfter, "health-stale-after", 10*time.Minute, "Mark resource streams stale after this age when including health")
	return cmd
}

func sqlSchemaCommand(ctx context.Context, stdout io.Writer, state *cliState) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "schema",
		Short: "Print the read-only SQL schema for agents.",
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := loadRuntimeConfig(cmd, state, "", false)
			if err != nil {
				return err
			}
			runCtx, _, err := runtimeContext(ctx, cmd.ErrOrStderr(), rt)
			if err != nil {
				return err
			}
			store, err := sqlite.OpenReadOnly(dbCommandPath(cmd, state, rt))
			if err != nil {
				return err
			}
			defer store.Close()
			schema, err := store.QuerySchema(runCtx)
			if err != nil {
				return err
			}
			return writeJSON(stdout, schema)
		},
	}
	return cmd
}

func sqlQueryCommand(ctx context.Context, stdout io.Writer, state *cliState) *cobra.Command {
	var sqlText string
	var maxRows int
	var readStdin bool
	cmd := &cobra.Command{
		Use:   "sql [SQL]",
		Short: "Run read-only SQL for agents.",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := loadRuntimeConfig(cmd, state, "", false)
			if err != nil {
				return err
			}
			runCtx, _, err := runtimeContext(ctx, cmd.ErrOrStderr(), rt)
			if err != nil {
				return err
			}
			query := sqlText
			if query == "" && len(args) > 0 {
				query = strings.Join(args, " ")
			}
			if query == "" && readStdin {
				data, err := io.ReadAll(cmd.InOrStdin())
				if err != nil {
					return err
				}
				query = string(data)
			}
			store, err := sqlite.OpenReadOnly(dbCommandPath(cmd, state, rt))
			if err != nil {
				return err
			}
			defer store.Close()
			result, err := store.QuerySQL(runCtx, sqlite.SQLQueryOptions{
				SQL:     query,
				MaxRows: maxRows,
			})
			if err != nil {
				return err
			}
			return writeJSON(stdout, result)
		},
	}
	cmd.Flags().StringVar(&sqlText, "sql", "", "Read-only SQL query; defaults to positional SQL")
	cmd.Flags().IntVar(&maxRows, "max-rows", 1000, "Maximum rows to return")
	cmd.Flags().BoolVar(&readStdin, "stdin", false, "Read SQL from stdin")
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
