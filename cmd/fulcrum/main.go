// fulcrum is the subscriber CLI for Fulcrum's knowledge sync. This file is
// deliberately thin: flag parsing and verbs live in internal/cli, the TUI
// (when it lands) in internal/tui, and neither imports the other.
package main

import (
	"os"
	"runtime/debug"

	"github.com/headwayio/fulcrum-cli/internal/cli"
)

// version is stamped by the release pipeline (-ldflags "-X main.version=…");
// go-install builds fall back to module build info so they stay honest.
var version = "dev"

func main() {
	app := &cli.App{
		Stdout:  os.Stdout,
		Stderr:  os.Stderr,
		Stdin:   os.Stdin,
		Version: resolveVersion(),
	}
	os.Exit(app.Main(os.Args[1:]))
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
