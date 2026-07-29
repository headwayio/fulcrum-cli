package tui

import (
	"strings"
	"testing"
)

// Word highlighting was wired up but never pinned at this level, so it
// could stop reaching the screen without a single test noticing.
func TestDiffScreenHighlightsChangedWords(t *testing.T) {
	deps := &fakeDeps{
		configured: true, serverURL: "http://srv", snapshot: draftSnapshot(),
		baseDocs: map[string][]byte{"skill-mine": []byte(
			"# mine\n\nthe steps to follow, and the traps to avoid.\n")},
		// Lopsided on purpose: one line rewritten, two appended — the shape
		// that used to disable highlighting for the whole run.
		localDocs: map[string][]byte{"skill-mine": []byte(
			"# mine\n\nthe steps to follow, and the traps to super avoid.\n\nA closing line\n")},
	}
	h := newTestModel(t, deps)
	waitContains(t, h, "skill-mine.md")
	h.Send(enter())
	waitPlain(t, h, "super avoid") // the highlight sits between the two words

	frame := string(finalFrame(t, h))
	// Reverse video is the emphasis; "7" is its SGR parameter.
	if !strings.Contains(frame, "\x1b[7") {
		t.Fatalf("no word highlighting reached the screen:\n%s", frame)
	}
	for _, line := range strings.Split(frame, "\n") {
		if !strings.Contains(stripANSI(line), "super avoid") {
			continue
		}
		if !strings.Contains(line, "super") || !strings.Contains(line, "\x1b[7") {
			t.Errorf("the rewritten word is not the highlighted one: %q", line)
		}
		return
	}
	t.Error("the rewritten line never appeared")
}
