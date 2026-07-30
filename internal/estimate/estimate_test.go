package estimate

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/headwayio/fulcrum-cli/internal/api"
)

// Gapped on purpose: even spacing hides off-by-one and midpoint errors.
var scale = []api.ScaleStep{
	{Label: "S", Points: 1, Hours: 4},
	{Label: "M", Points: 3, Hours: 16},
	{Label: "L", Points: 5, Hours: 40},
}

func TestExpected(t *testing.T) {
	// PERT weights the likely value four times, so the committed value sits
	// near it rather than at the midpoint of the range.
	if got := Expected(4, 16, 40); math.Abs(got-18) > 1e-9 {
		t.Fatalf("Expected(4,16,40) = %v, want 18", got)
	}
	// A degenerate range is legitimate: well-understood work with no spread.
	if got := Expected(8, 8, 8); got != 8 {
		t.Fatalf("Expected(8,8,8) = %v, want 8", got)
	}
}

func TestSigma(t *testing.T) {
	if got := Sigma(4, 40); math.Abs(got-6) > 1e-9 {
		t.Fatalf("Sigma(4,40) = %v, want 6", got)
	}
	if got := Sigma(8, 8); got != 0 {
		t.Fatalf("Sigma(8,8) = %v, want 0", got)
	}
}

// Variance adds, standard deviations do not. Summing sigmas would model all
// work going badly at once and report a range too wide to be useful.
func TestProjectSigmaAddsVarianceNotDeviations(t *testing.T) {
	got := ProjectSigma([]float64{3, 4})
	if math.Abs(got-5) > 1e-9 {
		t.Fatalf("ProjectSigma([3,4]) = %v, want 5 (not 7)", got)
	}
}

func TestSnap(t *testing.T) {
	cases := []struct {
		hours float64
		want  string
		why   string
	}{
		{4, "S", "exact match"},
		{16, "M", "exact match"},
		{40, "L", "exact match"},
		{0.5, "S", "below the smallest step clamps to it"},
		{500, "L", "above the largest step clamps to it"},
		{5, "S", "nearest neighbour"},
		{15, "M", "nearest neighbour"},
		{10, "M", "exact midpoint rounds UP"},
		{28, "L", "exact midpoint rounds UP"},
		{9.5, "S", "a hair below the midpoint rounds down"},
		{10.5, "M", "a hair above the midpoint rounds up"},
	}
	for _, c := range cases {
		if got := Snap(c.hours, scale); got != c.want {
			t.Errorf("Snap(%g) = %q, want %q (%s)", c.hours, got, c.want, c.why)
		}
	}
}

func TestSnapWithoutScale(t *testing.T) {
	if got := Snap(10, nil); got != "" {
		t.Fatalf("Snap with no scale = %q, want empty so callers keep their fallback", got)
	}
}

func TestSnapToleratesUnsortedScale(t *testing.T) {
	jumbled := []api.ScaleStep{{Label: "L", Hours: 40}, {Label: "S", Hours: 4}, {Label: "M", Hours: 16}}
	if got := Snap(15, jumbled); got != "M" {
		t.Fatalf("Snap(15) on an unsorted scale = %q, want M", got)
	}
}

// The reason the whole fixture mechanism exists: "we implemented the same
// rule as the server" stops being a claim and becomes a check.
func TestFixtureFailuresPassesOnAgreement(t *testing.T) {
	snapping := api.Snapping{
		Scale: scale,
		Cases: []api.SnappingCase{
			{Hours: 4, Label: "S"},
			{Hours: 10, Label: "M"},
			{Hours: 28, Label: "L"},
			{Hours: 500, Label: "L"},
		},
	}
	if failures := FixtureFailures(snapping); len(failures) != 0 {
		t.Fatalf("expected agreement with the server, got %v", failures)
	}
}

func TestFixtureFailuresCatchesDivergence(t *testing.T) {
	// The exact divergence this guards against: a client that rounds the
	// midpoint DOWN. Every other case still agrees, so nothing else notices.
	snapping := api.Snapping{
		Scale: scale,
		Cases: []api.SnappingCase{
			{Hours: 4, Label: "S"},
			{Hours: 10, Label: "S"},
		},
	}
	failures := FixtureFailures(snapping)
	if len(failures) != 1 {
		t.Fatalf("expected exactly one disagreement, got %v", failures)
	}
	if want := `10h: server says "S", this client says "M"`; failures[0] != want {
		t.Fatalf("failure message = %q, want %q", failures[0], want)
	}
}

// Replays tables recorded from the server's own ComplexitySnapper, across
// more than one scale shape — a gapped scale and a real configured one,
// which fail differently. If the server's rule ever changes, re-record this
// file and the disagreement shows up here rather than as estimates that are
// quietly one size off.
//
// Re-record with:
//
//	bin/rails runner '…Estimation::ComplexitySnapper.fixtures(values)…'
//
// in the sales-estimates repo.
func TestServerRecordedFixtures(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "snapping.json"))
	if err != nil {
		t.Fatalf("read recorded fixtures: %v", err)
	}
	var recorded []struct {
		Name  string             `json:"name"`
		Scale []api.ScaleStep    `json:"scale"`
		Cases []api.SnappingCase `json:"cases"`
	}
	if err := json.Unmarshal(raw, &recorded); err != nil {
		t.Fatalf("decode recorded fixtures: %v", err)
	}
	if len(recorded) == 0 {
		t.Fatal("no recorded scales")
	}

	for _, scenario := range recorded {
		t.Run(scenario.Name, func(t *testing.T) {
			if len(scenario.Cases) == 0 {
				t.Fatal("recorded scale carries no cases")
			}
			failures := FixtureFailures(api.Snapping{Scale: scenario.Scale, Cases: scenario.Cases})
			for _, failure := range failures {
				t.Errorf("disagrees with the server: %s", failure)
			}
		})
	}
}
