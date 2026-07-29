package tui

import (
	"fmt"
	"os/exec"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/headwayio/fulcrum-cli/internal/api"
)

// screen is one member of the Esc-popped stack. Screens hold *App for shared
// state (deps, size, snapshot) and return commands for every side effect.
type screen interface {
	init() tea.Cmd
	update(msg tea.Msg) (screen, tea.Cmd)
	view() string
	title() string
}

// App is the one root tea.Model.
type App struct {
	deps    Deps
	version string
	// markdownStyle is glamour's style name; tests pin "notty" so goldens
	// never depend on the host terminal.
	markdownStyle string

	width, height int
	stack         []screen
	// stackVersion bumps on every push/pop/swap so Update can tell when a
	// screen navigated mid-dispatch and skip writing the old screen back.
	stackVersion int
	snapshot     *Snapshot
	status       string
	// statusOffline marks that status holds the offline banner, so recovery
	// clears it while sync summaries survive refreshes.
	statusOffline bool
	authModal     string
	showKeys      bool
	quitting      bool
}

// New builds the root model. markdownStyle "auto" follows the terminal.
func New(deps Deps, version, markdownStyle string) *App {
	return &App{deps: deps, version: version, markdownStyle: markdownStyle, width: 80, height: 24}
}

// Run starts the program on the real terminal.
func Run(deps Deps, version string) error {
	program := tea.NewProgram(New(deps, version, "auto"))
	_, err := program.Run()
	return err
}

func (a *App) Init() tea.Cmd {
	configured, err := a.deps.Configured()
	if err != nil || !configured {
		auth := newAuthScreen(a)
		a.stack = []screen{auth}
		return auth.init()
	}
	list := newListScreen(a)
	a.stack = []screen{list}
	return list.init()
}

// failable lets the root intercept any message carrying an error — the
// mid-session 401 modal works on every screen because of this.
type failable interface{ failure() error }

// editCmd suspends the program and opens path in the user's editor.
func (a *App) editCmd(path string) tea.Cmd {
	command := exec.Command(a.deps.Editor(), path)
	return tea.ExecProcess(command, func(err error) tea.Msg {
		return editorFinishedMsg{err: err}
	})
}

func (m snapshotMsg) failure() error    { return m.err }
func (m docLoadedMsg) failure() error   { return m.err }
func (m syncedMsg) failure() error      { return m.err }
func (m publishedMsg) failure() error   { return m.err }
func (m proposalMsg) failure() error    { return m.err }
func (m projectsMsg) failure() error    { return m.err }
func (m scannedMsg) failure() error     { return m.err }
func (m factsPushedMsg) failure() error { return m.err }

func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.width, a.height = msg.Width, msg.Height
		return a, nil

	case tea.KeyPressMsg:
		key := msg.String()
		if key == "ctrl+c" {
			a.quitting = true
			return a, tea.Quit
		}
		if a.authModal != "" {
			// The modal owns the keyboard: re-auth or quit.
			switch key {
			case "l":
				a.authModal = ""
				auth := newAuthScreen(a)
				a.stack = []screen{auth}
				return a, auth.init()
			case "q", "esc":
				a.quitting = true
				return a, tea.Quit
			}
			return a, nil
		}
		if a.showKeys {
			a.showKeys = false // any key dismisses
			return a, nil
		}
		if key == "?" {
			a.showKeys = true
			return a, nil
		}
		if key == "esc" && len(a.stack) > 1 {
			a.stack = a.stack[:len(a.stack)-1]
			a.status = ""
			return a, nil
		}

	case authFailedMsg:
		a.authModal = msg.message
		return a, nil
	}

	if f, ok := msg.(failable); ok {
		if apiErr, is := api.AsError(f.failure()); is && apiErr.Status == 401 {
			a.authModal = apiErr.ServerMessage
			return a, nil
		}
	}

	if len(a.stack) == 0 {
		return a, nil
	}
	// Write the screen back only when the dispatch didn't navigate: update
	// may push/pop/swap (mutating the stack), and a blind writeback would
	// clobber whatever screen the navigation installed.
	idx := len(a.stack) - 1
	version := a.stackVersion
	next, cmd := a.stack[idx].update(msg)
	if a.stackVersion == version {
		a.stack[idx] = next
	}
	return a, cmd
}

// push adds a screen and returns its init command.
func (a *App) push(s screen) tea.Cmd {
	a.stack = append(a.stack, s)
	a.stackVersion++
	return s.init()
}

// pop returns to the previous screen.
func (a *App) pop() {
	if len(a.stack) > 1 {
		a.stack = a.stack[:len(a.stack)-1]
		a.stackVersion++
	}
}

// swapRoot replaces the whole stack (auth success → list).
func (a *App) swapRoot(s screen) tea.Cmd {
	a.stack = []screen{s}
	a.stackVersion++
	return s.init()
}

func (a *App) View() tea.View {
	if a.quitting {
		return tea.NewView("")
	}
	var body string
	if len(a.stack) > 0 {
		body = a.stack[len(a.stack)-1].view()
	}
	if a.showKeys {
		body = keysView()
	}
	if a.authModal != "" {
		body = a.modalView()
	}

	header := titleStyle.Render("fulcrum") + dimStyle.Render(" · "+a.headerContext())
	lines := []string{header, "", body}
	if a.status != "" && a.authModal == "" {
		lines = append(lines, "", dimStyle.Render(a.status))
	}
	view := tea.NewView(strings.Join(lines, "\n"))
	return view
}

func (a *App) headerContext() string {
	if len(a.stack) == 0 {
		return a.version
	}
	top := a.stack[len(a.stack)-1]
	parts := []string{top.title()}
	if a.snapshot != nil {
		if a.snapshot.OrgName != "" {
			parts = append([]string{a.snapshot.OrgName}, parts...)
		}
		if !a.snapshot.Reachable {
			parts = append(parts, errStyle.Render("OFFLINE"))
		}
	}
	return strings.Join(parts, " · ")
}

// keysView is the whole vocabulary in one place, so the per-row hint line
// can stay short and contextual.
func keysView() string {
	rows := [][2]string{
		{"enter", "open — reads, diffs, or shows the proposal, by state"},
		{"e", "edit in $EDITOR"},
		{"p", "publish your version as a proposal"},
		{"b", "keep your version as a local variant (again to drop it)"},
		{"m", "merge the team's version into yours"},
		{"x", "discard your edits and take the team's"},
		{"s", "sync — pulls documents and refreshes installed projects"},
		{"n", "draft a brand-new skill"},
		{"f", "push repository facts to a project"},
		{"r", "refresh"},
		{"esc", "back"},
		{"q", "quit"},
	}
	var b strings.Builder
	b.WriteString(titleStyle.Render("Keys") + "\n\n")
	for _, row := range rows {
		b.WriteString(fmt.Sprintf("  %-6s %s\n", accentStyle.Render(row[0]), row[1]))
	}
	b.WriteString("\n" + dimStyle.Render("any key closes"))
	return modalStyle.Render(b.String())
}

func (a *App) modalView() string {
	content := fmt.Sprintf("%s\n\n%s\n\n%s",
		errStyle.Render("Session rejected (401)"),
		"The server no longer accepts this token — it may have been\nrotated or revoked."+
			detailLine(a.authModal),
		"l log in again · q quit")
	return modalStyle.Render(content)
}

func detailLine(detail string) string {
	if detail == "" {
		return ""
	}
	return "\n\n" + dimStyle.Render(detail)
}
