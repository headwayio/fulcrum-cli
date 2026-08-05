package cli

import (
	"os"
	"os/exec"
	"path/filepath"
)

// installableSelf is the path to write into a harness's configuration.
//
// ABSOLUTE, because a harness is launched from a desktop app or a login shell
// whose PATH need not be the one the install ran under, and "command not
// found" surfaces inside the harness as an unexplained missing server.
//
// BUT NOT THE SYMLINK TARGET, which is the part that used to be wrong. A
// package manager installs into a VERSIONED directory and exposes a stable
// name pointing at it — Homebrew's /opt/homebrew/bin/fulcrum resolves to
// /opt/homebrew/Cellar/fulcrum/<version>/bin/fulcrum. Writing the resolved
// path bakes a version into every .mcp.json and every hook command, and the
// next `brew upgrade` deletes the directory they name. Nothing reports it: the
// MCP server simply stops appearing, and the Stop hook fails on every turn.
//
// So the stable name on PATH wins when it is genuinely us, compared by inode
// rather than by string so a DIFFERENT fulcrum earlier in PATH cannot claim
// the entry. The fallback is whatever os.Executable reports.
//
// The PATH lookup comes first on purpose: on Linux os.Executable reads
// /proc/self/exe, which the kernel has ALREADY resolved, so the versioned path
// is all it can offer and only the lookup can recover the stable one.
func installableSelf() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	return stablePathFor(executable), nil
}

// stablePathFor is the testable core of installableSelf.
func stablePathFor(executable string) string {
	onPath, err := exec.LookPath(filepath.Base(executable))
	if err != nil {
		return executable
	}
	absolute, err := filepath.Abs(onPath)
	if err != nil {
		return executable
	}
	if !sameFile(absolute, executable) {
		return executable
	}
	return absolute
}

// sameFile asks the filesystem rather than comparing strings, so a symlink and
// its target are recognised as one binary.
func sameFile(one, other string) bool {
	oneInfo, err := os.Stat(one)
	if err != nil {
		return false
	}
	otherInfo, err := os.Stat(other)
	if err != nil {
		return false
	}
	return os.SameFile(oneInfo, otherInfo)
}
