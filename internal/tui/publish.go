package tui

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/headwayio/fulcrum-cli/internal/diffx"
	"github.com/headwayio/fulcrum-cli/internal/state"
)

// publishScreen is the wizard: review the diff, write the required note,
// submit, see the verdict — with a stale base always called out.
type publishScreen struct {
	app *App
	row Row

	scroll  scroller
	note    *lineInput
	stage   int // 0 review+note, 1 submitting, 2 done
	errMsg  string
	receipt *publishedReceipt
	loaded  bool
}

type publishedReceipt struct {
	id             int64
	basedOnCurrent bool
	reviewURL      string
}

func newPublishScreen(a *App, row Row) *publishScreen {
	return &publishScreen{app: a, row: row, note: newLineInput("what changed, and why", "")}
}

func (s *publishScreen) init() tea.Cmd {
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

func (s *publishScreen) title() string    { return "publish" }
func (s *publishScreen) crumbs() []string { return []string{s.row.Filename, "publish"} }

func (s *publishScreen) update(msg tea.Msg) (screen, tea.Cmd) {
	switch msg := msg.(type) {
	case docLoadedMsg:
		s.loaded = true
		if msg.err != nil {
			s.errMsg = errorLine(msg.err)
			return s, nil
		}
		if msg.base != nil {
			unified := diffx.Unified(s.row.Filename, msg.base, msg.local)
			s.scroll.setLines(strings.Split(strings.TrimRight(diffx.Colorize(unified), "\n"), "\n"))
		}
		return s, nil

	case publishedMsg:
		if msg.err != nil {
			s.stage = 0
			s.errMsg = errorLine(msg.err)
			return s, nil
		}
		s.stage = 2
		s.receipt = &publishedReceipt{
			id:             msg.receipt.ID,
			basedOnCurrent: msg.receipt.BasedOnCurrent,
			reviewURL:      msg.receipt.ReviewURL,
		}
		return s, nil

	case tea.KeyPressMsg:
		key := msg.String()
		switch s.stage {
		case 0:
			switch key {
			case "enter":
				return s.submit()
			case "pgup", "pgdown", "ctrl+u", "ctrl+d":
				s.scroll.handleKey(strings.TrimPrefix(key, "ctrl+"), s.pageSize())
			default:
				s.note.handleKey(msg)
			}
		case 2:
			if key == "o" && s.receipt != nil {
				url := s.app.deps.ServerURL() + s.receipt.reviewURL
				_ = s.app.deps.OpenURL(url)
				s.app.status = "opened " + url
			}
		}
	}
	return s, nil
}

func (s *publishScreen) submit() (screen, tea.Cmd) {
	note := strings.TrimSpace(s.note.value)
	if note == "" {
		s.errMsg = "the reviewer note is required"
		return s, nil
	}
	deps := s.app.deps
	row := s.row
	s.stage = 1
	s.errMsg = ""
	return s, func() tea.Msg {
		local, err := deps.LocalDoc(row.Slug)
		if err != nil {
			return publishedMsg{err: err}
		}
		if diffx.HasConflictMarkers(local) {
			return publishedMsg{err: errors.New(
				"unresolved conflict markers in the file — resolve them (e) before publishing")}
		}
		var document map[string]any
		if row.Format == "json" {
			if err := json.Unmarshal(local, &document); err != nil {
				return publishedMsg{err: fmt.Errorf("not valid JSON, nothing submitted: %.120s", err.Error())}
			}
		} else {
			// Markdown (org skills) publishes as the content wrap.
			document = map[string]any{"content": string(local)}
		}
		receipt, err := deps.Publish(PublishRequest{
			Slug:         row.Slug,
			ProposalSlug: row.ProposalSlug,
			Document:     document,
			BaseDigest:   row.BaseDigest,
			Note:         note,
			// The bytes actually submitted, not a re-read: an editor saving
			// mid-flight would otherwise record a hash that was never sent.
			LocalSHA: state.HexSHA256(local),
		})
		return publishedMsg{receipt: receipt, err: err}
	}
}

func (s *publishScreen) pageSize() int { return max(s.app.height-14, 4) }

// verdict says what the submission means for the author, in the words the
// web review page uses — they are one keypress from reading them there.
//
// A draft needs its own sentences: the team has no version of it, so
// "based on the team's current version" would be false, and it would bury
// the thing that actually matters — approving a draft is what reveals it
// org-wide. A draft can still carry a stale base, since its creator can
// edit the same draft in the browser.
func (s *publishScreen) verdict() string {
	switch {
	case s.row.Draft && s.receipt.basedOnCurrent:
		return dimStyle.Render(
			"New skill — approving this publishes it to the team for the first time.") + "\n"
	case s.row.Draft:
		return warnStyle.Render(
			"Your draft moved on the server since you synced — the reviewer sees it flagged.") + "\n"
	case s.receipt.basedOnCurrent:
		return dimStyle.Render(
			"Based on the team's current version — the reviewer can apply it as-is.") + "\n"
	default:
		return warnStyle.Render(
			"Based on an older version — the team's copy moved since you synced.") + "\n" +
			warnStyle.Render(
				"Applying it would supersede those changes, so the reviewer sees it flagged.") + "\n"
	}
}

func (s *publishScreen) view() string {
	if !s.loaded {
		return dimStyle.Render("loading…")
	}

	var b strings.Builder
	switch s.stage {
	case 2:
		b.WriteString(okStyle.Render(fmt.Sprintf("Proposal #%d submitted.", s.receipt.id)) + "\n\n")
		b.WriteString(s.verdict())
		b.WriteString("review at " + accentStyle.Render(s.app.deps.ServerURL()+s.receipt.reviewURL) + "\n\n")
		b.WriteString(dimStyle.Render("o open in browser · esc back to documents"))
	case 1:
		b.WriteString(dimStyle.Render("submitting…"))
	default:
		if s.row.Classification == state.Conflicted {
			b.WriteString(errStyle.Render("remote moved since your sync — this proposal will be flagged stale") + "\n\n")
		}
		b.WriteString(s.scroll.view(s.pageSize()) + "\n\n")
		// The field gets its own labelled line and breathing room: buried in
		// the run of hints under a long diff, nothing said "you type here".
		b.WriteString(titleStyle.Render("Note to the reviewer") +
			dimStyle.Render(" (required)") + "\n")
		b.WriteString(accentStyle.Render("› ") + s.note.render(true) + "\n")
		if s.errMsg != "" {
			b.WriteString(errStyle.Render(s.errMsg) + "\n")
		}
		b.WriteString("\n" + dimStyle.Render("enter submit · pgup/pgdn scroll diff · esc cancel"))
	}
	return b.String()
}
