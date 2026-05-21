package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"kube-insight/internal/agent"
	"kube-insight/internal/api"
	appconfig "kube-insight/internal/config"
	"kube-insight/internal/storage"
	"kube-insight/internal/storage/sqlite"

	openaimodel "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/tool"
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
	readStore, closeTools, err := openAgentToolStore(ctx, dbPath, opts)
	if err != nil {
		return nil, nil, err
	}
	runner, err := agent.NewEinoRunner(ctx, agent.EinoRunnerConfig{
		Description: "Kubernetes investigation assistant backed by kube-insight evidence tools.",
		Model:       model,
		Tools:       configuredAgentTools(readStore),
	})
	if err != nil {
		if closeTools != nil {
			_ = closeTools()
		}
		return nil, nil, err
	}
	return runner, closeTools, nil
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

func openAgentToolStore(ctx context.Context, dbPath string, opts *api.ServerOptions) (api.ReadStore, func() error, error) {
	if opts.OpenStore != nil {
		store, err := opts.OpenStore(ctx)
		if err != nil {
			return nil, nil, err
		}
		if opts.KeepStoreOpen {
			return store, nil, nil
		}
		return store, store.Close, nil
	}
	if dbPath == "" {
		return nil, nil, errors.New("agent tools require a read store or sqlite database path")
	}
	store, err := sqlite.OpenReadOnly(dbPath)
	if err != nil {
		return nil, nil, err
	}
	return store, store.Close, nil
}

func configuredAgentTools(readStore api.ReadStore) []tool.BaseTool {
	tools := []tool.BaseTool{}
	if store, ok := readStore.(storage.ResourceHealthStore); ok {
		tools = append(tools, agent.NewHealthTool(store, agent.HealthToolOptions{}))
	}
	if store, ok := readStore.(storage.EvidenceSearchStore); ok {
		tools = append(tools, agent.NewSearchTool(store, agent.SearchToolOptions{}))
	}
	if store, ok := readStore.(storage.ServiceInvestigationStore); ok {
		tools = append(tools, agent.NewServiceInvestigationTool(store))
	}
	if store, ok := readStore.(storage.TopologyStore); ok {
		tools = append(tools, agent.NewTopologyTool(store))
	}
	if store, ok := readStore.(storage.ObjectHistoryStore); ok {
		tools = append(tools, agent.NewHistoryTool(store))
	}
	if store, ok := readStore.(storage.SQLQueryStore); ok {
		tools = append(tools, agent.NewSQLTool(store))
	}
	return tools
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
