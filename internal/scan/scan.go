// Package scan ports the Ruby reference client's FulcrumSkills::RepoFacts
// exactly: shallow, deterministic architecture facts from a local checkout —
// language mix by file extension, key dependencies from the manifests
// actually present. Facts leave the machine only when the developer runs
// push-facts; that explicitness is the privacy seam.
package scan

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// ExtensionLanguages is the Ruby client's 19-extension map, verbatim.
var ExtensionLanguages = map[string]string{
	".rb": "Ruby", ".erb": "ERB", ".js": "JavaScript", ".ts": "TypeScript",
	".tsx": "TypeScript", ".jsx": "JavaScript", ".py": "Python", ".go": "Go",
	".rs": "Rust", ".java": "Java", ".kt": "Kotlin", ".swift": "Swift",
	".php": "PHP", ".ex": "Elixir", ".exs": "Elixir", ".css": "CSS",
	".scss": "CSS", ".sql": "SQL", ".sh": "Shell",
}

// SkipDirs matches the Ruby list: skipped only at the repository top level
// (relative == skip or starts with "skip/"), exactly like the Ruby
// prefix check.
var SkipDirs = []string{"node_modules", "vendor", "tmp", "log", ".git", "dist", "build", "coverage"}

var gemPattern = regexp.MustCompile(`(?m)^\s*gem ["']([^"']+)["']`)

// Facts is the collected payload. Languages is unordered (JSON object);
// LanguageOrder carries the count-desc presentation order.
type Facts struct {
	Languages     map[string]int
	LanguageOrder []string
	Dependencies  []string
	Repository    string
}

// Payload is the wire shape push-facts sends (languages + dependencies only,
// like the Ruby client's slice).
func (f *Facts) Payload() map[string]any {
	return map[string]any{
		"languages":    f.Languages,
		"dependencies": f.Dependencies,
	}
}

// Collect walks dir like the Ruby client's Dir.glob(FNM_DOTMATCH) walk.
func Collect(dir string) (*Facts, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}

	counts := map[string]int{}
	err = filepath.WalkDir(abs, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, relErr := filepath.Rel(abs, path)
		if relErr != nil {
			return relErr
		}
		if entry.IsDir() {
			if relative != "." && topLevelSkipped(relative) {
				return filepath.SkipDir
			}
			return nil
		}
		if topLevelSkipped(relative) {
			return nil
		}
		if language, ok := ExtensionLanguages[filepath.Ext(path)]; ok {
			counts[language]++
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return &Facts{
		Languages:     counts,
		LanguageOrder: orderByCount(counts),
		Dependencies:  dependencies(abs),
		Repository:    filepath.Base(abs),
	}, nil
}

func topLevelSkipped(relative string) bool {
	for _, skip := range SkipDirs {
		if relative == skip || strings.HasPrefix(relative, skip+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// dependencies: Gemfile gems in file order, then package.json runtime
// dependencies (never devDependencies) in document order, deduped keeping
// first occurrence — the Ruby `deps.uniq` semantics.
func dependencies(dir string) []string {
	deps := []string{}
	if raw, err := os.ReadFile(filepath.Join(dir, "Gemfile")); err == nil {
		for _, match := range gemPattern.FindAllStringSubmatch(string(raw), -1) {
			deps = append(deps, match[1])
		}
	}
	if raw, err := os.ReadFile(filepath.Join(dir, "package.json")); err == nil {
		deps = append(deps, packageDependencies(raw)...)
	}
	return dedupe(deps)
}

// packageDependencies pulls the keys of "dependencies" in DOCUMENT order —
// Go's map decode would alphabetize them, and order-preserving dedupe is
// part of the pinned contract.
func packageDependencies(raw []byte) []string {
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	// Walk the top-level object looking for the "dependencies" key.
	tok, err := dec.Token()
	if err != nil || tok != json.Delim('{') {
		return nil
	}
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil
		}
		key, _ := keyTok.(string)
		if key != "dependencies" {
			var skip json.RawMessage
			if err := dec.Decode(&skip); err != nil {
				return nil
			}
			continue
		}
		open, err := dec.Token()
		if err != nil || open != json.Delim('{') {
			return nil
		}
		var keys []string
		for dec.More() {
			depTok, err := dec.Token()
			if err != nil {
				return nil
			}
			name, _ := depTok.(string)
			var version json.RawMessage
			if err := dec.Decode(&version); err != nil {
				return nil
			}
			keys = append(keys, name)
		}
		return keys
	}
	return nil
}

func dedupe(values []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, v := range values {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

func orderByCount(counts map[string]int) []string {
	names := make([]string, 0, len(counts))
	for name := range counts {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		if counts[names[i]] != counts[names[j]] {
			return counts[names[i]] > counts[names[j]]
		}
		return names[i] < names[j]
	})
	return names
}
