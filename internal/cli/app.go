// Package cli is the scriptable face: cobra verbs over the kernel packages.
// Exit-code taxonomy, pinned for CI and agent harnesses:
//
//	0 — success / everything fresh
//	1 — staleness (status) or docs skipped to protect local edits (sync)
//	2 — network, auth, or config failure — never conflated with staleness
//
// This package never imports the TUI; the TUI imports the same kernel.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mattn/go-isatty"

	"github.com/headwayio/fulcrum-cli/internal/api"
	"github.com/headwayio/fulcrum-cli/internal/config"
	"github.com/headwayio/fulcrum-cli/internal/workspace"
)

// Exit codes, named so intent reads at call sites.
const (
	ExitOK    = 0
	ExitStale = 1
	ExitError = 2
)

// App carries process dependencies so tests can substitute them.
type App struct {
	Stdout  io.Writer
	Stderr  io.Writer
	Stdin   io.Reader
	Version string
	// Getenv defaults to os.Getenv; the e2e harness injects script-scoped env.
	Getenv func(string) string
}

// codeError carries an exit code with its message.
type codeError struct {
	code int
	msg  string
}

func (e *codeError) Error() string { return e.msg }

func exitf(code int, format string, args ...any) error {
	return &codeError{code: code, msg: fmt.Sprintf(format, args...)}
}

// silentExit signals a non-zero code whose explanation was already printed.
func silentExit(code int) error { return &codeError{code: code} }

// ExitCode maps an error from Execute to the process exit code.
func ExitCode(err error) int {
	if err == nil {
		return ExitOK
	}
	var coded *codeError
	if errors.As(err, &coded) {
		return coded.code
	}
	return ExitError
}

// Report prints err for humans (nothing for silent exits). API errors print
// cleanly — code plus the server's message — never a backtrace.
func (a *App) Report(err error) {
	if err == nil {
		return
	}
	var coded *codeError
	if errors.As(err, &coded) && coded.msg == "" {
		return
	}
	fmt.Fprintf(a.Stderr, "fulcrum: %v\n", err)
}

func (a *App) getenv(key string) string {
	if a.Getenv != nil {
		return a.Getenv(key)
	}
	return os.Getenv(key)
}

// stdinTTY gates every interactive prompt. FULCRUM_FORCE_TTY exists for the
// test harness, where pipes are all there is.
func (a *App) stdinTTY() bool {
	if a.getenv("FULCRUM_FORCE_TTY") == "1" {
		return true
	}
	f, ok := a.Stdin.(*os.File)
	return ok && (isatty.IsTerminal(f.Fd()) || isatty.IsCygwinTerminal(f.Fd()))
}

func (a *App) keyring() config.Keyring {
	if a.getenv("FULCRUM_NO_KEYCHAIN") == "1" {
		return nil
	}
	return config.SystemKeyring{}
}

func (a *App) configDir() (string, error) {
	if dir := a.getenv("FULCRUM_CONFIG_DIR"); dir != "" {
		return dir, nil
	}
	return config.Dir()
}

func (a *App) resolveConfig() (*config.Resolved, error) {
	dir, err := a.configDir()
	if err != nil {
		return nil, exitf(ExitError, "no config dir: %v", err)
	}
	resolved, err := config.Resolve(dir, a.keyring(), a.getenv)
	if err != nil {
		return nil, exitf(ExitError, "%v", err)
	}
	return resolved, nil
}

// client requires a configured server and token; missing config is an
// ExitError with the fix spelled out.
func (a *App) client(resolved *config.Resolved) (*api.Client, error) {
	if resolved.URL == "" {
		return nil, exitf(ExitError,
			"no server configured — run `fulcrum login` or set %s", config.EnvURL)
	}
	if resolved.Token == "" {
		return nil, exitf(ExitError,
			"no API token — run `fulcrum login` or set %s (mint one at %s/settings/developer)",
			config.EnvToken, strings.TrimSuffix(resolved.URL, "/"))
	}
	if api.InsecureBaseURL(resolved.URL) {
		fmt.Fprintf(a.Stderr, "warning: %s sends your token over plaintext http\n", resolved.URL)
	}
	return &api.Client{
		BaseURL:        resolved.URL,
		Token:          resolved.Token,
		OrganizationID: resolved.OrganizationID,
		Version:        a.Version,
	}, nil
}

func (a *App) openWorkspace(resolved *config.Resolved) (*workspace.Workspace, error) {
	w, err := workspace.Load(resolved.SkillsDir)
	if err != nil {
		return nil, exitf(ExitError, "open workspace %s: %v", resolved.SkillsDir, err)
	}
	return w, nil
}

// wrapAPIError normalizes any client error to the exit taxonomy: contract
// and transport failures are both ExitError, printed cleanly.
func wrapAPIError(err error) error {
	if err == nil {
		return nil
	}
	if apiErr, ok := api.AsError(err); ok {
		// The server's message is API-shaped ("pass organization_id"); a
		// command-line user needs the command-line answer.
		if apiErr.Code == "organization_required" {
			var choices []string
			for _, org := range apiErr.Organizations {
				choices = append(choices, fmt.Sprintf("%d (%s)", org.ID, org.Name))
			}
			return exitf(ExitError,
				"your token reaches several organizations — set %s to one of: %s\n"+
					"       (or run `fulcrum login` and pick one)",
				config.EnvOrgID, strings.Join(choices, ", "))
		}
		return exitf(ExitError, "%v", apiErr)
	}
	return exitf(ExitError, "cannot reach the server: %v", err)
}

// confirm reads a y/N answer from stdin.
func (a *App) confirm(prompt string) bool {
	fmt.Fprintln(a.Stdout, prompt+" [y/N]")
	return strings.EqualFold(strings.TrimSpace(a.readLine()), "y")
}

func (a *App) readLine() string {
	var line []byte
	buf := make([]byte, 1)
	for {
		n, err := a.Stdin.Read(buf)
		if n > 0 {
			if buf[0] == '\n' {
				break
			}
			line = append(line, buf[0])
		}
		if err != nil {
			break
		}
	}
	return strings.TrimSuffix(string(line), "\r")
}

func background() context.Context { return context.Background() }
