// Package workspace owns the on-disk sync dir: document files, the shared
// .fulcrum-sync.json state, and pristine base copies under .fulcrum/base/
// (which enable local diffing without a network). All writes are atomic
// (temp file + rename) and state persists after every document so a killed
// sync never lies about what landed.
package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/headwayio/fulcrum-cli/internal/api"
	"github.com/headwayio/fulcrum-cli/internal/state"
)

// BaseDir is where pristine synced copies live, relative to the workspace.
// Additive beside the Ruby client's files; it never reads or writes this.
const BaseDir = ".fulcrum/base"

// Workspace is one sync dir shared (by contract) with the Ruby client.
type Workspace struct {
	Dir   string
	State *state.SyncState
}

// Load opens dir, creating it if needed.
func Load(dir string) (*Workspace, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	s, err := state.Load(dir)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", state.StateFile, err)
	}
	return &Workspace{Dir: dir, State: s}, nil
}

// DocStatus is one document's reconciled row — the single source for both
// `status` output and the TUI list.
type DocStatus struct {
	Slug           string
	Filename       string
	Format         string
	ProposalSlug   string
	RemoteDigest   string
	Draft          bool
	Classification state.Classification
	// Beta describes the local variant overriding this document, when there
	// is one. The row stays single: one skill, one line, whichever version
	// the developer is actually using.
	Beta *BetaStatus
}

// BetaStatus is the overlay a local variant puts on its document's row.
type BetaStatus struct {
	Filename string
	// CanonicalMoved is true when the canonical document has advanced past
	// the version this variant was forked from — mergeable, not broken.
	CanonicalMoved bool
	BaseDigest     string
}

// ReadLocal returns the local bytes for a synced document, nil (no error)
// when the file does not exist — nil is what classification expects.
func (w *Workspace) ReadLocal(slug string) ([]byte, error) {
	recorded := w.State.Document(slug)
	if recorded == nil {
		return nil, nil
	}
	body, err := os.ReadFile(filepath.Join(w.Dir, recorded.Filename))
	if os.IsNotExist(err) {
		return nil, nil
	}
	return body, err
}

// SyncDocument writes body atomically, stores the pristine base copy, records
// the sync in state, and persists state immediately.
func (w *Workspace) SyncDocument(doc api.ManifestDocument, body []byte) error {
	if err := writeAtomic(filepath.Join(w.Dir, doc.Filename), body); err != nil {
		return err
	}
	if err := writeAtomic(filepath.Join(w.Dir, BaseDir, doc.Filename), body); err != nil {
		return err
	}
	w.State.RecordSync(doc.Slug, doc.Filename, doc.Digest, body)
	return w.SaveState()
}

// BetaFilename is the variant's name beside its canonical document:
// skill-writing-specs.md → skill-writing-specs.beta.md. Flat, like every
// other workspace file, so the frozen Ruby client is unaffected and both
// versions sit side by side.
func BetaFilename(filename string) string {
	return strings.TrimSuffix(filename, filepath.Ext(filename)) + ".beta" + filepath.Ext(filename)
}

// StartBeta turns the working copy into a local variant: `content` (the
// developer's in-flight edits) becomes the beta, `canonical` becomes the
// document again, and the canonical version at this moment becomes the
// variant's merge base. Sync is free to move the canonical from here on.
func (w *Workspace) StartBeta(doc api.ManifestDocument, content, canonical []byte) error {
	betaName := BetaFilename(doc.Filename)
	if err := writeAtomic(filepath.Join(w.Dir, betaName), content); err != nil {
		return err
	}
	// The variant carries its own pristine base — the canonical file's base
	// moves with every sync and cannot answer "what did I fork from".
	if err := writeAtomic(filepath.Join(w.Dir, BaseDir, betaName), canonical); err != nil {
		return err
	}
	if err := w.SyncDocument(doc, canonical); err != nil {
		return err
	}
	w.State.RecordBeta(doc.Slug, betaName, doc.Digest)
	return w.SaveState()
}

// DropBeta hands authority back to the canonical document, keeping the
// variant's text under .fulcrum/discarded/ rather than deleting work.
func (w *Workspace) DropBeta(slug string) (string, error) {
	beta := w.State.BetaFor(slug)
	if beta == nil {
		return "", nil
	}
	kept := ""
	if content := w.ReadBeta(slug); content != nil {
		kept = filepath.Join(w.Dir, DiscardedDir, beta.Filename)
		if err := writeAtomic(kept, content); err != nil {
			return "", err
		}
	}
	_ = os.Remove(filepath.Join(w.Dir, beta.Filename))
	_ = os.Remove(filepath.Join(w.Dir, BaseDir, beta.Filename))
	w.State.DropBeta(slug)
	return kept, w.SaveState()
}

// ReadBeta returns the variant's current bytes, nil when there is none.
func (w *Workspace) ReadBeta(slug string) []byte {
	beta := w.State.BetaFor(slug)
	if beta == nil {
		return nil
	}
	content, err := os.ReadFile(filepath.Join(w.Dir, beta.Filename))
	if err != nil {
		return nil
	}
	return content
}

// BetaBase returns the canonical bytes the variant was forked from.
func (w *Workspace) BetaBase(slug string) []byte {
	beta := w.State.BetaFor(slug)
	if beta == nil {
		return nil
	}
	content, err := os.ReadFile(filepath.Join(w.Dir, BaseDir, beta.Filename))
	if err != nil {
		return nil
	}
	return content
}

// RebaseBeta records a merge of the canonical's current version into the
// variant: merged content becomes the variant, and the canonical it was
// merged with becomes the new base.
func (w *Workspace) RebaseBeta(doc api.ManifestDocument, merged, canonical []byte) error {
	beta := w.State.BetaFor(doc.Slug)
	if beta == nil {
		return fmt.Errorf("%s has no local variant", doc.Filename)
	}
	if err := writeAtomic(filepath.Join(w.Dir, beta.Filename), merged); err != nil {
		return err
	}
	if err := writeAtomic(filepath.Join(w.Dir, BaseDir, beta.Filename), canonical); err != nil {
		return err
	}
	w.State.RecordBeta(doc.Slug, beta.Filename, doc.Digest)
	return w.SaveState()
}

// DiscardedDir keeps a copy of thrown-away local edits. Discarding is the
// one destructive verb this client has; a copy costs nothing and means a
// wrong keypress is recoverable.
const DiscardedDir = ".fulcrum/discarded"

// BackupLocal copies the current working file aside and returns its path,
// or "" when there is nothing local to keep.
func (w *Workspace) BackupLocal(slug string) (string, error) {
	body, err := w.ReadLocal(slug)
	if err != nil || body == nil {
		return "", err
	}
	recorded := w.State.Document(slug)
	if recorded == nil {
		return "", nil
	}
	path := filepath.Join(w.Dir, DiscardedDir, recorded.Filename)
	if err := writeAtomic(path, body); err != nil {
		return "", err
	}
	return path, nil
}

// AdoptMerge records the server's current version as the new baseline while
// leaving merged content in the working file. That is what makes a merge
// honest downstream: the doc reclassifies from conflicted to drifted, and
// the base digest publish sends is the version actually merged against, so
// the proposal's based_on_current flag tells the truth.
func (w *Workspace) AdoptMerge(doc api.ManifestDocument, remote, merged []byte) error {
	if err := w.SyncDocument(doc, remote); err != nil {
		return err
	}
	return writeAtomic(filepath.Join(w.Dir, doc.Filename), merged)
}

// Base returns the pristine bytes from the last sync, nil when absent (a
// workspace last synced by the Ruby client has no bases yet).
func (w *Workspace) Base(slug string) []byte {
	recorded := w.State.Document(slug)
	if recorded == nil {
		return nil
	}
	body, err := os.ReadFile(filepath.Join(w.Dir, BaseDir, recorded.Filename))
	if err != nil {
		return nil
	}
	return body
}

// SaveState persists the shared state file.
func (w *Workspace) SaveState() error {
	return w.State.Save(w.Dir)
}

// Reconcile classifies every document against the manifest. A nil manifest
// is the offline path: rows come from the state file's last-known remote
// digests, so staleness still renders honestly with the network gone.
func (w *Workspace) Reconcile(manifest *api.Manifest) ([]DocStatus, error) {
	if manifest != nil {
		rows := make([]DocStatus, 0, len(manifest.Documents))
		for _, doc := range manifest.Documents {
			local, err := w.ReadLocal(doc.Slug)
			if err != nil {
				return nil, err
			}
			proposalSlug := ""
			if doc.ProposalSlug != nil {
				proposalSlug = *doc.ProposalSlug
			}
			rows = append(rows, DocStatus{
				Slug:           doc.Slug,
				Filename:       doc.Filename,
				Format:         doc.Format,
				ProposalSlug:   proposalSlug,
				RemoteDigest:   doc.Digest,
				Draft:          doc.Draft,
				Classification: w.State.Classify(doc.Slug, local, doc.Digest),
				Beta:           w.betaStatus(doc.Slug, doc.Digest),
			})
		}
		return rows, nil
	}

	slugs := make([]string, 0, len(w.State.Documents))
	for slug := range w.State.Documents {
		slugs = append(slugs, slug)
	}
	sort.Strings(slugs)
	rows := make([]DocStatus, 0, len(slugs))
	for _, slug := range slugs {
		recorded := w.State.Document(slug)
		local, err := w.ReadLocal(slug)
		if err != nil {
			return nil, err
		}
		rows = append(rows, DocStatus{
			Slug:         slug,
			Filename:     recorded.Filename,
			RemoteDigest: recorded.RemoteDigest,
			Beta:         w.betaStatus(slug, recorded.RemoteDigest),
			// Last-known digest: remote movement is invisible offline, and
			// that is the honest answer — never derive digests locally.
			Classification: w.State.Classify(slug, local, recorded.RemoteDigest),
		})
	}
	return rows, nil
}

func (w *Workspace) betaStatus(slug, canonicalDigest string) *BetaStatus {
	beta := w.State.BetaFor(slug)
	if beta == nil {
		return nil
	}
	return &BetaStatus{
		Filename:       beta.Filename,
		BaseDigest:     beta.BaseDigest,
		CanonicalMoved: beta.BaseDigest != canonicalDigest,
	}
}

// writeAtomic writes via temp file + rename in the destination directory, so
// readers never observe a torn file and a crash leaves the old bytes.
func writeAtomic(path string, body []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".fulcrum-write-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename

	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
