package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// brewShape lays out what a package manager actually installs: the real binary
// in a VERSIONED directory, and a stable name on PATH pointing at it.
func brewShape(t *testing.T) (stable, versioned string) {
	t.Helper()

	root := t.TempDir()
	cellar := filepath.Join(root, "Cellar", "fulcrum", "0.1.0", "bin")
	bin := filepath.Join(root, "bin")
	for _, dir := range []string{cellar, bin} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	versioned = filepath.Join(cellar, "fulcrum")
	if err := os.WriteFile(versioned, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	stable = filepath.Join(bin, "fulcrum")
	if err := os.Symlink(versioned, stable); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PATH", bin)
	return stable, versioned
}

// THE REGRESSION. Resolving the symlink bakes a version into every .mcp.json
// and every hook command, and the next upgrade deletes the directory they
// name — silently, because a missing MCP server just does not appear and the
// Stop hook only fails once a turn ends.
func TestPrefersTheStableNameOverTheVersionedTarget(t *testing.T) {
	stable, versioned := brewShape(t)

	got := stablePathFor(versioned)

	if got == versioned {
		t.Fatalf("wrote the versioned path %q — an upgrade deletes that directory", got)
	}
	if got != stable {
		t.Errorf("got %q, want the stable name %q", got, stable)
	}
}

// Called through the stable name, there is nothing to improve on.
func TestKeepsTheStableNameItWasCalledBy(t *testing.T) {
	stable, _ := brewShape(t)

	if got := stablePathFor(stable); got != stable {
		t.Errorf("got %q, want %q", got, stable)
	}
}

// A `go build` in a worktree is not on PATH, and its own path is the only
// honest answer.
func TestFallsBackToItsOwnPathWhenNotOnPath(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "fulcrum")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())

	if got := stablePathFor(binary); got != binary {
		t.Errorf("got %q, want %q", got, binary)
	}
}

// The comparison is by inode, not by name, so an OLDER fulcrum earlier in PATH
// cannot claim the entry and quietly become the binary every harness runs.
func TestIgnoresADifferentBinaryOfTheSameName(t *testing.T) {
	elsewhere := t.TempDir()
	impostor := filepath.Join(elsewhere, "fulcrum")
	if err := os.WriteFile(impostor, []byte("#!/bin/sh\necho other\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	binary := filepath.Join(dir, "fulcrum")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", elsewhere)

	got := stablePathFor(binary)
	if got == impostor {
		t.Fatalf("adopted a different binary that merely shares the name: %q", got)
	}
	if got != binary {
		t.Errorf("got %q, want %q", got, binary)
	}
}

// A curl installer drops a real file straight into a stable directory. There
// is no symlink, and the answer is the same either way — worth pinning because
// it is the shape Linux installs take.
func TestHandlesARealFileOnPath(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "fulcrum")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)

	if got := stablePathFor(binary); got != binary {
		t.Errorf("got %q, want %q", got, binary)
	}
}

// Whatever it returns must be absolute: a harness inherits neither our PATH
// nor our working directory.
func TestAlwaysReturnsAnAbsolutePath(t *testing.T) {
	stable, versioned := brewShape(t)

	for _, input := range []string{stable, versioned} {
		if got := stablePathFor(input); !filepath.IsAbs(got) {
			t.Errorf("stablePathFor(%q) = %q, which is not absolute", input, got)
		}
	}
}
