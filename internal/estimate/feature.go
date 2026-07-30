package estimate

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/headwayio/fulcrum-cli/internal/api"
)

// Payload is a features-json block — byte-compatible with what Fulcrum's
// estimation chat emits, so a locally-produced feature is the same artifact
// the web UI produces and can be pushed straight back into a project.
type Payload struct {
	Action   string    `json:"action"`
	Features []Feature `json:"features"`
}

// Feature is one proposed feature.
type Feature struct {
	ID             string      `json:"id"`
	Name           string      `json:"name"`
	Description    string      `json:"description"`
	PRD            string      `json:"prd,omitempty"`
	MoscowPriority string      `json:"moscow_priority"`
	Release        string      `json:"release,omitempty"`
	Estimates      []RoleEntry `json:"estimates"`
}

// RoleEntry is one role's estimate for a feature.
type RoleEntry struct {
	Role     string   `json:"role"`
	Estimate Estimate `json:"estimate"`
	Split    *Split   `json:"split,omitempty"`
}

// Split is the optional parallelization hint for large estimates.
type Split struct {
	Count     int    `json:"count"`
	Rationale string `json:"rationale"`
}

// Estimate is the structured estimate contract. The committed value is
// absent by design: it is derived, never supplied.
type Estimate struct {
	Low                 float64             `json:"low"`
	Likely              float64             `json:"likely"`
	High                float64             `json:"high"`
	Confidence          string              `json:"confidence"`
	ComponentsIncluded  []string            `json:"components_included"`
	ComponentsExcluded  []ExcludedComponent `json:"components_excluded"`
	Assumptions         []string            `json:"assumptions"`
	Exclusions          []string            `json:"exclusions"`
	Unknowns            []string            `json:"unknowns"`
	Dependencies        []string            `json:"dependencies"`
	Risks               []string            `json:"risks"`
	ReuseOrModification string              `json:"reuse_or_modification"`
	Rationale           string              `json:"rationale"`
}

// ExcludedComponent is a rubric component deliberately left out, with the
// reason. The rubric's coverage requirement is why the reason is mandatory:
// silence must never be mistaken for absence of work.
type ExcludedComponent struct {
	Component string `json:"component"`
	Reason    string `json:"reason"`
}

// Parse reads a features-json payload. Accepts either a bare payload or one
// still wrapped in the ```features-json fence the model emits, so a draft
// can be pasted straight out of an agent's output.
func Parse(raw []byte) (*Payload, error) {
	trimmed := strings.TrimSpace(string(raw))
	if fenced := extractFence(trimmed); fenced != "" {
		trimmed = fenced
	}

	payload := &Payload{}
	if err := json.Unmarshal([]byte(trimmed), payload); err != nil {
		return nil, fmt.Errorf("not a features-json payload: %w", err)
	}
	if payload.Action == "" {
		payload.Action = "add"
	}
	return payload, nil
}

func extractFence(s string) string {
	start := strings.Index(s, "```features-json")
	if start < 0 {
		return ""
	}
	rest := s[start+len("```features-json"):]
	end := strings.Index(rest, "```")
	if end < 0 {
		return ""
	}
	return strings.TrimSpace(rest[:end])
}

// Validate reports structural problems with a payload.
//
// Deliberately structural only: presence of the contract's fields, a
// monotonic range, positive hours. It does NOT check that a confidence level
// or component key exists in the organization's rubric — that vocabulary
// lives in the rubric document and the server validates it authoritatively
// through EstimateContract.for_rubric. Duplicating a rubric parse here would
// create a second opinion about what the vocabulary is, which is exactly the
// class of drift this whole design exists to prevent.
func (p *Payload) Validate() []string {
	var problems []string
	if len(p.Features) == 0 {
		return []string{"payload contains no features"}
	}

	for _, feature := range p.Features {
		label := feature.Name
		if label == "" {
			label = feature.ID
		}
		if label == "" {
			label = "(unnamed feature)"
		}
		if feature.Name == "" {
			problems = append(problems, fmt.Sprintf("%s: name is required", label))
		}
		if feature.ID == "" {
			problems = append(problems, fmt.Sprintf("%s: id is required", label))
		}
		if feature.Description == "" {
			problems = append(problems, fmt.Sprintf("%s: description is required", label))
		}
		if !validPriority(feature.MoscowPriority) {
			problems = append(problems, fmt.Sprintf(
				"%s: moscow_priority %q must be must_have, should_have, could_have or wont_have",
				label, feature.MoscowPriority))
		}
		if len(feature.Estimates) == 0 {
			problems = append(problems, fmt.Sprintf("%s: no role estimates", label))
		}
		for _, entry := range feature.Estimates {
			problems = append(problems, entry.problems(label)...)
		}
	}
	return problems
}

func (e RoleEntry) problems(feature string) []string {
	var problems []string
	where := fmt.Sprintf("%s / %s", feature, e.Role)
	if e.Role == "" {
		where = feature + " / (unnamed role)"
		problems = append(problems, where+": role is required")
	}

	est := e.Estimate
	switch {
	case est.Low <= 0 || est.Likely <= 0 || est.High <= 0:
		problems = append(problems, where+": low, likely and high must all be greater than zero")
	case est.Low > est.Likely:
		problems = append(problems, fmt.Sprintf("%s: low (%g) must not exceed likely (%g)", where, est.Low, est.Likely))
	case est.Likely > est.High:
		problems = append(problems, fmt.Sprintf("%s: likely (%g) must not exceed high (%g)", where, est.Likely, est.High))
	}

	if est.Confidence == "" {
		problems = append(problems, where+": confidence is required")
	}
	if est.ReuseOrModification == "" {
		problems = append(problems, where+": reuse_or_modification is required")
	}
	if strings.TrimSpace(est.Rationale) == "" {
		problems = append(problems, where+": rationale is required")
	}
	// The rubric's coverage requirement: every component is either included
	// or excluded WITH a reason, so silence is never read as absence of work.
	if len(est.ComponentsIncluded) == 0 {
		problems = append(problems, where+": components_included must name at least one rubric component")
	}
	for _, excluded := range est.ComponentsExcluded {
		if strings.TrimSpace(excluded.Reason) == "" {
			problems = append(problems, fmt.Sprintf("%s: component %q is excluded without a reason", where, excluded.Component))
		}
	}
	return problems
}

func validPriority(p string) bool {
	switch p {
	case "must_have", "should_have", "could_have", "wont_have":
		return true
	}
	return false
}

// Computed is one role's derived result, ready to display.
type Computed struct {
	Role       string
	Low        float64
	Likely     float64
	High       float64
	Expected   float64
	Sigma      float64
	Committed  string // the scale label the expected value snapped to
	Confidence string
}

// FeatureResult is a feature with its derived per-role numbers.
type FeatureResult struct {
	Feature  Feature
	Roles    []Computed
	Expected float64
	Sigma    float64
}

// Compute derives the committed value for every estimate and snaps it to the
// scale. This is the step that must agree with the server exactly.
func (p *Payload) Compute(scale []api.ScaleStep) []FeatureResult {
	results := make([]FeatureResult, 0, len(p.Features))
	for _, feature := range p.Features {
		result := FeatureResult{Feature: feature}
		var sigmas []float64
		for _, entry := range feature.Estimates {
			est := entry.Estimate
			expected := Expected(est.Low, est.Likely, est.High)
			sigma := Sigma(est.Low, est.High)
			sigmas = append(sigmas, sigma)
			result.Expected += expected
			result.Roles = append(result.Roles, Computed{
				Role:       entry.Role,
				Low:        est.Low,
				Likely:     est.Likely,
				High:       est.High,
				Expected:   expected,
				Sigma:      sigma,
				Committed:  Snap(expected, scale),
				Confidence: est.Confidence,
			})
		}
		result.Sigma = ProjectSigma(sigmas)
		sort.SliceStable(result.Roles, func(i, j int) bool { return result.Roles[i].Role < result.Roles[j].Role })
		results = append(results, result)
	}
	return results
}
