package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/headwayio/fulcrum-cli/internal/state"
)

// listScreen is home: every document's sync state, with "the state is the
// verb" — enter routes wherever the row's state points.
type listScreen struct {
	app     *App
	cursor  int
	loading bool
}

func newListScreen(a *App) *listScreen {
	return &listScreen{app: a, loading: true}
}

func (s *listScreen) init() tea.Cmd { return s.refreshCmd() }
func (s *listScreen) title() string { return "documents" }
func (s *listScreen) rows() []Row {
	if s.app.snapshot == nil {
		return nil
	}
	return s.app.snapshot.Rows
}

func (s *listScreen) refreshCmd() tea.Cmd {
	s.loading = true
	deps := s.app.deps
	return func() tea.Msg {
		snapshot, err := deps.Refresh()
		return snapshotMsg{snapshot: snapshot, err: err}
	}
}

func (s *listScreen) update(msg tea.Msg) (screen, tea.Cmd) {
	switch msg := msg.(type) {
	case snapshotMsg:
		s.loading = false
		if msg.err != nil {
			s.app.status = errStyle.Render(errorLine(msg.err))
			return s, nil
		}
		s.app.snapshot = msg.snapshot
		if s.cursor >= len(msg.snapshot.Rows) {
			s.cursor = 0
		}
		if !msg.snapshot.Reachable {
			s.app.status = errStyle.Render("offline: " + msg.snapshot.NetErr + " — showing last-known state")
		} else {
			s.app.status = ""
		}
		return s, nil

	case syncedMsg:
		s.loading = false
		if msg.err != nil {
			s.app.status = errStyle.Render(errorLine(msg.err))
			return s, nil
		}
		s.app.status = strings.Join(msg.lines, " · ")
		return s, s.refreshCmd()

	case tea.KeyPressMsg:
		return s.handleKey(msg.String())
	}
	return s, nil
}

func (s *listScreen) handleKey(key string) (screen, tea.Cmd) {
	rows := s.rows()
	switch key {
	case "q":
		s.app.quitting = true
		return s, tea.Quit
	case "up", "k":
		if s.cursor > 0 {
			s.cursor--
		}
	case "down", "j":
		if s.cursor < len(rows)-1 {
			s.cursor++
		}
	case "r":
		return s, s.refreshCmd()
	case "s":
		if s.offline() {
			s.app.status = "offline — sync needs the server"
			return s, nil
		}
		s.loading = true
		deps := s.app.deps
		return s, func() tea.Msg {
			lines, err := deps.SyncAll(false)
			return syncedMsg{lines: lines, err: err}
		}
	case "enter":
		if len(rows) == 0 {
			return s, nil
		}
		return s.routeEnter(rows[s.cursor])
	case "p":
		if len(rows) == 0 {
			return s, nil
		}
		return s.routePublish(rows[s.cursor])
	case "f":
		if s.offline() {
			s.app.status = "offline — push-facts needs the server"
			return s, nil
		}
		return s, s.app.push(newPushFactsScreen(s.app))
	}
	return s, nil
}

// routeEnter: the state is the verb.
func (s *listScreen) routeEnter(row Row) (screen, tea.Cmd) {
	switch row.Classification {
	case state.Drifted, state.Conflicted:
		return s, s.app.push(newDiffScreen(s.app, row))
	case state.Proposed:
		if s.offline() {
			s.app.status = "offline — proposal status needs the server"
			return s, nil
		}
		return s, s.app.push(newProposalScreen(s.app, row))
	case state.Missing, state.Unsynced:
		s.app.status = "press s to sync this document down"
		return s, nil
	default: // synced, behind
		return s, s.app.push(newReaderScreen(s.app, row))
	}
}

func (s *listScreen) routePublish(row Row) (screen, tea.Cmd) {
	if row.Format != "json" || row.ProposalSlug == "" {
		s.app.status = "generated rendering — edit the .json document and publish that"
		return s, nil
	}
	if s.offline() {
		s.app.status = "offline — publishing needs the server"
		return s, nil
	}
	if row.Classification != state.Drifted && row.Classification != state.Conflicted {
		s.app.status = "nothing to publish: no local edits on this document"
		return s, nil
	}
	return s, s.app.push(newPublishScreen(s.app, row))
}

func (s *listScreen) offline() bool {
	return s.app.snapshot != nil && !s.app.snapshot.Reachable
}

func (s *listScreen) view() string {
	if s.loading && s.app.snapshot == nil {
		return dimStyle.Render("loading…")
	}
	rows := s.rows()
	if len(rows) == 0 {
		return dimStyle.Render("no documents — press s to sync")
	}

	var b strings.Builder
	stale := 0
	for _, row := range rows {
		if row.Classification != state.Synced && row.Classification != state.Proposed {
			stale++
		}
	}
	freshness := fmt.Sprintf("%d document(s), %d stale", len(rows), stale)
	if stale == 0 {
		freshness = fmt.Sprintf("%d document(s), all fresh", len(rows))
	}
	b.WriteString(dimStyle.Render(freshness) + "\n\n")

	for i, row := range rows {
		marker := "  "
		name := row.Filename
		if i == s.cursor {
			marker = selectedStyle.Render("> ")
			name = selectedStyle.Render(name)
		}
		label := badge(row.Classification)
		if row.Outcome != "" {
			label = outcomeBadge(row.Outcome, row.OutcomeID)
		}
		b.WriteString(fmt.Sprintf("%s%-28s %-12s %s\n", marker, name, shortDigest(row.RemoteDigest), label))
	}

	b.WriteString("\n" + dimStyle.Render(s.hints(rows[s.cursor])))
	return b.String()
}

func (s *listScreen) hints(row Row) string {
	enterVerb := map[state.Classification]string{
		state.Synced:     "read",
		state.Behind:     "read",
		state.Drifted:    "diff",
		state.Conflicted: "diff",
		state.Proposed:   "proposal",
		state.Missing:    "—",
		state.Unsynced:   "—",
	}[row.Classification]
	hints := []string{"enter " + enterVerb, "s sync", "r refresh", "f push-facts", "q quit"}
	if row.Format == "json" && row.ProposalSlug != "" {
		hints = append(hints[:1], append([]string{"p publish"}, hints[1:]...)...)
	}
	return strings.Join(hints, " · ")
}
