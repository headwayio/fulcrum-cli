package cli

import (
	"strings"
	"testing"
)

// The order is the point: an estimate made after reading the card's own is
// anchored on it, so the brief comes after the agent has sized the work.
func TestStarterPromptEstimatesBeforeReadingTheBrief(t *testing.T) {
	prompt := starterPrompt("FUL-17", "Dynamic field mapping", "Developer")

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
	prompt := starterPrompt("FUL-17", "Dynamic field mapping", "Developer")
	for _, want := range []string{
		"I'm working Fulcrum card FUL-17 (Dynamic field mapping) in this repository.",
		`Call start_work with role "Developer" before you begin, and finish_work when you stop.`,
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt lacks %q:\n%s", want, prompt)
		}
	}

	bare := starterPrompt("FUL-17", "", "")
	if strings.Contains(bare, "FUL-17 (") || strings.Contains(bare, "with role") {
		t.Errorf("an unnamed card with no role should say neither:\n%s", bare)
	}
}
