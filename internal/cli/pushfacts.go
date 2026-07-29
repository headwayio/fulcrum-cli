package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/headwayio/fulcrum-cli/internal/scan"
)

func (a *App) pushFactsCmd() *cobra.Command {
	var project string
	var yes, dryRun bool
	cmd := &cobra.Command{
		Use:   "push-facts <repo-path>",
		Short: "Scan a local checkout and push its architecture facts to a project",
		Long: "Collects shallow, deterministic facts (language mix, key dependencies)\n" +
			"from a local checkout and pushes them to a project's architecture\n" +
			"profile. The FULL payload is shown before anything is sent — nothing\n" +
			"else leaves this machine. Declining exits 0.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runPushFacts(args[0], project, yes, dryRun)
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "target project id or unambiguous name (required)")
	cmd.Flags().BoolVar(&yes, "yes", false, "push without prompting (automation)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show the payload and stop")
	_ = cmd.MarkFlagRequired("project")
	return cmd
}

func (a *App) runPushFacts(repoPath, projectRef string, yes, dryRun bool) error {
	facts, err := scan.Collect(repoPath)
	if err != nil {
		return exitf(ExitError, "scan %s: %v", repoPath, err)
	}

	preview := struct {
		Languages    map[string]int `json:"languages"`
		Dependencies []string       `json:"dependencies"`
		Repository   string         `json:"repository"`
	}{facts.Languages, facts.Dependencies, facts.Repository}
	encoded, _ := json.MarshalIndent(preview, "", "  ")

	fmt.Fprintf(a.Stdout, "Facts for %s: %s · %d dependencies\n",
		facts.Repository, strings.Join(firstN(facts.LanguageOrder, 5), ", "), len(facts.Dependencies))
	fmt.Fprintln(a.Stdout, string(encoded))
	fmt.Fprintln(a.Stdout, "This payload is everything that would be sent — nothing else leaves this machine.")

	if dryRun {
		fmt.Fprintln(a.Stdout, "Dry run: nothing pushed.")
		return nil
	}

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

	if !yes && !a.confirm(fmt.Sprintf("Push to project %d (%s)?", project.ID, project.Name)) {
		fmt.Fprintln(a.Stdout, "Nothing pushed.")
		return nil
	}

	receipt, err := client.PushArchitecture(background(), project.ID, facts.Payload(), facts.Repository)
	if err != nil {
		return wrapAPIError(err)
	}
	fmt.Fprintf(a.Stdout, "Pushed — the next estimate for project %d sees these facts.\n", receipt.ProjectID)
	return nil
}

func firstN(values []string, n int) []string {
	if len(values) < n {
		return values
	}
	return values[:n]
}
