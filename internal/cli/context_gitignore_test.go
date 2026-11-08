package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A developer who wrote the root-anchored form by hand must not be handed a
// second, unanchored copy on every `fulcrum work`.
func TestEnsureGitignoreAcceptsEveryFormThatAlreadyIgnoresTheDirectory(t *testing.T) {
	for _, existing := range []string{"/.fulcrum/\n", "/.fulcrum\n", ".fulcrum/\n", ".fulcrum\n", "node_modules/\n  /.fulcrum/  \n"} {
		dir := t.TempDir()
		path := filepath.Join(dir, ".gitignore")
		if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
			t.Fatal(err)
		}
		added, err := ensureGitignore(dir)
		if err != nil {
			t.Fatalf("%q: %v", existing, err)
		}
		if added {
			t.Errorf("%q already ignores .fulcrum but an entry was appended", existing)
		}
		if content, _ := os.ReadFile(path); string(content) != existing {
			t.Errorf("%q was rewritten to %q", existing, content)
		}
	}
}

func TestEnsureGitignoreAddsTheEntryWhenNothingIgnoresTheDirectory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".gitignore")
	if err := os.WriteFile(path, []byte("/.fulcrum-old/\nfulcrum\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	added, err := ensureGitignore(dir)
	if err != nil || !added {
		t.Fatalf("entry not added: added=%v err=%v", added, err)
	}
	content, _ := os.ReadFile(path)
	if !strings.HasSuffix(string(content), "\n.fulcrum/\n") {
		t.Errorf("unexpected content: %q", content)
	}
}

func TestKnownHarnessesIncludesOmp(t *testing.T) {
	if got := knownHarnesses(); got != "claude, codex, kimi, omp" {
		t.Errorf("known harnesses: %q", got)
	}
}
