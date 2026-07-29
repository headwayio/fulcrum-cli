package diffx

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// JSONChange is one JSON-path-level difference.
type JSONChange struct {
	Path string
	Kind string // "added", "removed", "changed"
	Old  string // compact-rendered, "" when added
	New  string // compact-rendered, "" when removed
}

// JSONStructural diffs two JSON documents by path — the reviewer-shaped view
// for structured documents, where a unified text diff drowns the signal in
// syntax. Arrays compare index-wise; unparseable input returns an error and
// callers fall back to the text diff.
func JSONStructural(before, after []byte) ([]JSONChange, error) {
	var oldDoc, newDoc any
	if err := json.Unmarshal(before, &oldDoc); err != nil {
		return nil, fmt.Errorf("base is not JSON: %w", err)
	}
	if err := json.Unmarshal(after, &newDoc); err != nil {
		return nil, fmt.Errorf("local is not JSON: %w", err)
	}
	var changes []JSONChange
	walkJSON("", oldDoc, newDoc, &changes)
	return changes, nil
}

func walkJSON(path string, oldVal, newVal any, changes *[]JSONChange) {
	oldMap, oldIsMap := oldVal.(map[string]any)
	newMap, newIsMap := newVal.(map[string]any)
	if oldIsMap && newIsMap {
		keys := map[string]bool{}
		for k := range oldMap {
			keys[k] = true
		}
		for k := range newMap {
			keys[k] = true
		}
		sorted := make([]string, 0, len(keys))
		for k := range keys {
			sorted = append(sorted, k)
		}
		sort.Strings(sorted)
		for _, k := range sorted {
			childPath := k
			if path != "" {
				childPath = path + "." + k
			}
			oldChild, inOld := oldMap[k]
			newChild, inNew := newMap[k]
			switch {
			case !inOld:
				*changes = append(*changes, JSONChange{Path: childPath, Kind: "added", New: compact(newChild)})
			case !inNew:
				*changes = append(*changes, JSONChange{Path: childPath, Kind: "removed", Old: compact(oldChild)})
			default:
				walkJSON(childPath, oldChild, newChild, changes)
			}
		}
		return
	}

	oldArr, oldIsArr := oldVal.([]any)
	newArr, newIsArr := newVal.([]any)
	if oldIsArr && newIsArr {
		for i := 0; i < len(oldArr) || i < len(newArr); i++ {
			childPath := fmt.Sprintf("%s[%d]", path, i)
			switch {
			case i >= len(oldArr):
				*changes = append(*changes, JSONChange{Path: childPath, Kind: "added", New: compact(newArr[i])})
			case i >= len(newArr):
				*changes = append(*changes, JSONChange{Path: childPath, Kind: "removed", Old: compact(oldArr[i])})
			default:
				walkJSON(childPath, oldArr[i], newArr[i], changes)
			}
		}
		return
	}

	if compact(oldVal) != compact(newVal) {
		*changes = append(*changes, JSONChange{Path: path, Kind: "changed",
			Old: compact(oldVal), New: compact(newVal)})
	}
}

// RenderJSONChanges styles a change list, one line per change.
func RenderJSONChanges(changes []JSONChange) []string {
	if len(changes) == 0 {
		return []string{"no structural changes"}
	}
	lines := make([]string, 0, len(changes))
	for _, c := range changes {
		switch c.Kind {
		case "added":
			lines = append(lines, addStyle.Render("+ "+c.Path+" = "+c.New))
		case "removed":
			lines = append(lines, delStyle.Render("- "+c.Path+" = "+c.Old))
		default:
			lines = append(lines, hunkStyle.Render("~ "+c.Path)+"  "+
				delStyle.Render(c.Old)+" → "+addStyle.Render(c.New))
		}
	}
	return lines
}

// ConflictPaths returns the paths touched by BOTH change lists — the actual
// conflicts in a three-way view.
func ConflictPaths(ours, theirs []JSONChange) []string {
	theirPaths := map[string]bool{}
	for _, c := range theirs {
		theirPaths[c.Path] = true
	}
	var overlap []string
	for _, c := range ours {
		if theirPaths[c.Path] {
			overlap = append(overlap, c.Path)
		}
	}
	return overlap
}

func compact(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	s := string(raw)
	if len(s) > 80 {
		s = s[:77] + "…"
	}
	return strings.ReplaceAll(s, "\n", " ")
}
