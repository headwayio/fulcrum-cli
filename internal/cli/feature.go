package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/headwayio/fulcrum-cli/internal/estimate"
)

func (a *App) featureCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "feature",
		Short: "Work with a project's features",
	}
	cmd.AddCommand(a.featurePushCmd())
	return cmd
}

func (a *App) featurePushCmd() *cobra.Command {
	var project string
	var yes, dryRun bool
	cmd := &cobra.Command{
		Use:   "push [draft.json]",
		Short: "Append a locally-produced feature to a project's backlog",
		Long: "Sends a features-json draft up to a Fulcrum project.\n\n" +
			"APPEND ONLY. The server refuses every action but add, so a push can\n" +
			"add work to a backlog and can never modify or remove work already\n" +
			"there. An id that already exists is skipped, not overwritten.\n\n" +
			"The full payload is shown before anything is sent. Declining exits 0.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := ""
			if len(args) == 1 {
				path = args[0]
			}
			return a.runFeaturePush(path, project, yes, dryRun)
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "target project id or unambiguous name (required)")
	cmd.Flags().BoolVar(&yes, "yes", false, "push without prompting (automation)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would be sent and stop")
	_ = cmd.MarkFlagRequired("project")
	return cmd
}

func (a *App) runFeaturePush(path, projectRef string, yes, dryRun bool) error {
	raw, err := readDraft(a.Stdin, path)
	if err != nil {
		return err
	}
	payload, err := estimate.Parse(raw)
	if err != nil {
		return exitf(ExitError, "%v", err)
	}

	// Fail here rather than at the server: the contract is the same, and a
	// developer would rather be told what is wrong than have a round trip
	// tell them.
	if problems := payload.Validate(); len(problems) > 0 {
		fmt.Fprintln(a.Stderr, "This draft does not satisfy the estimate contract:")
		for _, problem := range problems {
			fmt.Fprintf(a.Stderr, "  - %s\n", problem)
		}
		return silentExit(ExitError)
	}
	if payload.Action != "add" {
		return exitf(ExitError,
			"only the add action can be pushed (this draft says %q) — modifying or removing "+
				"existing work belongs in the app, where it can be reviewed in context", payload.Action)
	}

	fmt.Fprintf(a.Stdout, "%d feature(s) to append:\n", len(payload.Features))
	for _, feature := range payload.Features {
		fmt.Fprintf(a.Stdout, "  %s (%s)\n", feature.Name, feature.MoscowPriority)
		for _, entry := range feature.Estimates {
			est := entry.Estimate
			fmt.Fprintf(a.Stdout, "    %s: %g–%g–%g (%s)\n",
				entry.Role, est.Low, est.Likely, est.High, est.Confidence)
		}
	}
	fmt.Fprintln(a.Stdout, "Committed hours are derived server-side from these ranges.")

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
	target, err := resolveProject(client, projectRef)
	if err != nil {
		return err
	}

	if !yes && !a.confirm(fmt.Sprintf("Append to project %d (%s)?", target.ID, target.Name)) {
		fmt.Fprintln(a.Stdout, "Nothing pushed.")
		return nil
	}

	receipt, err := client.PushFeatures(background(), target.ID, payload.Action, payload.Features)
	if err != nil {
		return wrapAPIError(err)
	}

	for _, created := range receipt.Created {
		fmt.Fprintf(a.Stdout, "created %s\n", created.Name)
		for _, est := range created.Estimates {
			fmt.Fprintf(a.Stdout, "  %s: %s (%gh committed)\n", est.Role, est.Complexity, est.Hours)
		}
	}
	for _, skipped := range receipt.Skipped {
		fmt.Fprintf(a.Stdout, "skipped %s (%s)\n", skipped.Name, skipped.Reason)
	}
	// Contract failures are the server's to report; surfacing them quietly
	// would hide that part of the estimate did not land.
	for _, dropped := range receipt.Dropped {
		fmt.Fprintf(a.Stderr, "dropped: %s\n", dropped)
	}
	if len(receipt.Created) > 0 {
		fmt.Fprintf(a.Stdout, "Review in Fulcrum: %s\n", receipt.ReviewURL)
	}
	if len(receipt.Dropped) > 0 {
		return silentExit(ExitStale)
	}
	return nil
}
