package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"

	"github.com/headwayio/fulcrum-cli/internal/state"
)

// readerScreen renders a document. Markdown goes through Glamour with the
// frontmatter lifted into the header; JSON renders as-is. Behind docs get a
// banner naming the digest movement.
type readerScreen struct {
	app *App
	row Row

	scroll  scroller
	search  *lineInput
	seeking bool
	query   string
	errMsg  string
	loaded  bool
	// showingRemote is the behind-doc preview: what the server has now,
	// before you re-sync over your (unchanged) local copy.
	showingRemote bool
	localLines    []string
	remoteLines   []string
}

func newReaderScreen(a *App, row Row) *readerScreen {
	return &readerScreen{app: a, row: row, search: newLineInput("search", "")}
}

func (s *readerScreen) init() tea.Cmd {
	deps := s.app.deps
	slug := s.row.Slug
	return func() tea.Msg {
		local, err := deps.LocalDoc(slug)
		if err != nil {
			return docLoadedMsg{slug: slug, err: err}
		}
		if local == nil {
			remote, remoteErr := deps.RemoteDoc(slug)
			return docLoadedMsg{slug: slug, remote: remote, err: remoteErr}
		}
		return docLoadedMsg{slug: slug, local: local}
	}
}

func (s *readerScreen) title() string { return s.row.Filename }

func (s *readerScreen) update(msg tea.Msg) (screen, tea.Cmd) {
	switch msg := msg.(type) {
	case docLoadedMsg:
		s.loaded = true
		if msg.err != nil {
			s.errMsg = errorLine(msg.err)
			return s, nil
		}
		if msg.local != nil {
			s.localLines = s.renderDocument(msg.local)
		}
		if msg.remote != nil {
			s.remoteLines = s.renderDocument(msg.remote)
		}
		if s.showingRemote && s.remoteLines != nil {
			s.scroll.setLines(s.remoteLines)
		} else if s.localLines != nil {
			s.scroll.setLines(s.localLines)
		} else {
			s.scroll.setLines(s.remoteLines)
		}
		return s, nil

	case syncedMsg:
		if msg.err != nil {
			s.app.status = errStyle.Render(errorLine(msg.err))
			return s, nil
		}
		// Re-synced: back to the list with fresh classifications.
		s.app.status = msg.summary.Line()
		s.app.pop()
		if list, ok := s.app.stack[0].(*listScreen); ok {
			return s, list.refreshCmd()
		}
		return s, nil

	case editorFinishedMsg:
		if msg.err != nil {
			s.app.status = errStyle.Render("editor: " + msg.err.Error())
			return s, nil
		}
		return s, s.init() // re-read the file the editor may have changed

	case tea.KeyPressMsg:
		key := msg.String()
		if s.seeking {
			switch key {
			case "enter":
				s.seeking = false
				s.query = s.search.value
				s.jumpToMatch(s.scroll.offset + 1)
			case "esc":
				s.seeking = false
				s.search.value = ""
			default:
				s.search.handleKey(msg)
			}
			return s, nil
		}
		switch key {
		case "/":
			s.seeking = true
			s.search.value = ""
			return s, nil
		case "n":
			if s.query != "" {
				s.jumpToMatch(s.scroll.offset + 1)
			}
			return s, nil
		case "e":
			if path := s.app.deps.LocalPath(s.row.Slug); path != "" {
				return s, s.app.editCmd(path)
			}
			s.app.status = "nothing local to edit"
			return s, nil
		case "v":
			if s.row.Classification != state.Behind {
				return s, nil
			}
			return s.toggleRemote()
		case "s":
			// Safe by construction: SyncAll(false) never clobbers local
			// edits, and this doc (synced/behind) has none.
			deps := s.app.deps
			s.app.status = "syncing…"
			return s, func() tea.Msg {
				summary, err := deps.SyncAll(false)
				return syncedMsg{summary: summary, err: err}
			}
		default:
			s.scroll.handleKey(key, s.pageSize())
		}
	}
	return s, nil
}

func (s *readerScreen) toggleRemote() (screen, tea.Cmd) {
	if s.showingRemote {
		s.showingRemote = false
		s.scroll.setLines(s.localLines)
		return s, nil
	}
	s.showingRemote = true
	if s.remoteLines != nil {
		s.scroll.setLines(s.remoteLines)
		return s, nil
	}
	deps := s.app.deps
	slug := s.row.Slug
	return s, func() tea.Msg {
		remote, err := deps.RemoteDoc(slug)
		return docLoadedMsg{slug: slug, remote: remote, err: err}
	}
}

// renderDocument splits frontmatter into header lines and Glamour-renders
// markdown bodies. The search runs over these rendered-unstyled lines.
func (s *readerScreen) renderDocument(body []byte) []string {
	text := string(body)
	var header []string
	if front, rest, ok := splitFrontmatter(text); ok {
		for _, line := range strings.Split(front, "\n") {
			if line != "" {
				header = append(header, dimStyle.Render(line))
			}
		}
		text = rest
	}

	var rendered string
	if s.row.Format == "markdown" || strings.HasSuffix(s.row.Filename, ".md") {
		renderer, err := glamour.NewTermRenderer(
			glamourStyle(s.app.markdownStyle),
			glamour.WithWordWrap(min(s.app.width-2, 100)),
		)
		if err == nil {
			if out, renderErr := renderer.Render(text); renderErr == nil {
				rendered = out
			}
		}
	}
	if rendered == "" {
		rendered = text
	}

	lines := header
	if len(header) > 0 {
		lines = append(lines, "")
	}
	return append(lines, strings.Split(strings.TrimRight(rendered, "\n"), "\n")...)
}

func glamourStyle(name string) glamour.TermRendererOption {
	if name == "auto" {
		// Follows the terminal's background via glamour's env detection.
		return glamour.WithEnvironmentConfig()
	}
	return glamour.WithStandardStyle(name)
}

func (s *readerScreen) jumpToMatch(from int) {
	lines := s.scroll.lines
	needle := strings.ToLower(s.query)
	for i := 0; i < len(lines); i++ {
		idx := (from + i) % len(lines)
		if strings.Contains(strings.ToLower(stripANSI(lines[idx])), needle) {
			s.scroll.offset = idx
			return
		}
	}
	s.app.status = fmt.Sprintf("no match for %q", s.query)
}

func (s *readerScreen) pageSize() int { return max(s.app.height-8, 4) }

func (s *readerScreen) view() string {
	if !s.loaded {
		return dimStyle.Render("loading " + s.row.Filename + "…")
	}
	if s.errMsg != "" {
		return errStyle.Render(s.errMsg)
	}

	var b strings.Builder
	if s.row.Classification == state.Behind {
		viewing := "viewing your local copy (old)"
		if s.showingRemote {
			viewing = "viewing the REMOTE version"
		}
		b.WriteString(warnStyle.Render(fmt.Sprintf(
			"remote moved: %s → %s — %s",
			shortDigest(s.row.BaseDigest), shortDigest(s.row.RemoteDigest), viewing)) + "\n\n")
	}
	b.WriteString(s.scroll.view(s.pageSize()))
	b.WriteString("\n\n")
	if s.seeking {
		b.WriteString("/" + s.search.render(true))
	} else {
		hint := "j/k scroll · / search · e edit · esc back"
		if s.row.Classification == state.Behind {
			hint = "v local/remote · s re-sync · " + hint
		}
		if s.query != "" {
			hint = "n next match · " + hint
		}
		b.WriteString(dimStyle.Render(hint + s.scroll.position(s.pageSize())))
	}
	return b.String()
}

// scroller is a minimal line scroller shared by reader and diff screens.
type scroller struct {
	lines  []string
	offset int
}

func (sc *scroller) setLines(lines []string) {
	sc.lines = lines
	sc.offset = 0
}

func (sc *scroller) handleKey(key string, page int) {
	switch key {
	case "down", "j":
		sc.offset++
	case "up", "k":
		sc.offset--
	case "pgdown", " ", "f":
		sc.offset += page
	case "pgup", "b":
		sc.offset -= page
	case "g", "home":
		sc.offset = 0
	case "G", "end":
		sc.offset = len(sc.lines)
	}
	sc.clamp(page)
}

func (sc *scroller) clamp(page int) {
	maxOffset := max(len(sc.lines)-page, 0)
	sc.offset = max(min(sc.offset, maxOffset), 0)
}

func (sc *scroller) view(page int) string {
	sc.clamp(page)
	end := min(sc.offset+page, len(sc.lines))
	return strings.Join(sc.lines[sc.offset:end], "\n")
}

func (sc *scroller) position(page int) string {
	if len(sc.lines) <= page {
		return ""
	}
	return fmt.Sprintf(" · line %d/%d", sc.offset+1, len(sc.lines))
}

func splitFrontmatter(text string) (front, rest string, ok bool) {
	if !strings.HasPrefix(text, "---\n") {
		return "", text, false
	}
	body := text[4:]
	end := strings.Index(body, "\n---\n")
	if end < 0 {
		return "", text, false
	}
	return body[:end], strings.TrimPrefix(body[end+5:], "\n"), true
}

// stripANSI removes escape sequences so search matches what the eye reads.
func stripANSI(s string) string {
	var b strings.Builder
	inEscape := false
	for _, r := range s {
		switch {
		case inEscape:
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
				inEscape = false
			}
		case r == 0x1b:
			inEscape = true
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
