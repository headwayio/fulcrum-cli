// Package install writes the workspace's skills into a project's agent
// harnesses. It lives outside both faces because both need it: the CLI's
// `skills install`, and every sync — from either face — that must leave the
// harnesses reading current skills rather than yesterday's.
package install

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/headwayio/fulcrum-cli/internal/skillmd"
	"github.com/headwayio/fulcrum-cli/internal/state"
	"github.com/headwayio/fulcrum-cli/internal/workspace"
)

// PinFile records what was installed, so a harness (the Electron
// feature-start flow, a script) can compare digests against the workspace
// without re-reading every file.
const PinFile = ".fulcrum-installed.json"

// Managed-block fences for AGENTS.md. Everything between them belongs to
// fulcrum; everything outside is the project's and is never touched.
const (
	agentsBegin = "<!-- BEGIN fulcrum skills — managed by `fulcrum skills install`, edits here are overwritten -->"
	agentsEnd   = "<!-- END fulcrum skills -->"
)

// Harness formats. A project commonly drives more than one agent, so all of
// these are written by default and kept in step — a skill the team approved
// should not depend on which tool a developer opened.
//
//	claude  .claude/skills/<slug>/SKILL.md — Claude Code
//	shared  .skills/<slug>/SKILL.md — the vendor-neutral SKILL.md directory
//	        (Kimi Code, OpenCode, other SKILL.md-compatible agents)
//	agents  a managed block in AGENTS.md — Codex, Kimi Code, and the rest of
//	        the AGENTS.md convention
//
// Deliberately per-project only: Kimi also reads a global ~/.agents/skills,
// and writing there would push team skills into every side project on the
// machine, which is the opposite of the point.
const (
	TargetClaude = "claude"
	TargetShared = "shared"
	TargetAgents = "agents"
)

// AllTargets is the default: everything a project might read.
var AllTargets = []string{TargetClaude, TargetShared, TargetAgents}

type skill struct {
	slug        string
	digest      string
	name        string
	description string
	content     []byte
}

// Targets picks harness formats. Default is every one of them: a project
// that drives Claude Code today may be driven by Codex or Kimi tomorrow, and
// a skill that only reached one of them is a skill the team cannot rely on.
// "auto" is the narrower opt-in for people who want only what the project
// already uses.
func Targets(projectDir, target string) ([]string, error) {
	switch target {
	case TargetClaude, TargetShared, TargetAgents:
		return []string{target}, nil
	case "all", "":
		return AllTargets, nil
	case "auto":
		var found []string
		if _, err := os.Stat(filepath.Join(projectDir, ".claude")); err == nil {
			found = append(found, TargetClaude)
		}
		if _, err := os.Stat(filepath.Join(projectDir, ".skills")); err == nil {
			found = append(found, TargetShared)
		}
		if _, err := os.Stat(filepath.Join(projectDir, "AGENTS.md")); err == nil {
			found = append(found, TargetAgents)
		}
		if len(found) == 0 {
			return AllTargets, nil
		}
		return found, nil
	default:
		return nil, fmt.Errorf("unknown target %q: use claude, shared, agents, all, or auto", target)
	}
}

// Into writes the workspace's skills into one project in the given formats,
// returning how many were installed. Progress lines go to out; non-fatal
// per-document problems go to problems.
func Into(w *workspace.Workspace, projectDir string, targets []string, out, problems io.Writer) (int, error) {
	var skills []skill
	for slug, record := range w.State.Documents {
		skillSlug, ok := strings.CutPrefix(slug, "skill-")
		if !ok {
			continue // the rubric pair and any future non-skill documents
		}
		// A local variant is what the developer is running, so it is what the
		// agents should run — installed under the canonical name, so the
		// harness still sees exactly one skill by that name.
		content := w.ReadBeta(slug)
		if content == nil {
			var readErr error
			content, readErr = w.ReadLocal(slug)
			if readErr != nil {
				return 0, fmt.Errorf("read %s: %w", record.Filename, readErr)
			}
		}
		if content == nil {
			fmt.Fprintf(problems, "skipped %s: local file missing — run `fulcrum sync`\n", record.Filename)
			continue
		}
		name, description := skillmd.Describe(string(content), skillSlug)
		skills = append(skills, skill{
			// The digest of what was actually installed, which is the
			// variant's when one is overriding.
			slug: skillSlug, digest: state.HexSHA256(content),
			name: name, description: description, content: content,
		})
	}

	if len(skills) == 0 {
		return 0, nil
	}
	sort.Slice(skills, func(i, j int) bool { return skills[i].slug < skills[j].slug })

	for _, name := range targets {
		var err error
		switch name {
		case TargetClaude:
			err = writeSkillDir(projectDir, filepath.Join(".claude", "skills"), skills, out)
		case TargetShared:
			err = writeSkillDir(projectDir, ".skills", skills, out)
		case TargetAgents:
			err = writeAgents(projectDir, skills, out)
		}
		if err != nil {
			return 0, err
		}
	}

	if err := writePins(projectDir, skills, targets, out); err != nil {
		return 0, err
	}
	return len(skills), nil
}

// Refresh re-runs the install for every project that ever received these
// skills, so the harnesses reading them never fall behind a sync. Projects
// that have moved or been deleted are forgotten rather than nagged about.
// Failures are reported, never fatal: a sync that pulled its documents
// succeeded, whatever some other directory does.
func Refresh(w *workspace.Workspace, out, problems io.Writer) {
	if len(w.State.Installs) == 0 {
		return
	}

	forgotten := false
	for _, dir := range append([]string(nil), w.State.Installs...) {
		if _, err := os.Stat(dir); err != nil {
			w.State.ForgetInstall(dir)
			forgotten = true
			continue
		}
		targets := recordedTargets(dir)
		installed, err := Into(w, dir, targets, io.Discard, problems)
		if err != nil {
			fmt.Fprintf(problems, "could not refresh %s: %v\n", dir, err)
			continue
		}
		fmt.Fprintf(out, "refreshed %d skill(s) in %s (%s)\n",
			installed, dir, strings.Join(targets, " + "))
	}
	if forgotten {
		_ = w.SaveState()
	}
}

// recordedTargets reads the formats a project was installed with, falling
// back to all of them.
func recordedTargets(projectDir string) []string {
	raw, err := os.ReadFile(filepath.Join(projectDir, ".fulcrum", PinFile))
	if err != nil {
		return AllTargets
	}
	var pins struct {
		Targets []string `json:"targets"`
	}
	if json.Unmarshal(raw, &pins) != nil || len(pins.Targets) == 0 {
		return AllTargets
	}
	return pins.Targets
}

// writeSkillDir writes one directory per skill — the SKILL.md layout, under
// whichever root the harness reads.
func writeSkillDir(projectDir, root string, skills []skill, out io.Writer) error {
	for _, s := range skills {
		path := filepath.Join(projectDir, root, s.slug, "SKILL.md")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, s.content, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
		fmt.Fprintf(out, "installed %s → %s\n", s.slug, path)
	}
	return nil
}

// writeAgents maintains one fulcrum-owned block in AGENTS.md. Bodies are
// inlined because AGENTS.md readers take the file's text as the whole
// instruction set — a link would be a link, not a skill.
func writeAgents(projectDir string, skills []skill, out io.Writer) error {
	path := filepath.Join(projectDir, "AGENTS.md")
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read %s: %w", path, err)
	}

	var block strings.Builder
	block.WriteString(agentsBegin + "\n\n## Team skills\n\n")
	block.WriteString("Maintained in Fulcrum and synced to this machine; edit them there " +
		"(or propose changes with `fulcrum publish`), not here.\n")
	for _, s := range skills {
		block.WriteString("\n### " + s.name + "\n\n")
		if s.description != "" {
			block.WriteString("_" + s.description + "_\n\n")
		}
		_, body, _ := skillmd.Split(string(s.content))
		block.WriteString(strings.TrimRight(body, "\n") + "\n")
	}
	block.WriteString("\n" + agentsEnd + "\n")

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	updated := replaceManagedBlock(string(existing), block.String())
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	fmt.Fprintf(out, "installed %d skill(s) → %s\n", len(skills), path)
	return nil
}

// replaceManagedBlock swaps fulcrum's block in place, or appends it, leaving
// every other line of the file untouched.
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

func writePins(projectDir string, skills []skill, targets []string, out io.Writer) error {
	pins := map[string]string{}
	for _, s := range skills {
		pins[s.slug] = s.digest
	}
	path := filepath.Join(projectDir, ".fulcrum", PinFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	encoded, _ := json.MarshalIndent(map[string]any{
		"installed": pins,
		"targets":   targets,
	}, "", "  ")
	if err := os.WriteFile(path, append(encoded, '\n'), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	fmt.Fprintf(out, "digests pinned in %s\n", path)
	return nil
}
