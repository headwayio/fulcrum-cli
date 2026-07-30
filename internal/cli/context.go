package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/headwayio/fulcrum-cli/internal/estimate"
)

// ContextDir is where the bundle lands inside the project being estimated.
// A directory rather than a dotfile because the skill writes its survey and
// draft alongside it, and one gitignore line should cover the lot.
const ContextDir = ".fulcrum"

const (
	contextFile  = "project-context.md"
	snappingFile = "snapping.json"
)

func (a *App) contextCmd() *cobra.Command {
	var project, dir string
	cmd := &cobra.Command{
		Use:   "context",
		Short: "Pull a project's estimation context for local estimating",
		Long: "Writes the project's estimation context into ./" + ContextDir + " — the rubric,\n" +
			"delivery roles, complexity scale, releases, and the inventory of\n" +
			"features this team has already priced.\n\n" +
			"That inventory is the point: it is what makes a local estimate reflect\n" +
			"how your team actually sizes work rather than what the internet thinks\n" +
			"the work takes.\n\n" +
			"Weekly rates are NOT included — a local estimate produces hours, and\n" +
			"cost stays server-side.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runContext(project, dir)
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "project id or unambiguous name (required)")
	cmd.Flags().StringVar(&dir, "dir", ".", "project directory to write into")
	_ = cmd.MarkFlagRequired("project")
	return cmd
}

func (a *App) runContext(projectRef, into string) error {
	resolved, err := a.resolveConfig()
	if err != nil {
		return err
	}
	client, err := a.client(resolved)
	if err != nil {
		return err
	}
	project, err := resolveProject(client, projectRef)
	if err != nil {
		return err
	}

	bundle, err := client.ProjectContext(background(), project.ID)
	if err != nil {
		return wrapAPIError(err)
	}

	// Refuse to ship a bundle this client would snap differently than the
	// server does. A context that looks right but rounds one size off is
	// worse than no context, because nothing about the output looks wrong.
	if failures := estimate.FixtureFailures(bundle.Fixtures); len(failures) > 0 {
		fmt.Fprintln(a.Stderr, "This client disagrees with the server's snapping rule:")
		for _, failure := range failures {
			fmt.Fprintf(a.Stderr, "  %s\n", failure)
		}
		return exitf(ExitError,
			"refusing to write a context whose committed values would not match Fulcrum's — upgrade the client")
	}

	dir := filepath.Join(into, ContextDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return exitf(ExitError, "create %s: %v", dir, err)
	}

	if err := os.WriteFile(filepath.Join(dir, contextFile), []byte(bundle.Body), 0o644); err != nil {
		return exitf(ExitError, "write %s: %v", contextFile, err)
	}
	encoded, err := json.MarshalIndent(bundle.Fixtures, "", "  ")
	if err != nil {
		return exitf(ExitError, "encode snapping table: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, snappingFile), append(encoded, '\n'), 0o644); err != nil {
		return exitf(ExitError, "write %s: %v", snappingFile, err)
	}

	ignored, err := ensureGitignore(into)
	if err != nil {
		fmt.Fprintf(a.Stderr, "could not update .gitignore: %v\n", err)
	}

	fmt.Fprintf(a.Stdout, "wrote %s (%.12s…) for %s\n",
		filepath.Join(ContextDir, contextFile), bundle.Digest, bundle.Project.Name)
	fmt.Fprintf(a.Stdout, "wrote %s (%d snapping cases)\n",
		filepath.Join(ContextDir, snappingFile), len(bundle.Fixtures.Cases))
	if ignored {
		fmt.Fprintf(a.Stdout, "added %s/ to .gitignore\n", ContextDir)
	}
	return nil
}

// ensureGitignore keeps the context out of version control. It carries a
// project's priced backlog; committing it would push that into every clone
// and every fork. Reports whether it added the entry.
func ensureGitignore(projectDir string) (bool, error) {
	path := filepath.Join(projectDir, ".gitignore")
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}

	entry := ContextDir + "/"
	for _, line := range strings.Split(string(existing), "\n") {
		switch strings.TrimSpace(line) {
		case entry, ContextDir:
			return false, nil
		}
	}

	body := string(existing)
	if body != "" && !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	body += entry + "\n"
	return true, os.WriteFile(path, []byte(body), 0o644)
}
