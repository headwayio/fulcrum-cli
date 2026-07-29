package tui

import (
	"errors"
	"fmt"

	"github.com/headwayio/fulcrum-cli/internal/api"
	"github.com/headwayio/fulcrum-cli/internal/scan"
	"github.com/headwayio/fulcrum-cli/internal/state"
)

// fakeDeps is the whole outside world, canned.
type fakeDeps struct {
	configured bool
	serverURL  string

	snapshot   *Snapshot
	refreshErr error

	localDocs  map[string][]byte
	baseDocs   map[string][]byte
	remoteDocs map[string][]byte

	validateManifest *api.Manifest
	validateErr      error
	savedURL         string
	savedToken       string

	publishReceipt *api.ProposalReceipt
	publishErr     error
	published      []string
	publishedSHAs  []string

	proposals   map[int64]*api.Proposal
	projects    []api.Project
	facts       *scan.Facts
	pushedTo    []int64
	opened      []string
	syncSummary SyncSummary
	refreshes   int
	drafted     []string
	draftErr    error

	merged         []string
	mergeConflicts int
	mergeErr       error
	discarded      []string
	betaStarted    []string
	betaDropped    []string
	chosenOrg      string
}

func (f *fakeDeps) Configured() (bool, error) { return f.configured, nil }
func (f *fakeDeps) ServerURL() string         { return f.serverURL }

func (f *fakeDeps) ValidateLogin(url, token, orgID string) (*api.Manifest, error) {
	if f.validateErr != nil {
		return nil, f.validateErr
	}
	return f.validateManifest, nil
}

func (f *fakeDeps) SaveLogin(url, token, orgID string) (string, error) {
	f.savedURL, f.savedToken = url, token
	return "keyring", nil
}

func (f *fakeDeps) SetOrganization(id string) error {
	f.chosenOrg = id
	// A picked organization unblocks whatever the 422 was refusing.
	f.refreshErr = nil
	return nil
}

func (f *fakeDeps) Refresh() (*Snapshot, error) {
	f.refreshes++
	if f.refreshErr != nil {
		return nil, f.refreshErr
	}
	return f.snapshot, nil
}

func (f *fakeDeps) LocalDoc(slug string) ([]byte, error)  { return f.localDocs[slug], nil }
func (f *fakeDeps) BaseDoc(slug string) []byte            { return f.baseDocs[slug] }
func (f *fakeDeps) RemoteDoc(slug string) ([]byte, error) { return f.remoteDocs[slug], nil }

func (f *fakeDeps) LocalPath(slug string) string {
	if f.localDocs[slug] == nil {
		return ""
	}
	return "/fake/workspace/" + slug
}

// Editor is `true`: exits 0 instantly, proving the handoff/refresh loop
// without a real editor.
func (f *fakeDeps) Editor() string { return "true" }

func (f *fakeDeps) SyncAll(force bool) (SyncSummary, error) { return f.syncSummary, nil }

func (f *fakeDeps) MergeRemote(slug string) (*MergeOutcome, error) {
	if f.mergeErr != nil {
		return nil, f.mergeErr
	}
	f.merged = append(f.merged, slug)
	return &MergeOutcome{Filename: slug + ".md", Conflicts: f.mergeConflicts}, nil
}

func (f *fakeDeps) DiscardLocal(slug string) (string, error) {
	f.discarded = append(f.discarded, slug)
	return "/fake/workspace/.fulcrum/discarded/" + slug + ".md", nil
}

func (f *fakeDeps) StartBeta(slug string) (string, error) {
	f.betaStarted = append(f.betaStarted, slug)
	return slug + ".beta.md", nil
}

func (f *fakeDeps) DropBeta(slug string) (string, error) {
	f.betaDropped = append(f.betaDropped, slug)
	return "/fake/workspace/.fulcrum/discarded/" + slug + ".beta.md", nil
}

func (f *fakeDeps) CreateSkillDraft(name string) (*api.SkillDraft, error) {
	if f.draftErr != nil {
		return nil, f.draftErr
	}
	f.drafted = append(f.drafted, name)
	return &api.SkillDraft{
		Slug: "skill-" + name, Filename: "skill-" + name + ".md", Format: "markdown",
		Digest: "draft-digest", Version: 1, ProposalSlug: "skill-" + name, Draft: true,
		Content: "---\nname: " + name + "\n---\n\nTemplate.\n",
	}, nil
}

func (f *fakeDeps) Publish(req PublishRequest) (*api.ProposalReceipt, error) {
	if f.publishErr != nil {
		return nil, f.publishErr
	}
	f.published = append(f.published,
		fmt.Sprintf("%s base=%s note=%s", req.ProposalSlug, req.BaseDigest, req.Note))
	f.publishedSHAs = append(f.publishedSHAs, req.LocalSHA)
	return f.publishReceipt, nil
}

func (f *fakeDeps) ProposalByID(id int64) (*api.Proposal, error) {
	if p, ok := f.proposals[id]; ok {
		return p, nil
	}
	return nil, errors.New("proposal not found")
}

func (f *fakeDeps) Projects() ([]api.Project, error) { return f.projects, nil }
func (f *fakeDeps) ScanRepo(path string) (*scan.Facts, error) {
	if f.facts == nil {
		return nil, errors.New("no repo there")
	}
	return f.facts, nil
}

func (f *fakeDeps) PushFacts(projectID int64, facts *scan.Facts) (*api.ArchitectureReceipt, error) {
	f.pushedTo = append(f.pushedTo, projectID)
	return &api.ArchitectureReceipt{ProjectID: projectID, Source: "local_scan"}, nil
}

func (f *fakeDeps) OpenURL(url string) error {
	f.opened = append(f.opened, url)
	return nil
}

// allStatesSnapshot has one row per classification plus a resolved-outcome
// row — the badge vocabulary in one frame.
func allStatesSnapshot() *Snapshot {
	digest := "aaaabbbbccccdddd"
	rows := []Row{
		{Slug: "doc-synced", Filename: "synced.md", Format: "markdown", RemoteDigest: digest,
			Classification: state.Synced},
		{Slug: "doc-drifted", Filename: "drifted.json", Format: "json", ProposalSlug: "doc-drifted-target",
			RemoteDigest: digest, BaseDigest: digest, Classification: state.Drifted},
		{Slug: "doc-behind", Filename: "behind.md", Format: "markdown", RemoteDigest: "eeeeffff00001111",
			BaseDigest: digest, Classification: state.Behind},
		{Slug: "doc-conflict", Filename: "conflicted.json", Format: "json", ProposalSlug: "doc-conflict-target",
			RemoteDigest: "eeeeffff00001111", BaseDigest: digest, Classification: state.Conflicted},
		{Slug: "doc-proposed", Filename: "proposed.json", Format: "json", ProposalSlug: "doc-proposed-target",
			RemoteDigest: digest, BaseDigest: digest, Classification: state.Proposed, ProposalID: 41},
		{Slug: "doc-missing", Filename: "missing.md", Format: "markdown", RemoteDigest: digest,
			Classification: state.Missing},
		{Slug: "doc-applied", Filename: "applied.json", Format: "json", ProposalSlug: "doc-applied-target",
			RemoteDigest: digest, BaseDigest: digest, Classification: state.Drifted,
			Outcome: "applied", OutcomeID: 7},
	}
	return &Snapshot{OrgName: "Corpus Primary Organization", UserEmail: "developer@corpus.usefulcrum.test",
		Reachable: true, Rows: rows}
}
