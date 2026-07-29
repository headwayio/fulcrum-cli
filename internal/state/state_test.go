package state

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// The seven-row matrix ported from spec/lib/fulcrum_skills_spec.rb — the Ruby
// reference client's behavioral pin, including proposed-beats-conflicted
// precedence and the missing-requires-prior-sync rule.
func TestClassifyMatrix(t *testing.T) {
	s := New()

	if got := s.Classify("x", []byte("a"), "d1"); got != Unsynced {
		t.Fatalf("never-synced doc = %q, want unsynced", got)
	}

	s.RecordSync("x", "x.json", "d1", []byte("a"))

	cases := []struct {
		name   string
		local  []byte
		remote string
		want   Classification
	}{
		{"both match last sync", []byte("a"), "d1", Synced},
		{"local edits, remote unchanged", []byte("edited"), "d1", Drifted},
		{"local untouched, remote moved", []byte("a"), "d2", Behind},
		{"both moved", []byte("edited"), "d2", Conflicted},
		{"local file deleted", nil, "d1", Missing},
	}
	for _, tc := range cases {
		if got := s.Classify("x", tc.local, tc.remote); got != tc.want {
			t.Errorf("%s = %q, want %q", tc.name, got, tc.want)
		}
	}

	// Proposal dedupe is by exact (slug, file_sha256): the submitted bytes
	// classify as proposed and BEAT conflicted even when remote moved.
	s.RecordProposal("x", 7, HexSHA256([]byte("edited")))
	if got := s.Classify("x", []byte("edited"), "d1"); got != Proposed {
		t.Errorf("submitted edits = %q, want proposed", got)
	}
	if got := s.Classify("x", []byte("edited"), "d2"); got != Proposed {
		t.Errorf("submitted edits with remote moved = %q, want proposed (precedence)", got)
	}
	// Different local bytes than the proposal → back to the ordinary chain.
	if got := s.Classify("x", []byte("edited again"), "d1"); got != Drifted {
		t.Errorf("re-edited past the proposal = %q, want drifted", got)
	}

	// missing requires a prior sync record: an unknown slug with nil body is
	// unsynced, not missing.
	if got := s.Classify("never-seen", nil, "d1"); got != Unsynced {
		t.Errorf("unknown slug with nil body = %q, want unsynced", got)
	}
}

// The vendored golden was produced by the actual Ruby client under the
// corpus's frozen clock. Loading and re-encoding it must reproduce the bytes
// exactly — the two clients share workspace dirs, so the file format is a
// compatibility contract, not an implementation detail.
func TestRubyGoldenRoundTrip(t *testing.T) {
	golden, err := os.ReadFile(filepath.Join("..", "..", "corpus", "state", "fulcrum-sync.json"))
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, StateFile), golden, 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(dir)
	if err != nil {
		t.Fatalf("load golden: %v", err)
	}

	if doc := loaded.Document("estimation-rubric-source"); doc == nil || doc.Filename != "estimation-rubric.json" {
		t.Fatalf("golden decode lost the source document: %+v", doc)
	}
	if !bytes.Equal(loaded.Encode(), golden) {
		t.Errorf("round-trip is not byte-exact:\ngot:\n%s\nwant:\n%s", loaded.Encode(), golden)
	}
}

// Files this client creates carry the additive version field; the Ruby
// loader tolerates unknown keys, so this never breaks sharing.
func TestNewStateCarriesVersion(t *testing.T) {
	dir := t.TempDir()
	s := New()
	s.RecordSync("x", "x.json", "d1", []byte("a"))
	if err := s.Save(dir); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(filepath.Join(dir, StateFile))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte(`"version": 1`)) {
		t.Errorf("new state file lacks version stamp:\n%s", raw)
	}
	if raw[len(raw)-1] != '\n' {
		t.Error("state file must end with a newline like the Ruby writer")
	}

	reloaded, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.FileVersion != Version {
		t.Errorf("reloaded version = %d, want %d", reloaded.FileVersion, Version)
	}
}

// Reconciling a proposal to a terminal status releases the proposed pin —
// the doc falls back to the ordinary chain (drifted/conflicted/behind).
func TestResolvedProposalStopsPinning(t *testing.T) {
	s := New()
	s.RecordSync("x", "x.json", "d1", []byte("a"))
	s.RecordProposal("x", 7, HexSHA256([]byte("edited")))

	if got := s.Classify("x", []byte("edited"), "d1"); got != Proposed {
		t.Fatalf("pending proposal = %q, want proposed", got)
	}

	if !s.AnnotateProposal(7, "applied") {
		t.Fatal("annotation must report the change")
	}
	if s.AnnotateProposal(7, "applied") {
		t.Error("re-annotating the same status must be a no-op")
	}
	if got := s.Classify("x", []byte("edited"), "d1"); got != Drifted {
		t.Errorf("applied proposal, remote unchanged = %q, want drifted", got)
	}
	if got := s.Classify("x", []byte("edited"), "d2"); got != Conflicted {
		t.Errorf("applied proposal, remote moved = %q, want conflicted", got)
	}

	resolved := s.ResolvedProposalFor("x", HexSHA256([]byte("edited")))
	if resolved == nil || resolved.Status != "applied" {
		t.Errorf("resolved lookup = %+v", resolved)
	}
	if s.ResolvedProposalFor("x", HexSHA256([]byte("other"))) != nil {
		t.Error("resolved lookup must match exact bytes")
	}
}

// A missing state file is an empty state, matching the Ruby loader.
func TestLoadMissingFile(t *testing.T) {
	s, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Classify("x", []byte("a"), "d1"); got != Unsynced {
		t.Errorf("empty state classify = %q, want unsynced", got)
	}
}
