package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/headwayio/fulcrum-cli/internal/install"
	"github.com/headwayio/fulcrum-cli/internal/state"
)

func (a *App) syncCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Pull the organization's documents into the workspace",
		Long: "Pulls every manifest document into the workspace.\n\n" +
			"Local edits are never destroyed silently (the Ruby reference client's\n" +
			"sharpest edge): drifted/conflicted/proposed docs prompt before\n" +
			"clobbering on a terminal, and are skipped — reported, exit 1 — when\n" +
			"stdin is not a terminal. --force clobbers without asking anywhere.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runSync(force)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "overwrite local edits without prompting")
	return cmd
}

func (a *App) runSync(force bool) error {
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

	manifest, _, err := client.Manifest(background(), "")
	if err != nil {
		return wrapAPIError(err)
	}

	interactive := a.stdinTTY()
	skipped := 0
	for _, doc := range manifest.Documents {
		local, err := w.ReadLocal(doc.Slug)
		if err != nil {
			return exitf(ExitError, "read %s: %v", doc.Filename, err)
		}
		classification := w.State.Classify(doc.Slug, local, doc.Digest)

		// A draft's server copy is the template it was minted from — there is
		// no team version to catch up to, and --force would replace your
		// writing with boilerplate. Bulk sync leaves drafts alone; `revert`
		// is the explicit per-document way to go back to the template.
		if doc.Draft {
			fmt.Fprintf(a.Stdout, "draft %s (unpublished, left alone)\n", doc.Filename)
			continue
		}

		if classification == state.Synced && !force {
			// Digest identity: nothing moved, nothing to fetch.
			fmt.Fprintf(a.Stdout, "fresh %s\n", doc.Filename)
			continue
		}
		if hasLocalEdits(classification) && !force {
			if !interactive {
				// Non-TTY contract: no prompt and no clobber, ever.
				fmt.Fprintf(a.Stderr, "skipped %s: %s — re-run with --force to overwrite\n",
					doc.Filename, classification)
				skipped++
				continue
			}
			if !a.confirm(fmt.Sprintf("%s has local edits (%s) — overwrite?", doc.Filename, classification)) {
				fmt.Fprintf(a.Stdout, "skipped %s (kept your local edits)\n", doc.Filename)
				skipped++
				continue
			}
		}

		res, err := client.Document(background(), doc.Slug, "")
		if err != nil {
			return wrapAPIError(err)
		}
		if err := w.SyncDocument(doc, res.Body); err != nil {
			return exitf(ExitError, "write %s: %v", doc.Filename, err)
		}
		fmt.Fprintf(a.Stdout, "synced %s (%.12s…)\n", doc.Filename, doc.Digest)
	}

	// Whatever landed, push it into the projects already reading these
	// skills — a synced-but-uninstalled skill is invisible to the harness.
	install.Refresh(w, a.Stdout, a.Stderr)

	if skipped > 0 {
		fmt.Fprintf(a.Stderr, "%d document(s) skipped to protect local edits\n", skipped)
		return silentExit(ExitStale)
	}
	return nil
}

func hasLocalEdits(c state.Classification) bool {
	return c == state.Drifted || c == state.Conflicted || c == state.Proposed
}
