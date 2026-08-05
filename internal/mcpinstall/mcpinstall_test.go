package mcpinstall_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/headwayio/fulcrum-cli/internal/mcpinstall"
)

func options(t *testing.T) mcpinstall.Options {
	t.Helper()
	return mcpinstall.Options{
		ProjectDir: t.TempDir(),
		HomeDir:    t.TempDir(),
		Command:    "/usr/local/bin/fulcrum",
		Args:       []string{"mcp"},
	}
}

func readServers(t *testing.T, path string) map[string]any {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var document map[string]any
	if err := json.Unmarshal(content, &document); err != nil {
		t.Fatalf("%s is not valid JSON: %v", path, err)
	}
	servers, _ := document["mcpServers"].(map[string]any)
	return servers
}

func TestInstallWritesEveryHarness(t *testing.T) {
	opts := options(t)

	results, err := mcpinstall.Install(mcpinstall.AllTargets, opts)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	for _, result := range results {
		if !result.Changed {
			t.Errorf("%s reported no change on a first install", result.Target)
		}
	}

	if _, ok := readServers(t, filepath.Join(opts.ProjectDir, ".mcp.json"))["fulcrum"]; !ok {
		t.Error("claude entry missing")
	}
	if _, ok := readServers(t, filepath.Join(opts.HomeDir, ".kimi", "mcp.json"))["fulcrum"]; !ok {
		t.Error("kimi entry missing")
	}

	toml, err := os.ReadFile(filepath.Join(opts.ProjectDir, ".codex", "config.toml"))
	if err != nil {
		t.Fatalf("read codex config: %v", err)
	}
	for _, want := range []string{"[mcp_servers.fulcrum]", `command = "/usr/local/bin/fulcrum"`, `args = ["mcp"]`} {
		if !strings.Contains(string(toml), want) {
			t.Errorf("codex config missing %q\ngot:\n%s", want, toml)
		}
	}
}

// A developer's .mcp.json usually already carries servers that have nothing
// to do with Fulcrum. Clobbering them would be a hostile way to be helpful.
func TestInstallPreservesServersItDidNotWrite(t *testing.T) {
	opts := options(t)
	path := filepath.Join(opts.ProjectDir, ".mcp.json")
	existing := `{"mcpServers":{"sentry":{"command":"npx","args":["-y","@sentry/mcp"]}},"otherKey":"kept"}`
	if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := mcpinstall.Install([]string{mcpinstall.TargetClaude}, opts); err != nil {
		t.Fatalf("Install: %v", err)
	}

	servers := readServers(t, path)
	if _, ok := servers["sentry"]; !ok {
		t.Error("an unrelated server was lost")
	}
	if _, ok := servers["fulcrum"]; !ok {
		t.Error("fulcrum was not added")
	}

	content, _ := os.ReadFile(path)
	var document map[string]any
	_ = json.Unmarshal(content, &document)
	if document["otherKey"] != "kept" {
		t.Error("an unrelated top-level key was lost")
	}
}

func TestInstallIsIdempotent(t *testing.T) {
	opts := options(t)
	if _, err := mcpinstall.Install(mcpinstall.AllTargets, opts); err != nil {
		t.Fatalf("first Install: %v", err)
	}

	before := map[string][]byte{}
	for _, path := range []string{
		filepath.Join(opts.ProjectDir, ".mcp.json"),
		filepath.Join(opts.ProjectDir, ".codex", "config.toml"),
		filepath.Join(opts.HomeDir, ".kimi", "mcp.json"),
	} {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		before[path] = content
	}

	results, err := mcpinstall.Install(mcpinstall.AllTargets, opts)
	if err != nil {
		t.Fatalf("second Install: %v", err)
	}
	for _, result := range results {
		if result.Changed {
			t.Errorf("%s rewrote an entry that already existed", result.Target)
		}
	}
	for path, content := range before {
		after, _ := os.ReadFile(path)
		if string(after) != string(content) {
			t.Errorf("%s changed on the second install", path)
		}
	}
}

// Appending a duplicate table would make the whole file a TOML parse error,
// which is worse than doing nothing.
func TestInstallLeavesAnExistingCodexTableAlone(t *testing.T) {
	opts := options(t)
	path := filepath.Join(opts.ProjectDir, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	existing := "[mcp_servers.fulcrum]\ncommand = \"/somewhere/else/fulcrum\"\n"
	if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := mcpinstall.Install([]string{mcpinstall.TargetCodex}, opts); err != nil {
		t.Fatalf("Install: %v", err)
	}

	after, _ := os.ReadFile(path)
	if string(after) != existing {
		t.Errorf("an existing codex entry was modified:\n%s", after)
	}
	if strings.Count(string(after), "[mcp_servers.fulcrum]") != 1 {
		t.Error("the table was duplicated, which makes the file unparseable")
	}
}

func TestInstallAppendsWithoutDisturbingExistingCodexConfig(t *testing.T) {
	opts := options(t)
	path := filepath.Join(opts.ProjectDir, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	existing := "model = \"gpt-5\"\n\n[mcp_servers.context7]\ncommand = \"npx\"\n"
	if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := mcpinstall.Install([]string{mcpinstall.TargetCodex}, opts); err != nil {
		t.Fatalf("Install: %v", err)
	}

	after, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(after), existing) {
		t.Errorf("existing config was not preserved verbatim:\n%s", after)
	}
	if !strings.Contains(string(after), "[mcp_servers.fulcrum]") {
		t.Error("fulcrum table not appended")
	}
}

func TestInstallRejectsAnUnknownHarness(t *testing.T) {
	if _, err := mcpinstall.Install([]string{"emacs"}, options(t)); err == nil {
		t.Fatal("expected an error naming the known harnesses")
	}
}
