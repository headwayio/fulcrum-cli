package tui

import (
	"testing"

	"charm.land/lipgloss/v2"
)

// The list above the status line already names every document and its state,
// so the sync report's job is to say what the sync *did* — leading with what
// changed, and never repeating the filenames.
func TestSyncSummaryLine(t *testing.T) {
	cases := []struct {
		name string
		sum  SyncSummary
		want string
	}{
		{
			name: "nothing moved",
			sum:  SyncSummary{Fresh: 5},
			want: "5 already current",
		},
		{
			name: "a draft is not staleness, so it is reported apart from the rest",
			sum:  SyncSummary{Fresh: 4, Drafts: 1},
			want: "4 already current · 1 draft untouched",
		},
		{
			name: "once something moved, already-current is filler",
			sum:  SyncSummary{Synced: 2, Fresh: 3},
			want: "synced 2",
		},
		{
			name: "skips are worth saying even when nothing synced",
			sum:  SyncSummary{Skipped: 2, Fresh: 3},
			want: "2 kept your edits",
		},
		{
			name: "everything at once",
			sum:  SyncSummary{Synced: 1, Skipped: 2, Fresh: 1, Drafts: 2, Projects: 3},
			want: "synced 1 · 2 kept your edits · 2 drafts untouched · refreshed 3 projects",
		},
		{
			name: "an empty workspace still says something true",
			sum:  SyncSummary{},
			want: "nothing to sync",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.sum.Line(); got != tc.want {
				t.Errorf("Line() = %q, want %q", got, tc.want)
			}
		})
	}
}

// A status line that wraps is a status line nobody reads, and the goldens
// pin an 80-column terminal. Columns, not bytes: the separator is a
// two-byte "·" that occupies one cell.
func TestSyncSummaryLineStaysOnOneLine(t *testing.T) {
	for _, worst := range []SyncSummary{
		{Synced: 99, Skipped: 99, Fresh: 99, Drafts: 99, Projects: 99},
		{Fresh: 99, Drafts: 99, Projects: 99}, // the quiet run, which does count Fresh
	} {
		if got := lipgloss.Width(worst.Line()); got > 80 {
			t.Errorf("summary is %d cols, wraps at 80: %q", got, worst.Line())
		}
	}
}
