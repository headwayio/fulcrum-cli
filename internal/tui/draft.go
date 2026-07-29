package tui

import (
	"fmt"
	"regexp"
	"strings"

	tea "charm.land/bubbletea/v2"
)

var kebabName = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// draftScreen names a new skill. The server mints the draft — template
// content, reserved slug, real id — visible only to you until you publish;
// admins first see it as a proposal in their skills inbox.
type draftScreen struct {
	app *App

	name    *lineInput
	minting bool
	errMsg  string
}

func newDraftScreen(a *App) *draftScreen {
	return &draftScreen{app: a, name: newLineInput("kebab-case-name (e.g. writing-request-specs)", "")}
}

func (s *draftScreen) init() tea.Cmd { return nil }
func (s *draftScreen) title() string { return "new skill" }

func (s *draftScreen) update(msg tea.Msg) (screen, tea.Cmd) {
	switch msg := msg.(type) {
	case draftCreatedMsg:
		if msg.err != nil {
			s.minting = false
			s.errMsg = errorLine(msg.err)
			return s, nil
		}
		s.app.pop()
		s.app.status = fmt.Sprintf("draft %s created — e to edit, p to publish when ready (only you see it until then)",
			msg.draft.Filename)
		if list, ok := s.app.stack[0].(*listScreen); ok {
			return s, list.refreshCmd()
		}
		return s, nil

	case tea.KeyPressMsg:
		if s.minting {
			return s, nil
		}
		if msg.String() == "enter" {
			name := strings.TrimSpace(s.name.value)
			if !kebabName.MatchString(name) {
				s.errMsg = "skill names are kebab-case: a-z, 0-9, hyphens"
				return s, nil
			}
			s.minting = true
			s.errMsg = ""
			deps := s.app.deps
			return s, func() tea.Msg {
				draft, err := deps.CreateSkillDraft(name)
				return draftCreatedMsg{draft: draft, err: err}
			}
		}
		s.name.handleKey(msg)
	}
	return s, nil
}

func (s *draftScreen) view() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("New skill") + "\n\n")
	b.WriteString("The name becomes the frontmatter identity and the synced filename.\n")
	b.WriteString(dimStyle.Render("Only you see the draft until you publish it for review.") + "\n\n")
	b.WriteString("Name: " + s.name.render(!s.minting) + "\n")
	if s.minting {
		b.WriteString("\n" + dimStyle.Render("minting draft…"))
	}
	if s.errMsg != "" {
		b.WriteString("\n" + errStyle.Render(s.errMsg))
	}
	b.WriteString("\n\n" + dimStyle.Render("enter create · esc cancel"))
	return b.String()
}
