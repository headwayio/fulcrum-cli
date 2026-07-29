package tui

import (
	"encoding/json"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/headwayio/fulcrum-cli/internal/api"
	"github.com/headwayio/fulcrum-cli/internal/scan"
)

// pushFactsScreen: path → scan → FULL payload preview ("nothing else leaves
// this machine") → project picker → explicit push.
type pushFactsScreen struct {
	app *App

	path     *lineInput
	stage    int // 0 path, 1 scanning, 2 preview, 3 picking, 4 pushing, 5 done
	facts    *scan.Facts
	preview  []string
	projects []api.Project
	cursor   int
	errMsg   string
	resultID int64
}

func newPushFactsScreen(a *App) *pushFactsScreen {
	return &pushFactsScreen{app: a, path: newLineInput("path to a local checkout (e.g. ~/Code/acme-app)", "")}
}

func (s *pushFactsScreen) init() tea.Cmd { return nil }
func (s *pushFactsScreen) title() string { return "push-facts" }

func (s *pushFactsScreen) update(msg tea.Msg) (screen, tea.Cmd) {
	switch msg := msg.(type) {
	case scannedMsg:
		if msg.err != nil {
			s.stage = 0
			s.errMsg = "scan failed: " + msg.err.Error()
			return s, nil
		}
		s.facts = msg.facts
		encoded, _ := json.MarshalIndent(map[string]any{
			"languages":    msg.facts.Languages,
			"dependencies": msg.facts.Dependencies,
			"repository":   msg.facts.Repository,
		}, "", "  ")
		s.preview = strings.Split(string(encoded), "\n")
		s.stage = 2
		return s, nil

	case projectsMsg:
		if msg.err != nil {
			s.stage = 2
			s.errMsg = errorLine(msg.err)
			return s, nil
		}
		s.projects = msg.projects
		s.stage = 3
		return s, nil

	case factsPushedMsg:
		if msg.err != nil {
			s.stage = 3
			s.errMsg = errorLine(msg.err)
			return s, nil
		}
		s.stage = 5
		s.resultID = msg.receipt.ProjectID
		return s, nil

	case tea.KeyPressMsg:
		return s.handleKey(msg)
	}
	return s, nil
}

func (s *pushFactsScreen) handleKey(msg tea.KeyPressMsg) (screen, tea.Cmd) {
	key := msg.String()
	switch s.stage {
	case 0:
		if key == "enter" {
			path := strings.TrimSpace(s.path.value)
			if path == "" {
				s.errMsg = "a repository path is required"
				return s, nil
			}
			s.stage = 1
			s.errMsg = ""
			deps := s.app.deps
			return s, func() tea.Msg {
				facts, err := deps.ScanRepo(path)
				return scannedMsg{facts: facts, err: err}
			}
		}
		s.path.handleKey(msg)
	case 2:
		if key == "enter" {
			s.stage = 3
			deps := s.app.deps
			return s, func() tea.Msg {
				projects, err := deps.Projects()
				return projectsMsg{projects: projects, err: err}
			}
		}
	case 3:
		switch key {
		case "up", "k":
			if s.cursor > 0 {
				s.cursor--
			}
		case "down", "j":
			if s.cursor < len(s.projects)-1 {
				s.cursor++
			}
		case "enter":
			if len(s.projects) == 0 {
				return s, nil
			}
			project := s.projects[s.cursor]
			facts := s.facts
			deps := s.app.deps
			s.stage = 4
			return s, func() tea.Msg {
				receipt, err := deps.PushFacts(project.ID, facts)
				return factsPushedMsg{receipt: receipt, err: err}
			}
		}
	}
	return s, nil
}

func (s *pushFactsScreen) view() string {
	var b strings.Builder
	switch s.stage {
	case 0:
		b.WriteString("Scan a local repository and push its architecture facts.\n\n")
		b.WriteString("Path: " + s.path.render(true) + "\n")
		if s.errMsg != "" {
			b.WriteString(errStyle.Render(s.errMsg) + "\n")
		}
		b.WriteString("\n" + dimStyle.Render("enter scan · esc back"))
	case 1:
		b.WriteString(dimStyle.Render("scanning…"))
	case 2:
		b.WriteString(strings.Join(s.preview, "\n") + "\n\n")
		b.WriteString(okStyle.Render("This payload is everything that would be sent — nothing else leaves this machine.") + "\n")
		if s.errMsg != "" {
			b.WriteString(errStyle.Render(s.errMsg) + "\n")
		}
		b.WriteString("\n" + dimStyle.Render("enter choose a project · esc cancel"))
	case 3:
		b.WriteString("Push to which project?\n\n")
		for i, p := range s.projects {
			marker := "  "
			name := p.Name
			if i == s.cursor {
				marker = selectedStyle.Render("> ")
				name = selectedStyle.Render(name)
			}
			arch := ""
			if p.HasArchitectureProfile {
				arch = dimStyle.Render("  (has a profile — pushing replaces it)")
			}
			b.WriteString(fmt.Sprintf("%s%-32s %s%s\n", marker, name, p.ClientName, arch))
		}
		if s.errMsg != "" {
			b.WriteString("\n" + errStyle.Render(s.errMsg))
		}
		b.WriteString("\n" + dimStyle.Render("enter push · esc cancel"))
	case 4:
		b.WriteString(dimStyle.Render("pushing…"))
	case 5:
		b.WriteString(okStyle.Render(fmt.Sprintf(
			"Pushed — the next estimate for project %d sees these facts.", s.resultID)))
		b.WriteString("\n\n" + dimStyle.Render("esc back to documents"))
	}
	return b.String()
}
