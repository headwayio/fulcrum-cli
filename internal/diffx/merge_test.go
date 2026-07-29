package diffx

import (
	"strings"
	"testing"
)

const baseDoc = "---\nname: skill\n---\n\n# Skill\n\nAlpha line.\nBeta line.\nGamma line.\n"

func TestMergeTakesEachSidesOwnChanges(t *testing.T) {
	ours := strings.Replace(baseDoc, "Alpha line.", "Alpha line, edited locally.", 1)
	theirs := strings.Replace(baseDoc, "Gamma line.", "Gamma line, edited on the server.", 1)

	merged, conflicts := Merge3([]byte(baseDoc), []byte(ours), []byte(theirs))
	if conflicts != 0 {
		t.Fatalf("disjoint edits must merge cleanly, got %d conflict(s):\n%s", conflicts, merged)
	}
	if !strings.Contains(string(merged), "Alpha line, edited locally.") {
		t.Error("lost our edit")
	}
	if !strings.Contains(string(merged), "Gamma line, edited on the server.") {
		t.Error("lost their edit")
	}
	if HasConflictMarkers(merged) {
		t.Error("clean merge must not carry markers")
	}
}

func TestMergeCollapsesIdenticalEdits(t *testing.T) {
	same := strings.Replace(baseDoc, "Beta line.", "Beta line, sharpened.", 1)

	merged, conflicts := Merge3([]byte(baseDoc), []byte(same), []byte(same))
	if conflicts != 0 {
		t.Fatalf("the same edit on both sides is not a conflict, got %d", conflicts)
	}
	if strings.Count(string(merged), "Beta line, sharpened.") != 1 {
		t.Errorf("the shared edit must appear once:\n%s", merged)
	}
}

func TestMergeMarksOverlappingEdits(t *testing.T) {
	ours := strings.Replace(baseDoc, "Beta line.", "Beta line, my wording.", 1)
	theirs := strings.Replace(baseDoc, "Beta line.", "Beta line, their wording.", 1)

	merged, conflicts := Merge3([]byte(baseDoc), []byte(ours), []byte(theirs))
	if conflicts != 1 {
		t.Fatalf("overlapping edits = %d conflict(s), want 1:\n%s", conflicts, merged)
	}
	text := string(merged)
	if !strings.Contains(text, MarkerOurs) || !strings.Contains(text, MarkerTheirs) {
		t.Errorf("missing markers:\n%s", text)
	}
	// Both versions survive for the human to choose between.
	if !strings.Contains(text, "my wording.") || !strings.Contains(text, "their wording.") {
		t.Errorf("a conflict must keep both sides:\n%s", text)
	}
	// Untouched context is not swallowed by the conflict region.
	if !strings.Contains(text, "Alpha line.") || !strings.Contains(text, "Gamma line.") {
		t.Errorf("context lost:\n%s", text)
	}
	if !HasConflictMarkers(merged) {
		t.Error("HasConflictMarkers must see its own output")
	}
}

func TestMergeHandlesAppendsAtTheEnd(t *testing.T) {
	ours := baseDoc + "Ours at the end.\n"
	theirs := baseDoc + "Theirs at the end.\n"

	merged, conflicts := Merge3([]byte(baseDoc), []byte(ours), []byte(theirs))
	if conflicts != 1 {
		t.Fatalf("competing appends = %d conflict(s), want 1:\n%s", conflicts, merged)
	}

	// One-sided append is clean.
	merged, conflicts = Merge3([]byte(baseDoc), []byte(ours), []byte(baseDoc))
	if conflicts != 0 || !strings.Contains(string(merged), "Ours at the end.") {
		t.Errorf("one-sided append = %d conflict(s):\n%s", conflicts, merged)
	}
}

func TestMergeHandlesDeletionsSeparatedByContext(t *testing.T) {
	// A deletion and an edit with an untouched line between them: the
	// stable line splits the regions, so both changes apply.
	ours := strings.Replace(baseDoc, "Alpha line.\n", "", 1)
	theirs := strings.Replace(baseDoc, "Gamma line.", "Gamma line, reworded.", 1)

	merged, conflicts := Merge3([]byte(baseDoc), []byte(ours), []byte(theirs))
	if conflicts != 0 {
		t.Fatalf("delete here, edit there = %d conflict(s):\n%s", conflicts, merged)
	}
	if strings.Contains(string(merged), "Alpha line.") {
		t.Errorf("our deletion was undone:\n%s", merged)
	}
	if !strings.Contains(string(merged), "Gamma line, reworded.") {
		t.Errorf("their edit was lost:\n%s", merged)
	}
}

// Adjacent changes with no common line between them fall in one region and
// conflict — verified to be byte-for-byte what `git merge-file` produces for
// the same three inputs, so the behavior is not a surprise to anyone who has
// merged before.
func TestMergeConflictsOnAdjacentChangesLikeGit(t *testing.T) {
	ours := strings.Replace(baseDoc, "Beta line.\n", "", 1)
	theirs := strings.Replace(baseDoc, "Gamma line.", "Gamma line, reworded.", 1)

	merged, conflicts := Merge3([]byte(baseDoc), []byte(ours), []byte(theirs))
	if conflicts != 1 {
		t.Fatalf("adjacent delete/edit = %d conflict(s), want 1:\n%s", conflicts, merged)
	}
	body := strings.SplitN(string(merged), MarkerOurs+"\n", 2)[1]
	if body != "Gamma line.\n"+MarkerSplit+"\nBeta line.\nGamma line, reworded.\n"+MarkerTheirs+"\n" {
		t.Errorf("conflict region differs from git's:\n%q", body)
	}
}

func TestMergeIsIdentityWhenNothingMoved(t *testing.T) {
	merged, conflicts := Merge3([]byte(baseDoc), []byte(baseDoc), []byte(baseDoc))
	if conflicts != 0 || string(merged) != baseDoc {
		t.Errorf("identical inputs must round-trip byte-exact (%d conflicts):\n%q", conflicts, merged)
	}
}

func TestHasConflictMarkersIgnoresMarkdownUnderlines(t *testing.T) {
	// A setext heading underline is not a merge marker.
	if HasConflictMarkers([]byte("Title\n=======\n\nBody.\n")) {
		t.Error("markdown underline must not read as a conflict")
	}
}
