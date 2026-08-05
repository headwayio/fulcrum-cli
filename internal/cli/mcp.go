package cli

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"

	"github.com/headwayio/fulcrum-cli/internal/mcpserver"
)

func (a *App) mcpCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Serve Fulcrum to a coding harness over the Model Context Protocol",
		Long: "Runs a Model Context Protocol server on stdin/stdout, so Claude Code,\n" +
			"Codex, or Kimi Code can pull Fulcrum context while you work.\n\n" +
			"You do not run this yourself — a harness launches it. Register it with\n" +
			"`fulcrum mcp install` in the project you are working on.\n\n" +
			"The tools come from your Fulcrum server, not from this binary, so a tool\n" +
			"added by your team is available the next time a harness starts.",
		Args: cobra.NoArgs,
		// The stdout pipe belongs to the protocol from here on, so cobra must
		// not print usage or errors into it.
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runMCP()
		},
	}
	cmd.AddCommand(a.mcpInstallCmd())
	return cmd
}

func (a *App) runMCP() error {
	resolved, err := a.resolveConfig()
	if err != nil {
		return err
	}
	client, err := a.client(resolved)
	if err != nil {
		return err
	}

	// A harness kills the server by closing stdin or signalling; either way
	// the run should end cleanly rather than being killed mid-write.
	ctx, stop := signal.NotifyContext(background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	workingDir, err := os.Getwd()
	if err != nil {
		return exitf(ExitError, "cannot determine the working directory: %v", err)
	}

	server, err := mcpserver.New(ctx, a.Version, client, workingDir)
	if err != nil {
		return wrapAPIError(err)
	}

	// Stderr is the only channel left for humans — harnesses surface it as
	// server logs, which is where a "wrong org" or "expired token" needs to
	// land. Nothing here may write to a.Stdout.
	fmt.Fprintf(a.Stderr, "fulcrum mcp %s serving %s\n", a.Version, resolved.URL)

	if err := server.Run(ctx, &mcp.StdioTransport{}); err != nil {
		return exitf(ExitError, "mcp server stopped: %v", err)
	}
	return nil
}
