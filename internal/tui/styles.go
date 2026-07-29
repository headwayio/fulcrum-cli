package tui

import (
	"fmt"

	"charm.land/lipgloss/v2"

	"github.com/headwayio/fulcrum-cli/internal/state"
)

var (
	titleStyle    = lipgloss.NewStyle().Bold(true)
	dimStyle      = lipgloss.NewStyle().Faint(true)
	errStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	okStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	warnStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	accentStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	selectedStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("5"))
	modalStyle    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(1, 2)
)

// badge is glyph + word — never color alone (the words survive ASCII and
// colorblindness; color is reinforcement).
func badge(c state.Classification) string {
	switch c {
	case state.Synced:
		return okStyle.Render("= synced")
	case state.Drifted:
		return warnStyle.Render("~ drifted")
	case state.Behind:
		return accentStyle.Render("v behind")
	case state.Conflicted:
		return errStyle.Render("! CONFLICTED")
	case state.Proposed:
		return accentStyle.Render("^ proposed")
	case state.Missing:
		return errStyle.Render("x missing")
	default:
		return dimStyle.Render("o unsynced")
	}
}

// draftBadge: an unpublished draft is nobody else's business, so its row
// says where the writing stands, not how far it has moved from the template
// it was minted from.
func draftBadge(c state.Classification) string {
	switch c {
	case state.Missing:
		return errStyle.Render("x draft · the file is gone")
	case state.Synced:
		return dimStyle.Render("* draft (only you) · still the template")
	case state.Proposed:
		return accentStyle.Render("* draft (only you) · awaiting review")
	default:
		return accentStyle.Render("* draft (only you) · ready to publish")
	}
}

// betaBadge marks a document a local variant is standing in for — the
// version that installs into projects.
func betaBadge(canonicalMoved bool) string {
	if canonicalMoved {
		return warnStyle.Render("β beta · the team's has moved")
	}
	return accentStyle.Render("β beta · yours, current")
}

// outcomeBadge names a reconciled proposal outcome on the row.
func outcomeBadge(outcome string, id int64) string {
	if outcome == "applied" {
		return okStyle.Render(fmt.Sprintf("+ applied #%d (re-sync)", id))
	}
	return errStyle.Render(fmt.Sprintf("- rejected #%d (edits remain)", id))
}

func shortDigest(digest string) string {
	if len(digest) > 10 {
		return digest[:10]
	}
	return digest
}
