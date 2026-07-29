package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/headwayio/fulcrum-cli/internal/api"
	"github.com/headwayio/fulcrum-cli/internal/diffx"
	"github.com/headwayio/fulcrum-cli/internal/state"
	"github.com/headwayio/fulcrum-cli/internal/workspace"
)

// statusLabels mirror the Ruby client's, with reconciled proposal outcomes
// (impossible there — its API was create-only) added.
var statusLabels = map[state.Classification]string{
	state.Synced:     "synced",
	state.Drifted:    "drifted (local edits — publishable)",
	state.Behind:     "behind (remote moved — re-sync)",
	state.Conflicted: "CONFLICTED (local edits AND remote moved)",
	state.Proposed:   "proposed (awaiting review in Fulcrum)",
	state.Missing:    "missing (local file deleted — re-sync)",
	state.Unsynced:   "unsynced (run sync)",
}

func (a *App) statusCmd() *cobra.Command {
	var asJSON, withDiff bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show each document's local state",
		Long: "Shows every document's state. Exit codes are the CI freshness gate:\n" +
			"0 = everything fresh, 1 = anything stale (drifted/behind/conflicted/\n" +
			"missing/unsynced), 2 = network/auth/config failure — staleness and\n" +
			"unreachability are never conflated. Offline, rows come from the last\n" +
			"known sync state. --diff appends a unified diff (last sync → local)\n" +
			"for every drifted or conflicted document.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runStatus(asJSON, withDiff)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "machine-readable output with a reachable flag")
	cmd.Flags().BoolVar(&withDiff, "diff", false, "show what changed on drifted/conflicted documents")
	return cmd
}

type statusRow struct {
	Slug         string `json:"slug"`
	Filename     string `json:"filename"`
	State        string `json:"state"`
	Label        string `json:"-"`
	RemoteDigest string `json:"remote_digest,omitempty"`
	// Beta names the local variant standing in for this document, when one
	// is; CanonicalMoved says the team has advanced past it.
	Beta           string `json:"beta,omitempty"`
	CanonicalMoved bool   `json:"canonical_moved,omitempty"`
}

type statusReport struct {
	Reachable bool        `json:"reachable"`
	Error     string      `json:"error,omitempty"`
	Documents []statusRow `json:"documents"`
}

func (a *App) runStatus(asJSON, withDiff bool) error {
	resolved, err := a.resolveConfig()
	if err != nil {
		return err
	}
	client, err := a.client(resolved)
	if err != nil {
		return err
	}
	w, err := a.openWorkspace(resolved)
	if err != nil {
		return err
	}

	report := statusReport{Reachable: true}
	var manifest *api.Manifest
	manifest, _, fetchErr := client.Manifest(background(), "")
	if fetchErr != nil {
		if _, isContract := api.AsError(fetchErr); isContract {
			// Auth/contract errors are config problems, not offline mode.
			return wrapAPIError(fetchErr)
		}
		report.Reachable = false
		report.Error = fetchErr.Error()
	} else {
		a.reconcileProposals(client, w, manifest)
	}

	rows, err := w.Reconcile(manifest) // nil manifest = offline last-known
	if err != nil {
		return exitf(ExitError, "%v", err)
	}

	stale := false
	for _, row := range rows {
		entry := statusRow{
			Slug:         row.Slug,
			Filename:     row.Filename,
			State:        string(row.Classification),
			Label:        a.rowLabel(w, row),
			RemoteDigest: row.RemoteDigest,
		}
		switch {
		case row.Beta != nil:
			// Running your own version is a choice, not staleness — but the
			// team moving past it is something to act on.
			entry.State = "beta"
			entry.Beta = row.Beta.Filename
			entry.CanonicalMoved = row.Beta.CanonicalMoved
			stale = stale || row.Beta.CanonicalMoved
		case row.Classification != state.Synced && row.Classification != state.Proposed:
			stale = true
		}
		report.Documents = append(report.Documents, entry)
	}

	if asJSON {
		encoded, _ := json.MarshalIndent(report, "", "  ")
		fmt.Fprintln(a.Stdout, string(encoded))
	} else {
		if !report.Reachable {
			fmt.Fprintf(a.Stderr, "offline: %s (showing last-known state)\n", report.Error)
		}
		fmt.Fprintf(a.Stdout, "%-28s %-12s %s\n", "DOCUMENT", "REMOTE", "STATE")
		for _, row := range report.Documents {
			digest := row.RemoteDigest
			if len(digest) > 10 {
				digest = digest[:10] + "…"
			}
			fmt.Fprintf(a.Stdout, "%-28s %-12s %s\n", row.Filename, digest, row.Label)
		}
		if withDiff {
			a.printDiffs(w, rows)
		}
	}

	switch {
	case !report.Reachable:
		return silentExit(ExitError)
	case stale:
		return silentExit(ExitStale)
	default:
		return nil
	}
}

// printDiffs appends a unified last-sync → local diff for every doc with
// local edits — the same renderer the TUI diff screen uses, in plain mode.
func (a *App) printDiffs(w *workspace.Workspace, rows []workspace.DocStatus) {
	for _, row := range rows {
		if row.Classification != state.Drifted && row.Classification != state.Conflicted {
			continue
		}
		local, err := w.ReadLocal(row.Slug)
		if err != nil || local == nil {
			continue
		}
		fmt.Fprintf(a.Stdout, "\n── %s (%s)\n", row.Filename, row.Classification)
		base := w.Base(row.Slug)
		if base == nil {
			fmt.Fprintln(a.Stdout, "no pristine base yet — sync once with this client to enable diffs")
			continue
		}
		unified := diffx.Unified(row.Filename, base, local)
		if unified == "" {
			fmt.Fprintln(a.Stdout, "no changes")
			continue
		}
		fmt.Fprint(a.Stdout, diffx.ColorizeIntraline(unified))
	}
}

// rowLabel names reconciled proposal outcomes instead of a bare drift.
func (a *App) rowLabel(w *workspace.Workspace, row workspace.DocStatus) string {
	// A local variant is the answer to "what am I running", so it replaces
	// the canonical's state on the row rather than sitting beside it.
	if row.Beta != nil {
		if row.Beta.CanonicalMoved {
			return "beta " + row.Beta.Filename + " (yours; the team's has moved — merge)"
		}
		return "beta " + row.Beta.Filename + " (yours; based on the current version)"
	}
	if row.Classification == state.Drifted || row.Classification == state.Conflicted {
		if local, err := w.ReadLocal(row.Slug); err == nil && local != nil {
			if p := w.State.ResolvedProposalFor(row.Slug, state.HexSHA256(local)); p != nil {
				if p.Status == "applied" {
					return "applied (proposal #" + fmt.Sprint(p.ID) + " accepted — re-sync)"
				}
				return "rejected (proposal #" + fmt.Sprint(p.ID) + " declined — local edits remain)"
			}
		}
	}
	return statusLabels[row.Classification]
}

// reconcileProposals annotates local proposal records from the index; a
// server without the capability (or a transient failure) changes nothing.
func (a *App) reconcileProposals(client *api.Client, w *workspace.Workspace, manifest *api.Manifest) {
	if manifest == nil || !manifest.API.Has("proposals_index") || len(w.State.Proposals) == 0 {
		return
	}
	proposals, err := client.Proposals(background())
	if err != nil {
		return
	}
	changed := false
	for _, p := range proposals {
		if w.State.AnnotateProposal(p.ID, p.Status) {
			changed = true
		}
	}
	if changed {
		_ = w.SaveState()
	}
}
