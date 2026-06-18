package token

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func tokenStr(t Token) string {
	switch t.Kind {
	case Ident:
		return fmt.Sprintf("ident(%s)", t.Val)
	case String:
		return fmt.Sprintf("string(%q)", t.Val)
	case Int:
		return fmt.Sprintf("int(%s)", t.Val)
	case Float:
		return fmt.Sprintf("float(%s)", t.Val)
	case True:
		return "bool(true)"
	case False:
		return "bool(false)"
	case Null:
		return "null"
	case Dot:
		return "."
	case EOF:
		return "EOF"
	default:
		return fmt.Sprintf("tok(%d)", t.Kind)
	}
}

func tokenize(t *testing.T, src string) []string {
	t.Helper()
	toks, err := Tokenize(strings.ReplaceAll(src, `\n`, "\n"))
	require.NoErrorf(t, err, "tokenize %q", src)
	var got []string
	for _, tok := range toks {
		got = append(got, tokenStr(tok))
	}
	return got
}

func TestLexer_Basic(t *testing.T) {
	assert.Equal(t, []string{`string("hello")`, "EOF"}, tokenize(t, `"hello"`))
	assert.Equal(t, []string{"int(42)", "EOF"}, tokenize(t, `42`))
	assert.Equal(t, []string{"bool(true)", "bool(false)", "null", "EOF"}, tokenize(t, `true false null`))
	assert.Equal(t, []string{`ident(magus)`, ".", `ident(project)`, ".", `ident(register)`, "EOF"}, tokenize(t, `magus.project.register`))
	assert.Equal(t, []string{"int(42)", "EOF"}, tokenize(t, `// comment\n42`))
}

// firstDoc returns the Doc of the first token whose Val (or keyword) matches
// ident, for asserting which declaration a comment block attached to.
func docOfIdent(toks []Token, ident string) string {
	for _, t := range toks {
		if t.Kind == Ident && t.Val == ident {
			return t.Doc
		}
	}
	return ""
}

func TestLexer_DocComments(t *testing.T) {
	docFor := func(t *testing.T, src, ident string) string {
		t.Helper()
		toks, err := Tokenize(src)
		require.NoError(t, err, "tokenize")
		return docOfIdent(toks, ident)
	}

	t.Run("single line comment attaches to next token", func(t *testing.T) {
		assert.Equal(t, "builds the thing", docFor(t, "// builds the thing\nbuild", "build"))
	})
	t.Run("contiguous lines join", func(t *testing.T) {
		assert.Equal(t, "line one\nline two", docFor(t, "// line one\n// line two\nbuild", "build"))
	})
	t.Run("blank line breaks the block", func(t *testing.T) {
		assert.Equal(t, "", docFor(t, "// not a doc\n\nbuild", "build"))
	})
	t.Run("only the last contiguous block attaches after a gap", func(t *testing.T) {
		assert.Equal(t, "fresh", docFor(t, "// stale\n\n// fresh\nbuild", "build"))
	})
	t.Run("block comment attaches", func(t *testing.T) {
		assert.Equal(t, "a block doc", docFor(t, "/* a block doc */\nbuild", "build"))
	})
	t.Run("trailing comment on a line does not attach to the next token", func(t *testing.T) {
		assert.Equal(t, "", docFor(t, "x // trailing\nbuild", "build"))
	})
}
