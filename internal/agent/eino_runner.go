package agent

import (
	"context"
	"errors"
	"fmt"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

const defaultEinoAgentName = "kube-insight-agent"

var ErrEinoModelRequired = errors.New("eino chat model is required")

type EinoRunnerConfig struct {
	Name          string
	Description   string
	Instruction   string
	Model         model.BaseChatModel
	Tools         []tool.BaseTool
	MaxIterations int
}

type EinoRunner struct {
	runner *adk.Runner
}

type EinoRunInput struct {
	Messages []Message
}

type EinoRunResult struct {
	FinalAnswer string
	Events      int
}

func NewEinoRunner(ctx context.Context, cfg EinoRunnerConfig) (*EinoRunner, error) {
	if cfg.Model == nil {
		return nil, ErrEinoModelRequired
	}
	name := cfg.Name
	if name == "" {
		name = defaultEinoAgentName
	}
	agentConfig := &adk.ChatModelAgentConfig{
		Name:          name,
		Description:   cfg.Description,
		Instruction:   cfg.Instruction,
		Model:         cfg.Model,
		MaxIterations: cfg.MaxIterations,
	}
	if len(cfg.Tools) > 0 {
		agentConfig.ToolsConfig = adk.ToolsConfig{ToolsNodeConfig: compose.ToolsNodeConfig{Tools: cfg.Tools}}
	}
	agent, err := adk.NewChatModelAgent(ctx, agentConfig)
	if err != nil {
		return nil, err
	}
	return &EinoRunner{runner: adk.NewRunner(ctx, adk.RunnerConfig{Agent: agent})}, nil
}

func (r *EinoRunner) Run(ctx context.Context, input EinoRunInput) (EinoRunResult, error) {
	if r == nil || r.runner == nil {
		return EinoRunResult{}, errors.New("eino runner is not initialized")
	}
	messages := make([]adk.Message, 0, len(input.Messages))
	for _, message := range input.Messages {
		messages = append(messages, einoMessage(message))
	}
	iter := r.runner.Run(ctx, messages)
	var result EinoRunResult
	for {
		event, ok := iter.Next()
		if !ok {
			return result, nil
		}
		result.Events++
		if event == nil {
			continue
		}
		if event.Err != nil {
			return result, event.Err
		}
		if event.Output == nil || event.Output.MessageOutput == nil || event.Output.MessageOutput.Message == nil {
			continue
		}
		if content := event.Output.MessageOutput.Message.Content; content != "" {
			result.FinalAnswer = content
		}
	}
}

func einoMessage(message Message) adk.Message {
	switch message.Role {
	case RoleSystem:
		return schema.SystemMessage(message.Content)
	case RoleAssistant:
		return schema.AssistantMessage(message.Content, nil)
	case RoleTool:
		if message.ID != "" {
			return schema.ToolMessage(message.Content, message.ID)
		}
		return &schema.Message{Role: schema.Tool, Content: message.Content}
	case RoleUser:
		return schema.UserMessage(message.Content)
	default:
		return schema.UserMessage(fmt.Sprintf("%s", message.Content))
	}
}
