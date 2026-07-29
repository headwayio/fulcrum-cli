package diffx

import (
	"strings"

	"charm.land/lipgloss/v2"
)

var (
	addWordStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Reverse(true)
	delWordStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Reverse(true)
)

// ColorizeIntraline styles a unified diff like Colorize, and additionally
// highlights the changed WORDS when a removed run pairs 1:1 with an added
// run — the eye lands on the edit, not the line.
func ColorizeIntraline(unified string) string {
	if unified == "" {
		return ""
	}
	lines := strings.Split(strings.TrimSuffix(unified, "\n"), "\n")
	out := make([]string, 0, len(lines))

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		switch {
		case strings.HasPrefix(line, "+++"), strings.HasPrefix(line, "---"):
			out = append(out, headerStyle.Render(line))
		case strings.HasPrefix(line, "@@"):
			out = append(out, hunkStyle.Render(line))
		case strings.HasPrefix(line, "-"):
			removed, added := collectRuns(lines, i)
			if len(removed) == len(added) && len(added) > 0 {
				for j := range removed {
					delLine, addLine := wordDiffPair(removed[j][1:], added[j][1:])
					out = append(out, delStyle.Render("-")+delLine)
					removed[j] = ""          // consumed
					added[j] = "+" + addLine // stash styled
				}
				for _, a := range added {
					out = append(out, addStyle.Render("+")+a[1:])
				}
				i += len(removed)*2 - 1
			} else {
				out = append(out, delStyle.Render(line))
			}
		case strings.HasPrefix(line, "+"):
			out = append(out, addStyle.Render(line))
		default:
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n") + "\n"
}

// collectRuns gathers the contiguous '-' run starting at i and the '+' run
// immediately after it.
func collectRuns(lines []string, i int) (removed, added []string) {
	j := i
	for j < len(lines) && strings.HasPrefix(lines[j], "-") && !strings.HasPrefix(lines[j], "---") {
		removed = append(removed, lines[j])
		j++
	}
	for j < len(lines) && strings.HasPrefix(lines[j], "+") && !strings.HasPrefix(lines[j], "+++") {
		added = append(added, lines[j])
		j++
	}
	return removed, added
}

// wordDiffPair renders old/new with changed words highlighted, via a
// token-level LCS. Tokens keep their trailing spaces so joins reproduce the
// original spacing exactly.
func wordDiffPair(oldLine, newLine string) (string, string) {
	oldTokens, newTokens := tokenize(oldLine), tokenize(newLine)
	keepOld, keepNew := lcsKeep(oldTokens, newTokens)

	var oldOut, newOut strings.Builder
	for i, token := range oldTokens {
		if keepOld[i] {
			oldOut.WriteString(token)
		} else {
			oldOut.WriteString(delWordStyle.Render(token))
		}
	}
	for i, token := range newTokens {
		if keepNew[i] {
			newOut.WriteString(token)
		} else {
			newOut.WriteString(addWordStyle.Render(token))
		}
	}
	return delStyle.Render("") + oldOut.String(), newOut.String()
}

// tokenize splits into word+whitespace units.
func tokenize(s string) []string {
	var tokens []string
	var current strings.Builder
	inSpace := false
	for _, r := range s {
		isSpace := r == ' ' || r == '\t'
		if current.Len() > 0 && isSpace != inSpace {
			tokens = append(tokens, current.String())
			current.Reset()
		}
		inSpace = isSpace
		current.WriteRune(r)
	}
	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}
	return tokens
}

// lcsKeep marks the tokens on each side that belong to the longest common
// subsequence — everything else is a changed word.
func lcsKeep(a, b []string) (keepA, keepB []bool) {
	table := make([][]int, len(a)+1)
	for i := range table {
		table[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				table[i][j] = table[i+1][j+1] + 1
			} else {
				table[i][j] = max(table[i+1][j], table[i][j+1])
			}
		}
	}
	keepA, keepB = make([]bool, len(a)), make([]bool, len(b))
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			keepA[i], keepB[j] = true, true
			i++
			j++
		case table[i+1][j] >= table[i][j+1]:
			i++
		default:
			j++
		}
	}
	return keepA, keepB
}
