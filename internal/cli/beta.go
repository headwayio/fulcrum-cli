package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/headwayio/fulcrum-cli/internal/api"
	"github.com/headwayio/fulcrum-cli/internal/diffx"
	"github.com/headwayio/fulcrum-cli/internal/workspace"
)

func (a *App) skillsBetaCmd() *cobra.Command {
	var drop bool
	cmd := &cobra.Command{
		Use:   "beta <slug>",
		Short: "Keep using your own version of a skill while the team's keeps syncing",
		Long: "Splits a document in two: your working version becomes <name>.beta.md,\n" +
			"and the canonical one goes back to whatever the server has and keeps\n" +
			"syncing from there. Your beta is what gets installed into projects, so\n" +
			"agents run your experiment — under the canonical name, so they still\n" +
			"see exactly one skill by that name.\n\n" +
			"Nothing is stuck: `fulcrum merge` pulls the team's newer version into\n" +
			"your beta, publishing it proposes it as the team's next version, and\n" +
			"--drop hands authority back to the canonical document.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runSkillsBeta(args[0], drop)
		},
	}
	cmd.Flags().BoolVar(&drop, "drop", false, "discard the local variant and follow the canonical document again")
	return cmd
}

func (a *App) runSkillsBeta(name string, drop bool) error {
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
	doc := findDocument(manifest, name)
	if doc == nil {
		return exitf(ExitError, "no document named %q — run `fulcrum status`", name)
	}

	if drop {
		kept, dropErr := w.DropBeta(doc.Slug)
		if dropErr != nil {
			return exitf(ExitError, "%v", dropErr)
		}
		if kept == "" {
			fmt.Fprintf(a.Stdout, "%s has no local variant.\n", doc.Filename)
			return nil
		}
		fmt.Fprintf(a.Stdout, "dropped the variant for %s — your text is kept at %s\n", doc.Filename, kept)
		return nil
	}

	if existing := w.State.BetaFor(doc.Slug); existing != nil {
		fmt.Fprintf(a.Stdout, "%s already has a local variant at %s\n", doc.Filename, existing.Filename)
		return nil
	}

	// The developer's current working copy becomes the variant. Falling back
	// to the canonical means `beta` also works as "start experimenting from
	// what the team has".
	content, err := w.ReadLocal(doc.Slug)
	if err != nil {
		return exitf(ExitError, "read %s: %v", doc.Filename, err)
	}
	res, err := client.Document(background(), doc.Slug, "")
	if err != nil {
		return wrapAPIError(err)
	}
	if content == nil {
		content = res.Body
	}

	if err := w.StartBeta(*doc, content, res.Body); err != nil {
		return exitf(ExitError, "%v", err)
	}
	fmt.Fprintf(a.Stdout,
		"your version of %s is now %s — it is what installs into projects\n",
		doc.Filename, workspace.BetaFilename(doc.Filename))
	fmt.Fprintf(a.Stdout,
		"%s follows the team again; `fulcrum merge` brings their changes into yours\n", doc.Filename)
	return nil
}

// findDocument matches a manifest document by slug or filename.
func findDocument(manifest *api.Manifest, name string) *api.ManifestDocument {
	for i := range manifest.Documents {
		if manifest.Documents[i].Slug == name || manifest.Documents[i].Filename == name {
			return &manifest.Documents[i]
		}
	}
	return nil
}

// publishableContent is what a proposal should carry for a document: the
// local variant when there is one, otherwise the working file — along with
// the base digest that makes based_on_current truthful for whichever it is.
func publishableContent(w *workspace.Workspace, doc api.ManifestDocument) (content []byte, baseDigest string, err error) {
	if beta := w.State.BetaFor(doc.Slug); beta != nil {
		return w.ReadBeta(doc.Slug), beta.BaseDigest, nil
	}
	content, err = w.ReadLocal(doc.Slug)
	if recorded := w.State.Document(doc.Slug); recorded != nil {
		// The digest recorded AT LAST SYNC, never the current manifest's —
		// this is what keeps based_on_current truthful. Do not "fix" it.
		baseDigest = recorded.RemoteDigest
	}
	return content, baseDigest, err
}

// betaNeedsMerge reports whether a variant has fallen behind its canonical.
func betaNeedsMerge(w *workspace.Workspace, doc api.ManifestDocument) bool {
	beta := w.State.BetaFor(doc.Slug)
	return beta != nil && beta.BaseDigest != doc.Digest
}

// mergeBeta three-way merges the canonical's current version into the
// variant, entirely from files already on disk.
func mergeBeta(w *workspace.Workspace, doc api.ManifestDocument, canonical []byte) (int, error) {
	base := w.BetaBase(doc.Slug)
	if base == nil {
		return 0, fmt.Errorf("%s has no recorded fork point", doc.Filename)
	}
	merged, conflicts := diffx.Merge3(base, w.ReadBeta(doc.Slug), canonical)
	if err := w.RebaseBeta(doc, merged, canonical); err != nil {
		return 0, err
	}
	return conflicts, nil
}
