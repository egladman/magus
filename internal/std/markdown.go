package std

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

//go:generate go run ../../cmd/magus-bindings-gen -module markdown -lang buzz -out gen/buzz/markdown.go

func init() { Register(Markdown) }

// Markdown is the "markdown" host module: GitHub-Flavored Markdown to semantic
// HTML. It backs the docs-site generator, which renders each docs/*.md page.
var Markdown = Module{
	Name: "markdown",
	Doc:  "GitHub-Flavored Markdown to semantic HTML.",
	Methods: []Method{
		{
			Name:    "to_html",
			Doc:     "Render GitHub-Flavored Markdown to semantic HTML. Auto-IDs every heading so #fragment links resolve, and rewrites relative .md links (incl. README.md → index.html) to their generated .html equivalents.",
			Args:    []Arg{{Name: "source", Type: TypeString}},
			Returns: []Ret{{Type: TypeString}},
			Impl:    MarkdownToHTML,
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
)

// MarkdownToHTML renders GFM source to semantic HTML.
func MarkdownToHTML(_ context.Context, source string) (string, error) {
	var buf bytes.Buffer
	if err := converter.Convert([]byte(source), &buf); err != nil {
		return "", fmt.Errorf("markdown.to_html: %w", err)
	}
	return buf.String(), nil
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
