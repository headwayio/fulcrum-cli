package install

import (
	"strings"
	"testing"
)

func TestBuiltinSkillsShip(t *testing.T) {
	skills := builtinSkills()
	if len(skills) == 0 {
		t.Fatal("no builtin skills embedded")
	}

	var estimating *skill
	for i := range skills {
		if skills[i].slug == "estimating-with-fulcrum" {
			estimating = &skills[i]
		}
	}
	if estimating == nil {
		t.Fatal("estimating-with-fulcrum is not embedded")
	}
	if estimating.description == "" {
		t.Error("description is empty — harnesses use it to decide when to load the skill")
	}
	if estimating.digest == "" {
		t.Error("digest is empty — the pin file records what was installed")
	}

	body := string(estimating.content)
	// The rules that keep the division of labour intact: the model supplies
	// judgment, the CLI supplies arithmetic.
	for _, required := range []string{
		"Never compute the committed hours yourself",
		"Never emit an `hours` field",
		"fulcrum context --project",
		"fulcrum estimate",
	} {
		if !strings.Contains(body, required) {
			t.Errorf("skill is missing a load-bearing instruction: %q", required)
		}
	}
}

// A team's own guidance outranks a client default. Installing both under one
// name would leave the harness reading two conflicting files.
func TestOrganizationSkillOverridesBuiltin(t *testing.T) {
	synced := []skill{{slug: "estimating-with-fulcrum", name: "theirs", content: []byte("org version")}}

	combined := withBuiltins(synced)

	var matching int
	for _, s := range combined {
		if s.slug == "estimating-with-fulcrum" {
			matching++
			if string(s.content) != "org version" {
				t.Errorf("builtin displaced the organization's skill: %q", s.content)
			}
		}
	}
	if matching != 1 {
		t.Fatalf("expected exactly one skill under the slug, got %d", matching)
	}
}

func TestBuiltinsAddedWhenNothingSynced(t *testing.T) {
	combined := withBuiltins(nil)
	if len(combined) == 0 {
		t.Fatal("a workspace that has synced nothing still needs the client's own skill")
	}
}
