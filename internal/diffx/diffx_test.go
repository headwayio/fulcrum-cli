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
