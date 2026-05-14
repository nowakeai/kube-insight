package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"

	"kube-insight/internal/benchmark"
	"kube-insight/internal/collector"
	appconfig "kube-insight/internal/config"
	"kube-insight/internal/logging"

	"github.com/spf13/cobra"
)

const defaultSQLiteDBPath = "kubeinsight.db"

func loadRuntimeConfig(cmd *cobra.Command, state *cliState, explicitPath string, validateDefaultFile bool) (runtimeSettings, error) {
	rt := runtimeSettings{Config: appconfig.Default()}
	path := explicitPath
	if path == "" {
		path = state.configFile
	}
	if path == "" && validateDefaultFile {
		path = "config/kube-insight.example.yaml"
	}
	if path != "" {
		cfg, err := appconfig.ReadFile(path)
		if err != nil {
			return rt, err
		}
		rt.Config = cfg
		rt.ConfigPath = path
		rt.ConfigProvided = true
	}
	if err := rt.Config.ApplyEnv("KUBE_INSIGHT", os.LookupEnv); err != nil {
		return rt, err
	}
	if err := rt.Config.ApplyOverrides(rootConfigOverrides(cmd, state)); err != nil {
		return rt, err
	}
	if err := rt.Config.Validate(); err != nil {
		return rt, err
	}
	rt.Kubeconfig = rt.Config.Collection.Kubeconfig
	if rt.Kubeconfig == "" {
		rt.Kubeconfig = os.Getenv("KUBECONFIG")
	}
	return rt, nil
}

func rootConfigOverrides(cmd *cobra.Command, state *cliState) map[string]string {
	overrides := map[string]string{}
	if flagChanged(cmd, "role") {
		overrides["instance.role"] = state.role
	}
	if flagChanged(cmd, "db") || flagChanged(cmd, "sqlite") {
		overrides["storage.sqlite.path"] = state.dbPath
	}
	if flagChanged(cmd, "kubeconfig") {
		overrides["collection.kubeconfig"] = state.kubeconfig
	}
	if flagChanged(cmd, "context") {
		overrides["collection.contexts"] = strings.Join(state.contexts, ",")
	}
	if flagChanged(cmd, "all-contexts") {
		overrides["collection.allContexts"] = strconv.FormatBool(state.allContexts)
	}
	if flagChanged(cmd, "namespace") {
		overrides["collection.namespace"] = state.namespace
	}
	if flagChanged(cmd, "concurrency") {
		overrides["collection.concurrency"] = strconv.Itoa(state.concurrency)
	}
	if flagChanged(cmd, "collection-enabled") {
		overrides["collection.enabled"] = strconv.FormatBool(state.collectionEnabled)
	}
	if flagChanged(cmd, "client-go") {
		overrides["collection.useClientGo"] = strconv.FormatBool(state.clientGo)
	}
	if flagChanged(cmd, "storage-maintenance") {
		overrides["storage.maintenance.enabled"] = strconv.FormatBool(state.maintenance)
	}
	if flagChanged(cmd, "maintenance-interval-seconds") {
		overrides["storage.maintenance.intervalSeconds"] = strconv.Itoa(state.maintenanceEvery)
	}
	if flagChanged(cmd, "maintenance-min-wal-bytes") {
		overrides["storage.maintenance.minWalBytes"] = strconv.FormatInt(state.minWalBytes, 10)
	}
	if flagChanged(cmd, "log-level") {
		overrides["logging.level"] = state.logLevel
	}
	if flagChanged(cmd, "log-format") {
		overrides["logging.format"] = state.logFormat
	}
	return overrides
}

func runtimeLogger(stderr io.Writer, rt runtimeSettings) (*slog.Logger, error) {
	return logging.New(stderr, logging.Config{
		Level:  rt.Config.Logging.Level,
		Format: rt.Config.Logging.Format,
	})
}

func runtimeContext(ctx context.Context, stderr io.Writer, rt runtimeSettings) (context.Context, *slog.Logger, error) {
	logger, err := runtimeLogger(stderr, rt)
	if err != nil {
		return ctx, nil, err
	}
	return logging.WithLogger(ctx, logger), logger, nil
}

func collectionFromRuntime(cmd *cobra.Command, state *cliState, rt runtimeSettings) collectionSettings {
	return collectionSettings{
		Contexts:    effectiveContexts(cmd, state, rt),
		AllContexts: effectiveAllContexts(cmd, state, rt),
		Namespace:   namespaceForCommand(cmd, state, rt, ""),
		Concurrency: effectiveConcurrency(cmd, state, rt),
		UseClientGo: effectiveClientGo(cmd, state, rt),
	}
}

func configPathForValidation(cmd *cobra.Command, state *cliState, localFile string) string {
	if flagChanged(cmd, "file") && localFile != "" {
		return localFile
	}
	if state.configFile != "" {
		return state.configFile
	}
	return "config/kube-insight.example.yaml"
}

func optionalDBPath(cmd *cobra.Command, state *cliState, rt runtimeSettings) string {
	return dbPathForOptions(cmd, state, rt, "")
}

func requiredDBPath(cmd *cobra.Command, state *cliState, rt runtimeSettings, command string) (string, error) {
	dbPath := optionalDBPath(cmd, state, rt)
	if dbPath == "" {
		return "", fmt.Errorf("%s requires --db", command)
	}
	return dbPath, nil
}

func dbPathForOptions(cmd *cobra.Command, state *cliState, rt runtimeSettings, optionValue string) string {
	if optionValue != "" {
		return optionValue
	}
	if flagChanged(cmd, "db") || flagChanged(cmd, "sqlite") {
		return state.dbPath
	}
	if rt.ConfigProvided || os.Getenv("KUBE_INSIGHT_STORAGE_SQLITE_PATH") != "" {
		return rt.Config.Storage.SQLite.Path
	}
	return ""
}

func effectiveContexts(cmd *cobra.Command, state *cliState, rt runtimeSettings) []string {
	if flagChanged(cmd, "context") {
		return state.contexts
	}
	if rt.ConfigProvided || os.Getenv("KUBE_INSIGHT_COLLECTION_CONTEXTS") != "" {
		return rt.Config.Collection.Contexts
	}
	return nil
}

func effectiveAllContexts(cmd *cobra.Command, state *cliState, rt runtimeSettings) bool {
	if flagChanged(cmd, "all-contexts") {
		return state.allContexts
	}
	return (rt.ConfigProvided || os.Getenv("KUBE_INSIGHT_COLLECTION_ALL_CONTEXTS") != "") && rt.Config.Collection.AllContexts
}

func effectiveClientGo(cmd *cobra.Command, state *cliState, rt runtimeSettings) bool {
	if flagChanged(cmd, "client-go") {
		return state.clientGo
	}
	if rt.ConfigProvided || os.Getenv("KUBE_INSIGHT_COLLECTION_USE_CLIENT_GO") != "" {
		return rt.Config.Collection.UseClientGo
	}
	return false
}

func namespaceForCommand(cmd *cobra.Command, state *cliState, rt runtimeSettings, localValue string) string {
	if localValue != "" {
		return localValue
	}
	if flagChanged(cmd, "namespace") {
		return state.namespace
	}
	if rt.ConfigProvided || os.Getenv("KUBE_INSIGHT_COLLECTION_NAMESPACE") != "" {
		return rt.Config.Collection.Namespace
	}
	return ""
}

func effectiveConcurrency(cmd *cobra.Command, state *cliState, rt runtimeSettings) int {
	if flagChanged(cmd, "concurrency") {
		return state.concurrency
	}
	if rt.ConfigProvided || os.Getenv("KUBE_INSIGHT_COLLECTION_CONCURRENCY") != "" {
		return rt.Config.Collection.Concurrency
	}
	return 0
}

func resourcesFromConfig(flagValues []string, cfg appconfig.Config) ([]collector.Resource, error) {
	values := flagValues
	if len(values) == 0 {
		values = cfg.Collection.Resources.Include
	}
	if len(values) == 0 {
		return nil, nil
	}
	return collector.ParseResources(values)
}

func effectiveResourceAll(rt runtimeSettings) bool {
	return (rt.ConfigProvided || os.Getenv("KUBE_INSIGHT_COLLECTION_RESOURCES_ALL") != "") && rt.Config.Collection.Resources.All
}

func applyValidationConfig(cmd *cobra.Command, cfg appconfig.Config, opts *benchmark.ValidationOptions) {
	if !cmd.Flags().Changed("max-stored-to-raw-ratio") {
		opts.MaxStoredToRawRatio = cfg.Validation.MaxStoredToRawRatio
	}
	if !cmd.Flags().Changed("max-latest-lookup-p95-ms") {
		opts.MaxLatestLookupP95MS = cfg.Validation.MaxLatestLookupP95MS
	}
	if !cmd.Flags().Changed("max-historical-get-p95-ms") {
		opts.MaxHistoricalGetP95MS = cfg.Validation.MaxHistoricalGetP95MS
	}
	if !cmd.Flags().Changed("max-service-investigation-p95-ms") {
		opts.MaxServiceInvestigationP95MS = cfg.Validation.MaxServiceInvestigationP95MS
	}
	if !cmd.Flags().Changed("min-service-versions") {
		opts.MinServiceVersions = cfg.Validation.MinServiceVersions
	}
	if !cmd.Flags().Changed("min-service-diffs") {
		opts.MinServiceDiffs = cfg.Validation.MinServiceDiffs
	}
}

func requireWriteRole(cfg appconfig.Config, command string) error {
	if configRole(cfg) == "api" {
		return fmt.Errorf("%s is disabled when instance.role is api", command)
	}
	return nil
}

func selectedContexts(ctx context.Context, selection collectionSettings) ([]string, error) {
	if selection.AllContexts {
		return configuredContexts(ctx, selection.UseClientGo)
	}
	if len(selection.Contexts) > 0 {
		return selection.Contexts, nil
	}
	current, err := currentContext(ctx, selection.UseClientGo)
	if err != nil {
		return nil, err
	}
	return []string{current}, nil
}

func singleContext(selection collectionSettings) string {
	if len(selection.Contexts) > 0 {
		return selection.Contexts[0]
	}
	return ""
}

func applyResourceExcludes(resources []collector.Resource, excludes []string) []collector.Resource {
	if len(resources) == 0 || len(excludes) == 0 {
		return resources
	}
	blocked := map[string]bool{}
	for _, exclude := range excludes {
		blocked[exclude] = true
	}
	out := resources[:0]
	for _, resource := range resources {
		if blocked[resource.Name] {
			continue
		}
		out = append(out, resource)
	}
	return out
}

func resourcesFromMap(in map[string]collector.Resource) []collector.Resource {
	out := make([]collector.Resource, 0, len(in))
	for _, resource := range in {
		out = append(out, resource)
	}
	return out
}

func flagChanged(cmd *cobra.Command, name string) bool {
	if flag := cmd.Flags().Lookup(name); flag != nil {
		return flag.Changed
	}
	if flag := cmd.InheritedFlags().Lookup(name); flag != nil {
		return flag.Changed
	}
	if flag := cmd.Root().PersistentFlags().Lookup(name); flag != nil {
		return flag.Changed
	}
	return false
}

func configRole(cfg appconfig.Config) string {
	if cfg.Instance.Role == "" {
		return "all"
	}
	return cfg.Instance.Role
}

func withKubeconfig(path string, fn func() error) error {
	if path == "" {
		return fn()
	}
	old, hadOld := os.LookupEnv("KUBECONFIG")
	if err := os.Setenv("KUBECONFIG", path); err != nil {
		return err
	}
	defer func() {
		if hadOld {
			_ = os.Setenv("KUBECONFIG", old)
		} else {
			_ = os.Unsetenv("KUBECONFIG")
		}
	}()
	return fn()
}

func writeJSON(stdout io.Writer, value any) error {
	encoder := json.NewEncoder(stdout)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}
