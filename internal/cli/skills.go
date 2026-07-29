package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/headwayio/fulcrum-cli/internal/api"
)

// installedPinFile records what `skills install` placed, so a harness (the
// Electron feature-start flow, a script) can compare digests against the
// workspace without re-reading every file.
const installedPinFile = ".fulcrum-installed.json"

func (a *App) skillsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skills",
		Short: "Work with the org's synced skill documents",
	}
	cmd.AddCommand(a.skillsInstallCmd(), a.skillsNewCmd())
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

// fulcrum skills install [project-dir]: copy the synced org skills into a
// project's .claude/skills/<slug>/SKILL.md — the shape Claude Code loads —
// so fulcrum feature work runs with the team-approved set instead of
// whatever personal skills happen to be on the machine. Idempotent:
// re-running converges on the workspace's current state.
func (a *App) skillsInstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install [project-dir]",
		Short: "Install synced org skills into a project's .claude/skills",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) == 1 {
				dir = args[0]
			}
			return a.runSkillsInstall(dir)
		},
	}
}

func (a *App) runSkillsInstall(projectDir string) error {
	resolved, err := a.resolveConfig()
	if err != nil {
		return err
	}
	w, err := a.openWorkspace(resolved)
	if err != nil {
		return err
	}

	type installed struct {
		slug   string
		digest string
	}
	var results []installed
	for slug, record := range w.State.Documents {
		skillSlug, ok := strings.CutPrefix(slug, "skill-")
		if !ok {
			continue // the rubric pair and any future non-skill documents
		}
		body, readErr := w.ReadLocal(slug)
		if readErr != nil {
			return exitf(ExitError, "read %s: %v", record.Filename, readErr)
		}
		if body == nil {
			fmt.Fprintf(a.Stderr, "skipped %s: local file missing — run `fulcrum sync`\n", record.Filename)
			continue
		}
		target := filepath.Join(projectDir, ".claude", "skills", skillSlug, "SKILL.md")
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return exitf(ExitError, "%v", err)
		}
		if err := os.WriteFile(target, body, 0o644); err != nil {
			return exitf(ExitError, "write %s: %v", target, err)
		}
		results = append(results, installed{slug: skillSlug, digest: record.FileSHA256})
		fmt.Fprintf(a.Stdout, "installed %s → %s\n", skillSlug, target)
	}

	if len(results) == 0 {
		fmt.Fprintln(a.Stdout, "No synced skills to install — `fulcrum sync` first, or the org has none yet.")
		return nil
	}

	sort.Slice(results, func(i, j int) bool { return results[i].slug < results[j].slug })
	pins := map[string]string{}
	for _, r := range results {
		pins[r.slug] = r.digest
	}
	pinPath := filepath.Join(projectDir, ".claude", "skills", installedPinFile)
	encoded, _ := json.MarshalIndent(map[string]any{"installed": pins}, "", "  ")
	if err := os.WriteFile(pinPath, append(encoded, '\n'), 0o644); err != nil {
		return exitf(ExitError, "write %s: %v", pinPath, err)
	}
	fmt.Fprintf(a.Stdout, "%d skill(s) installed; digests pinned in %s\n", len(results), pinPath)
	return nil
}
