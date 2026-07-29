package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/headwayio/fulcrum-cli/internal/diffx"
	"github.com/headwayio/fulcrum-cli/internal/state"
)

// diffScreen shows what changed since the last sync. JSON documents default
// to the structural path-level view (u toggles the unified text diff);
// markdown gets a unified diff with word-level intraline highlights. A
// conflicted doc becomes a three-way panel: your edits and the remote's
// changes, both against the shared base, with overlapping paths called out.
type diffScreen struct {
	app *App
	row Row

	scroll     scroller
	loaded     bool
	errMsg     string
	empty      bool
	unifiedRaw string // text diff base → local
	structural []string
	threeWay   []string
	showText   bool // u: force the unified text view for JSON docs

	local  []byte
	base   []byte
	remote []byte
}

func newDiffScreen(a *App, row Row) *diffScreen {
	return &diffScreen{app: a, row: row}
}

func (s *diffScreen) init() tea.Cmd {
	deps := s.app.deps
	slug := s.row.Slug
	conflicted := s.row.Classification == state.Conflicted
	return func() tea.Msg {
		local, err := deps.LocalDoc(slug)
		if err != nil {
			return docLoadedMsg{slug: slug, err: err}
		}
		msg := docLoadedMsg{slug: slug, local: local, base: deps.BaseDoc(slug)}
		if conflicted {
			// The third side: what the server has now.
			if remote, remoteErr := deps.RemoteDoc(slug); remoteErr == nil {
				msg.remote = remote
			}
		}
		return msg
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
		s.local, s.base, s.remote = msg.local, msg.base, msg.remote
		s.rebuild()
		return s, nil

	case editorFinishedMsg:
		if msg.err != nil {
			s.app.status = errStyle.Render("editor: " + msg.err.Error())
			return s, nil
		}
		return s, s.init() // recompute against the edited file

	case mergedMsg:
		if msg.err != nil {
			s.app.status = errStyle.Render(errorLine(msg.err))
			return s, nil
		}
		if msg.outcome.Conflicts > 0 {
			s.app.status = warnStyle.Render(fmt.Sprintf(
				"%s: %d conflict(s) — press e to resolve", msg.outcome.Filename, msg.outcome.Conflicts))
		} else {
			s.app.status = okStyle.Render(msg.outcome.Filename + " merged cleanly — ready to publish")
		}
		s.app.pop()
		if list, ok := s.app.stack[0].(*listScreen); ok {
			return s, list.refreshCmd()
		}
		return s, nil

	case tea.KeyPressMsg:
		key := msg.String()
		switch key {
		case "p":
			if s.row.Format == "json" && s.row.ProposalSlug != "" {
				return s, s.app.push(newPublishScreen(s.app, s.row))
			}
		case "m":
			if s.row.Classification == state.Conflicted {
				deps := s.app.deps
				slug := s.row.Slug
				s.app.status = "merging…"
				return s, func() tea.Msg {
					outcome, err := deps.MergeRemote(slug)
					return mergedMsg{outcome: outcome, err: err}
				}
			}
		case "e":
			if path := s.app.deps.LocalPath(s.row.Slug); path != "" {
				return s, s.app.editCmd(path)
			}
		case "u":
			if s.structural != nil || s.threeWay != nil {
				s.showText = !s.showText
				s.scroll.setLines(s.currentLines())
			}
		default:
			s.scroll.handleKey(key, s.pageSize())
		}
	}
	return s, nil
}

// rebuild derives every view from the loaded documents.
func (s *diffScreen) rebuild() {
	s.unifiedRaw = diffx.Unified(s.row.Filename, s.base, s.local)
	s.structural, s.threeWay = nil, nil
	s.empty = s.unifiedRaw == "" && s.remote == nil

	if s.row.Format == "json" {
		if s.remote != nil {
			s.threeWay = s.buildThreeWay()
		} else if ours, err := diffx.JSONStructural(s.base, s.local); err == nil {
			s.structural = diffx.RenderJSONChanges(ours)
		}
	} else if s.remote != nil {
		s.threeWay = s.buildTextThreeWay()
	}
	s.scroll.setLines(s.currentLines())
}

// buildThreeWay renders both sides' structural changes against the shared
// base, then names the paths BOTH touched — those are the real conflicts.
func (s *diffScreen) buildThreeWay() []string {
	ours, ourErr := diffx.JSONStructural(s.base, s.local)
	theirs, theirErr := diffx.JSONStructural(s.base, s.remote)
	if ourErr != nil || theirErr != nil {
		return s.buildTextThreeWay() // unparseable side: fall back to text
	}

	lines := []string{titleStyle.Render("YOUR EDITS (base → local)")}
	lines = append(lines, diffx.RenderJSONChanges(ours)...)
	lines = append(lines, "", titleStyle.Render("REMOTE CHANGES (base → remote)"))
	lines = append(lines, diffx.RenderJSONChanges(theirs)...)

	if overlap := diffx.ConflictPaths(ours, theirs); len(overlap) > 0 {
		lines = append(lines, "", errStyle.Render("!! BOTH SIDES CHANGED:"))
		for _, path := range overlap {
			lines = append(lines, errStyle.Render("   "+path))
		}
	} else {
		lines = append(lines, "", okStyle.Render("no overlapping paths — the reviewer can likely take both"))
	}
	return lines
}

func (s *diffScreen) buildTextThreeWay() []string {
	lines := []string{titleStyle.Render("YOUR EDITS (base → local)")}
	lines = append(lines, splitDiff(diffx.ColorizeIntraline(diffx.Unified(s.row.Filename, s.base, s.local)))...)
	lines = append(lines, "", titleStyle.Render("REMOTE CHANGES (base → remote)"))
	lines = append(lines, splitDiff(diffx.ColorizeIntraline(diffx.Unified(s.row.Filename, s.base, s.remote)))...)
	return lines
}

func (s *diffScreen) currentLines() []string {
	switch {
	case s.showText || (s.structural == nil && s.threeWay == nil):
		return splitDiff(diffx.ColorizeIntraline(s.unifiedRaw))
	case s.threeWay != nil:
		return s.threeWay
	default:
		return s.structural
	}
}

func splitDiff(colored string) []string {
	if colored == "" {
		return []string{dimStyle.Render("no changes")}
	}
	return strings.Split(strings.TrimRight(colored, "\n"), "\n")
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
		header = fmt.Sprintf("three-way: base, your edits, remote (%s)", s.row.Classification)
	}
	b.WriteString(dimStyle.Render(header) + "\n\n")
	b.WriteString(s.scroll.view(s.pageSize()))
	b.WriteString("\n\n")

	hints := []string{"j/k scroll", "e edit", "esc back"}
	if s.row.Format == "json" && s.row.ProposalSlug != "" {
		hints = append([]string{"p publish"}, hints...)
	}
	if s.row.Classification == state.Conflicted {
		hints = append([]string{"m merge the remote in"}, hints...)
	}
	if s.structural != nil || s.threeWay != nil {
		toggle := "u text diff"
		if s.showText {
			toggle = "u structured view"
		}
		hints = append([]string{toggle}, hints...)
	}
	b.WriteString(dimStyle.Render(strings.Join(hints, " · ") + s.scroll.position(s.pageSize())))
	return b.String()
}
