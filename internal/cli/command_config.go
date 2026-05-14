package cli

import (
	"io"

	"github.com/spf13/cobra"
)

func configCommand(stdout io.Writer, state *cliState) *cobra.Command {
	cmd := &cobra.Command{Use: "config", Short: "Inspect and validate configuration."}
	var file string
	validate := &cobra.Command{
		Use:   "validate",
		Short: "Validate the effective configuration.",
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := loadRuntimeConfig(cmd, state, configPathForValidation(cmd, state, file), true)
			if err != nil {
				return err
			}
			out := struct {
				OK           bool     `json:"ok"`
				File         string   `json:"file"`
				Version      string   `json:"version"`
				Role         string   `json:"role"`
				LogLevel     string   `json:"logLevel"`
				LogFormat    string   `json:"logFormat"`
				Storage      string   `json:"storage"`
				SQLitePath   string   `json:"sqlitePath,omitempty"`
				Maintenance  bool     `json:"maintenance"`
				MaintEvery   int      `json:"maintenanceIntervalSeconds"`
				Collection   bool     `json:"collection"`
				Kubeconfig   string   `json:"kubeconfig,omitempty"`
				UseClientGo  bool     `json:"useClientGo"`
				Contexts     []string `json:"contexts,omitempty"`
				AllContexts  bool     `json:"allContexts"`
				Namespace    string   `json:"namespace,omitempty"`
				Concurrency  int      `json:"concurrency"`
				ResourcesAll bool     `json:"resourcesAll"`
				Filters      int      `json:"filters"`
				Extractors   int      `json:"extractors"`
				PluginDirs   []string `json:"pluginDirs,omitempty"`
			}{
				OK:           true,
				File:         rt.ConfigPath,
				Version:      rt.Config.Version,
				Role:         configRole(rt.Config),
				LogLevel:     rt.Config.Logging.Level,
				LogFormat:    rt.Config.Logging.Format,
				Storage:      rt.Config.Storage.Driver,
				SQLitePath:   rt.Config.Storage.SQLite.Path,
				Maintenance:  rt.Config.Storage.Maintenance.Enabled,
				MaintEvery:   rt.Config.Storage.Maintenance.IntervalSeconds,
				Collection:   rt.Config.Collection.Enabled,
				Kubeconfig:   rt.Config.Collection.Kubeconfig,
				UseClientGo:  rt.Config.Collection.UseClientGo,
				Contexts:     rt.Config.Collection.Contexts,
				AllContexts:  rt.Config.Collection.AllContexts,
				Namespace:    rt.Config.Collection.Namespace,
				Concurrency:  rt.Config.Collection.Concurrency,
				ResourcesAll: rt.Config.Collection.Resources.All,
				Filters:      len(rt.Config.Filters),
				Extractors:   len(rt.Config.Extractors),
				PluginDirs:   rt.Config.Plugins.Goja.Directories,
			}
			return writeJSON(stdout, out)
		},
	}
	validate.Flags().StringVarP(&file, "file", "f", "", "Config file to validate; defaults to root --config or config/kube-insight.example.yaml")
	cmd.AddCommand(validate)
	return cmd
}
