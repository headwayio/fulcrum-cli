package diffx

import (
	"strings"
	"testing"
)

func TestUnifiedAndColorize(t *testing.T) {
	unified := Unified("x.json", []byte("{\"a\": 1}\n"), []byte("{\"a\": 2}\n"))
	if !strings.Contains(unified, "-{\"a\": 1}") || !strings.Contains(unified, "+{\"a\": 2}") {
		t.Fatalf("unified = %q", unified)
	}
	if Colorize(unified) == "" || ColorizeIntraline(unified) == "" {
		t.Fatal("colorizers must not eat the diff")
	}
	if Colorize("") != "" {
		t.Fatal("empty diff stays empty")
	}
}

// Intraline highlighting keeps every original character — styling only.
func TestIntralinePreservesText(t *testing.T) {
	unified := Unified("d.md", []byte("the quick brown fox\n"), []byte("the quick red fox\n"))
	styled := ColorizeIntraline(unified)
	plain := stripANSI(styled)
	if !strings.Contains(plain, "-the quick brown fox") {
		t.Errorf("old line mangled:\n%s", plain)
	}
	if !strings.Contains(plain, "+the quick red fox") {
		t.Errorf("new line mangled:\n%s", plain)
	}
}

func TestWordDiffMarksOnlyChangedWords(t *testing.T) {
	keepA, keepB := lcsKeep(tokenize("the quick brown fox"), tokenize("the quick red fox"))
	// tokens: [the][ ][quick][ ][brown][ ][fox] — only "brown"/"red" change.
	wantKeepA := []bool{true, true, true, true, false, true, true}
	for i, want := range wantKeepA {
		if keepA[i] != want {
			t.Errorf("keepA[%d] = %v, want %v", i, keepA[i], want)
		}
	}
	if keepB[4] {
		t.Error("replacement word must be marked changed")
	}
}

func TestJSONStructural(t *testing.T) {
	before := []byte(`{"status":"draft","components":[{"name":"auth","hours":4}],"gone":true}`)
	after := []byte(`{"status":"final","components":[{"name":"auth","hours":6},{"name":"billing"}],"fresh":1}`)

	changes, err := JSONStructural(before, after)
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]JSONChange{}
	for _, c := range changes {
		byPath[c.Path] = c
	}

	if c := byPath["status"]; c.Kind != "changed" || c.Old != `"draft"` || c.New != `"final"` {
		t.Errorf("status = %+v", c)
	}
	if c := byPath["components[0].hours"]; c.Kind != "changed" || c.New != "6" {
		t.Errorf("hours = %+v", c)
	}
	if c := byPath["components[1]"]; c.Kind != "added" {
		t.Errorf("new component = %+v", c)
	}
	if c := byPath["gone"]; c.Kind != "removed" {
		t.Errorf("gone = %+v", c)
	}
	if c := byPath["fresh"]; c.Kind != "added" {
		t.Errorf("fresh = %+v", c)
	}

	if _, err := JSONStructural([]byte("{ nope"), after); err == nil {
		t.Error("unparseable input must error so callers fall back to text")
	}
}

func TestConflictPaths(t *testing.T) {
	ours := []JSONChange{{Path: "status"}, {Path: "a.b"}}
	theirs := []JSONChange{{Path: "a.b"}, {Path: "other"}}
	overlap := ConflictPaths(ours, theirs)
	if len(overlap) != 1 || overlap[0] != "a.b" {
		t.Errorf("overlap = %v", overlap)
	}
}

func stripANSI(s string) string {
	var b strings.Builder
	inEscape := false
	for _, r := range s {
		switch {
		case inEscape:
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
				inEscape = false
			}
		case r == 0x1b:
			inEscape = true
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// highlighted reports the words reversed out on the first line of `styled`
// whose plain text contains `needle`.
func highlighted(t *testing.T, styled, needle string) []string {
	t.Helper()
	for _, line := range strings.Split(styled, "\n") {
		if !strings.Contains(stripANSI(line), needle) {
			continue
		}
		var marked []string
		// Reverse video is the emphasis; "7" is its SGR parameter.
		for _, chunk := range strings.Split(line, "\x1b[")[1:] {
			code, text, ok := strings.Cut(chunk, "m")
			if ok && strings.Contains(code, "7") && text != "" {
				marked = append(marked, text)
			}
		}
		return marked
	}
	t.Fatalf("no line containing %q in:\n%s", needle, stripANSI(styled))
	return nil
}

// Runs are routinely lopsided — one line rewritten while more are appended.
// Requiring equal counts gave up on exactly the edit worth highlighting.
func TestWordHighlightSurvivesALopsidedRun(t *testing.T) {
	old := "the steps to follow, and the traps to avoid.\n"
	new := "the steps to follow, and the traps to super avoid.\n\nA new closing line\n"
	styled := ColorizeIntraline(Unified("skill.md", []byte(old), []byte(new)))

	if got := highlighted(t, styled, "super avoid"); len(got) != 1 || got[0] != "super" {
		t.Errorf("marked %q, want just [super]", got)
	}
	// The appended lines pair with nothing, so nothing on them is marked.
	if got := highlighted(t, styled, "A new closing line"); len(got) != 0 {
		t.Errorf("an unrelated added line should carry no word marks, got %q", got)
	}
}

// A rewrite pairs with its own line, not with whichever happens to sit at
// the same index — and never with a line it has nothing in common with.
func TestWordHighlightPairsByContentNotPosition(t *testing.T) {
	old := "alpha beta gamma\ncompletely unrelated sentence here\n"
	new := "something else entirely different\nalpha beta DELTA\n"
	styled := ColorizeIntraline(Unified("d.md", []byte(old), []byte(new)))

	// "alpha beta gamma" → "alpha beta DELTA" despite being second in the
	// added run; the truly unrelated pair stays unmarked.
	if got := highlighted(t, styled, "alpha beta DELTA"); len(got) != 1 || got[0] != "DELTA" {
		t.Errorf("marked %q, want just [DELTA]", got)
	}
	if got := highlighted(t, styled, "something else entirely"); len(got) != 0 {
		t.Errorf("unrelated lines must not be word-diffed against each other, got %q", got)
	}
}

// Two lines sharing only their spaces have nothing in common: pairing them
// paints almost every word and hides where the real edit is.
func TestWordHighlightSkipsUnrelatedRewrites(t *testing.T) {
	old := "one two three four\n"
	new := "quite different words here\n"
	styled := ColorizeIntraline(Unified("d.md", []byte(old), []byte(new)))

	if got := highlighted(t, styled, "quite different"); len(got) != 0 {
		t.Errorf("marked %q, want nothing — the lines are unrelated", got)
	}
	if sim := similarity("one two three four", "quite different words here"); sim >= pairThreshold {
		t.Errorf("similarity %.2f should sit below the %.2f bar", sim, pairThreshold)
	}
}

// Whitespace is never the edit: reversing it smears a block across the gap
// between two words.
func TestWordHighlightNeverMarksWhitespace(t *testing.T) {
	styled := ColorizeIntraline(Unified("d.md",
		[]byte("keep this word\n"), []byte("keep that word\n")))
	for _, marked := range highlighted(t, styled, "keep that word") {
		if strings.TrimSpace(marked) == "" {
			t.Errorf("whitespace was marked as changed in %q", styled)
		}
	}
}

// The gap between two rewritten words belongs to the rewrite; the gap after
// a single edited word does not. One phrase, one highlight — not a row of
// boxes with untouched spaces between them.
func TestWordHighlightKeepsARewrittenPhraseWhole(t *testing.T) {
	styled := ColorizeIntraline(Unified("d.md",
		[]byte("Be kind.\n"), []byte("Be kind and specific.\n")))

	if got := highlighted(t, styled, "+Be kind and specific."); len(got) != 1 ||
		got[0] != "kind and specific." {
		t.Errorf("marked %q, want one whole phrase [kind and specific.]", got)
	}
	// A short line sharing one word out of six still pairs: the common
	// prefix says it is the same line, edited.
	if r := relatedness("Be kind.", "Be kind and specific."); r < pairThreshold {
		t.Errorf("relatedness %.2f is below the %.2f bar; the rewrite would go unmarked",
			r, pairThreshold)
	}
}

func TestWordHighlightLeavesTheGapAfterAnEditedWord(t *testing.T) {
	styled := ColorizeIntraline(Unified("d.md",
		[]byte("Jon says hi\n"), []byte("Jon hi\n")))

	got := highlighted(t, styled, "-Jon says hi")
	if len(got) != 1 || got[0] != "says" {
		t.Errorf("marked %q, want just [says] with its trailing space left alone", got)
	}
}
