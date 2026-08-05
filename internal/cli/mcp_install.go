package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/headwayio/fulcrum-cli/internal/mcpinstall"
	"github.com/headwayio/fulcrum-cli/internal/projectctx"
)

func (a *App) mcpInstallCmd() *cobra.Command {
	var dir, target string
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Register `fulcrum mcp` with the coding harnesses in this project",
		Long: "Writes the Model Context Protocol server entry so Claude Code, Codex and\n" +
			"Kimi Code can reach Fulcrum from this checkout.\n\n" +
			"Claude Code and Codex get PROJECT-scoped entries (.mcp.json and\n" +
			".codex/config.toml), so a teammate who clones the repository gets the\n" +
			"server without setting anything up. Kimi only reads a global file, which\n" +
			"costs nothing here: the server works out which project it is in from the\n" +
			"directory the harness was launched in.\n\n" +
			"An entry that already exists is left exactly as it is.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runMCPInstall(dir, target)
		},
	}
	cmd.Flags().StringVar(&dir, "dir", ".", "project directory to install into")
	cmd.Flags().StringVar(&target, "target", "",
		"comma-separated harnesses ("+strings.Join(mcpinstall.AllTargets, ", ")+"); default is all")
	return cmd
}

func (a *App) runMCPInstall(dir, target string) error {
	projectDir, err := filepath.Abs(dir)
	if err != nil {
		return exitf(ExitError, "%v", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return exitf(ExitError, "cannot find your home directory: %v", err)
	}

	// The absolute path of THIS binary, not the bare name: a harness is
	// launched from a desktop app or a login shell whose PATH need not be the
	// one this command ran under, and "command not found" surfaces inside the
	// harness as an unexplained missing server.
	executable, err := os.Executable()
	if err != nil {
		return exitf(ExitError, "cannot determine this binary's path: %v", err)
	}
	if resolved, linkErr := filepath.EvalSymlinks(executable); linkErr == nil {
		executable = resolved
	}

	targets := mcpinstall.AllTargets
	if strings.TrimSpace(target) != "" {
		targets = strings.Split(strings.ReplaceAll(target, " ", ""), ",")
	}

	results, err := mcpinstall.Install(targets, mcpinstall.Options{
		ProjectDir: projectDir,
		Command:    executable,
		Args:       []string{"mcp"},
		HomeDir:    home,
	})
	if err != nil {
		return exitf(ExitError, "%v", err)
	}

	for _, result := range results {
		switch {
		case result.Changed:
			fmt.Fprintf(a.Stdout, "registered with %s → %s\n", result.Target, display(result.Path, home))
		default:
			fmt.Fprintf(a.Stdout, "%s: %s (%s)\n", result.Target, result.Note, display(result.Path, home))
		}
	}

	// Say plainly whether the tools will be able to work out the project on
	// their own, because the answer changes what the developer has to type at
	// their agent for the rest of the day.
	local, _ := projectctx.Resolve(projectDir)
	if local == nil {
		fmt.Fprintf(a.Stdout,
			"\nThis checkout has no Fulcrum project linked yet, so the tools will ask you to\n"+
				"name a project. Run `fulcrum context --project <name>` to link one.\n")
	} else {
		fmt.Fprintf(a.Stdout, "\nThis checkout is project %s (id %d); the tools will use it by default.\n",
			local.ProjectName, local.ProjectID)
	}
	return nil
}

func display(path, home string) string {
	if home != "" && strings.HasPrefix(path, home+string(filepath.Separator)) {
		return "~" + path[len(home):]
	}
	return path
}
