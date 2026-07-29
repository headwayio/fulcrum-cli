package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/headwayio/fulcrum-cli/internal/state"
)

func (a *App) revertCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "revert [slug]",
		Short: "Throw away local edits and take the server's version",
		Long: "Replaces a document with what Fulcrum has now, discarding your local\n" +
			"edits — the deliberate way out of a conflict when your side is not\n" +
			"worth keeping. A copy of what you discarded is kept under\n" +
			".fulcrum/discarded/ in the workspace. With no slug, every locally\n" +
			"edited document is offered in turn.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			only := ""
			if len(args) == 1 {
				only = args[0]
			}
			return a.runRevert(only, yes)
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "discard without confirming (automation)")
	return cmd
}

func (a *App) runRevert(only string, yes bool) error {
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

	reverted, matched := 0, false
	for _, doc := range manifest.Documents {
		if only != "" && doc.Slug != only && doc.Filename != only {
			continue
		}
		matched = true

		local, readErr := w.ReadLocal(doc.Slug)
		if readErr != nil {
			return exitf(ExitError, "read %s: %v", doc.Filename, readErr)
		}
		classification := w.State.Classify(doc.Slug, local, doc.Digest)
		if !hasLocalEdits(classification) && classification != state.Missing {
			continue
		}

		if !yes && !a.confirm(fmt.Sprintf(
			"Discard your local edits to %s and take the server's version?", doc.Filename)) {
			continue
		}

		backup, backupErr := w.BackupLocal(doc.Slug)
		if backupErr != nil {
			return exitf(ExitError, "back up %s: %v", doc.Filename, backupErr)
		}

		res, docErr := client.Document(background(), doc.Slug, "")
		if docErr != nil {
			return wrapAPIError(docErr)
		}
		if err := w.SyncDocument(doc, res.Body); err != nil {
			return exitf(ExitError, "write %s: %v", doc.Filename, err)
		}

		reverted++
		if backup != "" {
			fmt.Fprintf(a.Stdout, "reverted %s — your version is kept at %s\n", doc.Filename, backup)
		} else {
			fmt.Fprintf(a.Stdout, "restored %s from the server\n", doc.Filename)
		}
	}

	if only != "" && !matched {
		return exitf(ExitError, "no document named %q — run `fulcrum status`", only)
	}
	if reverted == 0 {
		fmt.Fprintln(a.Stdout, "Nothing to revert.")
	}
	return nil
}
