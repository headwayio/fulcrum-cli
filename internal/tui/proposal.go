package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/headwayio/fulcrum-cli/internal/api"
)

// proposalScreen shows one proposal's live server state — the row said
// "proposed"; this says what the reviewer has (or hasn't) done with it.
type proposalScreen struct {
	app *App
	row Row

	proposal *api.Proposal
	errMsg   string
	loaded   bool
}

func newProposalScreen(a *App, row Row) *proposalScreen {
	return &proposalScreen{app: a, row: row}
}

func (s *proposalScreen) init() tea.Cmd {
	deps := s.app.deps
	id := s.row.ProposalID
	return func() tea.Msg {
		proposal, err := deps.ProposalByID(id)
		return proposalMsg{proposal: proposal, err: err}
	}
}

func (s *proposalScreen) title() string {
	return fmt.Sprintf("proposal #%d", s.row.ProposalID)
}

func (s *proposalScreen) update(msg tea.Msg) (screen, tea.Cmd) {
	switch msg := msg.(type) {
	case proposalMsg:
		s.loaded = true
		if msg.err != nil {
			s.errMsg = errorLine(msg.err)
			return s, nil
		}
		s.proposal = msg.proposal
		return s, nil

	case tea.KeyPressMsg:
		if msg.String() == "o" && s.proposal != nil {
			url := fmt.Sprintf("%s/knowledge_proposals/%d", s.app.deps.ServerURL(), s.proposal.ID)
			_ = s.app.deps.OpenURL(url)
			s.app.status = "opened " + url
		}
	}
	return s, nil
}

func (s *proposalScreen) view() string {
	if !s.loaded {
		return dimStyle.Render("fetching proposal…")
	}
	if s.errMsg != "" {
		return errStyle.Render(s.errMsg)
	}

	p := s.proposal
	var b strings.Builder
	statusLine := p.Status
	switch p.Status {
	case "applied":
		statusLine = okStyle.Render("applied")
	case "rejected":
		statusLine = errStyle.Render("rejected")
	case "pending":
		statusLine = accentStyle.Render("pending review")
	}
	b.WriteString(fmt.Sprintf("Proposal #%d · %s\n\n", p.ID, statusLine))
	if p.Note != "" {
		b.WriteString("note: " + p.Note + "\n")
	}
	if p.BasedOnCurrent {
		b.WriteString("base: the current version\n")
	} else {
		b.WriteString(warnStyle.Render("base: an older version — flagged for the reviewer") + "\n")
	}
	if len(p.ChangedSections) > 0 {
		b.WriteString("changed sections: " + strings.Join(p.ChangedSections, ", ") + "\n")
	}
	if p.ResolvedAt != nil {
		by := ""
		if p.ResolvedByName != nil {
			by = " by " + *p.ResolvedByName
		}
		b.WriteString(fmt.Sprintf("resolved%s at %s\n", by, *p.ResolvedAt))
	}
	b.WriteString("\n" + dimStyle.Render("o open review page · esc back"))
	return b.String()
}
