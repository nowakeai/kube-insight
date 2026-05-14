package cli

import (
	"context"
	"fmt"
	"io"
	"os"

	appconfig "kube-insight/internal/config"

	"github.com/spf13/cobra"
)

type cliState struct {
	configFile        string
	role              string
	dbPath            string
	kubeconfig        string
	contexts          []string
	allContexts       bool
	namespace         string
	concurrency       int
	collectionEnabled bool
	clientGo          bool
	logLevel          string
	logFormat         string
}

type runtimeSettings struct {
	Config         appconfig.Config
	ConfigPath     string
	ConfigProvided bool
	Kubeconfig     string
}

type collectionSettings struct {
	Contexts    []string
	AllContexts bool
	Namespace   string
	Concurrency int
	UseClientGo bool
}

type collectFlags struct {
	OutputDir         string
	DiscoverResources bool
	UseDBResources    bool
	Resources         []string
	MaxItems          int
}

func newRootCommand(ctx context.Context, stdout, stderr io.Writer) *cobra.Command {
	state := &cliState{configFile: os.Getenv("KUBE_INSIGHT_CONFIG")}
	root := &cobra.Command{
		Use:           "kube-insight",
		Short:         "Record Kubernetes resource history and extract troubleshooting evidence.",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
		CompletionOptions: cobra.CompletionOptions{
			HiddenDefaultCmd: true,
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	root.SetOut(stdout)
	root.SetErr(stderr)

	flags := root.PersistentFlags()
	flags.StringVar(&state.configFile, "config", state.configFile, "YAML config file; env: KUBE_INSIGHT_CONFIG")
	flags.StringVar(&state.role, "role", "", "Instance role: all, writer, or api; env: KUBE_INSIGHT_INSTANCE_ROLE")
	flags.StringVar(&state.dbPath, "db", "", "SQLite database path; env: KUBE_INSIGHT_STORAGE_SQLITE_PATH")
	flags.StringVar(&state.dbPath, "sqlite", "", "Alias for --db")
	flags.StringVar(&state.kubeconfig, "kubeconfig", "", "Kubeconfig path; env: KUBE_INSIGHT_COLLECTION_KUBECONFIG or KUBECONFIG")
	flags.StringArrayVar(&state.contexts, "context", nil, "Kubeconfig context; repeatable; env: KUBE_INSIGHT_COLLECTION_CONTEXTS")
	flags.BoolVar(&state.allContexts, "all-contexts", false, "Use all kubeconfig contexts; env: KUBE_INSIGHT_COLLECTION_ALL_CONTEXTS")
	flags.StringVarP(&state.namespace, "namespace", "n", "", "Namespace scope; empty means all namespaces where supported")
	flags.IntVar(&state.concurrency, "concurrency", 0, "Resource worker concurrency")
	flags.BoolVar(&state.collectionEnabled, "collection-enabled", false, "Enable collection/watch in effective config")
	flags.BoolVar(&state.clientGo, "client-go", false, "Use client-go instead of kubectl where supported")
	flags.StringVar(&state.logLevel, "log-level", "", "Log level: debug, info, warn, error; env: KUBE_INSIGHT_LOGGING_LEVEL")
	flags.StringVar(&state.logFormat, "log-format", "", "Log format: text or json; env: KUBE_INSIGHT_LOGGING_FORMAT")

	root.AddCommand(versionCommand(stdout))
	root.AddCommand(configCommand(stdout, state))
	root.AddCommand(dbCommand(ctx, stdout, state))
	root.AddCommand(watchCommand(ctx, stdout, stderr, state))
	root.AddCommand(queryCommand(ctx, stdout, state))
	root.AddCommand(serveCommand(ctx, stdout, stderr, state))
	root.AddCommand(devCommand(ctx, stdout, state))
	root.AddCommand(hiddenCommand(ingestCommand(ctx, stdout, state)))
	root.AddCommand(hiddenCommand(collectCommand(ctx, stdout, state)))
	root.AddCommand(hiddenCommand(discoverCommand(ctx, stdout, state)))
	root.AddCommand(hiddenCommand(generateCommand(ctx, stdout)))
	root.AddCommand(hiddenCommand(investigateCommand(ctx, stdout, state)))
	root.AddCommand(hiddenCommand(topologyCommand(ctx, stdout, state)))
	root.AddCommand(hiddenCommand(benchmarkCommand(ctx, stdout, state)))
	root.AddCommand(hiddenCommand(validateCommand(ctx, stdout, state)))
	root.AddCommand(&cobra.Command{
		Use:    "diff",
		Short:  "Diff retained resource versions.",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("diff is not implemented yet")
		},
	})
	return root
}

func versionCommand(stdout io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version.",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintf(stdout, "kube-insight %s\n", version)
			return nil
		},
	}
}

func hiddenCommand(cmd *cobra.Command) *cobra.Command {
	cmd.Hidden = true
	return cmd
}
