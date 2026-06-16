package std

import (
	"context"
	"strings"
	"testing"
)

func renderMD(t *testing.T, src string) string {
	t.Helper()
	out, err := MarkdownToHTML(context.Background(), src)
	if err != nil {
		t.Fatalf("MarkdownToHTML: %v", err)
	}
	return out
}

func TestMarkdownToHTMLTable(t *testing.T) {
	html := renderMD(t, "| a | b |\n| - | - |\n| 1 | 2 |\n")
	if !strings.Contains(html, "<table>") || !strings.Contains(html, "<td>1</td>") {
		t.Fatalf("GFM table not rendered:\n%s", html)
	}
}

func TestMarkdownToHTMLHeadingID(t *testing.T) {
	html := renderMD(t, "## Charm vs Target\n")
	if !strings.Contains(html, `id="charm-vs-target"`) {
		t.Fatalf("auto heading id missing:\n%s", html)
	}
}

func TestMarkdownToHTMLLinkRewrite(t *testing.T) {
	html := renderMD(t, "[t](targets.md) [r](magusfile/README.md#x) [e](https://example.com/a.md)")
	if !strings.Contains(html, `href="targets.html"`) {
		t.Errorf("relative .md not rewritten:\n%s", html)
	}
	if !strings.Contains(html, `href="magusfile/index.html#x"`) {
		t.Errorf("README.md#frag not rewritten:\n%s", html)
	}
	if !strings.Contains(html, `href="https://example.com/a.md"`) {
		t.Errorf("external .md link should be untouched:\n%s", html)
	}
}

func TestMarkdownToHTMLDefinitionList(t *testing.T) {
	// The generated man pages lean on PHP-Markdown definition lists for their
	// OPTIONS/TARGETS sections; verify the extension renders <dl>/<dt>/<dd>.
	html := renderMD(t, "**--depth** *int*\n: cap displayed depth\n")
	for _, want := range []string{"<dl>", "<dt><strong>--depth</strong> <em>int</em></dt>", "<dd>cap displayed depth</dd>"} {
		if !strings.Contains(html, want) {
			t.Fatalf("definition list missing %q:\n%s", want, html)
		}
	}
}

func TestMarkdownToHTMLAnchorOnlyUntouched(t *testing.T) {
	html := renderMD(t, "[x](#section)")
	if !strings.Contains(html, `href="#section"`) {
		t.Fatalf("anchor-only link should be untouched:\n%s", html)
	}
}
