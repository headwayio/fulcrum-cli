package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/headwayio/fulcrum-cli/internal/install"
)

func (a *App) skillsInstallCmd() *cobra.Command {
	var target string
	cmd := &cobra.Command{
		Use:   "install [project-dir]",
		Short: "Install synced org skills into a project's agent harnesses",
		Long: "Writes the org's synced skills into a project so agentic work there\n" +
			"runs on the team-approved set — in every format at once, because one\n" +
			"project often drives several agents:\n\n" +
			"  claude  .claude/skills/<slug>/SKILL.md — Claude Code\n" +
			"  shared  .skills/<slug>/SKILL.md — Kimi Code, OpenCode, and other\n" +
			"          SKILL.md-compatible agents\n" +
			"  agents  a managed block in AGENTS.md — Codex, Kimi Code, and the\n" +
			"          rest of the AGENTS.md convention\n\n" +
			"--target narrows it (claude|shared|agents|all|auto; auto writes only\n" +
			"the formats the project already uses). The project is remembered, so\n" +
			"later syncs refresh every format. Idempotent — re-running converges.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) == 1 {
				dir = args[0]
			}
			return a.runSkillsInstall(dir, target)
		},
	}
	cmd.Flags().StringVar(&target, "target", "all", "claude, shared, agents, all, or auto")
	return cmd
}

func (a *App) runSkillsInstall(projectDir, target string) error {
	targets, err := install.Targets(projectDir, target)
	if err != nil {
		return exitf(ExitError, "%v", err)
	}
	resolved, err := a.resolveConfig()
	if err != nil {
		return err
	}
	w, err := a.openWorkspace(resolved)
	if err != nil {
		return err
	}

	installed, err := install.Into(w, projectDir, targets, a.Stdout, a.Stderr)
	if err != nil {
		return exitf(ExitError, "%v", err)
	}
	if installed == 0 {
		fmt.Fprintln(a.Stdout, "No synced skills to install — `fulcrum sync` first, or the org has none yet.")
		return nil
	}

	// Remember the project so later syncs keep every format current rather
	// than leaving harnesses reading yesterday's skills.
	if w.State.RememberInstall(projectDir) {
		if err := w.SaveState(); err != nil {
			return exitf(ExitError, "save state: %v", err)
		}
	}
	fmt.Fprintf(a.Stdout, "%d skill(s) installed for %s\n", installed, strings.Join(targets, " + "))
	return nil
}
