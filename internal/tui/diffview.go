package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/headwayio/fulcrum-cli/internal/diffx"
	"github.com/headwayio/fulcrum-cli/internal/state"
)

// diffScreen shows pristine-base → local as a unified diff. For conflicted
// docs the remote also moved; the header says so plainly (the three-way
// panel is v1.1, see DECISIONS.md).
type diffScreen struct {
	app *App
	row Row

	scroll scroller
	loaded bool
	errMsg string
	empty  bool
}

func newDiffScreen(a *App, row Row) *diffScreen {
	return &diffScreen{app: a, row: row}
}

func (s *diffScreen) init() tea.Cmd {
	deps := s.app.deps
	slug := s.row.Slug
	return func() tea.Msg {
		local, err := deps.LocalDoc(slug)
		if err != nil {
			return docLoadedMsg{slug: slug, err: err}
		}
		return docLoadedMsg{slug: slug, local: local, base: deps.BaseDoc(slug)}
	}
}

func (s *diffScreen) title() string { return "diff · " + s.row.Filename }

func (s *diffScreen) update(msg tea.Msg) (screen, tea.Cmd) {
	switch msg := msg.(type) {
	case docLoadedMsg:
		s.loaded = true
		if msg.err != nil {
			s.errMsg = errorLine(msg.err)
			return s, nil
		}
		if msg.base == nil {
			// A workspace last synced by the Ruby client has no pristine
			// bases yet — say so instead of diffing against nothing.
			s.errMsg = "no pristine base for this document yet — run sync once with this client, edit, and return"
			return s, nil
		}
		unified := diffx.Unified(s.row.Filename, msg.base, msg.local)
		if unified == "" {
			s.empty = true
			return s, nil
		}
		s.scroll.setLines(strings.Split(strings.TrimRight(diffx.Colorize(unified), "\n"), "\n"))
		return s, nil

	case tea.KeyPressMsg:
		key := msg.String()
		if key == "p" && s.row.Format == "json" && s.row.ProposalSlug != "" {
			return s, s.app.push(newPublishScreen(s.app, s.row))
		}
		s.scroll.handleKey(key, s.pageSize())
	}
	return s, nil
}

func (s *diffScreen) pageSize() int { return max(s.app.height-8, 4) }

func (s *diffScreen) view() string {
	if !s.loaded {
		return dimStyle.Render("computing diff…")
	}
	if s.errMsg != "" {
		return errStyle.Render(s.errMsg)
	}
	if s.empty {
		return dimStyle.Render("no changes: local matches the last synced bytes")
	}

	var b strings.Builder
	header := fmt.Sprintf("last sync → local edits (%s)", s.row.Classification)
	if s.row.Classification == state.Conflicted {
		header += errStyle.Render("  — remote ALSO moved; a proposal will be flagged stale")
	}
	b.WriteString(dimStyle.Render(header) + "\n\n")
	b.WriteString(s.scroll.view(s.pageSize()))
	b.WriteString("\n\n")
	hint := "j/k scroll · esc back"
	if s.row.Format == "json" && s.row.ProposalSlug != "" {
		hint = "p publish · " + hint
	}
	b.WriteString(dimStyle.Render(hint + s.scroll.position(s.pageSize())))
	return b.String()
}
