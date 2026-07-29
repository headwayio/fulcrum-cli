package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/rogpeppe/go-internal/testscript"
)

// The e2e suite: testscript scenarios in testdata/script, each against its
// own fixture server seeded from the vendored corpus.
//
// Scripts drive the CLI IN-PROCESS through the custom `fulcrum` command:
//
//	fulcrum [-code N] [-in file] <args...>
//
// -code asserts the exact exit code (default 0) — the 0/1/2 taxonomy is the
// contract under test, and plain `! exec` cannot tell 1 from 2. -in feeds
// stdin from a file (with $VAR expansion). stdout/stderr land in
// $WORK/last-stdout.txt and $WORK/last-stderr.txt for grep/cmp assertions.
func TestScripts(t *testing.T) {
	testscript.Run(t, testscript.Params{
		Dir: filepath.Join("testdata", "script"),
		Setup: func(env *testscript.Env) error {
			srv := newFixtureServer()
			env.Defer(srv.Close)
			env.Setenv("FULCRUM_URL", srv.URL)
			env.Setenv("FULCRUM_API_TOKEN", fixtureToken)
			env.Setenv("FULCRUM_SKILLS_DIR", filepath.Join(env.WorkDir, "skills"))
			env.Setenv("FULCRUM_CONFIG_DIR", filepath.Join(env.WorkDir, "config"))
			env.Setenv("FULCRUM_NO_KEYCHAIN", "1")
			env.Setenv("CORPUS", corpusDir())
			// SRV_URL survives scripts that blank FULCRUM_URL (login flow).
			env.Setenv("SRV_URL", srv.URL)
			env.Values["srv"] = srv
			return nil
		},
		Cmds: map[string]func(ts *testscript.TestScript, neg bool, args []string){
			"fulcrum": cmdFulcrum,
			"srv":     cmdSrv,
		},
	})
}

func corpusDir() string {
	abs, err := filepath.Abs(filepath.Join("..", "..", "corpus"))
	if err != nil {
		panic(err)
	}
	return abs
}

func cmdFulcrum(ts *testscript.TestScript, neg bool, args []string) {
	wantCode := 0
	var stdin io.Reader = strings.NewReader("")

	for len(args) > 0 {
		switch args[0] {
		case "-code":
			code, err := strconv.Atoi(args[1])
			ts.Check(err)
			wantCode = code
			args = args[2:]
		case "-in":
			content := ts.ReadFile(args[1])
			stdin = strings.NewReader(expandEnv(ts, content))
			args = args[2:]
		default:
			goto run
		}
	}
run:
	var stdout, stderr bytes.Buffer
	app := &App{
		Stdout:  &stdout,
		Stderr:  &stderr,
		Stdin:   stdin,
		Version: "test",
		Getenv:  ts.Getenv,
	}
	code := app.Main(args)

	work := ts.Getenv("WORK")
	writeErr := os.WriteFile(filepath.Join(work, "last-stdout.txt"), stdout.Bytes(), 0o644)
	ts.Check(writeErr)
	writeErr = os.WriteFile(filepath.Join(work, "last-stderr.txt"), stderr.Bytes(), 0o644)
	ts.Check(writeErr)
	ts.Logf("fulcrum %s → %d\nstdout:\n%s\nstderr:\n%s",
		strings.Join(args, " "), code, stdout.String(), stderr.String())

	if neg {
		if code == wantCode {
			ts.Fatalf("fulcrum %s: exit %d, expected NOT %d", strings.Join(args, " "), code, wantCode)
		}
		return
	}
	if code != wantCode {
		ts.Fatalf("fulcrum %s: exit %d, want %d", strings.Join(args, " "), code, wantCode)
	}
}

// srv controls the script's fixture server:
//
//	srv apply <id> | srv reject <id> | srv count <n>
func cmdSrv(ts *testscript.TestScript, neg bool, args []string) {
	srv := ts.Value("srv").(*fixtureServer)
	if len(args) < 1 {
		ts.Fatalf("usage: srv apply|reject|count|edit|multiorg|singleorg …")
	}
	if args[0] != "multiorg" && args[0] != "singleorg" && len(args) < 2 {
		ts.Fatalf("usage: srv apply|reject|count <arg>")
	}
	switch args[0] {
	case "apply", "reject":
		status := map[string]string{"apply": "applied", "reject": "rejected"}[args[0]]
		if !srv.resolveProposalStatus(parseID(args[1]), status) {
			ts.Fatalf("no proposal %s on the fixture server", args[1])
		}
	case "count":
		want, err := strconv.Atoi(args[1])
		ts.Check(err)
		if got := srv.proposalCount(); got != want {
			ts.Fatalf("server has %d proposal(s), want %d", got, want)
		}
	case "multiorg", "singleorg":
		srv.mu.Lock()
		srv.multiOrg = args[0] == "multiorg"
		srv.mu.Unlock()
	case "edit":
		// srv edit <slug> <find> <replace-with> — a server-side edit that
		// moves the document's digest, the other half of a conflict.
		if len(args) != 4 {
			ts.Fatalf("usage: srv edit <slug> <find> <replacement>")
		}
		if !srv.editDocument(args[1], args[2], args[3]) {
			ts.Fatalf("srv edit: %q not found in %s", args[2], args[1])
		}
	default:
		ts.Fatalf("unknown srv subcommand %q", args[0])
	}
}

func expandEnv(ts *testscript.TestScript, content string) string {
	return os.Expand(content, func(key string) string { return ts.Getenv(key) })
}
