package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"

	"kube-insight/internal/api"
	"kube-insight/internal/collector"
	appconfig "kube-insight/internal/config"
	"kube-insight/internal/mcp"
	"kube-insight/internal/metrics"
	"kube-insight/internal/storage/clickhouse"
	webui "kube-insight/web"

	"github.com/spf13/cobra"
)

type serveOptions struct {
	API           bool
	MCP           bool
	WebUI         bool
	Metrics       bool
	Watch         bool
	APIListen     string
	MCPListen     string
	WebUIListen   string
	MetricsListen string
	Output        string
	WatchOpts     collector.WatchResourcesOptions
}

type serveSelection struct {
	API           bool
	MCP           bool
	WebUI         bool
	Metrics       bool
	Watch         bool
	APIListen     string
	MCPListen     string
	WebUIListen   string
	MetricsListen string
}

func serveCommand(ctx context.Context, stdout, stderr io.Writer, state *cliState) *cobra.Command {
	var opts serveOptions
	cmd := &cobra.Command{
		Use:   "serve [RESOURCE_PATTERN ...]",
		Short: "Run long-lived kube-insight services.",
		Long: `Run long-lived kube-insight services.

Examples:
  kube-insight serve --watch --api --mcp
  kube-insight serve --watch --api --mcp --metrics
  kube-insight serve --watch --api --mcp --webui
  kube-insight serve --watch pods events.events.k8s.io --api

With no component flags, serve uses enabled services from the config file. The
MCP component on the combined serve command exposes MCP Streamable HTTP at /mcp
and legacy SDK SSE at /sse. Use
"kube-insight serve mcp" for stdio MCP.`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServeCommand(ctx, stdout, stderr, state, cmd, args, opts)
		},
	}
	cmd.Flags().BoolVar(&opts.Watch, "watch", false, "Run Kubernetes discovery/list/watch writers")
	cmd.Flags().BoolVar(&opts.API, "api", false, "Run the read-only HTTP API")
	cmd.Flags().BoolVar(&opts.MCP, "mcp", false, "Run the HTTP MCP server with Streamable HTTP at /mcp and legacy SSE at /sse")
	cmd.Flags().BoolVar(&opts.WebUI, "webui", false, "Run the web UI server")
	cmd.Flags().BoolVar(&opts.Metrics, "metrics", false, "Run the Prometheus metrics server at /metrics")
	cmd.Flags().StringVar(&opts.APIListen, "api-listen", "", "API listen address; defaults to server.api.listen")
	cmd.Flags().StringVar(&opts.MCPListen, "mcp-listen", "", "MCP HTTP listen address; defaults to mcp.listen")
	cmd.Flags().StringVar(&opts.WebUIListen, "webui-listen", "", "Web UI listen address; defaults to server.web.listen")
	cmd.Flags().StringVar(&opts.MetricsListen, "metrics-listen", "", "Metrics listen address; defaults to server.metrics.listen")
	cmd.Flags().IntVar(&opts.WatchOpts.MaxEvents, "max-events", 0, "Stop watcher after N events")
	cmd.Flags().IntVar(&opts.WatchOpts.MaxRetries, "retries", -1, "Maximum watch retries; -1 retries forever")
	cmd.Flags().DurationVar(&opts.WatchOpts.Timeout, "timeout", 0, "Watch timeout; 0 runs until interrupted")
	addOutputFlag(cmd, &opts.Output, outputTable)
	cmd.AddCommand(serveAPICommand(ctx, stdout, stderr, state))
	cmd.AddCommand(serveMCPCommand(ctx, stdout, stderr, state))
	return cmd
}

func runServeCommand(ctx context.Context, stdout, stderr io.Writer, state *cliState, cmd *cobra.Command, resourceArgs []string, opts serveOptions) error {
	if err := validateOutputFormat(opts.Output); err != nil {
		return err
	}
	rt, err := loadRuntimeConfig(cmd, state, "", false)
	if err != nil {
		return err
	}
	selection, ok := buildServeSelection(cmd, rt, opts)
	if !ok {
		return cmd.Help()
	}
	if selection.Watch {
		if err := requireWriteRole(rt.Config, "serve --watch"); err != nil {
			return err
		}
	}
	if selection.API || selection.MCP || selection.WebUI || selection.Metrics {
		if err := requireReadServiceRole(rt.Config, "serve"); err != nil {
			return err
		}
	}
	runCtx, logger, err := runtimeContext(ctx, stderr, rt)
	if err != nil {
		return err
	}
	dbPath := dbCommandPath(cmd, state, rt)
	storageTarget := serviceStorageTarget(rt.Config, dbPath)
	logger.Info("serving", "db", storageTarget, "watch", selection.Watch, "api", selection.API, "mcp", selection.MCP, "webui", selection.WebUI, "metrics", selection.Metrics)

	serviceCtx, cancel := context.WithCancel(runCtx)
	defer cancel()
	errCh := make(chan error, 4)
	var wg sync.WaitGroup
	start := func(name string, fn func() error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := fn(); err != nil && !errors.Is(err, context.Canceled) {
				errCh <- fmt.Errorf("%s: %w", name, err)
				cancel()
			}
		}()
	}
	services := []serveStatusRow{}
	if selection.API {
		addr := selection.APIListen
		logger.Info("serving api", "listen", addr)
		services = append(services, serveStatusRow{"api", "serving", "http://" + addr})
		start("api", func() error {
			return api.ListenAndServe(serviceCtx, addr, apiServerOptions(rt.Config, dbPath, selection))
		})
	}
	if selection.MCP {
		addr := selection.MCPListen
		logger.Info("serving mcp http", "listen", addr)
		services = append(services, serveStatusRow{"mcp", "serving", "http://" + addr + "/mcp"})
		start("mcp", func() error {
			return mcp.ListenAndServe(serviceCtx, addr, mcpServerOptions(rt.Config, dbPath))
		})
	}
	if selection.WebUI {
		addr := selection.WebUIListen
		logger.Info("serving webui", "listen", addr)
		services = append(services, serveStatusRow{"webui", "serving", "http://" + addr})
		start("webui", func() error {
			return serveWebUI(serviceCtx, addr)
		})
	}
	if selection.Metrics {
		addr := selection.MetricsListen
		logger.Info("serving metrics", "listen", addr)
		services = append(services, serveStatusRow{"metrics", "serving", "http://" + addr + "/metrics"})
		start("metrics", func() error {
			return metrics.ListenAndServe(serviceCtx, addr, metricsServerOptions(rt.Config, dbPath))
		})
	}
	if selection.Watch {
		services = append(services, serveStatusRow{"watch", "running", watchTargetText(resourceArgs)})
		watchOpts := opts.WatchOpts
		start("watch", func() error {
			return runWatchResourcesCommand(serviceCtx, stdout, stderr, state, cmd, resourceArgs, watchOpts, "serve --watch")
		})
	}
	if err := writeServeStatus(stdout, opts.Output, storageTarget, services); err != nil {
		return err
	}
	go func() {
		wg.Wait()
		close(errCh)
	}()
	for err := range errCh {
		if err != nil {
			return err
		}
	}
	return nil
}

func buildServeSelection(cmd *cobra.Command, rt runtimeSettings, opts serveOptions) (serveSelection, bool) {
	flagMode := serveFlagChanged(cmd) || serveConfiguredByEnv()
	out := serveSelection{}
	if flagMode {
		out.API = opts.API || rt.Config.Server.API.Enabled
		out.MCP = opts.MCP || rt.Config.MCP.Enabled
		out.WebUI = opts.WebUI || rt.Config.Server.Web.Enabled
		out.Metrics = opts.Metrics || rt.Config.Server.Metrics.Enabled
		out.Watch = opts.Watch || (!cmd.Flags().Changed("watch") && envIsSet("KUBE_INSIGHT_COLLECTION_ENABLED") && rt.Config.Collection.Enabled)
	} else if rt.ConfigProvided {
		out.API = rt.Config.Server.API.Enabled
		out.MCP = rt.Config.MCP.Enabled
		out.WebUI = rt.Config.Server.Web.Enabled
		out.Metrics = rt.Config.Server.Metrics.Enabled
		out.Watch = rt.Config.Collection.Enabled
	} else {
		return out, false
	}
	out.APIListen = firstNonEmpty(opts.APIListen, rt.Config.Server.API.Listen, "127.0.0.1:8080")
	out.MCPListen = firstNonEmpty(opts.MCPListen, rt.Config.MCP.Listen, "127.0.0.1:8090")
	out.WebUIListen = firstNonEmpty(opts.WebUIListen, rt.Config.Server.Web.Listen, "127.0.0.1:8081")
	out.MetricsListen = firstNonEmpty(opts.MetricsListen, rt.Config.Server.Metrics.Listen, "127.0.0.1:9090")
	return out, out.API || out.MCP || out.WebUI || out.Metrics || out.Watch
}

func serviceStorageTarget(cfg appconfig.Config, dbPath string) string {
	switch storageDriver(cfg) {
	case "clickhouse":
		database := cfg.Storage.ClickHouse.Database
		if database == "" {
			database = "kube_insight"
		}
		return "clickhouse:" + database
	case "chdb":
		database := cfg.Storage.ChDB.Database
		if database == "" {
			database = "kube_insight"
		}
		return "chdb:" + database
	default:
		return dbPath
	}
}

func serveFlagChanged(cmd *cobra.Command) bool {
	for _, name := range []string{"api", "mcp", "webui", "metrics", "watch"} {
		if cmd.Flags().Changed(name) {
			return true
		}
	}
	return false
}

func serveConfiguredByEnv() bool {
	for _, name := range []string{
		"KUBE_INSIGHT_SERVER_API_ENABLED",
		"KUBE_INSIGHT_SERVER_WEB_ENABLED",
		"KUBE_INSIGHT_SERVER_METRICS_ENABLED",
		"KUBE_INSIGHT_MCP_ENABLED",
		"KUBE_INSIGHT_COLLECTION_ENABLED",
	} {
		if _, ok := os.LookupEnv(name); ok {
			return true
		}
	}
	return false
}

func envIsSet(name string) bool {
	_, ok := os.LookupEnv(name)
	return ok
}

func serveWebUI(ctx context.Context, listen string) error {
	if listen == "" {
		listen = "127.0.0.1:8081"
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writePlain(w, http.StatusOK, "ok\n")
	})
	mux.Handle("GET /", webui.Handler())
	server := &http.Server{
		Addr:              listen,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	done := make(chan error, 1)
	go func() {
		done <- server.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return err
		}
		err := <-done
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

type serveStatusRow struct {
	Component string `json:"component"`
	Status    string `json:"status"`
	Address   string `json:"address"`
}

func writeServeStatus(stdout io.Writer, output string, dbPath string, services []serveStatusRow) error {
	if output == outputJSON {
		return writeJSON(stdout, map[string]any{
			"db":       dbPath,
			"services": services,
		})
	}
	rows := make([][]string, 0, len(services)+1)
	rows = append(rows, []string{"storage", "using", dbPath})
	for _, service := range services {
		rows = append(rows, []string{service.Component, service.Status, service.Address})
	}
	return writeSection(stdout, "kube-insight services", []string{"Component", "Status", "Endpoint"}, rows)
}

func watchTargetText(resourceArgs []string) string {
	if len(resourceArgs) == 0 {
		return "all resources"
	}
	return fmt.Sprintf("%v", resourceArgs)
}

func requireReadServiceRole(cfg appconfig.Config, command string) error {
	if configRole(cfg) == "writer" {
		return fmt.Errorf("%s read listeners are disabled when instance.role is writer", command)
	}
	return nil
}

func writePlain(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

func serveAPICommand(ctx context.Context, stdout, stderr io.Writer, state *cliState) *cobra.Command {
	var listen string
	cmd := &cobra.Command{
		Use:   "api",
		Short: "Serve the read-only HTTP API for agents.",
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := loadRuntimeConfig(cmd, state, "", false)
			if err != nil {
				return err
			}
			if err := requireReadServiceRole(rt.Config, "serve api"); err != nil {
				return err
			}
			runCtx, logger, err := runtimeContext(ctx, stderr, rt)
			if err != nil {
				return err
			}
			dbPath := dbCommandPath(cmd, state, rt)
			addr := firstNonEmpty(listen, rt.Config.Server.API.Listen, "127.0.0.1:8080")
			logger.Info("serving api", "listen", addr, "db", dbPath)
			fmt.Fprintf(stdout, "serving api on http://%s\n", addr)
			return api.ListenAndServe(runCtx, addr, apiServerOptions(rt.Config, dbPath, serveSelection{API: true, APIListen: addr}))
		},
	}
	cmd.Flags().StringVar(&listen, "listen", "", "Listen address; defaults to server.api.listen")
	return cmd
}

func serveMCPCommand(ctx context.Context, stdout, stderr io.Writer, state *cliState) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Serve the stdio MCP tools for agents.",
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := loadRuntimeConfig(cmd, state, "", false)
			if err != nil {
				return err
			}
			if err := requireReadServiceRole(rt.Config, "serve mcp"); err != nil {
				return err
			}
			runCtx, logger, err := runtimeContext(ctx, stderr, rt)
			if err != nil {
				return err
			}
			dbPath := dbCommandPath(cmd, state, rt)
			logger.Info("serving mcp stdio", "db", serviceStorageTarget(rt.Config, dbPath))
			return mcp.ServeStdio(runCtx, cmd.InOrStdin(), stdout, mcpServerOptions(rt.Config, dbPath))
		},
	}
	return cmd
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func mcpServerOptions(cfg appconfig.Config, dbPath string) mcp.ServerOptions {
	opts := mcp.ServerOptions{DBPath: dbPath}
	switch storageDriver(cfg) {
	case "clickhouse":
		opts.OpenStore = func(context.Context) (mcp.ReadStore, error) {
			return newClickHouseStoreFromConfig(cfg)
		}
	case "chdb":
		var mu sync.Mutex
		var readStore mcp.ReadStore
		opts.KeepStoreOpen = true
		opts.OpenStore = func(context.Context) (mcp.ReadStore, error) {
			mu.Lock()
			defer mu.Unlock()
			if readStore != nil {
				return readStore, nil
			}
			store, err := newChDBStoreFromConfig(cfg)
			if err != nil {
				return nil, err
			}
			opened, ok := store.(mcp.ReadStore)
			if !ok {
				if closer, closeOK := store.(interface{ Close() error }); closeOK {
					_ = closer.Close()
				}
				return nil, fmt.Errorf("chdb store does not support MCP reads")
			}
			readStore = opened
			return readStore, nil
		}
		opts.Close = func() error {
			mu.Lock()
			defer mu.Unlock()
			if readStore == nil {
				return nil
			}
			err := readStore.Close()
			readStore = nil
			return err
		}
	}
	return opts
}

func metricsServerOptions(cfg appconfig.Config, dbPath string) metrics.ServerOptions {
	opts := metrics.ServerOptions{DBPath: dbPath, Driver: storageDriver(cfg)}
	if storageDriver(cfg) == "clickhouse" {
		ch := cfg.Storage.ClickHouse
		opts.ClickHouseEndpoint = os.Getenv(ch.DSNEnv)
		opts.ClickHouseOptions = clickhouse.OptionsFromConfig(ch)
	}
	return opts
}

func configuredServerInfoSelection(cfg appconfig.Config) serveSelection {
	return serveSelection{
		API:           cfg.Server.API.Enabled,
		MCP:           cfg.MCP.Enabled,
		WebUI:         cfg.Server.Web.Enabled,
		Metrics:       cfg.Server.Metrics.Enabled,
		Watch:         cfg.Collection.Enabled,
		APIListen:     firstNonEmpty(cfg.Server.API.Listen, "127.0.0.1:8080"),
		MCPListen:     firstNonEmpty(cfg.MCP.Listen, "127.0.0.1:8090"),
		WebUIListen:   firstNonEmpty(cfg.Server.Web.Listen, "127.0.0.1:8081"),
		MetricsListen: firstNonEmpty(cfg.Server.Metrics.Listen, "127.0.0.1:9090"),
	}
}

func apiServerInfo(cfg appconfig.Config, dbPath string, selection serveSelection) api.ServerInfo {
	apiKeyEnv := cfg.Server.Chat.EffectiveAPIKeyEnv()
	_, apiKeyConfigured := os.LookupEnv(apiKeyEnv)
	if apiKeyEnv == "" {
		apiKeyConfigured = false
	}
	baseURLEnv := cfg.Server.Chat.BaseURLEnv
	_, baseURLConfigured := os.LookupEnv(baseURLEnv)
	if baseURLEnv == "" {
		baseURLConfigured = false
	}
	return api.ServerInfo{
		Storage: api.ServerStorageInfo{
			Driver: storageDriver(cfg),
			Target: serviceStorageTarget(cfg, dbPath),
		},
		Components: map[string]api.ServerComponentInfo{
			"api":     componentInfo(selection.API, selection.APIListen, ""),
			"mcp":     componentInfo(selection.MCP, selection.MCPListen, "/mcp"),
			"webui":   componentInfo(selection.WebUI, selection.WebUIListen, ""),
			"metrics": componentInfo(selection.Metrics, selection.MetricsListen, "/metrics"),
			"watch":   {Enabled: selection.Watch},
		},
		Chat: api.ServerChatInfo{
			Enabled:           cfg.Server.Chat.Enabled,
			Provider:          cfg.Server.Chat.Provider,
			Model:             cfg.Server.Chat.Model,
			MaxIterations:     cfg.Server.Chat.EffectiveMaxIterations(),
			APIKeyEnv:         apiKeyEnv,
			APIKeyConfigured:  apiKeyConfigured,
			BaseURLEnv:        baseURLEnv,
			BaseURLConfigured: baseURLConfigured,
		},
	}
}

func componentInfo(enabled bool, listen string, path string) api.ServerComponentInfo {
	info := api.ServerComponentInfo{Enabled: enabled}
	if listen == "" {
		return info
	}
	info.Listen = listen
	if enabled {
		info.URL = "http://" + listen + path
	}
	return info
}

func apiServerOptions(cfg appconfig.Config, dbPath string, selections ...serveSelection) api.ServerOptions {
	selection := configuredServerInfoSelection(cfg)
	if len(selections) > 0 {
		selection = selections[0]
	}
	opts := api.ServerOptions{DBPath: dbPath, ServerInfo: apiServerInfo(cfg, dbPath, selection)}
	if cfg.Server.AgentRetention.Enabled {
		opts.AgentRetentionInterval = time.Duration(cfg.Server.AgentRetention.IntervalSeconds) * time.Second
		opts.AgentRetentionRunOnStart = cfg.Server.AgentRetention.RunOnStart
	}
	switch storageDriver(cfg) {
	case "clickhouse":
		opts.DBPath = ""
		if agentStore, err := newClickHouseStoreFromConfig(cfg); err == nil {
			opts.AgentStore = agentStore
		}
		opts.OpenStore = func(context.Context) (api.ReadStore, error) {
			return newClickHouseStoreFromConfig(cfg)
		}
	case "chdb":
		var mu sync.Mutex
		var readStore api.ReadStore
		opts.KeepStoreOpen = true
		opts.OpenStore = func(context.Context) (api.ReadStore, error) {
			mu.Lock()
			defer mu.Unlock()
			if readStore != nil {
				return readStore, nil
			}
			store, err := newChDBStoreFromConfig(cfg)
			if err != nil {
				return nil, err
			}
			opened, ok := store.(api.ReadStore)
			if !ok {
				if closer, closeOK := store.(interface{ Close() error }); closeOK {
					_ = closer.Close()
				}
				return nil, fmt.Errorf("chdb store does not support API reads")
			}
			readStore = opened
			return readStore, nil
		}
		opts.Close = func() error {
			mu.Lock()
			defer mu.Unlock()
			if readStore == nil {
				return nil
			}
			err := readStore.Close()
			readStore = nil
			return err
		}
	}
	configureAgentRunner(context.Background(), cfg, dbPath, &opts)
	return opts
}
