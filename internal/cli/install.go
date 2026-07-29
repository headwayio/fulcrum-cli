package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/headwayio/fulcrum-cli/internal/skillmd"
)

// installedPinFile records what was installed, so a harness (the Electron
// feature-start flow, a script) can compare digests against the workspace
// without re-reading every file.
const installedPinFile = ".fulcrum-installed.json"

// Managed-block fences for AGENTS.md. Everything between them belongs to
// fulcrum; everything outside is the project's and is never touched.
const (
	agentsBegin = "<!-- BEGIN fulcrum skills — managed by `fulcrum skills install`, edits here are overwritten -->"
	agentsEnd   = "<!-- END fulcrum skills -->"
)

// Harness formats. Agents covers every tool that reads AGENTS.md (Codex and
// the other adopters of that convention); Claude Code reads a directory of
// SKILL.md files instead. A tool with a different loader gets a new entry
// here rather than a new mechanism.
const (
	targetClaude = "claude"
	targetAgents = "agents"
)

type installableSkill struct {
	slug        string
	digest      string
	name        string
	description string
	content     []byte
}

func (a *App) skillsInstallCmd() *cobra.Command {
	var target string
	cmd := &cobra.Command{
		Use:   "install [project-dir]",
		Short: "Install synced org skills into a project's agent harness",
		Long: "Writes the org's synced skills into a project so agentic work there\n" +
			"runs on the team-approved set. Formats:\n\n" +
			"  claude  .claude/skills/<slug>/SKILL.md — Claude Code\n" +
			"  agents  a managed block in AGENTS.md — Codex and other readers\n" +
			"          of the AGENTS.md convention\n\n" +
			"Default is auto: whichever of those the project already uses, or\n" +
			"claude when it uses neither. --target claude|agents|all overrides.\n" +
			"Idempotent — re-running converges on the workspace's current state.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) == 1 {
				dir = args[0]
			}
			return a.runSkillsInstall(dir, target)
		},
	}
	cmd.Flags().StringVar(&target, "target", "auto", "claude, agents, all, or auto")
	return cmd
}

func (a *App) runSkillsInstall(projectDir, target string) error {
	targets, err := resolveTargets(projectDir, target)
	if err != nil {
		return err
	}

	resolved, err := a.resolveConfig()
	if err != nil {
		return err
	}
	w, err := a.openWorkspace(resolved)
	if err != nil {
		return err
	}

	var skills []installableSkill
	for slug, record := range w.State.Documents {
		skillSlug, ok := strings.CutPrefix(slug, "skill-")
		if !ok {
			continue // the rubric pair and any future non-skill documents
		}
		content, readErr := w.ReadLocal(slug)
		if readErr != nil {
			return exitf(ExitError, "read %s: %v", record.Filename, readErr)
		}
		if content == nil {
			fmt.Fprintf(a.Stderr, "skipped %s: local file missing — run `fulcrum sync`\n", record.Filename)
			continue
		}
		name, description := skillmd.Describe(string(content), skillSlug)
		skills = append(skills, installableSkill{
			slug: skillSlug, digest: record.FileSHA256,
			name: name, description: description, content: content,
		})
	}

	if len(skills) == 0 {
		fmt.Fprintln(a.Stdout, "No synced skills to install — `fulcrum sync` first, or the org has none yet.")
		return nil
	}
	sort.Slice(skills, func(i, j int) bool { return skills[i].slug < skills[j].slug })

	for _, name := range targets {
		switch name {
		case targetClaude:
			if err := a.installClaude(projectDir, skills); err != nil {
				return err
			}
		case targetAgents:
			if err := a.installAgents(projectDir, skills); err != nil {
				return err
			}
		}
	}

	if err := a.writePins(projectDir, skills, targets); err != nil {
		return err
	}
	fmt.Fprintf(a.Stdout, "%d skill(s) installed for %s\n", len(skills), strings.Join(targets, " + "))
	return nil
}

// resolveTargets picks harness formats: an explicit --target wins, otherwise
// whichever the project already uses, defaulting to claude for a project
// that uses neither (rather than creating an AGENTS.md nobody asked for).
func resolveTargets(projectDir, target string) ([]string, error) {
	switch target {
	case targetClaude, targetAgents:
		return []string{target}, nil
	case "all":
		return []string{targetClaude, targetAgents}, nil
	case "auto", "":
		var found []string
		if _, err := os.Stat(filepath.Join(projectDir, ".claude")); err == nil {
			found = append(found, targetClaude)
		}
		if _, err := os.Stat(filepath.Join(projectDir, "AGENTS.md")); err == nil {
			found = append(found, targetAgents)
		}
		if len(found) == 0 {
			return []string{targetClaude}, nil
		}
		return found, nil
	default:
		return nil, exitf(ExitError, "unknown --target %q: use claude, agents, all, or auto", target)
	}
}

// installClaude writes one directory per skill, the shape Claude Code loads.
func (a *App) installClaude(projectDir string, skills []installableSkill) error {
	for _, skill := range skills {
		path := filepath.Join(projectDir, ".claude", "skills", skill.slug, "SKILL.md")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return exitf(ExitError, "%v", err)
		}
		if err := os.WriteFile(path, skill.content, 0o644); err != nil {
			return exitf(ExitError, "write %s: %v", path, err)
		}
		fmt.Fprintf(a.Stdout, "installed %s → %s\n", skill.slug, path)
	}
	return nil
}

// installAgents maintains one fulcrum-owned block in AGENTS.md. Bodies are
// inlined because AGENTS.md readers take the file's text as the whole
// instruction set — a link would be a link, not a skill.
func (a *App) installAgents(projectDir string, skills []installableSkill) error {
	path := filepath.Join(projectDir, "AGENTS.md")
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return exitf(ExitError, "read %s: %v", path, err)
	}

	var block strings.Builder
	block.WriteString(agentsBegin + "\n\n## Team skills\n\n")
	block.WriteString("Maintained in Fulcrum and synced to this machine; edit them there " +
		"(or propose changes with `fulcrum publish`), not here.\n")
	for _, skill := range skills {
		block.WriteString("\n### " + skill.name + "\n\n")
		if skill.description != "" {
			block.WriteString("_" + skill.description + "_\n\n")
		}
		_, body, _ := skillmd.Split(string(skill.content))
		block.WriteString(strings.TrimRight(body, "\n") + "\n")
	}
	block.WriteString("\n" + agentsEnd + "\n")

	updated := replaceManagedBlock(string(existing), block.String())
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		return exitf(ExitError, "write %s: %v", path, err)
	}
	fmt.Fprintf(a.Stdout, "installed %d skill(s) → %s\n", len(skills), path)
	return nil
}

// replaceManagedBlock swaps fulcrum's block in place, or appends it,
// leaving every other line of the file untouched.
func replaceManagedBlock(existing, block string) string {
	start := strings.Index(existing, agentsBegin)
	if start >= 0 {
		if end := strings.Index(existing[start:], agentsEnd); end >= 0 {
			tail := existing[start+end+len(agentsEnd):]
			return existing[:start] + strings.TrimRight(block, "\n") + tail
		}
	}
	if existing == "" {
		return block
	}
	return strings.TrimRight(existing, "\n") + "\n\n" + block
}

func (a *App) writePins(projectDir string, skills []installableSkill, targets []string) error {
	pins := map[string]string{}
	for _, skill := range skills {
		pins[skill.slug] = skill.digest
	}
	path := filepath.Join(projectDir, ".fulcrum", installedPinFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return exitf(ExitError, "%v", err)
	}
	encoded, _ := json.MarshalIndent(map[string]any{
		"installed": pins,
		"targets":   targets,
	}, "", "  ")
	if err := os.WriteFile(path, append(encoded, '\n'), 0o644); err != nil {
		return exitf(ExitError, "write %s: %v", path, err)
	}
	fmt.Fprintf(a.Stdout, "digests pinned in %s\n", path)
	return nil
}
