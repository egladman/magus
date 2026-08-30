// Package docs holds the shared rendering helpers the docs generators
// (cmd/magus-docs, cmd/magus-spelldocs) use to emit the committed Markdown under
// docs/**. Keeping the frontmatter block in one place means both generators emit
// the same YAML the site's parser expects, so a fix here (quoting rules, key
// order) lands in every generated page at once.
package docs

import (
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Frontmatter is the frontmatter a generated docs page carries. Title and Tags are
// always emitted; PageType, GeneratedFrom, and Aliases only when set. Key order is
// fixed (title, page_type, generated_from, aliases, description, tags) so
// regenerated output stays byte-stable. The yaml tags let ParseFrontmatter read
// back a block WriteFrontmatter emitted.
type Frontmatter struct {
	Title    string `yaml:"title"`
	PageType string `yaml:"page_type"` // "overview" for hub/index pages; "" otherwise
	// GeneratedFrom names what this page was generated from, so the site can tell a
	// generated page from a hand-written one without guessing from its path, and can
	// point "Suggest an edit" at the real source instead of the generated .md. Either
	// a repo-relative path/glob ("internal/config/config.go", one or more comma-joined
	// globs) or an in-site section path ending in "/" ("reference/api/") for a page
	// whose true source has no single file - the renderer tells the two apart by that
	// trailing slash. "" for a hand-written page.
	GeneratedFrom string   `yaml:"generated_from"`
	Aliases       []string `yaml:"aliases"` // old clean URLs that should redirect here (parity on a move)
	Description   string   `yaml:"description"`
	Tags          []string `yaml:"tags"`
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

// StripFrontmatter returns content with any leading frontmatter block removed, so a caller
// parsing the markdown body (heading extraction, rendering) never sees the YAML header - and
// never mistakes the header's closing "---" for a setext-heading underline of the line above
// it. Content with no valid block is returned unchanged. It shares ParseFrontmatter's fence
// detection so the two agree on where the body begins.
func StripFrontmatter(content string) string {
	rest := strings.TrimPrefix(content, "\xef\xbb\xbf")
	if !strings.HasPrefix(rest, "---\n") && !strings.HasPrefix(rest, "---\r\n") {
		return content
	}
	nl := strings.IndexByte(rest, '\n')
	body := rest[nl+1:]
	for off := 0; off < len(body); {
		nlAt := strings.IndexByte(body[off:], '\n')
		line := body[off:]
		if nlAt >= 0 {
			line = line[:nlAt]
		}
		if strings.TrimRight(line, "\r") == "---" {
			if nlAt < 0 {
				return ""
			}
			return body[off+nlAt+1:]
		}
		if nlAt < 0 {
			break
		}
		off += nlAt + 1
	}
	return content
}

// WriteFrontmatter emits the site's YAML frontmatter block. Values containing a
// colon, quote, or edge whitespace are quoted so a YAML parser can't misread
// them. A page with no page_type/aliases leaves those fields zero.
func WriteFrontmatter(b *strings.Builder, f Frontmatter) {
	b.WriteString("---\n")
	fmt.Fprintf(b, "title: %s\n", yamlScalar(f.Title))
	if f.PageType != "" {
		fmt.Fprintf(b, "page_type: %s\n", yamlScalar(f.PageType))
	}
	if f.GeneratedFrom != "" {
		fmt.Fprintf(b, "generated_from: %s\n", yamlScalar(f.GeneratedFrom))
	}
	if len(f.Aliases) > 0 {
		b.WriteString("aliases: [")
		for i, a := range f.Aliases {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(yamlScalar(a))
		}
		b.WriteString("]\n")
	}
	fmt.Fprintf(b, "description: %s\n", yamlScalar(f.Description))
	b.WriteString("tags: [")
	for i, t := range f.Tags {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(yamlScalar(t))
	}
	b.WriteString("]\n---\n\n")
}

// yamlScalar renders s so a YAML parser reads back the identical string, double-quoting it
// whenever the plain form would carry YAML meaning. Beyond the structural cases (a ": " opens
// a mapping, a leading indicator starts a flow collection, tag, anchor, or comment, trailing
// space is trimmed), a value that resolves as a NON-string scalar - a bare 404, true, or null -
// must be quoted too: unmarshaled into a string field it errors, and ParseFrontmatter then
// drops the entire block, losing the page's title and tags silently.
func yamlScalar(s string) string {
	if !yamlNeedsQuote(s) {
		return s
	}
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

func yamlNeedsQuote(s string) bool {
	if s == "" {
		return false // an empty value reads back as an empty string
	}
	if s[0] == ' ' || s[len(s)-1] == ' ' {
		return true
	}
	switch s[0] {
	case '!', '&', '*', '[', ']', '{', '}', ',', '#', '|', '>', '@', '`', '%', '"', '\'', '?', ':', '-':
		return true
	}
	if strings.Contains(s, ": ") || strings.HasSuffix(s, ":") || strings.Contains(s, " #") {
		return true
	}
	if strings.ContainsAny(s, "\"'\n\t") {
		return true
	}
	return yamlResolvesNonString(s)
}

// yamlResolvesNonString reports whether s is a plain scalar YAML's core schema would type as
// something other than a string - a bool, null, or number - so it can be quoted back into one.
func yamlResolvesNonString(s string) bool {
	switch strings.ToLower(s) {
	case "null", "~", "true", "false", "yes", "no", "on", "off":
		return true
	}
	if _, err := strconv.ParseInt(s, 10, 64); err == nil {
		return true
	}
	if _, err := strconv.ParseFloat(s, 64); err == nil {
		return true
	}
	return false
}
