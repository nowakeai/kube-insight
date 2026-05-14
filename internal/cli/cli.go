package cli

import (
	"context"
	"io"
)

const version = "0.1.0-dev"

func Run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	cmd := newRootCommand(ctx, stdout, stderr)
	cmd.SetArgs(args)
	return cmd.Execute()
}
