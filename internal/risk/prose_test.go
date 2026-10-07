package risk

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	json "github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/spells"
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
	assert.Equal(t, "First.\n", NewProseDocument(blocks).Text)
}

// TestProseDocumentLocate pins the position table: every alert Vale reports at a document
// line and column has to land on the comment's own file, line and column.
func TestProseDocumentLocate(t *testing.T) {
	goSrc := "package x\n\n// Alpha beta.\n//\n//\tcode()\n// - gamma\nfunc f() {}\n\n\t// Delta.\n"
	tsSrc := "let a = 1; // Epsilon.\n"
	blocks := append(CommentBlocks("a.go", goSrc, commentSyntax(t, ".go")),
		CommentBlocks("b.ts", tsSrc, commentSyntax(t, ".ts"))...)
	doc := NewProseDocument(blocks)
	assert.Equal(t, "Alpha beta.\n- gamma\n\nDelta.\n\nEpsilon.\n", doc.Text,
		"blank and indented lines drop out and blocks are paragraphs")

	for _, tc := range []struct {
		line, col int
		want      ProseOrigin
	}{
		{1, 1, ProseOrigin{Path: "a.go", Line: 3, Col: 4}},
		{1, 7, ProseOrigin{Path: "a.go", Line: 3, Col: 10}},
		{2, 3, ProseOrigin{Path: "a.go", Line: 6, Col: 6}},
		{4, 1, ProseOrigin{Path: "a.go", Line: 9, Col: 5}},
		{6, 1, ProseOrigin{Path: "b.ts", Line: 1, Col: 15}},
	} {
		got, ok := doc.Locate(tc.line, tc.col)
		require.True(t, ok, "line %d", tc.line)
		assert.Equal(t, tc.want, got, "line %d col %d", tc.line, tc.col)
		src := map[string]string{"a.go": goSrc, "b.ts": tsSrc}[got.Path]
		docLine := strings.Split(doc.Text, "\n")[tc.line-1]
		srcLine := strings.Split(src, "\n")[got.Line-1]
		assert.Equal(t, docLine[tc.col-1:], srcLine[got.Col-1:], "the located column holds the same text")
	}
	for _, line := range []int{0, 3, 5, 99} {
		_, ok := doc.Locate(line, 1)
		assert.False(t, ok, "line %d is a separator or out of range", line)
	}
}

// TestProseExport is how hack/prose/comments.buzz reaches the scanner: it hands a request
// through MAGUS_PROSE_REQUEST and reads <MAGUS_PROSE_OUT>.md and .json back. Without the
// request it has nothing to do.
func TestProseExport(t *testing.T) {
	reqPath := os.Getenv("MAGUS_PROSE_REQUEST")
	out := os.Getenv("MAGUS_PROSE_OUT")
	if reqPath == "" || out == "" {
		t.Skip("MAGUS_PROSE_REQUEST and MAGUS_PROSE_OUT are unset")
	}
	raw, err := os.ReadFile(reqPath)
	require.NoError(t, err)
	var req struct {
		Root   string                          `json:"root"`
		Syntax map[string]spells.CommentSyntax `json:"syntax"`
		Paths  []string                        `json:"paths"`
	}
	require.NoError(t, json.Unmarshal(raw, &req))
	var blocks []CommentBlock
	for _, p := range req.Paths {
		syn, ok := req.Syntax[filepath.Ext(p)]
		if !ok {
			continue
		}
		src, err := os.ReadFile(filepath.Join(req.Root, p))
		require.NoError(t, err)
		blocks = append(blocks, CommentBlocks(p, string(src), syn)...)
	}
	doc := NewProseDocument(blocks)
	table, err := json.Marshal(doc)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(out+".md", []byte(doc.Text), 0o644))
	require.NoError(t, os.WriteFile(out+".json", table, 0o644))
}
