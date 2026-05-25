package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"time"

	openaimodel "github.com/cloudwego/eino-ext/components/model/openai"
	einomcp "github.com/cloudwego/eino-ext/components/tool/mcp"
	"github.com/cloudwego/eino/components/tool"
	mcpclient "github.com/mark3labs/mcp-go/client"
	markmcp "github.com/mark3labs/mcp-go/mcp"
	"kube-insight/internal/agent"
	"kube-insight/internal/api"
	appconfig "kube-insight/internal/config"
	insightmcp "kube-insight/internal/mcp"
)

const agentModelTimeout = 2 * time.Minute

func configureAgentRunner(ctx context.Context, cfg appconfig.Config, dbPath string, opts *api.ServerOptions) {
	if opts == nil || !cfg.Server.Chat.Enabled {
		return
	}
	runner, closeTools, err := newConfiguredAgentRunner(ctx, cfg, dbPath, opts)
	if err != nil {
		opts.AgentRunner = agentRunError{err: err}
		return
	}
	opts.AgentRunner = runner
	opts.Close = joinServeClose(opts.Close, closeTools)
}

func newConfiguredAgentRunner(ctx context.Context, cfg appconfig.Config, dbPath string, opts *api.ServerOptions) (api.AgentRunner, func() error, error) {
	model, err := newConfiguredChatModel(ctx, cfg.Server.Chat)
	if err != nil {
		return nil, nil, err
	}
	tools, closeTools, err := configuredMCPAgentTools(ctx, dbPath, opts)
	if err != nil {
		return nil, nil, err
	}
	runner, err := agent.NewEinoRunner(ctx, agent.EinoRunnerConfig{
		Description:        "Kubernetes investigation assistant backed by kube-insight MCP evidence tools.",
		Model:              model,
		Tools:              tools,
		EmitInternalEvents: true,
		EnableStreaming:    true,
	})
	if err != nil {
		if closeTools != nil {
			_ = closeTools()
		}
		return nil, nil, err
	}
	return withAgentRuntimeContext(runner, dbPath), closeTools, nil
}

func newConfiguredChatModel(ctx context.Context, chat appconfig.ChatConfig) (*openaimodel.ChatModel, error) {
	provider := strings.ToLower(strings.TrimSpace(chat.Provider))
	switch provider {
	case "openai", "openai-compatible":
	default:
		return nil, fmt.Errorf("unsupported chat provider %q", chat.Provider)
	}
	apiKeyEnv := chat.EffectiveAPIKeyEnv()
	if apiKeyEnv == "" {
		return nil, errors.New("server.chat.apiKeyEnv is required")
	}
	apiKey := os.Getenv(apiKeyEnv)
	if apiKey == "" {
		return nil, fmt.Errorf("chat API key env %s is not set", apiKeyEnv)
	}
	modelName := strings.TrimSpace(chat.Model)
	if modelName == "" {
		return nil, errors.New("server.chat.model is required")
	}
	return openaimodel.NewChatModel(ctx, &openaimodel.ChatModelConfig{
		APIKey:  apiKey,
		BaseURL: os.Getenv(chat.BaseURLEnv),
		Model:   modelName,
		Timeout: agentModelTimeout,
	})
}

func configuredMCPAgentTools(ctx context.Context, dbPath string, opts *api.ServerOptions) ([]tool.BaseTool, func() error, error) {
	mcpServer, err := insightmcp.NewServer(configuredMCPServerOptions(dbPath, opts))
	if err != nil {
		return nil, nil, err
	}
	// This MCP server is private to the in-process agent runner. Keep the
	// session alive for the API server lifetime; otherwise an idle agent can
	// retain a stale client session and fail later tool calls after timeout.
	httpServer := httptest.NewServer(mcpServer.StreamableHTTPHandler(0))
	cli, err := mcpclient.NewStreamableHttpClient(httpServer.URL)
	if err != nil {
		httpServer.Close()
		_ = mcpServer.Close()
		return nil, nil, err
	}
	closeTools := func() error {
		err := cli.Close()
		httpServer.Close()
		return errors.Join(err, mcpServer.Close())
	}
	if err := cli.Start(ctx); err != nil {
		_ = closeTools()
		return nil, nil, err
	}
	init := markmcp.InitializeRequest{}
	init.Params.ProtocolVersion = markmcp.LATEST_PROTOCOL_VERSION
	init.Params.ClientInfo = markmcp.Implementation{Name: "kube-insight-agent", Version: "0.1.0-dev"}
	if _, err := cli.Initialize(ctx, init); err != nil {
		_ = closeTools()
		return nil, nil, err
	}
	tools, err := einomcp.GetTools(ctx, &einomcp.Config{Cli: cli})
	if err != nil {
		_ = closeTools()
		return nil, nil, err
	}
	if len(tools) == 0 {
		_ = closeTools()
		return nil, nil, errors.New("agent MCP server returned no tools")
	}
	return agent.WrapRecoverableToolErrors(tools), closeTools, nil
}

func configuredMCPServerOptions(dbPath string, opts *api.ServerOptions) insightmcp.ServerOptions {
	if opts != nil && opts.OpenStore != nil {
		return insightmcp.ServerOptions{
			DBPath: dbPath,
			OpenStore: func(ctx context.Context) (insightmcp.ReadStore, error) {
				return opts.OpenStore(ctx)
			},
			KeepStoreOpen: opts.KeepStoreOpen,
		}
	}
	return insightmcp.ServerOptions{DBPath: dbPath}
}

type agentRunError struct {
	err error
}

func (r agentRunError) Run(context.Context, agent.EinoRunInput) (agent.EinoRunResult, error) {
	return agent.EinoRunResult{}, r.err
}

func joinServeClose(first, second func() error) func() error {
	if first == nil {
		return second
	}
	if second == nil {
		return first
	}
	return func() error {
		return errors.Join(first(), second())
	}
}
