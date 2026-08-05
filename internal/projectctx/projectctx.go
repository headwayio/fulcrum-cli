// Package projectctx owns the .fulcrum directory a checkout carries, and
// answers the question that only a process running IN that checkout can:
// which Fulcrum project is this?
//
// It exists as its own package because two faces need it and neither may
// import the other — internal/cli writes the directory, internal/mcpserver
// reads it to resolve a project without the model having to name one.
package projectctx

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/headwayio/fulcrum-cli/internal/skillmd"
)

const (
	// Dir is where the bundle lands inside the project being estimated. A
	// directory rather than a dotfile because the skill writes its survey and
	// draft alongside it, and one gitignore line should cover the lot.
	Dir = ".fulcrum"

	ContextFile  = "project-context.md"
	SnappingFile = "snapping.json"
)

// Local is what a checkout knows about itself.
type Local struct {
	ProjectID   int64
	ProjectName string
	// Digest of the bundle this directory was written from, so a caller can
	// say how stale the local copy is.
	Digest string
	// Root is the checkout directory, not the .fulcrum directory inside it.
	Root string
}

// Resolve walks up from dir looking for a .fulcrum/project-context.md and
// reads the project out of its frontmatter.
//
// Walking up rather than checking only dir: a harness is often launched from
// a subdirectory of the repository, and "no project here" would be wrong and
// confusing when the answer is two levels up. Returns nil with no error when
// there is genuinely no context — an un-provisioned checkout is an ordinary
// state, not a failure.
func Resolve(dir string) (*Local, error) {
	start, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}

	for current := start; ; {
		path := filepath.Join(current, Dir, ContextFile)
		if content, readErr := os.ReadFile(path); readErr == nil {
			return parse(content, current), nil
		}

		parent := filepath.Dir(current)
		if parent == current {
			return nil, nil // reached the filesystem root
		}
		current = parent
	}
}

func parse(content []byte, root string) *Local {
	frontmatter, _, ok := skillmd.Split(string(content))
	if !ok {
		return nil
	}

	id, err := strconv.ParseInt(strings.TrimSpace(skillmd.Field(frontmatter, "project_id")), 10, 64)
	if err != nil {
		return nil
	}

	return &Local{
		ProjectID:   id,
		ProjectName: skillmd.Field(frontmatter, "project"),
		Digest:      skillmd.Field(frontmatter, "digest"),
		Root:        root,
	}
}
