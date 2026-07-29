// Package tui is the interactive face: one root model, a screen stack, and
// every network/filesystem touch behind a tea.Cmd calling into Deps — which
// tests replace wholesale. internal/cli never imports this package.
package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/headwayio/fulcrum-cli/internal/api"
	"github.com/headwayio/fulcrum-cli/internal/config"
	"github.com/headwayio/fulcrum-cli/internal/scan"
	"github.com/headwayio/fulcrum-cli/internal/state"
	"github.com/headwayio/fulcrum-cli/internal/workspace"
)

// Row is one document line on the list screen.
type Row struct {
	Slug           string
	Filename       string
	Format         string
	ProposalSlug   string
	RemoteDigest   string
	Classification state.Classification
	// Draft marks a skill only its creator can see — publish reveals it.
	Draft bool
	// BaseDigest is the remote digest recorded AT LAST SYNC — what publish
	// sends as base_digest so based_on_current stays truthful.
	BaseDigest string
	// ProposalID is the pending proposal matching the local bytes (proposed
	// rows), zero otherwise.
	ProposalID int64
	// Outcome is "applied"/"rejected" when the local bytes match a resolved
	// proposal, with its id.
	Outcome   string
	OutcomeID int64
}

// Snapshot is everything the list screen shows for one refresh.
type Snapshot struct {
	OrgName   string
	UserEmail string
	Reachable bool
	NetErr    string
	// SkillProposals: the server accepts markdown (org skill) proposals.
	SkillProposals bool
	// SkillDrafts: the server mints developer-initiated draft skills.
	SkillDrafts bool
	Rows        []Row
	Manifest    *api.Manifest
}

// Deps is the TUI's whole outside world.
type Deps interface {
	// Configured reports whether a server URL and token resolve; the auth
	// screen shows when they don't. ServerURL returns whatever URL is known
	// (for the mint-token hint) even when not fully configured.
	Configured() (bool, error)
	ServerURL() string

	// ValidateLogin does a live manifest fetch with the candidate creds.
	ValidateLogin(url, token, orgID string) (*api.Manifest, error)
	// SaveLogin persists them; returns where the token went.
	SaveLogin(url, token, orgID string) (string, error)

	// Refresh fetches the manifest, reconciles proposals, and classifies —
	// falling back to last-known state when the server is unreachable.
	Refresh() (*Snapshot, error)

	LocalDoc(slug string) ([]byte, error)
	// LocalPath is the absolute path of the synced file — what $EDITOR opens.
	// Empty when the document was never synced here.
	LocalPath(slug string) string
	BaseDoc(slug string) []byte
	RemoteDoc(slug string) ([]byte, error)

	// Editor is the command that edits files ($EDITOR, fallback vi).
	Editor() string

	// SyncAll pulls fresh docs, skipping local edits unless force; returns
	// human summary lines.
	SyncAll(force bool) ([]string, error)
	// CreateSkillDraft mints a creator-only draft on the server and lands it
	// in the workspace, tracked like any synced document.
	CreateSkillDraft(name string) (*api.SkillDraft, error)
	Publish(slug string, doc map[string]any, baseDigest, note string) (*api.ProposalReceipt, error)
	ProposalByID(id int64) (*api.Proposal, error)

	Projects() ([]api.Project, error)
	ScanRepo(path string) (*scan.Facts, error)
	PushFacts(projectID int64, facts *scan.Facts) (*api.ArchitectureReceipt, error)

	OpenURL(url string) error
}

// Live wires Deps to the real kernel.
type Live struct {
	Resolved *config.Resolved
	Version  string

	// Conditional-GET cache: the manifest ETag from the last fetch buys a
	// 304 on every refresh until the rubric actually moves.
	lastManifest *api.Manifest
	manifestETag string
}

func (l *Live) client() *api.Client {
	return &api.Client{
		BaseURL:        l.Resolved.URL,
		Token:          l.Resolved.Token,
		OrganizationID: l.Resolved.OrganizationID,
		Version:        l.Version,
	}
}

func (l *Live) workspace() (*workspace.Workspace, error) {
	return workspace.Load(l.Resolved.SkillsDir)
}

func (l *Live) Configured() (bool, error) {
	return l.Resolved.URL != "" && l.Resolved.Token != "", nil
}

func (l *Live) ServerURL() string { return l.Resolved.URL }

func (l *Live) ValidateLogin(url, token, orgID string) (*api.Manifest, error) {
	probe := &api.Client{BaseURL: url, Token: token, OrganizationID: orgID, Version: l.Version}
	manifest, _, err := probe.Manifest(context.Background(), "")
	return manifest, err
}

func (l *Live) SaveLogin(url, token, orgID string) (string, error) {
	dir, err := config.Dir()
	if err != nil {
		return "", err
	}
	file, err := config.LoadFile(dir)
	if err != nil {
		return "", err
	}
	file.URL = url
	file.OrganizationID = orgID
	if err := file.SaveFile(dir); err != nil {
		return "", err
	}
	where, err := config.StoreToken(dir, config.SystemKeyring{}, token)
	if err != nil {
		return "", err
	}
	l.Resolved.URL = url
	l.Resolved.Token = token
	l.Resolved.OrganizationID = orgID
	return where, nil
}

func (l *Live) Refresh() (*Snapshot, error) {
	w, err := l.workspace()
	if err != nil {
		return nil, err
	}
	snapshot := &Snapshot{Reachable: true}

	manifest, res, fetchErr := l.client().Manifest(context.Background(), l.manifestETag)
	if fetchErr == nil && res != nil && res.NotModified {
		manifest = l.lastManifest // 304: the cached manifest is current
	}
	if fetchErr != nil {
		if _, isContract := api.AsError(fetchErr); isContract {
			return nil, fetchErr // auth/contract problems are not offline mode
		}
		snapshot.Reachable = false
		snapshot.NetErr = fetchErr.Error()
	} else {
		if res != nil && !res.NotModified {
			l.lastManifest, l.manifestETag = manifest, res.ETag
		}
		snapshot.Manifest = manifest
		snapshot.OrgName = manifest.Organization.Name
		snapshot.SkillProposals = manifest.API.Has("skill_proposals")
		snapshot.SkillDrafts = manifest.API.Has("skill_drafts")
		if manifest.User != nil {
			snapshot.UserEmail = manifest.User.Email
		}
		l.annotateProposals(w, manifest)
	}

	rows, err := w.Reconcile(manifest)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		r := Row{
			Slug:           row.Slug,
			Filename:       row.Filename,
			Format:         row.Format,
			ProposalSlug:   row.ProposalSlug,
			RemoteDigest:   row.RemoteDigest,
			Draft:          row.Draft,
			Classification: row.Classification,
		}
		if recorded := w.State.Document(row.Slug); recorded != nil {
			r.BaseDigest = recorded.RemoteDigest
		}
		if local, readErr := w.ReadLocal(row.Slug); readErr == nil && local != nil {
			sha := state.HexSHA256(local)
			if p := w.State.ResolvedProposalFor(row.Slug, sha); p != nil {
				r.Outcome, r.OutcomeID = p.Status, p.ID
			}
			for _, p := range w.State.Proposals {
				if p.Slug == row.Slug && p.FileSHA256 == sha && !p.Resolved() {
					r.ProposalID = p.ID
				}
			}
		}
		snapshot.Rows = append(snapshot.Rows, r)
	}
	return snapshot, nil
}

func (l *Live) annotateProposals(w *workspace.Workspace, manifest *api.Manifest) {
	if !manifest.API.Has("proposals_index") || len(w.State.Proposals) == 0 {
		return
	}
	proposals, err := l.client().Proposals(context.Background())
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

func (l *Live) LocalDoc(slug string) ([]byte, error) {
	w, err := l.workspace()
	if err != nil {
		return nil, err
	}
	return w.ReadLocal(slug)
}

func (l *Live) LocalPath(slug string) string {
	w, err := l.workspace()
	if err != nil {
		return ""
	}
	recorded := w.State.Document(slug)
	if recorded == nil {
		return ""
	}
	return filepath.Join(w.Dir, recorded.Filename)
}

func (l *Live) Editor() string {
	if editor := os.Getenv("EDITOR"); editor != "" {
		return editor
	}
	return "vi"
}

func (l *Live) BaseDoc(slug string) []byte {
	w, err := l.workspace()
	if err != nil {
		return nil
	}
	return w.Base(slug)
}

func (l *Live) RemoteDoc(slug string) ([]byte, error) {
	res, err := l.client().Document(context.Background(), slug, "")
	if err != nil {
		return nil, err
	}
	return res.Body, nil
}

func (l *Live) SyncAll(force bool) ([]string, error) {
	w, err := l.workspace()
	if err != nil {
		return nil, err
	}
	manifest, _, err := l.client().Manifest(context.Background(), "")
	if err != nil {
		return nil, err
	}
	var lines []string
	for _, doc := range manifest.Documents {
		local, readErr := w.ReadLocal(doc.Slug)
		if readErr != nil {
			return lines, readErr
		}
		c := w.State.Classify(doc.Slug, local, doc.Digest)
		if !force && c == state.Synced {
			// Digest identity: nothing moved, so there is nothing to fetch.
			lines = append(lines, fmt.Sprintf("fresh %s", doc.Filename))
			continue
		}
		if !force && (c == state.Drifted || c == state.Conflicted || c == state.Proposed) {
			lines = append(lines, fmt.Sprintf("skipped %s (%s — local edits kept)", doc.Filename, c))
			continue
		}
		res, docErr := l.client().Document(context.Background(), doc.Slug, "")
		if docErr != nil {
			return lines, docErr
		}
		if err := w.SyncDocument(doc, res.Body); err != nil {
			return lines, err
		}
		lines = append(lines, fmt.Sprintf("synced %s", doc.Filename))
	}
	return lines, nil
}

func (l *Live) CreateSkillDraft(name string) (*api.SkillDraft, error) {
	draft, err := l.client().CreateSkillDraft(context.Background(), name)
	if err != nil {
		return nil, err
	}
	w, err := l.workspace()
	if err != nil {
		return nil, err
	}
	// Land the template in the workspace, tracked like a synced document —
	// it classifies synced now, drifted the moment it's edited, and the
	// creator-only manifest row keeps it on the list.
	proposalSlug := draft.ProposalSlug
	err = w.SyncDocument(api.ManifestDocument{
		Slug: draft.Slug, Format: draft.Format, Digest: draft.Digest,
		Filename: draft.Filename, ProposalSlug: &proposalSlug, Draft: true,
	}, []byte(draft.Content))
	if err != nil {
		return nil, err
	}
	return draft, nil
}

func (l *Live) Publish(slug string, doc map[string]any, baseDigest, note string) (*api.ProposalReceipt, error) {
	receipt, err := l.client().SubmitProposal(context.Background(), slug, doc, baseDigest, note)
	if err != nil {
		return nil, err
	}
	return receipt, nil
}

func (l *Live) ProposalByID(id int64) (*api.Proposal, error) {
	proposals, err := l.client().Proposals(context.Background())
	if err != nil {
		return nil, err
	}
	for i := range proposals {
		if proposals[i].ID == id {
			return &proposals[i], nil
		}
	}
	return nil, fmt.Errorf("proposal %d not found", id)
}

func (l *Live) Projects() ([]api.Project, error) {
	return l.client().Projects(context.Background())
}

func (l *Live) ScanRepo(path string) (*scan.Facts, error) { return scan.Collect(path) }

func (l *Live) PushFacts(projectID int64, facts *scan.Facts) (*api.ArchitectureReceipt, error) {
	return l.client().PushArchitecture(context.Background(), projectID, facts.Payload(), facts.Repository)
}

func (l *Live) OpenURL(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	default:
		return exec.Command("xdg-open", url).Start()
	}
}
