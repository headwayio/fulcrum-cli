package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/headwayio/fulcrum-cli/internal/api"
	"github.com/headwayio/fulcrum-cli/internal/diffx"
	"github.com/headwayio/fulcrum-cli/internal/state"
)

func (a *App) publishCmd() *cobra.Command {
	var yes bool
	var note string
	cmd := &cobra.Command{
		Use:   "publish",
		Short: "Propose your local edits back for review",
		Long: "Submits locally edited JSON documents as reviewable proposals —\n" +
			"the ONLY path upstream; nothing leaves the machine without an explicit\n" +
			"per-document confirmation (--yes for automation). Conflicted docs are\n" +
			"submitted flagged stale for the reviewer.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runPublish(yes, note)
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "publish without prompting (automation)")
	cmd.Flags().StringVar(&note, "note", "", "reviewer note (required with --yes)")
	return cmd
}

func (a *App) runPublish(yes bool, note string) error {
	if yes && strings.TrimSpace(note) == "" {
		return exitf(ExitError, "--yes needs --note: the reviewer note is required")
	}

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

	submitted := 0
	for _, doc := range manifest.Documents {
		// Publishable = anything with a proposal slug: JSON sources and org
		// skills alike. Generated renderings have none.
		if proposalSlug(doc) == "" {
			continue
		}
		// A local variant is what gets proposed when there is one — it is the
		// version the developer is actually running.
		local, baseDigest, readErr := publishableContent(w, doc)
		if readErr != nil {
			return exitf(ExitError, "read %s: %v", doc.Filename, readErr)
		}
		beta := w.State.BetaFor(doc.Slug)
		if beta == nil {
			classification := w.State.Classify(doc.Slug, local, doc.Digest)
			if classification != state.Drifted && classification != state.Conflicted {
				continue
			}
			if classification == state.Conflicted {
				fmt.Fprintf(a.Stdout, "%s: remote moved since your sync — your proposal will be "+
					"flagged stale for the reviewer (`fulcrum merge` fixes that).\n", doc.Filename)
			}
		} else {
			if local == nil {
				continue
			}
			// Already-proposed variants stay quiet, same as any other doc.
			if w.State.Classify(doc.Slug, local, doc.Digest) == state.Proposed {
				continue
			}
			if betaNeedsMerge(w, doc) {
				fmt.Fprintf(a.Stdout, "%s: your variant is behind the current version — it will be "+
					"flagged stale (`fulcrum merge` fixes that).\n", beta.Filename)
			}
		}

		if diffx.HasConflictMarkers(local) {
			fmt.Fprintf(a.Stdout, "%s: unresolved conflict markers — resolve them, nothing submitted\n",
				doc.Filename)
			continue
		}

		if !yes && !a.confirm(fmt.Sprintf("Publish your edits to %s as a proposal?", doc.Filename)) {
			continue
		}

		docNote := note
		if !yes && docNote == "" {
			fmt.Fprintln(a.Stdout, "One-line note for the reviewer:")
			docNote = a.readLine()
		}

		var document map[string]any
		if doc.Format == "json" {
			if err := json.Unmarshal(local, &document); err != nil {
				fmt.Fprintf(a.Stdout, "%s: not valid JSON, nothing submitted (%.120s)\n", doc.Filename, err.Error())
				continue
			}
		} else {
			// Markdown publishes as the content wrap — needs the server to
			// accept skill proposals.
			if !manifest.API.Has("skill_proposals") {
				fmt.Fprintf(a.Stdout, "%s: this server does not accept markdown proposals yet, skipped\n", doc.Filename)
				continue
			}
			document = map[string]any{"content": string(local)}
		}

		receipt, err := client.SubmitProposal(background(), proposalSlug(doc), document, baseDigest, docNote)
		if err != nil {
			return wrapAPIError(err)
		}
		if err := w.RecordProposal(doc.Slug, receipt.ID, state.HexSHA256(local)); err != nil {
			return exitf(ExitError, "save state: %v", err)
		}
		submitted++
		fmt.Fprintf(a.Stdout, "Proposal #%d submitted — review at %s\n", receipt.ID, receipt.ReviewURL)
	}

	if submitted == 0 {
		fmt.Fprintln(a.Stdout, "Nothing published.")
	} else {
		fmt.Fprintf(a.Stdout, "%d proposal(s) submitted.\n", submitted)
	}
	return nil
}

// proposalSlug prefers the manifest's mapping (contract 1); the documented
// "-source" convention covers JSON docs on pre-contract servers. Markdown
// without an explicit mapping is a generated rendering — not proposable.
func proposalSlug(doc api.ManifestDocument) string {
	if doc.ProposalSlug != nil {
		return *doc.ProposalSlug
	}
	if doc.Format == "json" {
		return strings.TrimSuffix(doc.Slug, "-source")
	}
	return ""
}
