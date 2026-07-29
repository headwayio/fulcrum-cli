package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/headwayio/fulcrum-cli/internal/api"
)

func (a *App) skillsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skills",
		Short: "Work with the org's synced skill documents",
	}
	cmd.AddCommand(a.skillsInstallCmd(), a.skillsNewCmd(), a.skillsBetaCmd())
	return cmd
}

// fulcrum skills new <name>: mint a creator-only draft skill on the server
// and land its template in the workspace. Nobody else — including admins —
// sees it until `fulcrum publish` submits it for review.
func (a *App) skillsNewCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "new <kebab-case-name>",
		Short: "Draft a new org skill (only you see it until you publish)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runSkillsNew(args[0])
		},
	}
}

func (a *App) runSkillsNew(name string) error {
	resolved, err := a.resolveConfig()
	if err != nil {
		return err
	}
	client, err := a.client(resolved)
	if err != nil {
		return err
	}
	w, err := a.openWorkspace(resolved)
	if err != nil {
		return err
	}

	draft, err := client.CreateSkillDraft(background(), name)
	if err != nil {
		return wrapAPIError(err)
	}
	proposalSlug := draft.ProposalSlug
	err = w.SyncDocument(api.ManifestDocument{
		Slug: draft.Slug, Format: draft.Format, Digest: draft.Digest,
		Filename: draft.Filename, ProposalSlug: &proposalSlug, Draft: true,
	}, []byte(draft.Content))
	if err != nil {
		return exitf(ExitError, "write %s: %v", draft.Filename, err)
	}

	fmt.Fprintf(a.Stdout, "draft %s created (v%d) — only you see it until you publish\n",
		draft.Filename, draft.Version)
	fmt.Fprintf(a.Stdout, "edit it, then: fulcrum publish\n")
	return nil
}
