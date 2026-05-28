package agent

import (
	"context"

	"github.com/cloudwego/eino/adk"
)

type runExecutionContextKey struct{}
type toolCallExecutionContextKey struct{}

type RunExecutionContext struct {
	Store    Store
	RunID    string
	Provider string
	Model    string
}

type ToolCallExecutionContext struct {
	Name   string
	CallID string
}

func withRunExecutionContext(ctx context.Context, info RunExecutionContext) context.Context {
	if info.Store == nil || info.RunID == "" {
		return ctx
	}
	return context.WithValue(ctx, runExecutionContextKey{}, info)
}

func RunExecutionContextFromContext(ctx context.Context) (RunExecutionContext, bool) {
	info, ok := ctx.Value(runExecutionContextKey{}).(RunExecutionContext)
	return info, ok && info.Store != nil && info.RunID != ""
}

func withToolCallExecutionContext(ctx context.Context, tCtx *adk.ToolContext) context.Context {
	if tCtx == nil || tCtx.CallID == "" {
		return ctx
	}
	return context.WithValue(ctx, toolCallExecutionContextKey{}, ToolCallExecutionContext{
		Name:   tCtx.Name,
		CallID: tCtx.CallID,
	})
}

func ToolCallExecutionContextFromContext(ctx context.Context) (ToolCallExecutionContext, bool) {
	info, ok := ctx.Value(toolCallExecutionContextKey{}).(ToolCallExecutionContext)
	return info, ok && info.CallID != ""
}
