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

// SyncState mirrors the Ruby class. The exported fields marshal in the
// Ruby writer's key order.
type SyncState struct {
	Documents map[string]*Document `json:"documents"`
	Proposals []*Proposal          `json:"proposals"`
	// FileVersion is 0 for files written by the Ruby client (absent key) and
	// Version for files this client created.
	FileVersion int `json:"version,omitempty"`
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
		FileSHA256:   hexSHA256(body),
	}
}

// RecordProposal mirrors Ruby record_proposal.
func (s *SyncState) RecordProposal(slug string, id int64, fileSHA256 string) {
	s.Proposals = append(s.Proposals, &Proposal{Slug: slug, ID: id, FileSHA256: fileSHA256})
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

	localSHA := hexSHA256(localBody)
	localChanged := localSHA != recorded.FileSHA256
	remoteMoved := remoteDigest != recorded.RemoteDigest

	switch {
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

// proposed mirrors Ruby proposed?: dedupe by exact (slug, file_sha256).
func (s *SyncState) proposed(slug, fileSHA256 string) bool {
	for _, p := range s.Proposals {
		if p.Slug == slug && p.FileSHA256 == fileSHA256 {
			return true
		}
	}
	return false
}

func hexSHA256(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
