package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/headwayio/fulcrum-cli/internal/agenthook"
)

func runToolUse(t *testing.T, payload string) (string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	app := &App{Stdout: &stdout, Stderr: &stderr, Stdin: strings.NewReader(payload), Version: "test", Getenv: func(string) string { return "" }}
	if code := app.Main([]string{"hook", "tool-use"}); code != 0 {
		t.Fatalf("a hook must never fail the session: exit %d, stderr %q", code, stderr.String())
	}
	return stdout.String(), stderr.String()
}

// The one job of the PreToolUse hook: name the session on a start_work call
// that did not, in the shape Claude Code applies.
func TestToolUseHookNamesTheSessionOnStartWork(t *testing.T) {
	out, _ := runToolUse(t, `{"session_id":"sess-1","hook_event_name":"PreToolUse","tool_name":"mcp__fulcrum__start_work","tool_input":{"feature":"FUL-17","role":"Development"}}`)

	var answer struct {
		Hook struct {
			Event    string         `json:"hookEventName"`
			Decision string         `json:"permissionDecision"`
			Input    map[string]any `json:"updatedInput"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out), &answer); err != nil {
		t.Fatalf("answer is not the hook contract: %q (%v)", out, err)
	}
	if answer.Hook.Event != "PreToolUse" || answer.Hook.Decision != "allow" {
		t.Errorf("wrong envelope: %+v", answer.Hook)
	}
	if answer.Hook.Input["session_ref"] != "sess-1" || answer.Hook.Input["feature"] != "FUL-17" || answer.Hook.Input["role"] != "Development" {
		t.Errorf("input was not amended in place: %v", answer.Hook.Input)
	}
}

// A subagent's calls carry the parent's session id and its own agent id; the
// ref must name the subagent, or every sibling would be pinned as one.
func TestToolUseHookNamesASubagentByItsAgentID(t *testing.T) {
	out, _ := runToolUse(t, `{"session_id":"sess-1","agent_id":"agent-9","tool_name":"mcp__fulcrum__finish_work","tool_input":{"feature":"FUL-17"}}`)
	if !strings.Contains(out, `"session_ref":"sess-1/agent-9"`) {
		t.Errorf("subagent ref missing: %s", out)
	}
}

func TestToolUseHookIsSilentForEverythingElse(t *testing.T) {
	for name, payload := range map[string]string{
		"other tool":    `{"session_id":"s","tool_name":"Bash","tool_input":{"command":"ls"}}`,
		"named already": `{"session_id":"s","tool_name":"mcp__fulcrum__start_work","tool_input":{"feature":"FUL-17","session_ref":"other"}}`,
		"no session":    `{"tool_name":"mcp__fulcrum__start_work","tool_input":{"feature":"FUL-17"}}`,
		"empty stdin":   ``,
		"not json":      `{not json`,
	} {
		out, _ := runToolUse(t, payload)
		if out != "" {
			t.Errorf("%s: hook answered when it should have stayed silent: %q", name, out)
		}
	}
}

// transcriptStart anchors the synthetic transcripts: turn n is prompted at
// transcriptStart + (n-1) minutes and answered 30 seconds later.
var transcriptStart = time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)

// at is an instant inside turn n, offset from its prompt.
func at(n int, offset time.Duration) time.Time {
	return transcriptStart.Add(time.Duration(n-1)*time.Minute + offset)
}

// writeTranscript writes a Claude Code transcript of `turns` one-response
// turns, in the record shape the harness writes.
func writeTranscript(t *testing.T, path string, turns int) {
	t.Helper()
	var b strings.Builder
	for n := 1; n <= turns; n++ {
		fmt.Fprintf(&b, `{"type":"user","timestamp":%q,"message":{"content":"turn %d"}}`+"\n",
			at(n, 0).Format(time.RFC3339Nano), n)
		fmt.Fprintf(&b, `{"type":"assistant","timestamp":%q,"message":{"id":"msg-%d","model":"claude-opus-5","usage":{"input_tokens":10,"output_tokens":20}}}`+"\n",
			at(n, 30*time.Second).Format(time.RFC3339Nano), n)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

// hookStop is one Stop hook firing against the fixture server.
type hookStop struct {
	srv        *fixtureServer
	configDir  string
	transcript string
	token      string
}

func newHookStop(t *testing.T) *hookStop {
	t.Helper()
	srv := newFixtureServer()
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	return &hookStop{srv: srv, configDir: filepath.Join(dir, "config"), transcript: filepath.Join(dir, "session.jsonl"), token: fixtureToken}
}

const qaSession = "sess-qa"

func (h *hookStop) run(t *testing.T, extra ...string) (string, string) {
	t.Helper()
	env := map[string]string{
		"FULCRUM_URL": h.srv.URL, "FULCRUM_API_TOKEN": h.token,
		"FULCRUM_CONFIG_DIR": h.configDir, "FULCRUM_NO_KEYCHAIN": "1",
	}
	var stdout, stderr bytes.Buffer
	app := &App{Stdout: &stdout, Stderr: &stderr, Stdin: strings.NewReader(""), Version: "test",
		Getenv: func(key string) string { return env[key] }}
	args := append([]string{"hook", "stop", "--transcript", h.transcript, "--session", qaSession, "--cwd", t.TempDir()}, extra...)
	if code := app.Main(args); code != 0 {
		t.Fatalf("a hook must never fail the session: exit %d, stderr %q", code, stderr.String())
	}
	return stdout.String(), stderr.String()
}

func (h *hookStop) pin(t *testing.T, pin agenthook.SessionPin) {
	t.Helper()
	pin.Feature, pin.FeatureID, pin.ProjectID = "FUL-17", 1994, 2
	if err := agenthook.WriteSessionPin(h.configDir, qaSession, pin); err != nil {
		t.Fatal(err)
	}
}

// posted takes the turn indices the server received since the last call.
func (h *hookStop) posted() []int {
	h.srv.mu.Lock()
	defer h.srv.mu.Unlock()
	got := h.srv.telemetry
	h.srv.telemetry = nil
	return got
}

func (h *hookStop) watermark() int {
	return agenthook.LoadState(h.configDir).PostedThrough(qaSession)
}

func span(from, to int) []int {
	var out []int
	for n := from; n <= to; n++ {
		out = append(out, n)
	}
	return out
}

// An orchestrator that opens a QA episode at turn 26 and finishes it at turn
// 30 must put exactly turns 26–30 on the card: not the 25 turns before it,
// not the ten after. The start and finish turns both count, and the turns
// either side are settled in the watermark so no later pin inherits them.
func TestHookStopPostsOnlyTheTurnsInsideTheSessionPinsWindow(t *testing.T) {
	h := newHookStop(t)

	// Turn 26 calls start_work; its Stop fires once it ends.
	h.pin(t, agenthook.SessionPin{StartedAt: at(26, 10*time.Second)})
	writeTranscript(t, h.transcript, 26)
	h.run(t)
	if got := h.posted(); !reflect.DeepEqual(got, []int{26}) {
		t.Fatalf("start turn: posted %v, want [26]", got)
	}
	if got := h.watermark(); got != 26 {
		t.Fatalf("watermark %d after the start turn, want 26", got)
	}

	// Turn 30 calls finish_work; ten more turns follow before the next Stop.
	h.pin(t, agenthook.SessionPin{StartedAt: at(26, 10*time.Second), FinishedAt: at(30, 20*time.Second)})
	writeTranscript(t, h.transcript, 40)
	h.run(t)
	if got := h.posted(); !reflect.DeepEqual(got, span(27, 30)) {
		t.Fatalf("posted %v, want 27–30 and nothing after finish_work", got)
	}
	if got := h.watermark(); got != 40 {
		t.Fatalf("watermark %d, want 40: the turns after finish_work must be settled", got)
	}

	// The next start_work opens a fresh window; nothing before it follows.
	h.pin(t, agenthook.SessionPin{StartedAt: at(41, 5*time.Second)})
	writeTranscript(t, h.transcript, 42)
	h.run(t)
	if got := h.posted(); !reflect.DeepEqual(got, []int{41, 42}) {
		t.Fatalf("next window posted %v, want [41 42]", got)
	}
}

// Excluded 1–25, posted 26–30, excluded 31–40, in one firing. A post that
// fails must leave the watermark just below the first unposted turn — the
// excluded turns before it settled, the ones it would have posted not — and
// the retry must then settle everything.
func TestHookStopKeepsTheWatermarkHonestWhenExcludedAndPostedTurnsInterleave(t *testing.T) {
	h := newHookStop(t)
	h.pin(t, agenthook.SessionPin{StartedAt: at(26, 10*time.Second), FinishedAt: at(30, 20*time.Second)})
	writeTranscript(t, h.transcript, 40)

	h.token = "revoked"
	_, stderr := h.run(t)
	if !strings.Contains(stderr, "could not record agent time") {
		t.Fatalf("the failed post was not reported: %q", stderr)
	}
	if got := h.watermark(); got != 25 {
		t.Fatalf("watermark %d after a failed post, want 25", got)
	}

	h.token = fixtureToken
	h.run(t)
	if got := h.posted(); !reflect.DeepEqual(got, span(26, 30)) {
		t.Fatalf("posted %v, want 26–30", got)
	}
	if got := h.watermark(); got != 40 {
		t.Fatalf("watermark %d, want 40", got)
	}
}

// A pin written by a bridge that predates windows has no started_at, and
// claims every turn not yet recorded, as it always did.
func TestHookStopPostsEverythingForAPinWithoutAWindow(t *testing.T) {
	h := newHookStop(t)
	if err := os.MkdirAll(filepath.Join(h.configDir, agenthook.PinsDir), 0o755); err != nil {
		t.Fatal(err)
	}
	old := `{"feature":"FUL-17","feature_id":1994,"project_id":2,"role":"Development","updated_at":"2026-10-09T12:00:00Z"}`
	if err := os.WriteFile(filepath.Join(h.configDir, agenthook.PinsDir, qaSession+".json"), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	writeTranscript(t, h.transcript, 10)
	h.run(t)
	if got := h.posted(); !reflect.DeepEqual(got, span(1, 10)) {
		t.Fatalf("posted %v, want 1–10", got)
	}
	if got := h.watermark(); got != 10 {
		t.Fatalf("watermark %d, want 10", got)
	}
}

func TestHookStopDryRunNamesTheWindowAndCounts(t *testing.T) {
	h := newHookStop(t)
	h.pin(t, agenthook.SessionPin{StartedAt: at(26, 10*time.Second), FinishedAt: at(30, 20*time.Second)})
	writeTranscript(t, h.transcript, 40)

	out, _ := h.run(t, "--dry-run")
	for _, want := range []string{
		"via the session pin",
		"window:   2026-10-14T09:25:10Z → 2026-10-14T09:29:20Z",
		"40 in the transcript, 40 not yet recorded: 5 to post, 35 outside the window",
		"active:   2m30s in the turns to post",
		"in 50, out 100",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("dry run lacks %q:\n%s", want, out)
		}
	}
	if got := h.posted(); len(got) != 0 {
		t.Errorf("a dry run posted %v", got)
	}
	if got := h.watermark(); got != 0 {
		t.Errorf("a dry run moved the watermark to %d", got)
	}

	h.pin(t, agenthook.SessionPin{StartedAt: at(26, 10*time.Second)})
	if out, _ := h.run(t, "--dry-run"); !strings.Contains(out, "→ open (no finish_work yet)") ||
		!strings.Contains(out, "15 to post, 25 outside the window") {
		t.Errorf("open window misreported:\n%s", out)
	}
}
