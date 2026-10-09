package mcpinstall

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
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

// HookEvent is the harness event main-session telemetry rides on. Stop fires
// when the agent finishes a turn and hands control back, which is the moment
// the transcript is complete and quiet.
const HookEvent = "Stop"

// SubagentHookEvent fires when a Claude Code subagent hands back. Its payload
// names the subagent's own transcript, so `hook stop` reads that file and
// records the work under "<session>/<agent>" rather than on the parent.
const SubagentHookEvent = "SubagentStop"

// ToolUseHookEvent fires before every tool call; the matcher narrows it to
// the two Fulcrum tools that name a session's card, where `hook tool-use`
// fills in the session_ref the model cannot know.
const ToolUseHookEvent = "PreToolUse"

// ToolUseMatcher is a Claude Code tool-name pattern: MCP tools are exposed
// as mcp__<server>__<tool>.
const ToolUseMatcher = "mcp__fulcrum__start_work|mcp__fulcrum__finish_work"

// ToolUseCommand is the marker the tool-use hook is recognised by.
const ToolUseCommand = "hook tool-use"

// claudeHooks is every registration the Claude target makes: the event, the
// matcher (empty means every tool), and the subcommand that answers it.
var claudeHooks = []struct{ event, matcher, command string }{
	{HookEvent, "", HookCommand},
	{SubagentHookEvent, "", HookCommand},
	{ToolUseHookEvent, ToolUseMatcher, ToolUseCommand},
}

// OmpHookFile is where the OMP telemetry hook is written, relative to the
// user's home directory.
//
// USER SCOPE, NOT THE PROJECT'S .omp/hooks/, for the same two reasons the
// Claude hook avoids the committed settings file: the factory names this
// machine's binary by absolute path, and a hook is a side effect a teammate
// did not ask for. OMP discovers user hooks from ~/.omp/agent/hooks/post/ in
// every project, which is fine: `fulcrum hook stop` records nothing for a
// checkout with no pin.
const OmpHookFile = ".omp/agent/hooks/post/fulcrum-telemetry.ts"

// unsupported explains, per harness, why nothing was installed.
//
// ALL FOUR HARNESSES HAVE HOOKS, and all four record token usage on disk —
// Claude Code, Codex and Kimi verified 2026-08-05, OMP 2026-10-09. Claude Code
// hands the hook a Stop payload naming its own transcript; OMP exposes the
// transcript path and session id to a hook factory, which builds the same
// payload itself (see ompHookTemplate). The other two hand over a session id
// and leave the reader to locate the file themselves, in a different format
// each:
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
// not happen is the product implying every harness reports tokens when some do.
var unsupported = map[string]string{
	TargetCodex: "hooks exist, but Fulcrum cannot read Codex rollout files yet, so no tokens are recorded",
	TargetKimi:  "hooks exist, but Fulcrum cannot read Kimi wire logs yet, so no tokens are recorded",
}

// ompHookTemplate is the extension factory OMP loads. %s is the JS string
// literal of the fulcrum binary's path.
//
// Two events, because OMP fires them differently per session kind:
//
//   - session_stop fires in the MAIN session when the agent hands control
//     back, and is awaited before the turn settles, so the transcript is
//     complete. It never fires for task/subagent sessions.
//   - agent_end fires in EVERY session when an agent loop ends. It is used
//     only for subagents, whose transcripts live inside the parent's session
//     directory under the agent's name rather than the <stamp>_<id>.jsonl
//     shape of a main session; a subagent's assignment is one turn.
//
// The hook never throws: an unhandled error in a handler takes the session
// down, and telemetry is not worth that.
const ompHookTemplate = `// Written by ` + "`fulcrum mcp install`" + `. Records what an agent spends on the card
// pinned to the checkout it runs in, by handing OMP's own transcript to
// ` + "`fulcrum hook stop`" + `. Safe everywhere: with no pin, nothing is recorded.
import { spawn } from "node:child_process";
import { basename } from "node:path";

const FULCRUM = %s;
const MAIN_SESSION_FILE = /^\d{4}-\d{2}-\d{2}T[^_]+_[0-9a-f-]+\.jsonl$/;

function record(pi, ctx) {
  const manager = ctx && ctx.sessionManager;
  const transcript = manager && manager.getSessionFile ? manager.getSessionFile() : undefined;
  const session = manager && manager.getSessionId ? manager.getSessionId() : undefined;
  if (!transcript || !session) return Promise.resolve();
  const payload = JSON.stringify({
    session_id: session,
    transcript_path: transcript,
    cwd: ctx.cwd,
    hook_event_name: "Stop",
  });
  return new Promise((resolve) => {
    let child;
    try {
      child = spawn(FULCRUM, ["hook", "stop"], { stdio: ["pipe", "ignore", "pipe"] });
    } catch (error) {
      if (pi.logger) pi.logger.warn("fulcrum telemetry: " + String(error));
      resolve();
      return;
    }
    let diagnostics = "";
    child.stderr.on("data", (chunk) => { diagnostics += String(chunk); });
    child.on("error", (error) => {
      if (pi.logger) pi.logger.warn("fulcrum telemetry: " + String(error));
      resolve();
    });
    child.on("close", () => {
      if (diagnostics.trim() && pi.logger) pi.logger.warn(diagnostics.trim());
      resolve();
    });
    child.stdin.end(payload);
  });
}

// OMP reaches MCP tools through its ` + "`write`" + ` tool against an xd:// route, with the
// arguments as JSON content; a harness that exposes them as first-class tools
// names them with the server as a prefix. Both shapes are recognised.
const WORK_ROUTE = /^xd:\/\/mcp__fulcrum_(start_work|finish_work)$/;
const WORK_TOOL = /^(?:mcp__)?fulcrum__?(start_work|finish_work)$/;

// withSessionRef fills session_ref — the key the telemetry hook attributes a
// transcript by — when the model left it out. A ref the model named is kept:
// an orchestrator may pin another session on purpose.
function withSessionRef(args, session) {
  if (!args || typeof args !== "object") return null;
  if (typeof args.session_ref === "string" && args.session_ref.trim()) return null;
  return { ...args, session_ref: session };
}

export default function (pi) {
  if (pi.setLabel) pi.setLabel("Fulcrum telemetry");
  pi.on("tool_call", async (event, ctx) => {
    const manager = ctx && ctx.sessionManager;
    const session = manager && manager.getSessionId ? manager.getSessionId() : undefined;
    if (!session || !event || !event.input) return;
    if (event.toolName === "write" && WORK_ROUTE.test(String(event.input.path || "")) && typeof event.input.content === "string") {
      let args;
      try { args = JSON.parse(event.input.content); } catch { return; }
      const filled = withSessionRef(args, session);
      if (filled) return { input: { ...event.input, content: JSON.stringify(filled) } };
      return;
    }
    if (WORK_TOOL.test(String(event.toolName || ""))) {
      const filled = withSessionRef(event.input, session);
      if (filled) return { input: filled };
    }
  });
  pi.on("session_stop", async (_event, ctx) => {
    await record(pi, ctx);
  });
  pi.on("agent_end", async (_event, ctx) => {
    const manager = ctx && ctx.sessionManager;
    const file = manager && manager.getSessionFile ? manager.getSessionFile() : undefined;
    if (!file || MAIN_SESSION_FILE.test(basename(file))) return;
    await record(pi, ctx);
  });
}
`

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
		case TargetOmp:
			result, err := installOmpHook(opts)
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

	// Each event is checked on its own, so a settings file from an older
	// install that only knew Stop gains the two newer registrations without
	// being given a second Stop.
	changed := false
	for _, registration := range claudeHooks {
		matchers, _ := hooks[registration.event].([]any)
		if hookAlreadyInstalled(matchers, registration.command) {
			continue
		}
		group := map[string]any{
			"hooks": []any{
				map[string]any{"type": "command", "command": strings.TrimSpace(opts.Command + " " + registration.command)},
			},
		}
		if registration.matcher != "" {
			group["matcher"] = registration.matcher
		}
		hooks[registration.event] = append(matchers, group)
		changed = true
	}
	if !changed {
		return Result{Target: TargetClaude, Path: path, Changed: false,
			Note: "telemetry hooks already registered — left as they are"}, nil
	}
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

// installOmpHook writes the factory once. A file that already carries the
// hook command is left as it is, so a developer who edited it keeps their
// version; a file at that path that does not is somebody else's and is not
// overwritten either.
func installOmpHook(opts Options) (Result, error) {
	path := filepath.Join(opts.HomeDir, filepath.FromSlash(OmpHookFile))

	if existing, err := os.ReadFile(path); err == nil {
		if strings.Contains(string(existing), HookCommand) {
			return Result{Target: TargetOmp, Path: path, Changed: false,
				Note: "telemetry hook already installed — left as it is"}, nil
		}
		return Result{Target: TargetOmp, Path: path, Changed: false,
			Note: "a different hook already lives at this path, so it was left alone"}, nil
	} else if !os.IsNotExist(err) {
		return Result{}, err
	}

	content := fmt.Sprintf(ompHookTemplate, strconv.Quote(strings.TrimSpace(opts.Command)))
	if err := writeFile(path, []byte(content)); err != nil {
		return Result{}, err
	}
	return Result{Target: TargetOmp, Path: path, Changed: true}, nil
}

// hookAlreadyInstalled walks the harness's nested shape — a list of matcher
// groups, each holding its own list of hooks — without assuming any of it is
// well-formed, because this file belongs to the developer.
func hookAlreadyInstalled(matchers []any, command string) bool {
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
			if installed, ok := hook["command"].(string); ok && strings.Contains(installed, command) {
				return true
			}
		}
	}
	return false
}
