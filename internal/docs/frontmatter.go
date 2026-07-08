// Package docs holds the shared rendering helpers the docs generators
// (cmd/magus-docs, cmd/magus-spelldocs) use to emit the committed Markdown under
// docs/**. Keeping the frontmatter block in one place means both generators emit
// the same YAML the site's parser expects, so a fix here (quoting rules, key
// order) lands in every generated page at once.
package docs

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// Frontmatter is the frontmatter a generated docs page carries. Title and Tags are
// always emitted; PageType and Aliases only when set. Key order is fixed (title,
// page_type, aliases, description, tags) so regenerated output stays byte-stable.
// The yaml tags let ParseFrontmatter read back a block WriteFrontmatter emitted.
type Frontmatter struct {
	Title       string   `yaml:"title"`
	PageType    string   `yaml:"page_type"` // "overview" for hub/index pages; "" otherwise
	Aliases     []string `yaml:"aliases"`   // old clean URLs that should redirect here (parity on a move)
	Description string   `yaml:"description"`
	Tags        []string `yaml:"tags"`
}

// ParseFrontmatter reads a leading YAML frontmatter block (a "---" line, the YAML
// body, then a closing "---" line) off a markdown document, returning the parsed
// fields and ok=true. A document with no leading block, or one whose YAML does not
// parse, yields a zero Frontmatter and ok=false - callers treat frontmatter as
// best-effort metadata, never a hard error. The two failure modes (no block present
// vs. a present-but-malformed block) deliberately collapse to the same ok=false:
// the sole caller wants the fields or nothing, and cares about neither reason. It is
// the read counterpart to WriteFrontmatter, kept here so both halves of the format
// live together.
func ParseFrontmatter(content string) (Frontmatter, bool) {
	// A frontmatter block must open on the very first line. Tolerate a UTF-8 BOM
	// and either newline style, but nothing else before the fence.
	rest := strings.TrimPrefix(content, "\ufeff")
	if !strings.HasPrefix(rest, "---\n") && !strings.HasPrefix(rest, "---\r\n") {
		return Frontmatter{}, false
	}
	nl := strings.IndexByte(rest, '\n')
	body := rest[nl+1:]
	// The closing fence is a line that is exactly "---" (its own YAML would read a
	// bare "---" as a document separator, so scan lines rather than yaml-parse).
	end := -1
	for off := 0; off < len(body); {
		nlAt := strings.IndexByte(body[off:], '\n')
		line := body[off:]
		if nlAt >= 0 {
			line = line[:nlAt]
		}
		if strings.TrimRight(line, "\r") == "---" {
			end = off
			break
		}
		if nlAt < 0 {
			break
		}
		off += nlAt + 1
	}
	if end < 0 {
		return Frontmatter{}, false
	}
	var f Frontmatter
	if err := yaml.Unmarshal([]byte(body[:end]), &f); err != nil {
		return Frontmatter{}, false
	}
	return f, true
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
