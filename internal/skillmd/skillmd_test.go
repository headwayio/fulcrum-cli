package skillmd

import "testing"

const doc = "---\nname: writing-specs\ndescription: How we write specs.\n---\n\n# Writing specs\n\nBody text.\n"

func TestSplit(t *testing.T) {
	frontmatter, body, ok := Split(doc)
	if !ok {
		t.Fatal("frontmatter not detected")
	}
	if frontmatter != "name: writing-specs\ndescription: How we write specs." {
		t.Errorf("frontmatter = %q", frontmatter)
	}
	if body != "# Writing specs\n\nBody text.\n" {
		t.Errorf("body = %q", body)
	}
}

func TestSplitWithoutFrontmatter(t *testing.T) {
	_, body, ok := Split("# Just markdown\n")
	if ok {
		t.Error("no frontmatter must report false")
	}
	if body != "# Just markdown\n" {
		t.Errorf("body must be the whole document, got %q", body)
	}

	// An unterminated block is not frontmatter either.
	if _, _, ok := Split("---\nname: x\n\nbody\n"); ok {
		t.Error("unterminated frontmatter must report false")
	}
}

func TestFieldReadsFlatScalars(t *testing.T) {
	frontmatter, _, _ := Split(doc)
	if got := Field(frontmatter, "name"); got != "writing-specs" {
		t.Errorf("name = %q", got)
	}
	if got := Field(frontmatter, "description"); got != "How we write specs." {
		t.Errorf("description = %q", got)
	}
	if got := Field(frontmatter, "missing"); got != "" {
		t.Errorf("absent key = %q, want empty", got)
	}
	// Quotes are conventional in YAML and not part of the value.
	if got := Field("name: \"quoted\"", "name"); got != "quoted" {
		t.Errorf("quoted value = %q", got)
	}
	// A colon inside the value survives.
	if got := Field("description: Use when: it applies", "description"); got != "Use when: it applies" {
		t.Errorf("value with colon = %q", got)
	}
}

func TestDescribeFallsBackToSlug(t *testing.T) {
	name, description := Describe(doc, "fallback")
	if name != "writing-specs" || description != "How we write specs." {
		t.Errorf("describe = %q / %q", name, description)
	}

	name, description = Describe("# no frontmatter\n", "fallback")
	if name != "fallback" || description != "" {
		t.Errorf("fallback = %q / %q", name, description)
	}
}
