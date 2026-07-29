package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/headwayio/fulcrum-cli/internal/diffx"
	"github.com/headwayio/fulcrum-cli/internal/state"
)

func (a *App) mergeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "merge [slug]",
		Short: "Three-way merge the server's version into conflicted documents",
		Long: "A conflicted document moved on both sides. Merge combines the two\n" +
			"against the copy from your last sync — the same three-way merge git\n" +
			"does — taking each side's changes where they do not overlap and\n" +
			"writing conflict markers where they do. Afterwards the document is\n" +
			"drifted against the current version, so publishing it is truthful\n" +
			"rather than flagged stale. Exits 1 when markers were written.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			only := ""
			if len(args) == 1 {
				only = args[0]
			}
			return a.runMerge(only)
		},
	}
}

func (a *App) runMerge(only string) error {
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

	merged, conflicted := 0, 0
	for _, doc := range manifest.Documents {
		if only != "" && doc.Slug != only && doc.Filename != only {
			continue
		}
		local, readErr := w.ReadLocal(doc.Slug)
		if readErr != nil {
			return exitf(ExitError, "read %s: %v", doc.Filename, readErr)
		}

		// A local variant merges the team's newer version into itself; a
		// plain document merges the server's into the working file. Same
		// three-way merge either way, different pair of sides.
		hasBeta := w.State.BetaFor(doc.Slug) != nil
		if hasBeta && !betaNeedsMerge(w, doc) {
			continue
		}
		if !hasBeta && w.State.Classify(doc.Slug, local, doc.Digest) != state.Conflicted {
			continue
		}
		if !hasBeta && w.Base(doc.Slug) == nil {
			fmt.Fprintf(a.Stderr, "%s: no pristine base — sync once with this client, then merge\n", doc.Filename)
			continue
		}

		res, docErr := client.Document(background(), doc.Slug, "")
		if docErr != nil {
			return wrapAPIError(docErr)
		}

		var conflicts int
		target := doc.Filename
		if hasBeta {
			target = w.State.BetaFor(doc.Slug).Filename
			conflicts, err = mergeBeta(w, doc, res.Body)
			if err != nil {
				return exitf(ExitError, "%v", err)
			}
		} else {
			var content []byte
			content, conflicts = diffx.Merge3(w.Base(doc.Slug), local, res.Body)
			if err := w.AdoptMerge(doc, res.Body, content); err != nil {
				return exitf(ExitError, "write %s: %v", doc.Filename, err)
			}
		}

		merged++
		if conflicts > 0 {
			conflicted++
			fmt.Fprintf(a.Stdout, "merged %s with %d conflict(s) — markers written, resolve them before publishing\n",
				target, conflicts)
		} else {
			fmt.Fprintf(a.Stdout, "merged %s cleanly — now based on the current version\n", target)
		}
	}

	if merged == 0 {
		fmt.Fprintln(a.Stdout, "Nothing to merge.")
		return nil
	}
	if conflicted > 0 {
		return silentExit(ExitStale)
	}
	return nil
}
