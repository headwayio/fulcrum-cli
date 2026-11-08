package mcpinstall_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/headwayio/fulcrum-cli/internal/mcpinstall"
)

func claudeSettings(t *testing.T, dir string) map[string]any {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(dir, ".claude", mcpinstall.SettingsFile))
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var document map[string]any
	if err := json.Unmarshal(content, &document); err != nil {
		t.Fatalf("settings are not valid JSON: %v", err)
	}
	return document
}

func stopCommands(t *testing.T, document map[string]any) []string {
	t.Helper()
	return eventCommands(t, document, mcpinstall.HookEvent)
}

func eventCommands(t *testing.T, document map[string]any, event string) []string {
	t.Helper()
	hooks, _ := document["hooks"].(map[string]any)
	matchers, _ := hooks[event].([]any)

	var found []string
	for _, entry := range matchers {
		group, _ := entry.(map[string]any)
		inner, _ := group["hooks"].([]any)
		for _, item := range inner {
			hook, _ := item.(map[string]any)
			if command, ok := hook["command"].(string); ok {
				found = append(found, command)
			}
		}
	}
	return found
}

func TestInstallHooksRegistersTheStopHook(t *testing.T) {
	opts := options(t)

	results, err := mcpinstall.InstallHooks([]string{mcpinstall.TargetClaude}, opts)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if !results[0].Changed {
		t.Fatalf("nothing was written: %+v", results[0])
	}

	commands := stopCommands(t, claudeSettings(t, opts.ProjectDir))
	if len(commands) != 1 {
		t.Fatalf("expected one Stop hook, got %d: %v", len(commands), commands)
	}
	if !strings.HasPrefix(commands[0], opts.Command) || !strings.Contains(commands[0], "hook stop") {
		t.Errorf("hook command is wrong: %q", commands[0])
	}
}

// The command is an absolute path so a harness launched from a desktop app
// finds it; a bare name would depend on a PATH we do not control.
func TestHookCommandIsAnAbsolutePath(t *testing.T) {
	opts := options(t)
	if _, err := mcpinstall.InstallHooks([]string{mcpinstall.TargetClaude}, opts); err != nil {
		t.Fatal(err)
	}

	commands := stopCommands(t, claudeSettings(t, opts.ProjectDir))
	if !filepath.IsAbs(strings.Fields(commands[0])[0]) {
		t.Errorf("hook command is not absolute: %q", commands[0])
	}
}

func TestInstallHooksIsIdempotent(t *testing.T) {
	opts := options(t)

	if _, err := mcpinstall.InstallHooks([]string{mcpinstall.TargetClaude}, opts); err != nil {
		t.Fatal(err)
	}
	results, err := mcpinstall.InstallHooks([]string{mcpinstall.TargetClaude}, opts)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Changed {
		t.Errorf("second install rewrote the file")
	}

	if commands := stopCommands(t, claudeSettings(t, opts.ProjectDir)); len(commands) != 1 {
		t.Errorf("hook was installed twice: %v", commands)
	}
}

// The settings file belongs to the developer and usually carries hooks that
// have nothing to do with Fulcrum.
func TestInstallHooksKeepsWhatIsAlreadyThere(t *testing.T) {
	opts := options(t)
	path := filepath.Join(opts.ProjectDir, ".claude", mcpinstall.SettingsFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	existing := `{
	  "permissions": {"allow": ["Bash(ls:*)"]},
	  "hooks": {"Stop": [{"hooks": [{"type": "command", "command": "/usr/bin/say done"}]}]}
	}`
	if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := mcpinstall.InstallHooks([]string{mcpinstall.TargetClaude}, opts); err != nil {
		t.Fatal(err)
	}

	document := claudeSettings(t, opts.ProjectDir)
	if _, ok := document["permissions"]; !ok {
		t.Error("unrelated settings were dropped")
	}
	commands := stopCommands(t, document)
	if len(commands) != 2 {
		t.Fatalf("expected the existing hook plus ours, got %v", commands)
	}
	if commands[0] != "/usr/bin/say done" {
		t.Errorf("the developer's own hook was disturbed: %v", commands)
	}
}

func TestUnparseableSettingsAreLeftAlone(t *testing.T) {
	opts := options(t)
	path := filepath.Join(opts.ProjectDir, ".claude", mcpinstall.SettingsFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := mcpinstall.InstallHooks([]string{mcpinstall.TargetClaude}, opts); err == nil {
		t.Fatal("expected a refusal rather than a clobbered file")
	}

	content, _ := os.ReadFile(path)
	if string(content) != "{not json" {
		t.Errorf("the file was rewritten: %q", content)
	}
}

// Claiming every harness reports tokens when only some do would be the
// product lying about its own coverage.
func TestHarnessesThatRecordNothingSaySo(t *testing.T) {
	opts := options(t)

	results, err := mcpinstall.InstallHooks(mcpinstall.AllTargets, opts)
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range results {
		switch result.Target {
		case mcpinstall.TargetClaude, mcpinstall.TargetOmp:
			if !result.Changed {
				t.Errorf("%s should have been installed", result.Target)
			}
		default:
			if result.Changed {
				t.Errorf("%s reported an install it cannot support", result.Target)
			}
			if !strings.Contains(result.Note, "no tokens are recorded") {
				t.Errorf("%s does not say plainly that it records nothing: %q", result.Target, result.Note)
			}
		}
	}
}

// Hooks must NOT land in the committed settings.json: the command is this
// machine's absolute path, so a teammate who cloned it would get a Stop hook
// that fails on every turn.
func TestHooksAreNotWrittenToTheCommittedSettings(t *testing.T) {
	opts := options(t)
	if _, err := mcpinstall.InstallHooks([]string{mcpinstall.TargetClaude}, opts); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(opts.ProjectDir, ".claude", "settings.json")); !os.IsNotExist(err) {
		t.Error("the shared settings.json was written to")
	}
}

func TestInstallHooksWritesTheOmpFactoryAtUserScope(t *testing.T) {
	opts := options(t)

	results, err := mcpinstall.InstallHooks([]string{mcpinstall.TargetOmp}, opts)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if !results[0].Changed {
		t.Fatalf("nothing was written: %+v", results[0])
	}

	path := filepath.Join(opts.HomeDir, filepath.FromSlash(mcpinstall.OmpHookFile))
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the factory was not written under the home dir: %v", err)
	}
	for _, want := range []string{`"` + opts.Command + `"`, "hook stop", "session_stop", "agent_end", "tool_call", "session_ref", "export default function"} {
		if !strings.Contains(string(content), want) {
			t.Errorf("factory lacks %q", want)
		}
	}
	if entries, _ := os.ReadDir(filepath.Join(opts.ProjectDir, ".omp")); len(entries) != 0 {
		t.Errorf("the hook must not land in the project: %v", entries)
	}
}

func TestInstallHooksLeavesAnExistingOmpFactoryAlone(t *testing.T) {
	opts := options(t)
	if _, err := mcpinstall.InstallHooks([]string{mcpinstall.TargetOmp}, opts); err != nil {
		t.Fatalf("first install: %v", err)
	}
	path := filepath.Join(opts.HomeDir, filepath.FromSlash(mcpinstall.OmpHookFile))
	edited := []byte("// mine\nconst x = 'hook stop';\n")
	if err := os.WriteFile(path, edited, 0o644); err != nil {
		t.Fatal(err)
	}

	results, err := mcpinstall.InstallHooks([]string{mcpinstall.TargetOmp}, opts)
	if err != nil {
		t.Fatalf("second install: %v", err)
	}
	if results[0].Changed {
		t.Errorf("an edited factory was overwritten")
	}
	if content, _ := os.ReadFile(path); string(content) != string(edited) {
		t.Errorf("file changed: %q", content)
	}
}

func TestInstallHooksDoesNotClobberAForeignFile(t *testing.T) {
	opts := options(t)
	path := filepath.Join(opts.HomeDir, filepath.FromSlash(mcpinstall.OmpHookFile))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("export default function () {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	results, err := mcpinstall.InstallHooks([]string{mcpinstall.TargetOmp}, opts)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if results[0].Changed || !strings.Contains(results[0].Note, "left alone") {
		t.Errorf("foreign file was not protected: %+v", results[0])
	}
}

// Subagent work is a separate transcript in Claude Code, announced by its own
// event; and the session can only be named on start_work by a tool hook. One
// install registers all three, and an older settings file that only knew Stop
// is brought up to date without a second Stop.
func TestInstallHooksRegistersSubagentAndToolUseHooks(t *testing.T) {
	opts := options(t)
	if _, err := mcpinstall.InstallHooks([]string{mcpinstall.TargetClaude}, opts); err != nil {
		t.Fatal(err)
	}
	document := claudeSettings(t, opts.ProjectDir)

	if got := eventCommands(t, document, mcpinstall.SubagentHookEvent); len(got) != 1 || !strings.Contains(got[0], "hook stop") {
		t.Errorf("SubagentStop not registered: %v", got)
	}
	tool := eventCommands(t, document, mcpinstall.ToolUseHookEvent)
	if len(tool) != 1 || !strings.Contains(tool[0], "hook tool-use") {
		t.Fatalf("PreToolUse not registered: %v", tool)
	}
	hooks, _ := document["hooks"].(map[string]any)
	group, _ := hooks[mcpinstall.ToolUseHookEvent].([]any)[0].(map[string]any)
	if group["matcher"] != mcpinstall.ToolUseMatcher {
		t.Errorf("PreToolUse must be narrowed to the work tools: %v", group["matcher"])
	}

	// Second install: nothing changes, nothing doubles.
	results, err := mcpinstall.InstallHooks([]string{mcpinstall.TargetClaude}, opts)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Changed {
		t.Error("a complete install reported a change")
	}
	for _, event := range []string{mcpinstall.HookEvent, mcpinstall.SubagentHookEvent, mcpinstall.ToolUseHookEvent} {
		if got := eventCommands(t, claudeSettings(t, opts.ProjectDir), event); len(got) != 1 {
			t.Errorf("%s doubled: %v", event, got)
		}
	}
}

func TestInstallHooksUpgradesAStopOnlySettingsFile(t *testing.T) {
	opts := options(t)
	path := filepath.Join(opts.ProjectDir, ".claude", mcpinstall.SettingsFile)
	old := `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"/old/fulcrum hook stop"}]}]}}`
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}

	results, err := mcpinstall.InstallHooks([]string{mcpinstall.TargetClaude}, opts)
	if err != nil {
		t.Fatal(err)
	}
	if !results[0].Changed {
		t.Fatal("the two missing events were not added")
	}
	document := claudeSettings(t, opts.ProjectDir)
	if got := eventCommands(t, document, mcpinstall.HookEvent); len(got) != 1 || got[0] != "/old/fulcrum hook stop" {
		t.Errorf("the existing Stop hook was touched: %v", got)
	}
	if len(eventCommands(t, document, mcpinstall.SubagentHookEvent)) != 1 || len(eventCommands(t, document, mcpinstall.ToolUseHookEvent)) != 1 {
		t.Error("new events missing after upgrade")
	}
}
