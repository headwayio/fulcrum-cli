// fulcrum is the subscriber CLI for Fulcrum's knowledge sync. This file is
// deliberately thin: flag parsing and verbs live in internal/cli, the TUI
// (when it lands) in internal/tui, and neither imports the other.
package main

import (
	"fmt"
	"os"
	"runtime/debug"

	"github.com/mattn/go-isatty"

	"github.com/headwayio/fulcrum-cli/internal/cli"
	"github.com/headwayio/fulcrum-cli/internal/config"
	"github.com/headwayio/fulcrum-cli/internal/tui"
)

// version is stamped by the release pipeline (-ldflags "-X main.version=…");
// go-install builds fall back to module build info so they stay honest.
var version = "dev"

func main() {
	if len(os.Args) == 1 && stdioIsTerminal() {
		os.Exit(runTUI())
	}
	app := &cli.App{
		Stdout:  os.Stdout,
		Stderr:  os.Stderr,
		Stdin:   os.Stdin,
		Version: resolveVersion(),
	}
	os.Exit(app.Main(os.Args[1:]))
}

// Bare invocation on a real terminal opens the TUI; piped invocations fall
// through to `status` via the cobra root, so scripts never see a UI.
func stdioIsTerminal() bool {
	return isatty.IsTerminal(os.Stdin.Fd()) && isatty.IsTerminal(os.Stdout.Fd())
}

func runTUI() int {
	dir := os.Getenv("FULCRUM_CONFIG_DIR")
	if dir == "" {
		var err error
		if dir, err = config.Dir(); err != nil {
			fmt.Fprintf(os.Stderr, "fulcrum: no config dir: %v\n", err)
			return 2
		}
	}
	var ring config.Keyring = config.SystemKeyring{}
	if os.Getenv("FULCRUM_NO_KEYCHAIN") == "1" {
		ring = nil
	}
	resolved, err := config.Resolve(dir, ring, os.Getenv)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fulcrum: %v\n", err)
		return 2
	}
	deps := &tui.Live{Resolved: resolved, Version: resolveVersion()}
	if err := tui.Run(deps, resolveVersion()); err != nil {
		fmt.Fprintf(os.Stderr, "fulcrum: %v\n", err)
		return 2
	}
	return 0
}

func resolveVersion() string {
	if version != "dev" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return version
}
