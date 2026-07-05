// Package docs holds the shared rendering helpers the docs generators
// (cmd/magus-docs, cmd/magus-spelldocs) use to emit the committed Markdown under
// docs/**. Keeping the frontmatter block in one place means both generators emit
// the same YAML the site's parser expects, so a fix here (quoting rules, key
// order) lands in every generated page at once.
package docs

import (
	"fmt"
	"strings"
)

// Frontmatter is the frontmatter a generated docs page carries. Title and Tags are
// always emitted; PageType and Aliases only when set. Key order is fixed (title,
// page_type, aliases, description, tags) so regenerated output stays byte-stable.
type Frontmatter struct {
	Title       string
	PageType    string   // "overview" for hub/index pages; "" otherwise
	Aliases     []string // old clean URLs that should redirect here (parity on a move)
	Description string
	Tags        []string
}

// WriteFrontmatter emits the site's YAML frontmatter block. Values containing a
// colon, quote, or edge whitespace are quoted so a YAML parser can't misread
// them. A page with no page_type/aliases leaves those fields zero.
func WriteFrontmatter(b *strings.Builder, f Frontmatter) {
	b.WriteString("---\n")
	fmt.Fprintf(b, "title: %s\n", YAMLScalar(f.Title))
	if f.PageType != "" {
		fmt.Fprintf(b, "page_type: %s\n", YAMLScalar(f.PageType))
	}
	if len(f.Aliases) > 0 {
		b.WriteString("aliases: [")
		for i, a := range f.Aliases {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(YAMLScalar(a))
		}
		b.WriteString("]\n")
	}
	fmt.Fprintf(b, "description: %s\n", YAMLScalar(f.Description))
	b.WriteString("tags: [")
	for i, t := range f.Tags {
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
