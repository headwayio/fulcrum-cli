package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
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
