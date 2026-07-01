package std

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
	"gopkg.in/yaml.v3"
)

//go:generate go run ../cmd/magus-utils bindings -module markdown -lang buzz -out ../host/gen/markdown.go

func init() { Register(Markdown) }

// Markdown is the "markdown" host module: GitHub-Flavored Markdown to semantic
// HTML. It backs the docs-site generator, which renders each docs/*.md page.
var Markdown = Module{
	Name: "markdown",
	Doc:  "GitHub-Flavored Markdown to semantic HTML.",
	Methods: []Method{
		{
			Name:    "to_html",
			Doc:     "Render GitHub-Flavored Markdown to semantic HTML. Strips a leading YAML frontmatter block (a \"---\" fenced header at the top of the document) before rendering. Auto-IDs every heading so #fragment links resolve, and rewrites relative .md links (incl. README.md → index.html) to their generated .html equivalents. Raw HTML in the source is passed through (intended for trusted, first-party docs).",
			Args:    []Arg{{Name: "source", Type: TypeString}},
			Returns: []Ret{{Type: TypeString}},
			Impl:    MarkdownToHTML,
		},
		{
			Name:    "frontmatter",
			Doc:     "Parse the leading YAML frontmatter block (a \"---\" fenced header at the top of the document) and return it as a JSON object string; decode with serialize.jsonDecode. Returns \"{}\" when no frontmatter is present.",
			Args:    []Arg{{Name: "source", Type: TypeString}},
			Returns: []Ret{{Type: TypeString}},
			Impl:    MarkdownFrontmatter,
		},
		{
			Name:    "strip_frontmatter",
			Doc:     "Return the Markdown body with any leading YAML frontmatter block removed (the source unchanged when none is present).",
			Args:    []Arg{{Name: "source", Type: TypeString}},
			Returns: []Ret{{Type: TypeString}},
			Impl:    MarkdownStripFrontmatter,
		},
	},
}

// converter renders GitHub-Flavored Markdown to semantic HTML. GFM brings
// tables, strikethrough, autolinks, and task lists; DefinitionList renders
// PHP-Markdown-style `term\n: body` blocks (used by the generated man pages) to
// <dl>/<dt>/<dd>; WithAutoHeadingID stamps an id on every heading so in-page
// `#fragment` links resolve; mdLinkRewriter turns relative links between docs
// (`foo.md`, `dir/README.md#x`) into their generated `.html` equivalents so the
// rendered site stays navigable.
var converter = goldmark.New(
	goldmark.WithExtensions(extension.GFM, extension.DefinitionList),
	goldmark.WithParserOptions(
		parser.WithAutoHeadingID(),
		parser.WithASTTransformers(util.Prioritized(mdLinkRewriter{}, 100)),
	),
	// WithUnsafe passes raw HTML in the source through instead of dropping it.
	// magus renders first-party Markdown (the repo's own README and docs/), which
	// uses raw HTML for things Markdown can't express - e.g. the centered hero
	// image on the landing page. Safe here because the input is trusted.
	goldmark.WithRendererOptions(html.WithUnsafe()),
)

// MarkdownToHTML renders GFM source to semantic HTML, first stripping any
// leading YAML frontmatter so the header never renders as content.
func MarkdownToHTML(_ context.Context, source string) (string, error) {
	_, body, _ := splitFrontmatter(source)
	var buf bytes.Buffer
	if err := converter.Convert([]byte(body), &buf); err != nil {
		return "", fmt.Errorf("markdown.to_html: %w", err)
	}
	return buf.String(), nil
}

// MarkdownFrontmatter parses the leading YAML frontmatter block and returns it
// as a JSON object string (so Buzz callers decode it with serialize.jsonDecode);
// "{}" when the document carries no frontmatter. The YAML is parsed here rather
// than in to_html so a malformed header surfaces as an error only to callers
// that actually read the metadata, never breaking a page render.
func MarkdownFrontmatter(_ context.Context, source string) (string, error) {
	fm, _, ok := splitFrontmatter(source)
	if !ok || strings.TrimSpace(fm) == "" {
		return "{}", nil
	}
	var meta map[string]any
	if err := yaml.Unmarshal([]byte(fm), &meta); err != nil {
		return "", fmt.Errorf("markdown.frontmatter: %w", err)
	}
	if meta == nil {
		return "{}", nil
	}
	b, err := json.Marshal(meta)
	if err != nil {
		return "", fmt.Errorf("markdown.frontmatter: %w", err)
	}
	return string(b), nil
}

// MarkdownStripFrontmatter returns the Markdown body with any leading YAML
// frontmatter block removed; the source is returned unchanged when none is
// present. It backs the search index, whose page text must not include the
// raw header.
func MarkdownStripFrontmatter(_ context.Context, source string) (string, error) {
	_, body, _ := splitFrontmatter(source)
	return body, nil
}

// splitFrontmatter separates an optional leading YAML frontmatter block from the
// Markdown body. A block is a "---" line at the very top of the document, its
// YAML content, and a closing "---" (or "...") line; everything after is the
// body. ok is false when no well-formed block is present, in which case fm is ""
// and body is the source unchanged. The fence content is returned raw (unparsed)
// so callers that only need the body never pay for a YAML parse.
func splitFrontmatter(source string) (fm, body string, ok bool) {
	// The opening fence must be the document's first line and exactly "---".
	nl := strings.IndexByte(source, '\n')
	if nl < 0 || strings.TrimRight(source[:nl], " \t\r") != "---" {
		return "", source, false
	}
	rest := source[nl+1:]
	// Scan line by line for the closing fence ("---" or "...").
	off := 0
	for off <= len(rest) {
		seg := rest[off:]
		i := strings.IndexByte(seg, '\n')
		line := seg
		if i >= 0 {
			line = seg[:i]
		}
		switch strings.TrimRight(line, " \t\r") {
		case "---", "...":
			if i < 0 {
				return rest[:off], "", true
			}
			return rest[:off], seg[i+1:], true
		}
		if i < 0 {
			return "", source, false // ran out of lines without a closing fence
		}
		off += i + 1
	}
	return "", source, false
}

// mdLinkRewriter rewrites relative *.md link destinations to *.html so links
// between source docs point at the generated pages. README.md maps to index.html
// (matching the generator's output naming). Absolute, scheme-qualified,
// root-relative, and anchor-only links are left untouched.
type mdLinkRewriter struct{}

func (mdLinkRewriter) Transform(doc *ast.Document, _ text.Reader, _ parser.Context) {
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		if link, ok := n.(*ast.Link); ok {
			link.Destination = rewriteMdLink(link.Destination)
		}
		return ast.WalkContinue, nil
	})
}

func rewriteMdLink(dest []byte) []byte {
	s := string(dest)
	if s == "" ||
		strings.HasPrefix(s, "#") ||
		strings.HasPrefix(s, "/") ||
		strings.HasPrefix(s, "mailto:") ||
		strings.Contains(s, "://") {
		return dest
	}
	path, frag := s, ""
	if i := strings.IndexByte(s, '#'); i >= 0 {
		path, frag = s[:i], s[i:]
	}
	if !strings.HasSuffix(path, ".md") {
		return dest
	}
	if path == "README.md" || strings.HasSuffix(path, "/README.md") {
		path = strings.TrimSuffix(path, "README.md") + "index.html"
	} else {
		path = strings.TrimSuffix(path, ".md") + ".html"
	}
	return []byte(path + frag)
}
