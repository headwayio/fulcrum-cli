package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/headwayio/fulcrum-cli/internal/api"
)

// authScreen is first-run login: server + masked token, validated by a live
// manifest fetch. It prints and deep-links the exact member-settings URL
// where a token is minted — the whole subscriber journey starts there.
type authScreen struct {
	app *App

	url    *lineInput
	token  *lineInput
	orgID  *lineInput
	stage  int // 0 url, 1 token, 2 org (multi-org only), 3 validating, 4 saving
	errMsg string
	notice string
}

func newAuthScreen(a *App) *authScreen {
	s := &authScreen{app: a}
	s.url = newLineInput("https://your-fulcrum.example", a.deps.ServerURL())
	s.token = newLineInput("paste your API token", "")
	s.token.mask = true
	s.orgID = newLineInput("organization id", "")
	return s
}

func (s *authScreen) init() tea.Cmd { return nil }
func (s *authScreen) title() string { return "connect" }

func (s *authScreen) update(msg tea.Msg) (screen, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		if s.stage >= 3 {
			return s, nil // validating/saving: ignore typing
		}
		if msg.String() == "enter" {
			return s.advance()
		}
		s.currentInput().handleKey(msg)
		return s, nil

	case loginCheckedMsg:
		if apiErr, ok := api.AsError(msg.err); ok && apiErr.Code == "organization_required" {
			// The credentials are fine; they just reach several
			// organizations. Pick from the list the server named, then
			// validate again with that answer.
			url, token := msg.url, msg.token
			picker := newOrgPickerScreen(s.app, apiErr.Organizations)
			picker.onChosen = func(orgID string) tea.Cmd {
				s.orgID.value = orgID
				s.app.pop()
				return s.validate(url, token, orgID)
			}
			return s, s.app.push(picker)
		}
		if msg.err != nil {
			s.stage = 1
			s.errMsg = errorLine(msg.err)
			return s, nil
		}
		s.stage = 4
		who := ""
		if msg.manifest.User != nil {
			who = " as " + msg.manifest.User.Email
		}
		s.notice = fmt.Sprintf("Connected to %s%s — storing credentials…", msg.manifest.Organization.Name, who)
		return s, func() tea.Msg {
			where, err := s.app.deps.SaveLogin(msg.url, msg.token, msg.orgID)
			return loginSavedMsg{where: where, err: err}
		}

	case loginSavedMsg:
		if msg.err != nil {
			s.stage = 1
			s.errMsg = "could not store credentials: " + msg.err.Error()
			return s, nil
		}
		list := newListScreen(s.app)
		return s, s.app.swapRoot(list)
	}
	return s, nil
}

func (s *authScreen) advance() (screen, tea.Cmd) {
	s.errMsg = ""
	switch s.stage {
	case 0:
		if strings.TrimSpace(s.url.value) == "" {
			s.errMsg = "a server URL is required"
			return s, nil
		}
		s.url.value = strings.TrimSuffix(strings.TrimSpace(s.url.value), "/")
		s.stage = 1
		return s, nil
	case 1:
		if strings.TrimSpace(s.token.value) == "" {
			s.errMsg = "paste the token minted at " + s.mintURL()
			return s, nil
		}
	case 2:
		if strings.TrimSpace(s.orgID.value) == "" {
			s.errMsg = "an organization id is required"
			return s, nil
		}
	}
	url, token, orgID := s.url.value, strings.TrimSpace(s.token.value), strings.TrimSpace(s.orgID.value)
	return s, s.validate(url, token, orgID)
}

// validate checks credentials with a live manifest fetch — the same step
// whether they were just typed or an organization was just chosen.
func (s *authScreen) validate(url, token, orgID string) tea.Cmd {
	s.stage = 3
	s.notice = "validating against " + url + "…"
	return func() tea.Msg {
		manifest, err := s.app.deps.ValidateLogin(url, token, orgID)
		return loginCheckedMsg{manifest: manifest, url: url, token: token, orgID: orgID, err: err}
	}
}

func (s *authScreen) currentInput() *lineInput {
	switch s.stage {
	case 0:
		return s.url
	case 2:
		return s.orgID
	default:
		return s.token
	}
}

func (s *authScreen) mintURL() string {
	base := strings.TrimSpace(s.url.value)
	if base == "" {
		base = "<server>"
	}
	return strings.TrimSuffix(base, "/") + "/settings/developer"
}

func (s *authScreen) view() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Connect to Fulcrum") + "\n\n")
	b.WriteString(fmt.Sprintf("Mint a personal API token at %s\n", accentStyle.Render(s.mintURL())))
	b.WriteString(dimStyle.Render("(any signed-in member can; it is shown exactly once)") + "\n\n")

	b.WriteString(field("Server", s.url, s.stage == 0))
	b.WriteString(field("Token", s.token, s.stage == 1))
	if s.stage == 2 {
		b.WriteString(field("Organization", s.orgID, true))
	}
	if s.notice != "" {
		b.WriteString("\n" + s.notice + "\n")
	}
	if s.errMsg != "" {
		b.WriteString("\n" + errStyle.Render(s.errMsg) + "\n")
	}
	b.WriteString("\n" + dimStyle.Render("enter continue · ctrl+c quit"))
	return b.String()
}

func field(label string, input *lineInput, active bool) string {
	marker := "  "
	if active {
		marker = selectedStyle.Render("> ")
	}
	return fmt.Sprintf("%s%-13s %s\n", marker, label+":", input.render(active))
}

func orgChoices(apiErr *api.Error) string {
	return dimStyle.Render(apiErr.ServerMessage)
}

func errorLine(err error) string {
	if apiErr, ok := api.AsError(err); ok {
		return apiErr.Error()
	}
	return "cannot reach the server: " + err.Error()
}

// lineInput is a deliberately tiny single-line input — no external bubble,
// so golden frames depend on nothing but this package.
type lineInput struct {
	value       string
	placeholder string
	mask        bool
}

func newLineInput(placeholder, value string) *lineInput {
	return &lineInput{placeholder: placeholder, value: value}
}

func (i *lineInput) handleKey(msg tea.KeyPressMsg) {
	switch msg.String() {
	case "backspace":
		if len(i.value) > 0 {
			runes := []rune(i.value)
			i.value = string(runes[:len(runes)-1])
		}
	case "ctrl+u":
		i.value = ""
	default:
		if text := msg.Key().Text; text != "" {
			i.value += text
		}
	}
}

func (i *lineInput) render(active bool) string {
	shown := i.value
	if i.mask && shown != "" {
		shown = strings.Repeat("*", len([]rune(shown)))
	}
	if shown == "" {
		// Caret first, then the hint. Parked after the placeholder it reads
		// as if the hint were already your text and the field were answered.
		if active {
			return caretMark + dimStyle.Render(i.placeholder)
		}
		return dimStyle.Render(i.placeholder)
	}
	if active {
		return shown + caretMark
	}
	return shown
}
