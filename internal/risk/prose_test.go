package risk

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCommentBlocksGroupConsecutiveLineComments(t *testing.T) {
	src := "package x\n\n// One.\n// Two.\nfunc f() {} // trailing\n\n// Three.\nvar s = \"// not a comment\"\n//go:generate x\n"
	blocks := CommentBlocks("x.go", src, commentSyntax(t, ".go"))
	require.Len(t, blocks, 3)
	assert.Equal(t, []CommentLine{{Line: 3, Col: 3, Text: " One."}, {Line: 4, Col: 3, Text: " Two."}}, blocks[0].Lines)
	assert.Equal(t, []CommentLine{{Line: 5, Col: 15, Text: " trailing"}}, blocks[1].Lines, "a trailing comment stands alone")
	assert.Equal(t, []CommentLine{{Line: 7, Col: 3, Text: " Three."}}, blocks[2].Lines,
		"a blank line ends a block; a string and a directive are not comments")
}

func TestCommentBlocksCountColumnsInRunes(t *testing.T) {
	blocks := CommentBlocks("x.go", "s := \"é\" // é note\n", commentSyntax(t, ".go"))
	require.Len(t, blocks, 1)
	assert.Equal(t, 12, blocks[0].Lines[0].Col, "Vale counts a column in runes, so the table does too")
}

func TestCommentBlocksSplitBlockComments(t *testing.T) {
	src := "x := 1\n\t/**\n\t * First.\n\t *   code\n\t */\n"
	blocks := CommentBlocks("x.ts", src, commentSyntax(t, ".ts"))
	require.Len(t, blocks, 1)
	assert.Equal(t, []CommentLine{
		{Line: 2, Col: 5, Text: ""},
		{Line: 3, Col: 4, Text: " First."},
		{Line: 4, Col: 4, Text: "   code"},
		{Line: 5, Col: 1, Text: "\t "},
	}, blocks[0].Lines, "the decorating stars are dropped and the closer with them")
	assert.Equal(t, []CommentBlock{{Path: "x.ts", Lines: []CommentLine{{Line: 3, Col: 5, Text: "First."}}}},
		ProseBlocks(blocks))
}

// TestProseBlocksPointAtTheirText pins the position table hack/prose builds: a prose
// linter's column on a kept line, added to that line's Col, has to land on the same text
// in the source.
func TestProseBlocksPointAtTheirText(t *testing.T) {
	sources := map[string]string{
		"a.go": "package x\n\n// Alpha beta.\n//\n//\tcode()\n// - gamma\nfunc f() {}\n\n\t// Délta épsilon.\n",
		"b.ts": "let a = 1; // Epsilon.\n",
	}
	blocks := append(CommentBlocks("a.go", sources["a.go"], commentSyntax(t, ".go")),
		CommentBlocks("b.ts", sources["b.ts"], commentSyntax(t, ".ts"))...)
	kept := ProseBlocks(blocks)
	require.Len(t, kept, 3, "the trailing comment is its own block")
	assert.Equal(t, []CommentLine{{Line: 3, Col: 4, Text: "Alpha beta."}, {Line: 6, Col: 4, Text: "- gamma"}},
		kept[0].Lines, "blank and indented lines drop out")

	for _, b := range kept {
		for _, l := range b.Lines {
			src := []rune(strings.Split(sources[b.Path], "\n")[l.Line-1])
			text := []rune(l.Text)
			for col := 1; col <= len(text); col++ {
				at := l.Col + col - 1
				require.LessOrEqual(t, at+len(text)-col, len(src), "%s:%d", b.Path, l.Line)
				assert.Equal(t, string(text[col-1:]), string(src[at-1:at-1+len(text)-col+1]),
					"%s:%d column %d", b.Path, l.Line, col)
			}
		}
	}
}
