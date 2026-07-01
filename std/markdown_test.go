package std

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func renderMD(t *testing.T, src string) string {
	t.Helper()
	out, err := MarkdownToHTML(context.Background(), src)
	require.NoError(t, err)
	return out
}

func TestMarkdownToHTMLTable(t *testing.T) {
	html := renderMD(t, "| a | b |\n| - | - |\n| 1 | 2 |\n")
	assert.Contains(t, html, "<table>", "GFM table not rendered:\n%s", html)
	assert.Contains(t, html, "<td>1</td>", "GFM table not rendered:\n%s", html)
}

func TestMarkdownToHTMLHeadingID(t *testing.T) {
	html := renderMD(t, "## Charm vs Target\n")
	assert.Contains(t, html, `id="charm-vs-target"`, "auto heading id missing:\n%s", html)
}

func TestMarkdownToHTMLLinkRewrite(t *testing.T) {
	html := renderMD(t, "[t](targets.md) [r](magusfile/README.md#x) [e](https://example.com/a.md)")
	assert.Contains(t, html, `href="targets.html"`, "relative .md not rewritten:\n%s", html)
	assert.Contains(t, html, `href="magusfile/index.html#x"`, "README.md#frag not rewritten:\n%s", html)
	assert.Contains(t, html, `href="https://example.com/a.md"`, "external .md link should be untouched:\n%s", html)
}

func TestMarkdownToHTMLDefinitionList(t *testing.T) {
	// The generated man pages lean on PHP-Markdown definition lists for their
	// OPTIONS/TARGETS sections; verify the extension renders <dl>/<dt>/<dd>.
	html := renderMD(t, "**--depth** *int*\n: cap displayed depth\n")
	for _, want := range []string{"<dl>", "<dt><strong>--depth</strong> <em>int</em></dt>", "<dd>cap displayed depth</dd>"} {
		assert.Contains(t, html, want, "definition list missing %q:\n%s", want, html)
	}
}

func TestMarkdownToHTMLAnchorOnlyUntouched(t *testing.T) {
	html := renderMD(t, "[x](#section)")
	assert.Contains(t, html, `href="#section"`, "anchor-only link should be untouched:\n%s", html)
}

func TestMarkdownToHTMLStripsFrontmatter(t *testing.T) {
	html := renderMD(t, "---\ntitle: Remote Cache\ntags: [cache, remote]\n---\n# Body\n\nHello.\n")
	assert.Contains(t, html, "<h1", "body heading should still render:\n%s", html)
	assert.Contains(t, html, "Hello.", "body text should still render:\n%s", html)
	// The header must not leak into the page as content (no stray <hr> or keys).
	assert.NotContains(t, html, "tags:", "frontmatter leaked into rendered HTML:\n%s", html)
	assert.NotContains(t, html, "<hr", "opening fence rendered as a thematic break:\n%s", html)
}

func TestMarkdownFrontmatter(t *testing.T) {
	ctx := context.Background()

	js, err := MarkdownFrontmatter(ctx, "---\ntitle: Remote Cache\ntags: [cache, remote]\ndraft: true\n---\n# Body\n")
	require.NoError(t, err)
	// json.Marshal sorts map keys, so the shape is deterministic.
	assert.JSONEq(t, `{"title":"Remote Cache","tags":["cache","remote"],"draft":true}`, js)

	// No frontmatter -> empty object, never an error.
	js, err = MarkdownFrontmatter(ctx, "# Just a heading\n")
	require.NoError(t, err)
	assert.Equal(t, "{}", js)

	// An empty block is still a (stripped) block, but carries no fields.
	js, err = MarkdownFrontmatter(ctx, "---\n---\nbody\n")
	require.NoError(t, err)
	assert.Equal(t, "{}", js)

	// Malformed YAML surfaces as an error to metadata readers only.
	_, err = MarkdownFrontmatter(ctx, "---\ntitle: [unterminated\n---\nbody\n")
	require.Error(t, err)
}

func TestMarkdownStripFrontmatter(t *testing.T) {
	ctx := context.Background()

	body, err := MarkdownStripFrontmatter(ctx, "---\ntitle: X\n---\n# Body\n\ntext\n")
	require.NoError(t, err)
	assert.Equal(t, "# Body\n\ntext\n", body)

	// A document with no frontmatter is returned unchanged, including a leading
	// "---" that is a thematic break rather than an opening fence.
	const noFM = "# Heading\n\n---\n\nmore\n"
	body, err = MarkdownStripFrontmatter(ctx, noFM)
	require.NoError(t, err)
	assert.Equal(t, noFM, body)

	// An opening fence with no closing fence is not frontmatter.
	const unclosed = "---\ntitle: X\nstill going\n"
	body, err = MarkdownStripFrontmatter(ctx, unclosed)
	require.NoError(t, err)
	assert.Equal(t, unclosed, body)
}
