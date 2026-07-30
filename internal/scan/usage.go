package scan

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Dependency usage: how many source files actually reference each declared
// dependency.
//
// This is the fact Fulcrum most conspicuously lacks. Its whole picture of a
// repository is a language histogram and a list of dependency NAMES, so it
// knows react-datatables is present and cannot know whether ripping it out
// touches two files or two hundred. That difference is most of the estimate.
//
// Deliberately counts only. File paths would sharpen it further and are the
// most identifying thing in a repository, and this payload leaves the
// developer's machine — a count carries the estimating signal without
// carrying the map.

// maxScannedBytes bounds per-file reads so a vendored bundle or a checked-in
// minified asset cannot dominate the scan.
const maxScannedBytes = 512 * 1024

var (
	// import x from "pkg" / export … from "pkg" / import "pkg"
	jsFromPattern = regexp.MustCompile(`(?m)(?:from|import)\s+["']([^"'\n]+)["']`)
	// require("pkg") / require.resolve("pkg")
	jsRequirePattern = regexp.MustCompile(`require(?:\.resolve)?\(\s*["']([^"'\n]+)["']\s*\)`)
	// Ruby: require "pkg" (require_relative is intentionally not matched —
	// it never names a dependency)
	rubyRequirePattern = regexp.MustCompile(`(?m)^\s*require\s+["']([^"'\n]+)["']`)
)

// scannableExtensions are the files worth opening. Everything else is
// counted for the language histogram but never read.
var scannableExtensions = map[string]bool{
	".js": true, ".jsx": true, ".ts": true, ".tsx": true, ".mjs": true, ".cjs": true,
	".rb": true, ".erb": true, ".rake": true,
}

// countUsage returns dependency name -> number of files referencing it,
// omitting dependencies with no references at all. Only names in declared is
// counted, so a typo'd import or a stdlib require never becomes a phantom
// dependency.
func countUsage(root string, declared []string) map[string]int {
	if len(declared) == 0 {
		return nil
	}
	known := make(map[string]bool, len(declared))
	for _, name := range declared {
		known[name] = true
	}

	usage := map[string]int{}
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil // an unreadable corner must not fail the whole scan
		}
		relative, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		if entry.IsDir() {
			if relative != "." && topLevelSkipped(relative) {
				return filepath.SkipDir
			}
			return nil
		}
		if topLevelSkipped(relative) || !scannableExtensions[filepath.Ext(path)] {
			return nil
		}
		if info, statErr := entry.Info(); statErr == nil && info.Size() > maxScannedBytes {
			return nil
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}

		// Per FILE, not per occurrence: three imports of the same package in
		// one file is one file to change.
		for name := range referencedIn(string(raw), known) {
			usage[name]++
		}
		return nil
	})
	return usage
}

// referencedIn returns the declared dependencies a file references.
func referencedIn(body string, known map[string]bool) map[string]bool {
	found := map[string]bool{}
	add := func(specifier string) {
		if name := packageOf(specifier); name != "" && known[name] {
			found[name] = true
		}
	}
	for _, match := range jsFromPattern.FindAllStringSubmatch(body, -1) {
		add(match[1])
	}
	for _, match := range jsRequirePattern.FindAllStringSubmatch(body, -1) {
		add(match[1])
	}
	for _, match := range rubyRequirePattern.FindAllStringSubmatch(body, -1) {
		add(match[1])
	}
	return found
}

// packageOf reduces an import specifier to the package it names.
// "@scope/pkg/deep" -> "@scope/pkg"; "pkg/sub" -> "pkg"; relative and
// absolute paths name no package at all.
func packageOf(specifier string) string {
	if specifier == "" || strings.HasPrefix(specifier, ".") || strings.HasPrefix(specifier, "/") {
		return ""
	}
	parts := strings.Split(specifier, "/")
	if strings.HasPrefix(specifier, "@") {
		if len(parts) < 2 {
			return ""
		}
		return parts[0] + "/" + parts[1]
	}
	return parts[0]
}
