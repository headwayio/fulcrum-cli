// Package skillmd reads the frontmatter-plus-markdown shape every skill
// document uses. Shared by the installer (which needs name and description
// to render harness formats) and the TUI reader (which lifts the
// frontmatter into its header).
package skillmd

import "strings"

// Split separates the frontmatter block from the body. ok is false when the
// document has no frontmatter, in which case body is the whole content.
func Split(content string) (frontmatter, body string, ok bool) {
	if !strings.HasPrefix(content, "---\n") {
		return "", content, false
	}
	rest := content[4:]
	end := strings.Index(rest, "\n---\n")
	if end < 0 {
		return "", content, false
	}
	return rest[:end], strings.TrimPrefix(rest[end+5:], "\n"), true
}

// Field reads a top-level scalar out of a frontmatter block. Deliberately
// not a YAML parser: skills declare flat name/description keys, and pulling
// in a parser to read two strings would be the tail wagging the dog.
func Field(frontmatter, key string) string {
	for _, line := range strings.Split(frontmatter, "\n") {
		name, value, found := strings.Cut(line, ":")
		if !found || strings.TrimSpace(name) != key {
			continue
		}
		return strings.Trim(strings.TrimSpace(value), `"'`)
	}
	return ""
}

// Describe returns the document's declared name and description, falling
// back to the slug when the frontmatter is missing or incomplete.
func Describe(content, slug string) (name, description string) {
	frontmatter, _, ok := Split(content)
	if !ok {
		return slug, ""
	}
	name = Field(frontmatter, "name")
	if name == "" {
		name = slug
	}
	return name, Field(frontmatter, "description")
}
