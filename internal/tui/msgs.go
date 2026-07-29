package tui

import (
	"github.com/headwayio/fulcrum-cli/internal/api"
	"github.com/headwayio/fulcrum-cli/internal/scan"
)

// Every cross-screen tea.Msg lives here, per the plan: message types are the
// screens' shared vocabulary, and scattering them breeds duplicate meanings.

// snapshotMsg carries a Refresh result to the list screen.
type snapshotMsg struct {
	snapshot *Snapshot
	err      error
}

// loginCheckedMsg carries a ValidateLogin result to the auth screen.
type loginCheckedMsg struct {
	manifest *api.Manifest
	url      string
	token    string
	orgID    string
	err      error
}

// loginSavedMsg reports SaveLogin.
type loginSavedMsg struct {
	where string
	err   error
}

// docLoadedMsg carries document bytes to the reader/diff screens.
type docLoadedMsg struct {
	slug   string
	local  []byte
	base   []byte
	remote []byte
	err    error
}

// syncedMsg reports a SyncAll.
type syncedMsg struct {
	lines []string
	err   error
}

// publishedMsg reports a proposal submission.
type publishedMsg struct {
	receipt *api.ProposalReceipt
	err     error
}

// proposalMsg carries proposal detail.
type proposalMsg struct {
	proposal *api.Proposal
	err      error
}

// projectsMsg carries the project list to the push-facts picker.
type projectsMsg struct {
	projects []api.Project
	err      error
}

// scannedMsg carries repo facts to the push-facts preview.
type scannedMsg struct {
	facts *scan.Facts
	err   error
}

// factsPushedMsg reports the architecture push.
type factsPushedMsg struct {
	receipt *api.ArchitectureReceipt
	err     error
}

// authFailedMsg is emitted when any in-session call comes back 401 — the
// root model raises the re-auth modal over whatever screen is showing.
type authFailedMsg struct {
	message string
}
