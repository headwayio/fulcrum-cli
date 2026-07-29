package diffx

import (
	"bytes"
	"strings"
)

// Conflict markers, in git's default style so every editor and merge tool
// already understands them.
const (
	MarkerOurs   = "<<<<<<< your local edits"
	MarkerSplit  = "======="
	MarkerTheirs = ">>>>>>> the current version in Fulcrum"
)

// HasConflictMarkers reports whether content still carries unresolved merge
// markers. Only the angle-bracket markers are tested: a bare ======= line is
// a legitimate markdown setext heading underline.
func HasConflictMarkers(content []byte) bool {
	for _, line := range strings.Split(string(content), "\n") {
		if strings.HasPrefix(line, "<<<<<<<") || strings.HasPrefix(line, ">>>>>>>") {
			return true
		}
	}
	return false
}

// Merge3 performs a three-way line merge — the same shape git uses, over the
// three snapshots the workspace already keeps: the pristine copy from the
// last sync (base), the working file (ours), and what the server has now
// (theirs). Regions only one side touched are taken silently; regions both
// sides touched identically collapse to one copy; genuine overlaps are
// written with conflict markers. Returns the merged bytes and how many
// conflicts they contain.
func Merge3(base, ours, theirs []byte) ([]byte, int) {
	baseLines := splitLines(base)
	ourLines := splitLines(ours)
	theirLines := splitLines(theirs)

	oursOf := alignLines(baseLines, ourLines)
	theirsOf := alignLines(baseLines, theirLines)

	var merged []string
	conflicts := 0
	b, o, t := 0, 0, 0

	for {
		// The next line all three agree on, and that no cursor has passed.
		stable := -1
		for k := b; k < len(baseLines); k++ {
			if oursOf[k] >= o && theirsOf[k] >= t {
				stable = k
				break
			}
		}
		if stable == -1 {
			lines, conflicted := resolveRegion(baseLines[b:], ourLines[o:], theirLines[t:])
			merged = append(merged, lines...)
			if conflicted {
				conflicts++
			}
			break
		}

		no, nt := oursOf[stable], theirsOf[stable]
		if stable > b || no > o || nt > t {
			lines, conflicted := resolveRegion(baseLines[b:stable], ourLines[o:no], theirLines[t:nt])
			merged = append(merged, lines...)
			if conflicted {
				conflicts++
			}
		}
		merged = append(merged, baseLines[stable])
		b, o, t = stable+1, no+1, nt+1
	}

	return []byte(strings.Join(merged, "\n")), conflicts
}

// resolveRegion decides one divergent stretch.
func resolveRegion(base, ours, theirs []string) ([]string, bool) {
	switch {
	case equalLines(ours, base):
		return theirs, false // only the server moved
	case equalLines(theirs, base):
		return ours, false // only we moved
	case equalLines(ours, theirs):
		return ours, false // both made the same edit
	}

	region := make([]string, 0, len(ours)+len(theirs)+3)
	region = append(region, MarkerOurs)
	region = append(region, ours...)
	region = append(region, MarkerSplit)
	region = append(region, theirs...)
	region = append(region, MarkerTheirs)
	return region, true
}

// alignLines maps each line of a to its LCS-matched line in b, or -1. The
// mapping is monotonic, which is what lets the merge walk all three files
// with a single forward pass.
func alignLines(a, b []string) []int {
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

	matches := make([]int, len(a))
	for i := range matches {
		matches[i] = -1
	}
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			matches[i] = j
			i++
			j++
		case table[i+1][j] >= table[i][j+1]:
			i++
		default:
			j++
		}
	}
	return matches
}

func splitLines(content []byte) []string {
	if len(content) == 0 {
		return nil
	}
	return strings.Split(string(bytes.ReplaceAll(content, []byte("\r\n"), []byte("\n"))), "\n")
}

func equalLines(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
