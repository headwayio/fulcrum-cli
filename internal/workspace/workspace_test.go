package workspace

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/headwayio/fulcrum-cli/internal/api"
	"github.com/headwayio/fulcrum-cli/internal/state"
)

func corpusManifest(t *testing.T) *api.Manifest {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "corpus", "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	m := &api.Manifest{}
	if err := json.Unmarshal(raw, m); err != nil {
		t.Fatal(err)
	}
	return m
}

func corpusDoc(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "corpus", "documents", name))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func syncedWorkspace(t *testing.T) (*Workspace, *api.Manifest) {
	t.Helper()
	w, err := Load(filepath.Join(t.TempDir(), "skills"))
	if err != nil {
		t.Fatal(err)
	}
	manifest := corpusManifest(t)
	for _, doc := range manifest.Documents {
		if err := w.SyncDocument(doc, corpusDoc(t, doc.Filename)); err != nil {
			t.Fatal(err)
		}
	}
	return w, manifest
}

func TestSyncWritesDocsBasesAndState(t *testing.T) {
	w, manifest := syncedWorkspace(t)

	for _, doc := range manifest.Documents {
		body, err := os.ReadFile(filepath.Join(w.Dir, doc.Filename))
		if err != nil || !bytes.Equal(body, corpusDoc(t, doc.Filename)) {
			t.Errorf("%s not written verbatim (err %v)", doc.Filename, err)
		}
		if base := w.Base(doc.Slug); !bytes.Equal(base, corpusDoc(t, doc.Filename)) {
			t.Errorf("%s pristine base missing or wrong", doc.Slug)
		}
	}

	// State persisted to disk (not just memory) — reload and classify.
	reloaded, err := state.Load(w.Dir)
	if err != nil {
		t.Fatal(err)
	}
	doc := manifest.Documents[0]
	if got := reloaded.Classify(doc.Slug, corpusDoc(t, doc.Filename), doc.Digest); got != state.Synced {
		t.Errorf("after sync = %q, want synced", got)
	}
}

func TestReconcileAgainstManifest(t *testing.T) {
	w, manifest := syncedWorkspace(t)

	// Edit the JSON source locally → drifted; delete the markdown → missing.
	if err := os.WriteFile(filepath.Join(w.Dir, "estimation-rubric.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(w.Dir, "estimation-rubric.md")); err != nil {
		t.Fatal(err)
	}

	rows, err := w.Reconcile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	bySlug := map[string]DocStatus{}
	for _, row := range rows {
		bySlug[row.Slug] = row
	}
	if got := bySlug["estimation-rubric-source"]; got.Classification != state.Drifted || got.ProposalSlug != "estimation-rubric" {
		t.Errorf("edited source = %+v", got)
	}
	if got := bySlug["estimation-rubric"]; got.Classification != state.Missing || got.ProposalSlug != "" {
		t.Errorf("deleted markdown = %+v", got)
	}
}

// Offline: rows come from last-known state, so a dead network still renders
// an honest list instead of an error or a lie.
func TestReconcileOffline(t *testing.T) {
	w, _ := syncedWorkspace(t)
	if err := os.WriteFile(filepath.Join(w.Dir, "estimation-rubric.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	rows, err := w.Reconcile(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("offline rows = %d", len(rows))
	}
	bySlug := map[string]DocStatus{}
	for _, row := range rows {
		bySlug[row.Slug] = row
	}
	if bySlug["estimation-rubric-source"].Classification != state.Drifted {
		t.Errorf("offline edited doc = %q, want drifted", bySlug["estimation-rubric-source"].Classification)
	}
	if bySlug["estimation-rubric"].Classification != state.Synced {
		t.Errorf("offline untouched doc = %q, want synced", bySlug["estimation-rubric"].Classification)
	}
}

// The state file is written after EVERY document, so a sync killed mid-way
// records exactly the documents that landed.
func TestPerDocumentStatePersistence(t *testing.T) {
	w, err := Load(filepath.Join(t.TempDir(), "skills"))
	if err != nil {
		t.Fatal(err)
	}
	manifest := corpusManifest(t)
	first := manifest.Documents[0]
	if err := w.SyncDocument(first, corpusDoc(t, first.Filename)); err != nil {
		t.Fatal(err)
	}

	reloaded, err := state.Load(w.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Document(first.Slug) == nil {
		t.Error("first document not persisted before the second synced")
	}
	if reloaded.Document(manifest.Documents[1].Slug) != nil {
		t.Error("unsynced document must not be recorded")
	}
}

func TestAtomicWriteLeavesNoTempFiles(t *testing.T) {
	w, _ := syncedWorkspace(t)
	entries, err := os.ReadDir(w.Dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if len(entry.Name()) > 14 && entry.Name()[:14] == ".fulcrum-write" {
			t.Errorf("stray temp file %s", entry.Name())
		}
	}
}
