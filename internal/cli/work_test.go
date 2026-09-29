package cli

import (
	"strings"
	"testing"

	"github.com/headwayio/fulcrum-cli/internal/projectctx"
)

// The order is the point: an estimate made after reading the card's own is
// anchored on it, so the brief comes after the agent has sized the work.
func TestStarterPromptEstimatesBeforeReadingTheBrief(t *testing.T) {
	prompt := starterPrompt("FUL-17", "Dynamic field mapping", "Developer", false)

	prd := strings.Index(prompt, "get_feature_prd")
	brief := strings.Index(prompt, "Then call get_feature to read the brief")
	update := strings.Index(prompt, "update_estimate")
	start := strings.Index(prompt, "Call start_work")
	if prd < 0 || brief < 0 || update < 0 || start < 0 {
		t.Fatalf("prompt is missing a step:\n%s", prompt)
	}
	if !(prd < brief && brief < update && update < start) {
		t.Fatalf("steps out of order (prd %d, brief %d, update %d, start %d):\n%s",
			prd, brief, update, start, prompt)
	}
}

func TestStarterPromptKeepsTheCardAndRole(t *testing.T) {
	prompt := starterPrompt("FUL-17", "Dynamic field mapping", "Developer", false)
	for _, want := range []string{
		"I'm working Fulcrum card FUL-17 (Dynamic field mapping) in this repository.",
		`Call start_work with role "Developer" before you begin, and finish_work when you stop.`,
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt lacks %q:\n%s", want, prompt)
		}
	}

	bare := starterPrompt("FUL-17", "", "", false)
	if strings.Contains(bare, "FUL-17 (") || strings.Contains(bare, "with role") {
		t.Errorf("an unnamed card with no role should say neither:\n%s", bare)
	}
}

// Only --without-estimates names the tool that withholds every card's
// sizing; plain `fulcrum work` reads the rubric with the priced inventory.
func TestStarterPromptNamesTheRubricToolForTheMode(t *testing.T) {
	anchored := starterPrompt("FUL-17", "", "", false)
	if !strings.Contains(anchored, `get_project_prompt (scope "estimating")`) ||
		strings.Contains(anchored, "without_estimates") {
		t.Errorf("plain work should read the rubric with get_project_prompt:\n%s", anchored)
	}

	blind := starterPrompt("FUL-17", "", "", true)
	if !strings.Contains(blind, `get_project_prompt_without_estimates (scope "estimating")`) {
		t.Errorf("--without-estimates should read the rubric with get_project_prompt_without_estimates:\n%s", blind)
	}
	if strings.Replace(blind, "_without_estimates", "", 1) != anchored {
		t.Errorf("the two prompts should differ only in the rubric tool:\n%s\n---\n%s", anchored, blind)
	}
}

func TestWorkProject(t *testing.T) {
	brief := "---\nname: feature-brief\nproject: Acme App\nproject_id: 7\nfeature: ACME-3\n---\n\n# ACME-3 — Cart\n"
	linked := &projectctx.Local{ProjectID: 7, ProjectName: "Acme App"}
	other := &projectctx.Local{ProjectID: 9, ProjectName: "Beta Portal"}

	// A checkout with no project takes the card's.
	if id, err := workProject(nil, "ACME-3", brief); err != nil || id != 7 {
		t.Errorf("unlinked: got %d, %v; want 7", id, err)
	}

	// A linked checkout works its own project's cards.
	if id, err := workProject(linked, "ACME-3", brief); err != nil || id != 7 {
		t.Errorf("same project: got %d, %v; want 7", id, err)
	}

	// Any other project's card is refused, naming both sides.
	_, err := workProject(other, "ACME-3", brief)
	if err == nil {
		t.Fatal("a card from another project should be refused")
	}
	for _, want := range []string{`"Acme App" (project 7)`, `"Beta Portal" (project 9)`, "Nothing was changed"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal lacks %q: %v", want, err)
		}
	}

	// With no project named in the brief there is nothing to link to.
	bare := "---\nname: feature-brief\n---\n"
	if _, err := workProject(nil, "ACME-3", bare); err == nil {
		t.Error("an unlinked checkout and a brief with no project should be refused")
	}
	if id, err := workProject(linked, "ACME-3", bare); err != nil || id != 7 {
		t.Errorf("linked, brief without project: got %d, %v; want 7", id, err)
	}
}
