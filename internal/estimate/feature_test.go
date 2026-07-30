package estimate

import (
	"math"
	"strings"
	"testing"
)

const validDraft = `{
  "action": "add",
  "features": [{
    "id": "dnd-swap",
    "name": "Swap react-datatables for react-drag-n-drop",
    "description": "<p>Replace the table library across 23 call sites.</p>",
    "moscow_priority": "should_have",
    "estimates": [{
      "role": "Development",
      "estimate": {
        "low": 8, "likely": 16, "high": 40,
        "confidence": "medium",
        "components_included": ["frontend", "testing"],
        "components_excluded": [{"component": "data_migrations", "reason": "No schema change"}],
        "assumptions": ["The new library covers server-side pagination"],
        "exclusions": [], "unknowns": [], "dependencies": [], "risks": [],
        "reuse_or_modification": "modify-existing",
        "rationale": "23 call sites, 4 with custom renderers, none covered by tests"
      }
    }]
  }]
}`

func TestParse(t *testing.T) {
	payload, err := Parse([]byte(validDraft))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if payload.Action != "add" || len(payload.Features) != 1 {
		t.Fatalf("unexpected payload: %+v", payload)
	}
	if got := payload.Features[0].Estimates[0].Estimate.Likely; got != 16 {
		t.Fatalf("likely = %v, want 16", got)
	}
}

// A draft pasted straight out of an agent's output still carries the fence.
func TestParseAcceptsFencedBlock(t *testing.T) {
	fenced := "Here is my estimate.\n\n```features-json\n" + validDraft + "\n```\n"
	payload, err := Parse([]byte(fenced))
	if err != nil {
		t.Fatalf("Parse fenced: %v", err)
	}
	if len(payload.Features) != 1 {
		t.Fatalf("expected 1 feature, got %d", len(payload.Features))
	}
}

func TestParseDefaultsActionToAdd(t *testing.T) {
	payload, err := Parse([]byte(`{"features":[]}`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if payload.Action != "add" {
		t.Fatalf("action = %q, want add — append is the safe default", payload.Action)
	}
}

func TestValidateAcceptsAWellFormedDraft(t *testing.T) {
	payload, _ := Parse([]byte(validDraft))
	if problems := payload.Validate(); len(problems) != 0 {
		t.Fatalf("expected no problems, got %v", problems)
	}
}

func TestValidateRejectsANonMonotonicRange(t *testing.T) {
	broken := strings.Replace(validDraft, `"low": 8, "likely": 16`, `"low": 20, "likely": 16`, 1)
	payload, _ := Parse([]byte(broken))
	problems := payload.Validate()
	if !containsSubstring(problems, "must not exceed likely") {
		t.Fatalf("expected a monotonicity problem, got %v", problems)
	}
}

// A degenerate range is legitimate: well-understood work with no spread.
// Rejecting it would force an artificial spread, which the rubric forbids.
func TestValidateAcceptsADegenerateRange(t *testing.T) {
	flat := strings.Replace(validDraft, `"low": 8, "likely": 16, "high": 40`, `"low": 16, "likely": 16, "high": 16`, 1)
	payload, _ := Parse([]byte(flat))
	if problems := payload.Validate(); len(problems) != 0 {
		t.Fatalf("a degenerate range must be legitimate, got %v", problems)
	}
}

// The rubric's coverage requirement: silence must never be read as absence
// of work, so an exclusion without a reason is a contract violation.
func TestValidateRejectsAnExclusionWithoutAReason(t *testing.T) {
	broken := strings.Replace(validDraft, `"reason": "No schema change"`, `"reason": "  "`, 1)
	payload, _ := Parse([]byte(broken))
	if problems := payload.Validate(); !containsSubstring(problems, "excluded without a reason") {
		t.Fatalf("expected a coverage problem, got %v", problems)
	}
}

func TestValidateRequiresTheContractsFields(t *testing.T) {
	cases := map[string][2]string{
		"rationale": {
			`"rationale": "23 call sites, 4 with custom renderers, none covered by tests"`,
			`"rationale": "   "`,
		},
		"confidence": {`"confidence": "medium",`, `"confidence": "",`},
		"reuse_or_modification": {
			`"reuse_or_modification": "modify-existing",`,
			`"reuse_or_modification": "",`,
		},
		"components_included": {
			`"components_included": ["frontend", "testing"],`,
			`"components_included": [],`,
		},
	}
	for field, swap := range cases {
		t.Run(field, func(t *testing.T) {
			payload, err := Parse([]byte(strings.Replace(validDraft, swap[0], swap[1], 1)))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if problems := payload.Validate(); !containsSubstring(problems, field) {
				t.Fatalf("expected a problem naming %s, got %v", field, problems)
			}
		})
	}
}

func TestValidateRejectsAnUnknownPriority(t *testing.T) {
	broken := strings.Replace(validDraft, `"should_have"`, `"nice_to_have"`, 1)
	payload, _ := Parse([]byte(broken))
	if problems := payload.Validate(); !containsSubstring(problems, "moscow_priority") {
		t.Fatalf("expected a priority problem, got %v", problems)
	}
}

func TestValidateReportsAnEmptyPayload(t *testing.T) {
	payload, _ := Parse([]byte(`{"action":"add","features":[]}`))
	if problems := payload.Validate(); len(problems) != 1 {
		t.Fatalf("expected exactly one problem, got %v", problems)
	}
}

func TestCompute(t *testing.T) {
	payload, _ := Parse([]byte(validDraft))
	results := payload.Compute(scale)
	if len(results) != 1 || len(results[0].Roles) != 1 {
		t.Fatalf("unexpected results: %+v", results)
	}

	role := results[0].Roles[0]
	// (8 + 4*16 + 40) / 6 = 18.666…, which snaps to M (16h) — nearer than L.
	if math.Abs(role.Expected-18.6667) > 0.001 {
		t.Fatalf("expected = %v, want ~18.667", role.Expected)
	}
	if role.Committed != "M" {
		t.Fatalf("committed = %q, want M", role.Committed)
	}
	if math.Abs(role.Sigma-5.3333) > 0.001 {
		t.Fatalf("sigma = %v, want ~5.333", role.Sigma)
	}
}

// The committed value is derived, never supplied — a draft carrying its own
// hours cannot smuggle a number its range does not support.
func TestComputeIgnoresAnySuppliedHours(t *testing.T) {
	smuggled := strings.Replace(validDraft, `"low": 8,`, `"hours": 999, "low": 8,`, 1)
	payload, err := Parse([]byte(smuggled))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	results := payload.Compute(scale)
	if got := results[0].Roles[0].Committed; got != "M" {
		t.Fatalf("committed = %q, want M — supplied hours must be ignored", got)
	}
}

func containsSubstring(values []string, needle string) bool {
	for _, value := range values {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}
