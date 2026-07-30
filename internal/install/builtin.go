package install

import (
	"embed"
	"path/filepath"
	"sort"
	"strings"

	"github.com/headwayio/fulcrum-cli/internal/skillmd"
	"github.com/headwayio/fulcrum-cli/internal/state"
)

//go:embed skills/*.md
var builtinFS embed.FS

// Builtin skills ship with the client rather than with the organization's
// knowledge, because they describe how to drive THIS binary — `fulcrum
// context`, `fulcrum estimate`, `fulcrum feature push`. Versioning them with
// the commands they invoke is the only way the instructions and the tool
// cannot disagree.
//
// An organization that publishes a skill under the same slug wins: a team's
// own guidance outranks a client default, and installing both under one name
// would leave the harness reading two conflicting files.
func builtinSkills() []skill {
	entries, err := builtinFS.ReadDir("skills")
	if err != nil {
		return nil
	}

	var skills []skill
	for _, entry := range entries {
		content, readErr := builtinFS.ReadFile(filepath.Join("skills", entry.Name()))
		if readErr != nil {
			continue
		}
		slug := strings.TrimSuffix(entry.Name(), ".md")
		name, description := skillmd.Describe(string(content), slug)
		skills = append(skills, skill{
			slug: slug, digest: state.HexSHA256(content),
			name: name, description: description, content: content,
		})
	}
	sort.Slice(skills, func(i, j int) bool { return skills[i].slug < skills[j].slug })
	return skills
}

// withBuiltins adds the client's own skills to the synced set, letting an
// organization's skill of the same slug take precedence.
func withBuiltins(synced []skill) []skill {
	taken := make(map[string]bool, len(synced))
	for _, s := range synced {
		taken[s.slug] = true
	}
	for _, s := range builtinSkills() {
		if !taken[s.slug] {
			synced = append(synced, s)
		}
	}
	return synced
}
