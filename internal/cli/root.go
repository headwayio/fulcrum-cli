package cli

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

// Root builds the command tree. Bare invocation currently runs status; when
// the TUI lands it takes over bare TTY invocations while piped invocations
// keep meaning status.
func (a *App) Root() *cobra.Command {
	root := &cobra.Command{
		Use:           "fulcrum",
		Short:         "Sync your organization's codified estimation knowledge",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runStatus(false, false)
		},
	}
	root.SetOut(a.Stdout)
	root.SetErr(a.Stderr)

	root.AddCommand(a.syncCmd(), a.statusCmd(), a.projectsCmd(), a.publishCmd(),
		a.pushFactsCmd(), a.catCmd(), a.loginCmd(), a.versionCmd(), a.skillsCmd(),
		a.mergeCmd())
	return root
}

// Main executes the tree and returns the process exit code.
func (a *App) Main(args []string) int {
	root := a.Root()
	root.SetArgs(args)
	err := root.Execute()
	a.Report(err)
	return ExitCode(err)
}

func (a *App) versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the client version",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintf(a.Stdout, "fulcrum %s (%s/%s)\n", a.Version, runtime.GOOS, runtime.GOARCH)
			return nil
		},
	}
}

func (a *App) catCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "cat <slug>",
		Short: "Print a document's current remote bytes",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			resolved, err := a.resolveConfig()
			if err != nil {
				return err
			}
			client, err := a.client(resolved)
			if err != nil {
				return err
			}
			res, err := client.Document(background(), args[0], "")
			if err != nil {
				return wrapAPIError(err)
			}
			_, writeErr := a.Stdout.Write(res.Body)
			return writeErr
		},
	}
}
