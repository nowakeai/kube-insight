package cli

import (
	"fmt"
	"io"
	"strings"

	"kube-insight/internal/processing"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func configCommand(stdout io.Writer, state *cliState) *cobra.Command {
	cmd := &cobra.Command{Use: "config", Short: "Inspect and validate configuration."}
	var file string
	var showFile string
	var validateOutput string
	var showOutput string
	var showEffective bool
	var catalogOutput string
	validate := &cobra.Command{
		Use:   "validate",
		Short: "Validate the effective configuration.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateOutputFormat(validateOutput); err != nil {
				return err
			}
			rt, err := loadRuntimeConfig(cmd, state, configPathForValidation(cmd, state, file), false)
			if err != nil {
				return err
			}
			out := configValidationSummaryFromRuntime(rt)
			if validateOutput == outputJSON {
				return writeJSON(stdout, out)
			}
			return writeConfigValidationTable(stdout, out)
		},
	}
	validate.Flags().StringVarP(&file, "file", "f", "", "Config file to validate; defaults to root --config or embedded defaults")
	addOutputFlag(validate, &validateOutput, outputTable)
	show := &cobra.Command{
		Use:   "show",
		Short: "Print the effective configuration.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateConfigShowOutput(showOutput); err != nil {
				return err
			}
			if !showEffective {
				return fmt.Errorf("config show currently only supports --effective=true")
			}
			rt, err := loadRuntimeConfig(cmd, state, configPathForShow(cmd, state, showFile), false)
			if err != nil {
				return err
			}
			cfg := rt.Config.WithEffectiveProfileRules()
			if cfg.Collection.Kubeconfig == "" {
				cfg.Collection.Kubeconfig = rt.Kubeconfig
			}
			switch showOutput {
			case "json":
				return writeJSON(stdout, cfg)
			default:
				return writeYAML(stdout, cfg)
			}
		},
	}
	show.Flags().StringVarP(&showFile, "file", "f", "", "Config file to show; defaults to root --config or config/kube-insight.example.yaml")
	show.Flags().StringVar(&showOutput, "output", "yaml", "Output format: yaml or json")
	show.Flags().BoolVar(&showEffective, "effective", true, "Print config after defaults, file overlay, env, and CLI overrides")
	_ = show.Flags().MarkHidden("effective")
	catalog := &cobra.Command{
		Use:   "catalog",
		Short: "List built-in processing components.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateOutputFormat(catalogOutput); err != nil {
				return err
			}
			catalog := processingCatalog()
			if catalogOutput == outputJSON {
				return writeJSON(stdout, catalog)
			}
			return writeProcessingCatalogTable(stdout, catalog)
		},
	}
	addOutputFlag(catalog, &catalogOutput, outputTable)
	cmd.AddCommand(validate)
	cmd.AddCommand(show)
	cmd.AddCommand(catalog)
	return cmd
}

type configValidationSummary struct {
	OK            bool     `json:"ok"`
	File          string   `json:"file"`
	Version       string   `json:"version"`
	Role          string   `json:"role"`
	LogLevel      string   `json:"logLevel"`
	LogFormat     string   `json:"logFormat"`
	Storage       string   `json:"storage"`
	SQLitePath    string   `json:"sqlitePath,omitempty"`
	Maintenance   bool     `json:"maintenance"`
	MaintEvery    int      `json:"maintenanceIntervalSeconds"`
	Retention     bool     `json:"retention"`
	Metrics       bool     `json:"metrics"`
	MetricsListen string   `json:"metricsListen,omitempty"`
	Collection    bool     `json:"collection"`
	Kubeconfig    string   `json:"kubeconfig,omitempty"`
	UseClientGo   bool     `json:"useClientGo"`
	Contexts      []string `json:"contexts,omitempty"`
	AllContexts   bool     `json:"allContexts"`
	Namespace     string   `json:"namespace,omitempty"`
	Concurrency   int      `json:"concurrency"`
	ResourcesAll  bool     `json:"resourcesAll"`
	FilterChains  int      `json:"filterChains"`
	Filters       int      `json:"filters"`
	ExtractorSets int      `json:"extractorSets"`
	Extractors    int      `json:"extractors"`
	PluginDirs    []string `json:"pluginDirs,omitempty"`
}

type configProcessingCatalog struct {
	Filters    []processing.BuiltInFilter    `json:"filters"`
	Extractors []processing.BuiltInExtractor `json:"extractors"`
}

func configValidationSummaryFromRuntime(rt runtimeSettings) configValidationSummary {
	return configValidationSummary{
		OK:            true,
		File:          rt.ConfigPath,
		Version:       rt.Config.Version,
		Role:          configRole(rt.Config),
		LogLevel:      rt.Config.Logging.Level,
		LogFormat:     rt.Config.Logging.Format,
		Storage:       rt.Config.Storage.Driver,
		SQLitePath:    rt.Config.Storage.SQLite.Path,
		Maintenance:   rt.Config.Storage.Maintenance.Enabled,
		MaintEvery:    rt.Config.Storage.Maintenance.IntervalSeconds,
		Retention:     rt.Config.Storage.Retention.Enabled,
		Metrics:       rt.Config.Server.Metrics.Enabled,
		MetricsListen: rt.Config.Server.Metrics.Listen,
		Collection:    rt.Config.Collection.Enabled,
		Kubeconfig:    rt.Config.Collection.Kubeconfig,
		UseClientGo:   rt.Config.Collection.UseClientGo,
		Contexts:      rt.Config.Collection.Contexts,
		AllContexts:   rt.Config.Collection.AllContexts,
		Namespace:     rt.Config.Collection.Namespace,
		Concurrency:   rt.Config.Collection.Concurrency,
		ResourcesAll:  rt.Config.Collection.Resources.All,
		FilterChains:  len(rt.Config.Processing.FilterChains),
		Filters:       len(rt.Config.Processing.Filters),
		ExtractorSets: len(rt.Config.Processing.ExtractorSets),
		Extractors:    len(rt.Config.Processing.Extractors),
		PluginDirs:    rt.Config.Plugins.Goja.Directories,
	}
}

func writeConfigValidationTable(stdout io.Writer, out configValidationSummary) error {
	rows := [][]string{
		{"ok", boolText(out.OK)},
		{"file", emptyText(out.File, "<embedded/default>")},
		{"version", out.Version},
		{"role", out.Role},
		{"logging", out.LogLevel + "/" + out.LogFormat},
		{"storage", out.Storage},
		{"sqlite", out.SQLitePath},
		{"maintenance", fmt.Sprintf("%s every %ds", boolText(out.Maintenance), out.MaintEvery)},
		{"retention", boolText(out.Retention)},
		{"collection", boolText(out.Collection)},
		{"client-go", boolText(out.UseClientGo)},
		{"contexts", emptyText(strings.Join(out.Contexts, ","), "<current>")},
		{"all-contexts", boolText(out.AllContexts)},
		{"namespace", emptyText(out.Namespace, "<all>")},
		{"concurrency", fmt.Sprintf("%d", out.Concurrency)},
		{"resources-all", boolText(out.ResourcesAll)},
		{"processing", fmt.Sprintf("%d chains, %d filters, %d sets, %d extractors", out.FilterChains, out.Filters, out.ExtractorSets, out.Extractors)},
		{"metrics", fmt.Sprintf("%s %s", boolText(out.Metrics), out.MetricsListen)},
	}
	return writeTable(stdout, []string{"setting", "value"}, rows)
}

func processingCatalog() configProcessingCatalog {
	return configProcessingCatalog{
		Filters:    processing.BuiltInFilterCatalog(),
		Extractors: processing.BuiltInExtractorCatalog(),
	}
}

func writeProcessingCatalogTable(stdout io.Writer, catalog configProcessingCatalog) error {
	rows := make([][]string, 0, len(catalog.Filters)+len(catalog.Extractors))
	for _, filter := range catalog.Filters {
		rows = append(rows, []string{"filter", filter.Name, filter.Action})
	}
	for _, extractor := range catalog.Extractors {
		rows = append(rows, []string{"extractor", extractor.Name, ""})
	}
	return writeTable(stdout, []string{"type", "name", "required action"}, rows)
}

func emptyText(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func configPathForShow(cmd *cobra.Command, state *cliState, localFile string) string {
	if flagChanged(cmd, "file") && localFile != "" {
		return localFile
	}
	return state.configFile
}

func validateConfigShowOutput(format string) error {
	switch format {
	case "yaml", "json":
		return nil
	default:
		return fmt.Errorf("unsupported output format %q; use yaml or json", format)
	}
}

func writeYAML(stdout io.Writer, value any) error {
	encoder := yaml.NewEncoder(stdout)
	encoder.SetIndent(2)
	defer encoder.Close()
	return encoder.Encode(value)
}
