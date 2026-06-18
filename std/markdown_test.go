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
