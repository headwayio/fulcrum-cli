package projectctx

import (
	"strconv"
	"strings"
)

// The brief get_feature returns opens with YAML frontmatter carrying the
// card's numeric ids, and a heading of the form "# FUL-17 — Dynamic field
// mapping". Both `fulcrum work` and the MCP bridge lift the ids from it, so
// the telemetry hook — which fires on its own and cannot ask — has them.

// BriefField reads a field out of the brief's YAML frontmatter, or "".
func BriefField(brief, key string) string {
	for _, line := range strings.Split(brief, "\n") {
		if line == "---" && strings.HasPrefix(brief, "---") {
			continue
		}
		name, value, found := strings.Cut(line, ":")
		if !found || strings.TrimSpace(name) != key {
			continue
		}
		return strings.TrimSpace(value)
	}
	return ""
}

// BriefID reads a numeric field out of the brief's YAML frontmatter. Best
// effort: 0 when the field is absent or not a number.
func BriefID(brief, key string) int64 {
	id, err := strconv.ParseInt(BriefField(brief, key), 10, 64)
	if err != nil {
		return 0
	}
	return id
}

// FeatureName lifts the card's name out of the brief's heading. Best effort:
// a pin is still useful with only the id.
func FeatureName(brief string) string {
	for _, line := range strings.Split(brief, "\n") {
		if !strings.HasPrefix(line, "# ") {
			continue
		}
		heading := strings.TrimPrefix(line, "# ")
		if _, after, found := strings.Cut(heading, "—"); found {
			return strings.TrimSpace(after)
		}
		return strings.TrimSpace(heading)
	}
	return ""
}
