package agenthook_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/headwayio/fulcrum-cli/internal/agenthook"
)

func TestWatermarkRoundTrips(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)

	state := agenthook.LoadState(dir)
	state.Record("session-a", "FUL-17", 12, now)
	if err := state.Save(dir, now); err != nil {
		t.Fatalf("save: %v", err)
	}

	if got := agenthook.LoadState(dir).PostedThrough("session-a"); got != 12 {
		t.Errorf("watermark did not survive: got %d, want 12", got)
	}
}

// A transcript read while it is being written can come up short. Rewinding
// would re-send turns the server already has.
func TestWatermarkNeverGoesBackwards(t *testing.T) {
	now := time.Now()
	state := agenthook.LoadState(t.TempDir())

	state.Record("session-a", "FUL-17", 12, now)
	state.Record("session-a", "FUL-17", 4, now)

	if got := state.PostedThrough("session-a"); got != 12 {
		t.Errorf("watermark rewound: got %d, want 12", got)
	}
}

func TestAnUnknownSessionHasPostedNothing(t *testing.T) {
	if got := agenthook.LoadState(t.TempDir()).PostedThrough("never-seen"); got != 0 {
		t.Errorf("got %d, want 0", got)
	}
}

// Losing the watermark must cost a re-send and nothing else — the server
// dedupes — so corruption is treated as empty rather than fatal.
func TestCorruptStateIsTreatedAsEmpty(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, agenthook.StateFile), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	state := agenthook.LoadState(dir)
	if got := state.PostedThrough("session-a"); got != 0 {
		t.Errorf("got %d, want 0", got)
	}
	// And it must still be writable afterwards.
	state.Record("session-a", "FUL-17", 3, time.Now())
	if err := state.Save(dir, time.Now()); err != nil {
		t.Fatalf("save over corrupt state: %v", err)
	}
}

func TestQuietSessionsArePruned(t *testing.T) {
	dir := t.TempDir()
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC)

	state := agenthook.LoadState(dir)
	state.Record("ancient", "FUL-1", 5, old)
	state.Record("current", "FUL-2", 5, now)
	if err := state.Save(dir, now); err != nil {
		t.Fatal(err)
	}

	reloaded := agenthook.LoadState(dir)
	if got := reloaded.PostedThrough("ancient"); got != 0 {
		t.Errorf("a session quiet for months was kept: %d", got)
	}
	if got := reloaded.PostedThrough("current"); got != 5 {
		t.Errorf("an active session was pruned: %d", got)
	}
}

// The watermark records posting by THIS machine and must not turn up in a
// teammate's diff, so it is written 0600 in the config dir.
func TestStateIsWrittenPrivately(t *testing.T) {
	dir := t.TempDir()
	state := agenthook.LoadState(dir)
	state.Record("session-a", "FUL-17", 1, time.Now())
	if err := state.Save(dir, time.Now()); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(filepath.Join(dir, agenthook.StateFile))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("state file mode is %o, want 600", perm)
	}
}
