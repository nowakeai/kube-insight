package cli

import (
	"context"
	"fmt"
	"io"

	"kube-insight/internal/api"
	"kube-insight/internal/mcp"

	"github.com/spf13/cobra"
)

func serveCommand(ctx context.Context, stdout, stderr io.Writer, state *cliState) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Serve read-only API surfaces.",
	}
	cmd.AddCommand(serveAPICommand(ctx, stdout, stderr, state))
	cmd.AddCommand(serveMCPCommand(ctx, stdout, stderr, state))
	return cmd
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
			runCtx, logger, err := runtimeContext(ctx, stderr, rt)
			if err != nil {
				return err
			}
			dbPath := dbCommandPath(cmd, state, rt)
			addr := listen
			if addr == "" {
				addr = rt.Config.Server.API.Listen
			}
			if addr == "" {
				addr = "127.0.0.1:8080"
			}
			logger.Info("serving api", "listen", addr, "db", dbPath)
			fmt.Fprintf(stdout, "serving api on http://%s\n", addr)
			return api.ListenAndServe(runCtx, addr, api.ServerOptions{DBPath: dbPath})
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
			runCtx, logger, err := runtimeContext(ctx, stderr, rt)
			if err != nil {
				return err
			}
			dbPath := dbCommandPath(cmd, state, rt)
			logger.Info("serving mcp stdio", "db", dbPath)
			return mcp.ServeStdio(runCtx, cmd.InOrStdin(), stdout, mcp.ServerOptions{DBPath: dbPath})
		},
	}
	return cmd
}
