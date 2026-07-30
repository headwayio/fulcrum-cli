package scan

import (
	"os"
	"path/filepath"
	"testing"
)

func writeRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		full := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestPackageOf(t *testing.T) {
	cases := map[string]string{
		"react":                   "react",
		"react-datatables/styles": "react-datatables",
		"@scope/pkg":              "@scope/pkg",
		"@scope/pkg/deep/path":    "@scope/pkg",
		"./relative":              "",
		"../up":                   "",
		"/absolute":               "",
		"@dangling":               "",
		"":                        "",
	}
	for specifier, want := range cases {
		if got := packageOf(specifier); got != want {
			t.Errorf("packageOf(%q) = %q, want %q", specifier, got, want)
		}
	}
}

func TestCountUsageCountsFilesNotOccurrences(t *testing.T) {
	dir := writeRepo(t, map[string]string{
		"package.json": `{"dependencies":{"react-datatables":"1.0.0","react":"18.0.0"}}`,
		// Three references to the same package in one file is still one file
		// to change.
		"app/a.js": `import DataTable from "react-datatables"
import {Row} from "react-datatables/row"
const x = require("react-datatables")`,
		"app/b.jsx": `import DataTable from "react-datatables"`,
		"app/c.js":  `import React from "react"`,
	})

	usage := countUsage(dir, []string{"react-datatables", "react"})

	if usage["react-datatables"] != 2 {
		t.Errorf("react-datatables = %d, want 2 files", usage["react-datatables"])
	}
	if usage["react"] != 1 {
		t.Errorf("react = %d, want 1 file", usage["react"])
	}
}

// A typo'd import or a stdlib require must never become a phantom dependency.
func TestCountUsageOnlyCountsDeclaredDependencies(t *testing.T) {
	dir := writeRepo(t, map[string]string{
		"app/a.rb": `require "json"
require "rails"`,
	})

	usage := countUsage(dir, []string{"rails"})

	if _, present := usage["json"]; present {
		t.Error("counted an undeclared require as a dependency")
	}
	if usage["rails"] != 1 {
		t.Errorf("rails = %d, want 1", usage["rails"])
	}
}

func TestCountUsageIgnoresRelativeImports(t *testing.T) {
	dir := writeRepo(t, map[string]string{
		"app/a.js": `import helper from "./helper"
import other from "../lib/other"`,
	})
	if len(countUsage(dir, []string{"helper", "other"})) != 0 {
		t.Error("relative imports must not count as dependency usage")
	}
}

// require_relative never names a dependency.
func TestCountUsageIgnoresRequireRelative(t *testing.T) {
	dir := writeRepo(t, map[string]string{
		"app/a.rb": `require_relative "rails"`,
	})
	if len(countUsage(dir, []string{"rails"})) != 0 {
		t.Error("require_relative must not count as dependency usage")
	}
}

func TestCountUsageSkipsVendoredDirectories(t *testing.T) {
	dir := writeRepo(t, map[string]string{
		"node_modules/pkg/index.js": `import x from "react-datatables"`,
		"app/a.js":                  `import x from "react-datatables"`,
	})
	if got := countUsage(dir, []string{"react-datatables"})["react-datatables"]; got != 1 {
		t.Errorf("counted %d files, want 1 — node_modules must be skipped", got)
	}
}

func TestCountUsageOmitsUnreferencedDependencies(t *testing.T) {
	dir := writeRepo(t, map[string]string{"app/a.js": `import x from "react"`})
	usage := countUsage(dir, []string{"react", "never-imported"})
	if _, present := usage["never-imported"]; present {
		t.Error("an unreferenced dependency should be absent, not zero")
	}
}

func TestCollectIncludesUsage(t *testing.T) {
	dir := writeRepo(t, map[string]string{
		"package.json": `{"dependencies":{"react-datatables":"1.0.0"}}`,
		"app/a.js":     `import DataTable from "react-datatables"`,
	})

	facts, err := Collect(dir)
	if err != nil {
		t.Fatal(err)
	}
	if facts.Usage["react-datatables"] != 1 {
		t.Errorf("usage = %v, want react-datatables: 1", facts.Usage)
	}
	if _, present := facts.Payload()["usage"]; !present {
		t.Error("usage must ride the wire payload")
	}
}

// A repo with nothing to count sends no usage key at all, so the server
// keeps rendering exactly as it did before this existed.
func TestPayloadOmitsEmptyUsage(t *testing.T) {
	facts := &Facts{Languages: map[string]int{"Ruby": 1}, Dependencies: []string{"rails"}}
	if _, present := facts.Payload()["usage"]; present {
		t.Error("empty usage must be omitted from the payload")
	}
}
