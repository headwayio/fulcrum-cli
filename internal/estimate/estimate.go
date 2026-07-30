// Package estimate carries the arithmetic a local estimate shares with
// Fulcrum: PERT derivation of the committed value, and snapping that value
// onto the project's complexity scale.
//
// None of this runs in the model. The LLM's job is judgment — the low,
// likely and high of a range, and the reasoning behind them. Turning that
// range into the number a project is scheduled and costed from is
// arithmetic, and arithmetic done by a language model is the weakest link in
// an otherwise reproducible chain.
//
// The rule here mirrors Estimation::ComplexitySnapper on the server. It is
// held to that by a test that replays the server's own generated fixtures
// (see FixtureFailures), so a divergence fails a build instead of quietly
// producing estimates one size off.
package estimate

import (
	"fmt"
	"math"
	"sort"

	"github.com/headwayio/fulcrum-cli/internal/api"
)

// Expected is the committed value derived from a three-point range:
// (low + 4*likely + high) / 6. Never supplied by the generator, so an
// estimate cannot commit to a number its own range does not support.
func Expected(low, likely, high float64) float64 {
	return (low + 4*likely + high) / 6.0
}

// Sigma is the standard deviation of one estimate: (high - low) / 6.
func Sigma(low, high float64) float64 {
	return (high - low) / 6.0
}

// ProjectSigma combines per-estimate sigmas. Variance adds; standard
// deviations do not — summing every low and every high would model all work
// going badly at once and yield a range too wide to be useful.
func ProjectSigma(sigmas []float64) float64 {
	var variance float64
	for _, s := range sigmas {
		variance += s * s
	}
	return math.Sqrt(variance)
}

// Snap maps hours onto the nearest scale label. Exact match wins; below the
// smallest label snaps to it and above the largest snaps to that; otherwise
// nearest neighbour, and an exact midpoint rounds UP — a value sitting
// precisely between two sizes is not evidence the work is small.
//
// Returns "" when there is no scale, leaving the fallback to the caller.
func Snap(hours float64, scale []api.ScaleStep) string {
	sorted := sortedScale(scale)
	if len(sorted) == 0 {
		return ""
	}

	for _, step := range sorted {
		if step.Hours == hours {
			return step.Label
		}
	}

	var lower, upper *api.ScaleStep
	for i := range sorted {
		switch {
		case sorted[i].Hours < hours:
			lower = &sorted[i]
		case sorted[i].Hours > hours && upper == nil:
			upper = &sorted[i]
		}
	}

	switch {
	case lower == nil:
		return sorted[0].Label
	case upper == nil:
		return sorted[len(sorted)-1].Label
	case hours-lower.Hours < upper.Hours-hours:
		return lower.Label
	default:
		return upper.Label
	}
}

// FixtureFailures replays the server's generated snapping table against this
// implementation and returns a description of every disagreement. An empty
// result is the only acceptable outcome: the whole point of the fixtures is
// that "we implemented the same rule" stops being a claim and becomes a
// check.
func FixtureFailures(s api.Snapping) []string {
	var failures []string
	for _, c := range s.Cases {
		if got := Snap(c.Hours, s.Scale); got != c.Label {
			failures = append(failures,
				fmt.Sprintf("%gh: server says %q, this client says %q", c.Hours, c.Label, got))
		}
	}
	return failures
}

func sortedScale(scale []api.ScaleStep) []api.ScaleStep {
	sorted := make([]api.ScaleStep, 0, len(scale))
	sorted = append(sorted, scale...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Hours < sorted[j].Hours })
	return sorted
}
