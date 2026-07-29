package diffx

import (
	"strings"

	"charm.land/lipgloss/v2"
)

var (
	addWordStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Reverse(true)
	delWordStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Reverse(true)
)

// pairThreshold is how much two lines must have in common before one is
// treated as a rewrite of the other. Highlighting unrelated lines against
// each other is worse than not highlighting at all: it paints most of both
// lines and buries the real edit.
const pairThreshold = 0.5

// ColorizeIntraline styles a unified diff like Colorize, and additionally
// highlights the changed WORDS within each removed line and the added line
// it became — the eye lands on the edit, not the line.
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
			out = append(out, renderRun(removed, added)...)
			i += len(removed) + len(added) - 1
		case strings.HasPrefix(line, "+"):
			out = append(out, addStyle.Render(line))
		default:
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n") + "\n"
}

// renderRun emits one removed/added run in unified order — every '-' then
// every '+' — with word highlighting on the lines that paired up and plain
// colour on the ones that did not. Runs are routinely lopsided (one line
// rewritten while two more are appended), so an equal-length rule gives up
// on exactly the edits worth highlighting.
func renderRun(removed, added []string) []string {
	pairs := pairRuns(removed, added)
	partnerOf := make(map[int]int, len(pairs))
	for old, new := range pairs {
		partnerOf[new] = old
	}

	out := make([]string, 0, len(removed)+len(added))
	for i, line := range removed {
		j, paired := pairs[i]
		if !paired {
			out = append(out, delStyle.Render(line))
			continue
		}
		out = append(out, delStyle.Render("-")+highlight(line[1:], added[j][1:], delStyle, delWordStyle))
	}
	for j, line := range added {
		i, paired := partnerOf[j]
		if !paired {
			out = append(out, addStyle.Render(line))
			continue
		}
		out = append(out, addStyle.Render("+")+highlight(line[1:], removed[i][1:], addStyle, addWordStyle))
	}
	return out
}

// pairRuns matches each removed line to the added line it most likely
// became, strongest match first so a good pair is never stolen by a weaker
// one earlier in the run. Lines that clear no match stay unpaired.
func pairRuns(removed, added []string) map[int]int {
	type candidate struct {
		old, new int
		score    float64
	}
	var ranked []candidate
	for i, oldLine := range removed {
		for j, newLine := range added {
			if score := relatedness(oldLine[1:], newLine[1:]); score >= pairThreshold {
				ranked = append(ranked, candidate{i, j, score})
			}
		}
	}
	// Insertion sort, descending: runs are a handful of lines, and keeping
	// it stable leaves equally-good matches in document order.
	for i := 1; i < len(ranked); i++ {
		for j := i; j > 0 && ranked[j].score > ranked[j-1].score; j-- {
			ranked[j], ranked[j-1] = ranked[j-1], ranked[j]
		}
	}

	pairs := make(map[int]int)
	takenNew := make(map[int]bool)
	for _, c := range ranked {
		if _, used := pairs[c.old]; used || takenNew[c.new] {
			continue
		}
		pairs[c.old] = c.new
		takenNew[c.new] = true
	}
	return pairs
}

// relatedness scores two lines as candidates for the same line, rewritten.
// Whole-word overlap alone is too coarse for short lines — "Be kind."
// becoming "Be kind and specific." shares one whole word out of six — so a
// long common prefix or suffix counts too: edits are localised, and
// unrelated lines rarely start or end alike.
func relatedness(oldLine, newLine string) float64 {
	return max(similarity(oldLine, newLine), affinity(oldLine, newLine))
}

// affinity is how much of the shorter line survives at its two edges.
func affinity(a, b string) float64 {
	ra, rb := []rune(a), []rune(b)
	shorter := min(len(ra), len(rb))
	if shorter == 0 {
		return 0
	}
	prefix := 0
	for prefix < shorter && ra[prefix] == rb[prefix] {
		prefix++
	}
	// Bounded by what the prefix left, so a line cannot count twice.
	suffix := 0
	for suffix < shorter-prefix && ra[len(ra)-1-suffix] == rb[len(rb)-1-suffix] {
		suffix++
	}
	return float64(prefix+suffix) / float64(shorter)
}

// similarity is the share of words two lines agree on, in order. Whitespace
// is ignored: lines whose only common ground is the spaces between their
// words have nothing in common.
func similarity(oldLine, newLine string) float64 {
	a, b := words(oldLine), words(newLine)
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	keepA, _ := lcsKeep(a, b)
	common := 0
	for _, kept := range keepA {
		if kept {
			common++
		}
	}
	return 2 * float64(common) / float64(len(a)+len(b))
}

// highlight renders line against its partner: the whole line carries its
// add/remove colour, and only the differing words are reversed out.
func highlight(line, partner string, base, changed lipgloss.Style) string {
	tokens, partnerTokens := tokenize(line), tokenize(partner)
	keep, _ := lcsKeep(tokens, partnerTokens)

	changedAt := make([]bool, len(tokens))
	for i := range tokens {
		changedAt[i] = !keep[i]
	}
	// Whitespace is only part of the change when it sits INSIDE one: the
	// gap between two rewritten words belongs to the rewrite, while the
	// space after a single edited word is just a gap, and marking it smears
	// a block into the untouched text. Neighbours are read from the
	// original flags so a run of spaces cannot cascade.
	original := append([]bool(nil), changedAt...)
	for i, token := range tokens {
		if strings.TrimSpace(token) != "" {
			continue
		}
		changedAt[i] = i > 0 && i < len(tokens)-1 && original[i-1] && original[i+1]
	}

	// Coalesce neighbours that share a style before rendering: styling each
	// token on its own is visually identical but emits an escape sequence
	// per word, which bloats every frame and every golden.
	var out strings.Builder
	var run strings.Builder
	runChanged := false
	flush := func() {
		if run.Len() == 0 {
			return
		}
		if runChanged {
			out.WriteString(changed.Render(run.String()))
		} else {
			out.WriteString(base.Render(run.String()))
		}
		run.Reset()
	}
	for i, token := range tokens {
		if changedAt[i] != runChanged {
			flush()
			runChanged = changedAt[i]
		}
		run.WriteString(token)
	}
	flush()
	return out.String()
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

// tokenize splits into word+whitespace units. Tokens keep their spacing so
// joins reproduce the original line exactly.
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

// words is tokenize without the whitespace runs — what "how alike are these
// two lines" has to be measured on.
func words(s string) []string {
	var out []string
	for _, token := range tokenize(s) {
		if strings.TrimSpace(token) != "" {
			out = append(out, token)
		}
	}
	return out
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
