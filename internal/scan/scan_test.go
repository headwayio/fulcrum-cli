package scan

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// The fake-repo spec ported from spec/lib/fulcrum_skills_spec.rb: language
// counts, top-level SKIP_DIRS, and the pinned dependency order
// rails, pg, stimulus (Gemfile order first, then package.json document
// order, deduped keeping first occurrence).
func TestCollectMatchesRubyFakeRepoSpec(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "fake-repo")
	write := func(rel, content string) {
		t.Helper()
		path := filepath.Join(repo, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("app/model.rb", "class X; end\n")
	write("app/view.erb", "<p>hi</p>\n")
	write("node_modules/dep/skipme.js", "ignored\n")
	write("Gemfile", "gem \"rails\"\ngem 'pg'\n")
	write("package.json", `{"dependencies":{"stimulus":"^3"}}`)

	facts, err := Collect(repo)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(facts.Languages, map[string]int{"Ruby": 1, "ERB": 1}) {
		t.Errorf("languages = %v", facts.Languages)
	}
	if !reflect.DeepEqual(facts.Dependencies, []string{"rails", "pg", "stimulus"}) {
		t.Errorf("dependencies = %v (order is part of the contract)", facts.Dependencies)
	}
	if facts.Repository != "fake-repo" {
		t.Errorf("repository = %q", facts.Repository)
	}
}

// The Ruby prefix check skips only TOP-LEVEL dirs: app/vendor is scanned.
func TestSkipDirsAreTopLevelOnly(t *testing.T) {
	repo := t.TempDir()
	nested := filepath.Join(repo, "app", "vendor")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "keep.rb"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "vendor"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "vendor", "skip.rb"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	facts, err := Collect(repo)
	if err != nil {
		t.Fatal(err)
	}
	if facts.Languages["Ruby"] != 1 {
		t.Errorf("nested app/vendor must be scanned, top-level vendor skipped: %v", facts.Languages)
	}
}

func TestDependencyDedupeKeepsFirst(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "Gemfile"), []byte("gem \"rails\"\ngem \"rails\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "package.json"),
		[]byte(`{"devDependencies":{"jest":"^29"},"dependencies":{"zeta":"1","alpha":"2","rails":"3"}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	facts, err := Collect(repo)
	if err != nil {
		t.Fatal(err)
	}
	// devDependencies never counted; document order preserved; dupes keep
	// their first position.
	if !reflect.DeepEqual(facts.Dependencies, []string{"rails", "zeta", "alpha"}) {
		t.Errorf("dependencies = %v", facts.Dependencies)
	}
}

func TestLanguageOrderCountDescNameAsc(t *testing.T) {
	repo := t.TempDir()
	for _, name := range []string{"a.rb", "b.rb", "c.go", "d.py"} {
		if err := os.WriteFile(filepath.Join(repo, name), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	facts, err := Collect(repo)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(facts.LanguageOrder, []string{"Ruby", "Go", "Python"}) {
		t.Errorf("order = %v", facts.LanguageOrder)
	}
}
