package tui

import (
	"strings"
	"testing"
)

// A caret sitting after the placeholder reads as an answered field. It
// belongs where the first character will land.
func TestNoteFieldPutsTheCaretWhereTypingStarts(t *testing.T) {
	deps := &fakeDeps{
		configured: true, serverURL: "http://srv", snapshot: allStatesSnapshot(),
		localDocs: map[string][]byte{"doc-drifted": []byte("{\"a\": 2}\n")},
		baseDocs:  map[string][]byte{"doc-drifted": []byte("{\"a\": 1}\n")},
	}
	h := newTestModel(t, deps)
	waitContains(t, h, "drifted.json")
	h.Type("j")
	h.Type("p")
	waitContains(t, h, "Note to the reviewer")

	view := finalView(t, h)
	if view.Cursor == nil {
		t.Fatal("no terminal cursor: an empty required field has to show where to type")
	}
	// The real cursor blinks; a drawn one cannot. It must also be gone from
	// the content, or the sentinel would print as a stray NUL.
	if strings.Contains(view.Content, caretMark) {
		t.Error("the caret sentinel leaked into the rendered frame")
	}

	line := lineAt(t, view.Content, view.Cursor.Y)
	if !strings.Contains(stripANSI(line), "› what changed, and why") {
		t.Errorf("cursor is on line %d, which is not the note field: %q", view.Cursor.Y, stripANSI(line))
	}
	// Column 2: just past the "› " prompt, on the placeholder's first letter.
	if view.Cursor.X != 2 {
		t.Errorf("cursor at column %d, want 2 — the start of the field", view.Cursor.X)
	}
}

func TestNoteFieldCaretFollowsWhatYouType(t *testing.T) {
	deps := &fakeDeps{
		configured: true, serverURL: "http://srv", snapshot: allStatesSnapshot(),
		localDocs: map[string][]byte{"doc-drifted": []byte("{\"a\": 2}\n")},
		baseDocs:  map[string][]byte{"doc-drifted": []byte("{\"a\": 1}\n")},
	}
	h := newTestModel(t, deps)
	waitContains(t, h, "drifted.json")
	h.Type("j")
	h.Type("p")
	waitContains(t, h, "Note to the reviewer")
	h.Type("widened")
	waitContains(t, h, "widened")

	view := finalView(t, h)
	if view.Cursor == nil {
		t.Fatal("cursor vanished once the field had a value")
	}
	if want := 2 + len("widened"); view.Cursor.X != want {
		t.Errorf("cursor at column %d, want %d — just past the typed text", view.Cursor.X, want)
	}
}
