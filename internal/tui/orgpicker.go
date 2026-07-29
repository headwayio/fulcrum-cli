package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/headwayio/fulcrum-cli/internal/api"
)

// orgPickerScreen appears whenever the server says it needs to be told which
// organization this token means. The 422 names the choices, so this is a
// list to pick from rather than an id to go and find.
type orgPickerScreen struct {
	app *App

	orgs   []api.Organization
	cursor int
	saving bool
	errMsg string
	// onChosen takes over what happens with the choice. Nil means the
	// default: remember it and reload the documents.
	onChosen func(orgID string) tea.Cmd
}

func newOrgPickerScreen(a *App, orgs []api.Organization) *orgPickerScreen {
	return &orgPickerScreen{app: a, orgs: orgs}
}

func (s *orgPickerScreen) init() tea.Cmd { return nil }
func (s *orgPickerScreen) title() string { return "choose an organization" }

func (s *orgPickerScreen) update(msg tea.Msg) (screen, tea.Cmd) {
	switch msg := msg.(type) {
	case orgChosenMsg:
		if msg.err != nil {
			s.saving = false
			s.errMsg = errorLine(msg.err)
			return s, nil
		}
		s.app.status = "working in " + msg.name
		list := newListScreen(s.app)
		return s, s.app.swapRoot(list)

	case tea.KeyPressMsg:
		if s.saving {
			return s, nil
		}
		switch msg.String() {
		case "up", "k":
			if s.cursor > 0 {
				s.cursor--
			}
		case "down", "j":
			if s.cursor < len(s.orgs)-1 {
				s.cursor++
			}
		case "enter":
			if len(s.orgs) == 0 {
				return s, nil
			}
			chosen := s.orgs[s.cursor]
			id := fmt.Sprint(chosen.ID)
			s.saving = true
			if s.onChosen != nil {
				return s, s.onChosen(id)
			}
			deps := s.app.deps
			return s, func() tea.Msg {
				err := deps.SetOrganization(id)
				return orgChosenMsg{name: chosen.Name, err: err}
			}
		}
	}
	return s, nil
}

func (s *orgPickerScreen) view() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Which organization?") + "\n\n")
	b.WriteString(dimStyle.Render("Your token reaches more than one, so Fulcrum needs to know which "+
		"one you mean. This is remembered.") + "\n\n")

	for i, org := range s.orgs {
		marker := "  "
		name := org.Name
		if i == s.cursor {
			marker = selectedStyle.Render("> ")
			name = selectedStyle.Render(name)
		}
		b.WriteString(fmt.Sprintf("%s%-32s %s\n", marker, name, dimStyle.Render(fmt.Sprint(org.ID))))
	}
	if s.saving {
		b.WriteString("\n" + dimStyle.Render("saving…"))
	}
	if s.errMsg != "" {
		b.WriteString("\n" + errStyle.Render(s.errMsg))
	}
	b.WriteString("\n" + dimStyle.Render("enter choose · q quit"))
	return b.String()
}
