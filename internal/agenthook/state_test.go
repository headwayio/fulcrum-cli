package agenthook_test

import (
	"os"
	"path/filepath"
	"strings"
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

func TestSessionPinRoundTripsAndPrunes(t *testing.T) {
	dir := t.TempDir()
	pin := agenthook.SessionPin{Feature: "FUL-17", FeatureID: 1994, ProjectID: 24, Role: "Development", UpdatedAt: time.Now()}
	if err := agenthook.WriteSessionPin(dir, "01a1/odd ref", pin); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := agenthook.ReadSessionPin(dir, "01a1/odd ref")
	if got == nil || got.FeatureID != 1994 || got.ProjectID != 24 || got.Role != "Development" {
		t.Fatalf("pin did not survive: %+v", got)
	}
	if agenthook.ReadSessionPin(dir, "nobody") != nil {
		t.Error("an unknown session must have no pin")
	}

	// A pin missing an id is useless to the hook and must read as absent.
	path := filepath.Join(dir, agenthook.PinsDir, "broken.json")
	if err := os.WriteFile(path, []byte(`{"feature":"FUL-9","feature_id":0,"project_id":24}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if agenthook.ReadSessionPin(dir, "broken") != nil {
		t.Error("a pin without ids was returned")
	}

	agenthook.PruneSessionPins(dir, time.Now().Add(31*24*time.Hour))
	if agenthook.ReadSessionPin(dir, "01a1/odd ref") != nil {
		t.Error("a month-old pin was kept")
	}
}

// The window survives the round trip to the nanosecond, and a pin written
// by a bridge that predates windows reads back with both sides open.
func TestSessionPinWindowRoundTrips(t *testing.T) {
	dir := t.TempDir()
	started := time.Date(2026, 10, 14, 9, 25, 10, 123456789, time.UTC)
	finished := started.Add(4 * time.Minute)
	pin := agenthook.SessionPin{Feature: "FUL-17", FeatureID: 1994, ProjectID: 24, StartedAt: started, FinishedAt: finished, UpdatedAt: finished}
	if err := agenthook.WriteSessionPin(dir, "sess", pin); err != nil {
		t.Fatal(err)
	}
	got := agenthook.ReadSessionPin(dir, "sess")
	if got == nil || !got.StartedAt.Equal(started) || !got.FinishedAt.Equal(finished) {
		t.Fatalf("window did not survive: %+v", got)
	}

	// An open window writes no finished_at at all, rather than a zero time.
	pin.FinishedAt = time.Time{}
	if err := agenthook.WriteSessionPin(dir, "open", pin); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, agenthook.PinsDir, "open.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "finished_at") || !strings.Contains(string(raw), "started_at") {
		t.Errorf("open window serialised wrongly: %s", raw)
	}

	old := `{"feature":"FUL-9","feature_id":7,"project_id":24,"updated_at":"2026-10-09T12:00:00Z"}`
	if err := os.WriteFile(filepath.Join(dir, agenthook.PinsDir, "old.json"), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	legacy := agenthook.ReadSessionPin(dir, "old")
	if legacy == nil || legacy.Windowed() {
		t.Fatalf("an old pin must read as unwindowed: %+v", legacy)
	}
	if !legacy.Covers(agenthook.Turn{StartedAt: started.Add(-time.Hour), EndedAt: started.Add(-time.Hour)}) {
		t.Error("an old pin must cover every turn")
	}
}

// Overlap, not containment: the turn that called start_work began before the
// pin and the turn that called finish_work ends after it closed.
func TestSessionPinCoversTurnsThatOverlapItsWindow(t *testing.T) {
	start := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	pin := agenthook.SessionPin{StartedAt: start, FinishedAt: start.Add(10 * time.Minute)}
	turn := func(from, to time.Duration) agenthook.Turn {
		return agenthook.Turn{StartedAt: start.Add(from), EndedAt: start.Add(to)}
	}
	for name, c := range map[string]struct {
		turn agenthook.Turn
		want bool
	}{
		"before the pin":        {turn(-5*time.Minute, -time.Minute), false},
		"calls start_work":      {turn(-time.Minute, time.Minute), true},
		"ends as the pin opens": {turn(-time.Minute, 0), true},
		"inside":                {turn(2*time.Minute, 3*time.Minute), true},
		"calls finish_work":     {turn(9*time.Minute, 11*time.Minute), true},
		"starts as it closes":   {turn(10*time.Minute, 11*time.Minute), true},
		"after finish_work":     {turn(11*time.Minute, 12*time.Minute), false},
	} {
		if got := pin.Covers(c.turn); got != c.want {
			t.Errorf("%s: Covers = %v, want %v", name, got, c.want)
		}
	}
}

// finish_work closes the window only when it names the pinned card — by the
// reference start_work was given, in any case, or by numeric id.
func TestFinishSessionPinClosesOnlyAMatchingPin(t *testing.T) {
	dir := t.TempDir()
	started := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	pin := agenthook.SessionPin{Feature: "FUL-17", FeatureID: 1994, ProjectID: 24, StartedAt: started, UpdatedAt: started}
	if err := agenthook.WriteSessionPin(dir, "sess", pin); err != nil {
		t.Fatal(err)
	}

	closed, err := agenthook.FinishSessionPin(dir, "sess", "FUL-18", started.Add(time.Minute))
	if err != nil || closed || !agenthook.ReadSessionPin(dir, "sess").FinishedAt.IsZero() {
		t.Fatalf("a finish_work for another card closed the pin (closed=%v, err=%v)", closed, err)
	}
	if closed, _ := agenthook.FinishSessionPin(dir, "nobody", "FUL-17", started); closed {
		t.Error("closed a pin that does not exist")
	}

	for _, reference := range []string{"ful-17", "1994"} {
		finished := started.Add(5 * time.Minute)
		closed, err := agenthook.FinishSessionPin(dir, "sess", reference, finished)
		got := agenthook.ReadSessionPin(dir, "sess")
		if err != nil || !closed || !got.FinishedAt.Equal(finished) || !got.StartedAt.Equal(started) {
			t.Errorf("%s: did not close the window: closed=%v err=%v pin=%+v", reference, closed, err, got)
		}
	}
}

func TestSessionRefKeepsSubagentsApartFromTheirParent(t *testing.T) {
	if got := agenthook.SessionRef("sess-1", ""); got != "sess-1" {
		t.Errorf("main session: %q", got)
	}
	if got := agenthook.SessionRef("sess-1", "agent-9"); got != "sess-1/agent-9" {
		t.Errorf("subagent: %q", got)
	}
	if got := agenthook.SessionRef("", "agent-9"); got != "" {
		t.Errorf("an agent without a session is nothing to key on: %q", got)
	}
}

func TestInjectSessionRefOnlyFillsABlankOnTheWorkTools(t *testing.T) {
	amended, ok := agenthook.InjectSessionRef("mcp__fulcrum__start_work", map[string]any{"feature": "FUL-17"}, "sess-1")
	if !ok || amended["session_ref"] != "sess-1" || amended["feature"] != "FUL-17" {
		t.Errorf("start_work not amended: %v %v", ok, amended)
	}
	if _, ok := agenthook.InjectSessionRef("finish_work", map[string]any{}, "sess-1"); !ok {
		t.Error("the bare tool name must be accepted too")
	}
	if _, ok := agenthook.InjectSessionRef("mcp__fulcrum__start_work", map[string]any{"session_ref": "chosen"}, "sess-1"); ok {
		t.Error("a session the model named was overridden")
	}
	if _, ok := agenthook.InjectSessionRef("mcp__fulcrum__get_feature", map[string]any{}, "sess-1"); ok {
		t.Error("a read-only tool was amended")
	}
	if _, ok := agenthook.InjectSessionRef("mcp__fulcrum__start_work", nil, "sess-1"); !ok {
		t.Error("a call with no input at all should still be named")
	}
}
