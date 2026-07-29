// Package diffx renders unified diffs for both the TUI diff screen and the
// plain CLI. v1 is deliberately text-only — the structural JSON-path diff is
// a later iteration (see DECISIONS.md).
package diffx

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/aymanbagabas/go-udiff"
)

var (
	addStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	delStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	hunkStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	headerStyle = lipgloss.NewStyle().Bold(true)
)

// Unified returns a plain unified diff of before → after.
func Unified(name string, before, after []byte) string {
	return udiff.Unified("a/"+name, "b/"+name, string(before), string(after))
}

// Colorize styles a unified diff line-by-line. With the color profile forced
// to ASCII (tests, dumb terminals) the output degrades to the plain diff.
func Colorize(unified string) string {
	if unified == "" {
		return ""
	}
	lines := strings.Split(strings.TrimSuffix(unified, "\n"), "\n")
	for i, line := range lines {
		switch {
		case strings.HasPrefix(line, "+++"), strings.HasPrefix(line, "---"):
			lines[i] = headerStyle.Render(line)
		case strings.HasPrefix(line, "@@"):
			lines[i] = hunkStyle.Render(line)
		case strings.HasPrefix(line, "+"):
			lines[i] = addStyle.Render(line)
		case strings.HasPrefix(line, "-"):
			lines[i] = delStyle.Render(line)
		}
	}
	return strings.Join(lines, "\n") + "\n"
}
