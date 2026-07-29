// Package state is the exact port of the Ruby reference client's
// FulcrumSkills::SyncState — pure local-state bookkeeping for the sync
// workspace: what is synced, what drifted locally, what moved remotely, and
// what is already proposed upstream. No network, no filesystem beyond
// load/save. The .fulcrum-sync.json schema is a compatibility contract: both
// clients must be able to share a workspace dir, so this file stays
// byte-compatible with the Ruby writer and is extended additively only.
package state

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
)

// StateFile is the workspace-relative filename, shared with the Ruby client.
const StateFile = ".fulcrum-sync.json"

// Version is stamped into state files created by this client. The Ruby
// loader tolerates unknown keys (verified), so this is additive.
const Version = 1

// Classification is one document's local state. Precedence lives in
// Classify; the values match the Ruby symbols.
type Classification string

const (
	Unsynced   Classification = "unsynced"
	Missing    Classification = "missing"
	Synced     Classification = "synced"
	Drifted    Classification = "drifted"
	Behind     Classification = "behind"
	Conflicted Classification = "conflicted"
	Proposed   Classification = "proposed"
)

// Document is the per-slug sync record.
type Document struct {
	Filename     string `json:"filename"`
	RemoteDigest string `json:"remote_digest"`
	FileSHA256   string `json:"file_sha256"`
}

// Proposal is a locally recorded submission. Status is this client's
// additive extension, reconciled from the proposals index; the Ruby client
// neither writes nor reads it.
type Proposal struct {
	Slug       string `json:"slug"`
	ID         int64  `json:"id"`
	FileSHA256 string `json:"file_sha256"`
	Status     string `json:"status,omitempty"`
}

// Beta is a local variant of a synced document — the version a developer is
// actually working with while the canonical one keeps syncing beside it.
// Deliberately NOT a seventh classification: "conflicted" means resolve me
// now, and running an experiment is not that.
type Beta struct {
	// Slug is the canonical document this overrides.
	Slug string `json:"slug"`
	// Filename is the beta's own file, <name>.beta.md, so both versions are
	// visible and diffable in the workspace.
	Filename string `json:"filename"`
	// BaseDigest is the canonical version this was forked from or last
	// merged with — what publish sends, so based_on_current stays truthful
	// however far the canonical has moved since.
	BaseDigest string `json:"base_digest"`
}

// SyncState mirrors the Ruby class. The exported fields marshal in the
// Ruby writer's key order.
type SyncState struct {
	Documents map[string]*Document `json:"documents"`
	Proposals []*Proposal          `json:"proposals"`
	// FileVersion is 0 for files written by the Ruby client (absent key) and
	// Version for files this client created.
	FileVersion int `json:"version,omitempty"`
	// Installs are project directories skills were installed into, so a sync
	// can refresh every harness that is reading them. Additive and omitted
	// when empty — the Ruby client tolerates unknown keys, and a workspace
	// that never installed anything writes the same bytes as before.
	Installs []string `json:"installs,omitempty"`
	// Betas are local variants overriding their canonical documents. Also
	// additive and omitted when empty.
	Betas []*Beta `json:"betas,omitempty"`
}

// New returns an empty state as this client creates it.
func New() *SyncState {
	return &SyncState{
		Documents:   map[string]*Document{},
		Proposals:   []*Proposal{},
		FileVersion: Version,
	}
}

// Load reads dir's state file; a missing file is an empty state (but marked
// with this client's version, exactly as the Ruby client treats absence).
func Load(dir string) (*SyncState, error) {
	raw, err := os.ReadFile(filepath.Join(dir, StateFile))
	if os.IsNotExist(err) {
		return New(), nil
	}
	if err != nil {
		return nil, err
	}
	// Start from zero values, not New(): a Ruby-written file has no version
	// key and must round-trip byte-exact, so absence stays absence.
	loaded := &SyncState{}
	if err := json.Unmarshal(raw, loaded); err != nil {
		return nil, err
	}
	if loaded.Documents == nil {
		loaded.Documents = map[string]*Document{}
	}
	if loaded.Proposals == nil {
		loaded.Proposals = []*Proposal{}
	}
	return loaded, nil
}

// Save writes the state file exactly as the Ruby client does:
// two-space-indented JSON, no HTML escaping, trailing newline.
func (s *SyncState) Save(dir string) error {
	return os.WriteFile(filepath.Join(dir, StateFile), s.Encode(), 0o644)
}

// Encode renders the byte-compatible file body.
func (s *SyncState) Encode() []byte {
	var buf jsonBuffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	// Encode cannot fail for this shape.
	_ = enc.Encode(s)
	return buf.bytes
}

type jsonBuffer struct{ bytes []byte }

func (b *jsonBuffer) Write(p []byte) (int, error) {
	b.bytes = append(b.bytes, p...)
	return len(p), nil
}

// RecordSync mirrors Ruby record_sync: called per document as sync writes it.
func (s *SyncState) RecordSync(slug, filename, remoteDigest string, body []byte) {
	s.Documents[slug] = &Document{
		Filename:     filename,
		RemoteDigest: remoteDigest,
		FileSHA256:   HexSHA256(body),
	}
}

// BaseBehind reports bookkeeping that has fallen behind the server: the
// recorded base predates what is being served. True even when the file
// already holds the right bytes (your own proposal, applied), because every
// later comparison is made against that base — so it is worth a re-sync.
func (s *SyncState) BaseBehind(slug, remoteDigest string) bool {
	recorded := s.Documents[slug]
	return recorded != nil && recorded.RemoteDigest != remoteDigest
}

// ForgetResolvedProposals drops applied/rejected records for a document the
// workspace has now caught up with. An outcome is news, and news expires:
// left in place it matches the same local bytes forever, so the row would
// keep announcing an approval from weeks ago.
func (s *SyncState) ForgetResolvedProposals(slug string) {
	kept := s.Proposals[:0]
	for _, p := range s.Proposals {
		if p.Slug == slug && p.Resolved() {
			continue
		}
		kept = append(kept, p)
	}
	s.Proposals = kept
}

// RecordProposal mirrors Ruby record_proposal.
func (s *SyncState) RecordProposal(slug string, id int64, fileSHA256 string) {
	s.Proposals = append(s.Proposals, &Proposal{Slug: slug, ID: id, FileSHA256: fileSHA256})
}

// RememberInstall records a project directory as an install site. Returns
// true when this is new, so callers know whether to persist.
func (s *SyncState) RememberInstall(dir string) bool {
	absolute, err := filepath.Abs(dir)
	if err != nil {
		absolute = dir
	}
	for _, known := range s.Installs {
		if known == absolute {
			return false
		}
	}
	s.Installs = append(s.Installs, absolute)
	sort.Strings(s.Installs)
	return true
}

// ForgetInstall drops a project directory (it moved, or was deleted).
func (s *SyncState) ForgetInstall(dir string) bool {
	for i, known := range s.Installs {
		if known == dir {
			s.Installs = append(s.Installs[:i], s.Installs[i+1:]...)
			return true
		}
	}
	return false
}

// BetaFor returns the local variant overriding slug, nil when there is none.
func (s *SyncState) BetaFor(slug string) *Beta {
	for _, beta := range s.Betas {
		if beta.Slug == slug {
			return beta
		}
	}
	return nil
}

// RecordBeta creates or re-bases the variant for slug.
func (s *SyncState) RecordBeta(slug, filename, baseDigest string) {
	if existing := s.BetaFor(slug); existing != nil {
		existing.Filename = filename
		existing.BaseDigest = baseDigest
		return
	}
	s.Betas = append(s.Betas, &Beta{Slug: slug, Filename: filename, BaseDigest: baseDigest})
	sort.Slice(s.Betas, func(i, j int) bool { return s.Betas[i].Slug < s.Betas[j].Slug })
}

// DropBeta removes the variant for slug, handing authority back to the
// canonical document. Reports whether there was one.
func (s *SyncState) DropBeta(slug string) bool {
	for i, beta := range s.Betas {
		if beta.Slug == slug {
			s.Betas = append(s.Betas[:i], s.Betas[i+1:]...)
			return true
		}
	}
	return false
}

// Document returns the sync record for slug, nil when never synced.
func (s *SyncState) Document(slug string) *Document {
	return s.Documents[slug]
}

// Classify ports the Ruby precedence chain exactly:
//
//	unsynced → missing → proposed → conflicted → drifted → behind → synced
//
// localBody nil means the local file does not exist; an existing empty file
// is an empty, non-nil slice. remoteDigest is opaque — never derived locally.
func (s *SyncState) Classify(slug string, localBody []byte, remoteDigest string) Classification {
	recorded := s.Documents[slug]
	if recorded == nil {
		return Unsynced
	}
	if localBody == nil {
		return Missing
	}

	localSHA := HexSHA256(localBody)
	localChanged := localSHA != recorded.FileSHA256
	remoteMoved := remoteDigest != recorded.RemoteDigest

	switch {
	// Both sides hold the same bytes — normally your own proposal coming
	// back applied. Nothing can be in conflict with itself, whatever the
	// last-sync bookkeeping still says. (A document whose digest is not its
	// body's SHA, like the rendered rubric, simply never matches here.)
	case localSHA == remoteDigest:
		return Synced
	case localChanged && s.proposed(slug, localSHA):
		return Proposed
	case localChanged && remoteMoved:
		return Conflicted
	case localChanged:
		return Drifted
	case remoteMoved:
		return Behind
	default:
		return Synced
	}
}

// proposed mirrors Ruby proposed?: dedupe by exact (slug, file_sha256). One
// additive difference: proposals reconciled to a resolved status no longer
// pin the doc as proposed — the Ruby client shows :proposed forever because
// its API was create-only.
func (s *SyncState) proposed(slug, fileSHA256 string) bool {
	for _, p := range s.Proposals {
		if p.Slug == slug && p.FileSHA256 == fileSHA256 && !p.Resolved() {
			return true
		}
	}
	return false
}

// Resolved reports whether the proposal's reconciled status is terminal.
func (p *Proposal) Resolved() bool {
	return p.Status == "applied" || p.Status == "rejected"
}

// ResolvedProposalFor returns the terminal proposal matching the local
// bytes, if any — status output names the outcome instead of a bare drift.
func (s *SyncState) ResolvedProposalFor(slug, fileSHA256 string) *Proposal {
	for _, p := range s.Proposals {
		if p.Slug == slug && p.FileSHA256 == fileSHA256 && p.Resolved() {
			return p
		}
	}
	return nil
}

// AnnotateProposal records the reconciled status for a proposal id (from the
// proposals index). Returns true when something changed.
func (s *SyncState) AnnotateProposal(id int64, status string) bool {
	for _, p := range s.Proposals {
		if p.ID == id && p.Status != status {
			p.Status = status
			return true
		}
	}
	return false
}

// HexSHA256 is the digest both clients use for local file identity.
func HexSHA256(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
