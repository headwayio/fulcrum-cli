// Package mcpinstall registers `fulcrum mcp` with the coding harnesses a
// project might be worked in.
//
// Three harnesses, two file formats, and one rule: every entry is
// PROJECT-scoped, so it lives in the checkout and a teammate who clones it
// gets the server for free.
//
// KIMI'S PATH IS A TRAP. `~/.kimi/mcp.json` belongs to the older Kimi CLI,
// which is being phased out; Kimi CODE — the one that ships as `kimi` today —
// reads `~/.kimi-code/mcp.json` globally and `.kimi-code/mcp.json` per
// project. We targeted the retired product's path for a while and the install
// reported success the whole time, because writing a JSON file nothing reads
// cannot fail. Verified against the docs and a real install 2026-08-05.
package mcpinstall

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const ServerName = "fulcrum"

const (
	TargetClaude = "claude"
	TargetCodex  = "codex"
	TargetKimi   = "kimi"
)

// AllTargets is the default: the same project is often opened in more than
// one harness, and a developer should not have to decide which one counts.
var AllTargets = []string{TargetClaude, TargetCodex, TargetKimi}

// Result describes one target's outcome, so the caller can report what
// actually changed rather than claiming it wrote everything.
type Result struct {
	Target  string
	Path    string
	Changed bool
	Note    string
}

// Options is what to write. Env is only populated when configuration lives
// somewhere a harness cannot inherit — normally the binary reads the same
// config file every other fulcrum command does.
type Options struct {
	ProjectDir string
	Command    string
	Args       []string
	Env        map[string]string
	HomeDir    string
}

// Install writes an entry for each target, leaving any existing one alone.
// Idempotent: running it twice reports the second run as unchanged.
func Install(targets []string, opts Options) ([]Result, error) {
	results := make([]Result, 0, len(targets))
	for _, target := range targets {
		var (
			result Result
			err    error
		)
		switch target {
		case TargetClaude:
			result, err = installJSON(target, filepath.Join(opts.ProjectDir, ".mcp.json"), opts)
		case TargetKimi:
			result, err = installJSON(target, filepath.Join(opts.ProjectDir, ".kimi-code", "mcp.json"), opts)
		case TargetCodex:
			result, err = installCodex(opts)
		default:
			return nil, fmt.Errorf("unknown harness %q — known: %s", target, strings.Join(AllTargets, ", "))
		}
		if err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, nil
}

// installJSON handles the mcpServers shape, which Claude Code and Kimi share.
//
// The file is decoded, amended and re-encoded rather than overwritten: a
// developer's .mcp.json usually already carries servers that have nothing to
// do with Fulcrum, and clobbering them would be a hostile way to be helpful.
func installJSON(target, path string, opts Options) (Result, error) {
	document := map[string]any{}
	if existing, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(existing, &document); err != nil {
			return Result{}, fmt.Errorf("%s is not valid JSON, so it was left alone: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return Result{}, err
	}

	servers, _ := document["mcpServers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	if _, present := servers[ServerName]; present {
		return Result{Target: target, Path: path, Changed: false,
			Note: "already registered — left as it is"}, nil
	}

	entry := map[string]any{"command": opts.Command}
	if len(opts.Args) > 0 {
		entry["args"] = opts.Args
	}
	if len(opts.Env) > 0 {
		entry["env"] = opts.Env
	}
	servers[ServerName] = entry
	document["mcpServers"] = servers

	encoded, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return Result{}, err
	}
	if err := writeFile(path, append(encoded, '\n')); err != nil {
		return Result{}, err
	}
	return Result{Target: target, Path: path, Changed: true}, nil
}

// installCodex appends a TOML table.
//
// APPEND, not merge: a full merge needs a TOML parser, and re-emitting a
// developer's whole config through one risks reordering and comment loss for
// a file this only ever adds to. Tables are order-independent, so appending a
// new one is valid — and the one case appending would corrupt (a table that
// already exists, which is a TOML parse error when duplicated) is exactly the
// case detected and skipped.
func installCodex(opts Options) (Result, error) {
	path := filepath.Join(opts.ProjectDir, ".codex", "config.toml")
	header := fmt.Sprintf("[mcp_servers.%s]", ServerName)

	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return Result{}, err
	}
	for _, line := range strings.Split(string(existing), "\n") {
		if strings.TrimSpace(line) == header {
			return Result{Target: TargetCodex, Path: path, Changed: false,
				Note: "already registered — left as it is"}, nil
		}
	}

	var b strings.Builder
	b.Write(existing)
	if len(existing) > 0 && !strings.HasSuffix(string(existing), "\n") {
		b.WriteString("\n")
	}
	if len(existing) > 0 {
		b.WriteString("\n")
	}
	b.WriteString(header + "\n")
	fmt.Fprintf(&b, "command = %s\n", tomlString(opts.Command))
	if len(opts.Args) > 0 {
		quoted := make([]string, 0, len(opts.Args))
		for _, arg := range opts.Args {
			quoted = append(quoted, tomlString(arg))
		}
		fmt.Fprintf(&b, "args = [%s]\n", strings.Join(quoted, ", "))
	}
	if len(opts.Env) > 0 {
		fmt.Fprintf(&b, "\n[mcp_servers.%s.env]\n", ServerName)
		for _, key := range sortedKeys(opts.Env) {
			fmt.Fprintf(&b, "%s = %s\n", key, tomlString(opts.Env[key]))
		}
	}

	if err := writeFile(path, []byte(b.String())); err != nil {
		return Result{}, err
	}
	return Result{Target: TargetCodex, Path: path, Changed: true}, nil
}

func writeFile(path string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, content, 0o644)
}

// tomlString quotes a basic TOML string. Paths can carry backslashes on
// Windows and spaces anywhere, and neither may reach the file raw.
func tomlString(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\t", `\t`)
	return `"` + replacer.Replace(value) + `"`
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}
