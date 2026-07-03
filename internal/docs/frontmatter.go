// Package docs holds the shared rendering helpers the docs generators
// (cmd/magus-docs, cmd/magus-spelldocs) use to emit the committed Markdown under
// docs/**. Keeping the frontmatter block in one place means both generators write
// the same YAML the site's front-matter parser expects, so a fix here (quoting
// rules, key order) lands in every generated page at once.
package docs

import (
	"fmt"
	"strings"
)

// WriteFrontmatter emits the site's YAML frontmatter block at the top of a
// generated docs page. Values that contain a colon are quoted to keep YAML
// parsers from reading the second half as a nested mapping.
func WriteFrontmatter(b *strings.Builder, title, description string, tags []string) {
	b.WriteString("---\n")
	fmt.Fprintf(b, "title: %s\n", YAMLScalar(title))
	fmt.Fprintf(b, "description: %s\n", YAMLScalar(description))
	b.WriteString("tags: [")
	for i, t := range tags {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(YAMLScalar(t))
	}
	b.WriteString("]\n---\n\n")
}

// YAMLScalar quotes a scalar when it would otherwise confuse a YAML parser: a
// colon reads as a mapping, a quote as a string delimiter, and leading/trailing
// spaces are trimmed by the parser unless quoted.
func YAMLScalar(s string) string {
	if !strings.ContainsAny(s, ":\"'") && (len(s) == 0 || (s[0] != ' ' && s[len(s)-1] != ' ')) {
		return s
	}
	return "\"" + strings.ReplaceAll(s, "\"", "\\\"") + "\""
}
