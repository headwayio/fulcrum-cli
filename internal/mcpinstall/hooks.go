package mcpinstall

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// HookCommand is the marker a previously-installed hook is recognised by.
// Matching on the SUBCOMMAND rather than the whole line means a developer who
// moved their binary, or who wrapped the call, is not given a second copy.
const HookCommand = "hook stop"

// SettingsFile is where Claude Code hooks are written.
//
// LOCAL, NOT THE COMMITTED settings.json — and the opposite choice from the
// MCP entry one file over, which IS committed on purpose. Two reasons, both
// of which bite immediately:
//
//   - The command is this binary's ABSOLUTE PATH, because a harness launched
//     from a desktop app does not inherit the PATH the install ran under. That
//     path is true on one machine. Committed, it would be wrong for every
//     teammate, and a Stop hook that cannot be found fails on EVERY TURN.
//   - A hook runs code on every turn. An MCP server sits inert until a model
//     calls it, so shipping one to a teammate is a courtesy; shipping a hook
//     is a side effect they did not ask for.
//
// Claude Code gitignores this file for exactly this class of setting.
const SettingsFile = "settings.local.json"

// HookEvent is the harness event telemetry rides on. Stop fires when the
// agent finishes a turn and hands control back, which is the moment the
// transcript is complete and quiet.
const HookEvent = "Stop"

// unsupported explains, per harness, why nothing was installed.
//
// ALL THREE HARNESSES HAVE HOOKS, and all three record token usage on disk —
// verified 2026-08-05. What Claude Code alone provides is a Stop payload that
// names its own transcript, so the hook can find the usage without guessing.
// The other two hand over a session id and leave the reader to locate the file
// themselves, in a different format each:
//
//   - Codex writes ~/.codex/sessions/<date>/rollout-<stamp>-<session id>.jsonl,
//     with usage in `event_msg` records whose payload type is `token_count`.
//   - Kimi writes ~/.kimi-code/sessions/wd_<workspace>/session_<id>/agents/
//     main/wire.jsonl, with usage as {inputOther, output, inputCacheRead,
//     inputCacheCreation}.
//
// Both are reachable and neither is written yet. Installing a hook that would
// find no transcript and warn on EVERY TURN would be worse than installing
// nothing, so they are declined — with the reason, because the thing that must
// not happen is the product implying all three report tokens when one does.
var unsupported = map[string]string{
	TargetCodex: "hooks exist, but Fulcrum cannot read Codex rollout files yet, so no tokens are recorded",
	TargetKimi:  "hooks exist, but Fulcrum cannot read Kimi wire logs yet, so no tokens are recorded",
}

// InstallHooks registers the telemetry hook for a harness.
func InstallHooks(targets []string, opts Options) ([]Result, error) {
	results := make([]Result, 0, len(targets))
	for _, target := range targets {
		switch target {
		case TargetClaude:
			result, err := installClaudeHook(opts)
			if err != nil {
				return nil, err
			}
			results = append(results, result)
		case TargetCodex, TargetKimi:
			results = append(results, Result{
				Target:  target,
				Changed: false,
				Note:    unsupported[target],
			})
		default:
			return nil, fmt.Errorf("unknown harness %q — known: %s", target, strings.Join(AllTargets, ", "))
		}
	}
	return results, nil
}

func installClaudeHook(opts Options) (Result, error) {
	path := filepath.Join(opts.ProjectDir, ".claude", SettingsFile)

	document := map[string]any{}
	if existing, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(existing, &document); err != nil {
			return Result{}, fmt.Errorf("%s is not valid JSON, so it was left alone: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return Result{}, err
	}

	hooks, _ := document["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	matchers, _ := hooks[HookEvent].([]any)

	if hookAlreadyInstalled(matchers) {
		return Result{Target: TargetClaude, Path: path, Changed: false,
			Note: "telemetry hook already registered — left as it is"}, nil
	}

	command := strings.TrimSpace(opts.Command + " hook stop")
	matchers = append(matchers, map[string]any{
		"hooks": []any{
			map[string]any{"type": "command", "command": command},
		},
	})
	hooks[HookEvent] = matchers
	document["hooks"] = hooks

	encoded, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return Result{}, err
	}
	if err := writeFile(path, append(encoded, '\n')); err != nil {
		return Result{}, err
	}
	return Result{Target: TargetClaude, Path: path, Changed: true}, nil
}

// hookAlreadyInstalled walks the harness's nested shape — a list of matcher
// groups, each holding its own list of hooks — without assuming any of it is
// well-formed, because this file belongs to the developer.
func hookAlreadyInstalled(matchers []any) bool {
	for _, entry := range matchers {
		group, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		inner, ok := group["hooks"].([]any)
		if !ok {
			continue
		}
		for _, item := range inner {
			hook, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if command, ok := hook["command"].(string); ok && strings.Contains(command, HookCommand) {
				return true
			}
		}
	}
	return false
}
