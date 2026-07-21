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

func TestLexer_NumberLiterals(t *testing.T) {
	// Upstream Buzz (src/Scanner.zig) accepts:
	//   - decimal ints/floats with _ separator between digits
	//   - hex prefix 0x (lowercase only)
	//   - binary prefix 0b (lowercase only)
	// NO octal prefix, NO exponent syntax, NO uppercase prefix, NO leading
	// or trailing underscore in the digit run.
	assert.Equal(t, []string{"int(0x1a)", "EOF"}, tokenize(t, `0x1a`))
	assert.Equal(t, []string{"int(0xDEADBEEF)", "EOF"}, tokenize(t, `0xDEADBEEF`))
	assert.Equal(t, []string{"int(0b1010)", "EOF"}, tokenize(t, `0b1010`))

	// Underscore digit separators (upstream-conformant).
	assert.Equal(t, []string{"int(1_000_000)", "EOF"}, tokenize(t, `1_000_000`))
	assert.Equal(t, []string{"int(0xFF_FF)", "EOF"}, tokenize(t, `0xFF_FF`))
	assert.Equal(t, []string{"int(0b1100_1010)", "EOF"}, tokenize(t, `0b1100_1010`))
	assert.Equal(t, []string{"float(6.022_140)", "EOF"}, tokenize(t, `6.022_140`))
	assert.Equal(t, []string{"float(1_000.5)", "EOF"}, tokenize(t, `1_000.5`))

	// Leading zero without prefix stays decimal (no implicit-octal footgun,
	// matching upstream).
	assert.Equal(t, []string{"int(010)", "EOF"}, tokenize(t, `010`))
	assert.Equal(t, []string{"int(0755)", "EOF"}, tokenize(t, `0755`))
}

func TestLexer_NumberLiterals_UpstreamDivergenceRejected(t *testing.T) {
	// These forms are NOT accepted by upstream Buzz; ensure gopherbuzz does
	// not accept them either. Each snippet must produce a tokenization
	// failure OR tokenize into two separate tokens (int + identifier), never
	// as a single integer.
	rejected := []string{
		`0o755`, // no octal prefix in upstream
		`0X1A`,  // uppercase hex prefix rejected
		`0B10`,  // uppercase binary prefix rejected
	}
	for _, src := range rejected {
		toks, _ := Tokenize(src)
		// If it lexed as one Int + EOF, that's the bug we are guarding against.
		if len(toks) == 2 && toks[0].Kind == Int && toks[1].Kind == EOF {
			t.Errorf("%q: unexpectedly tokenized as a single Int(%q); upstream Buzz rejects this form",
				src, toks[0].Val)
		}
	}
}

func TestLexer_NumberLiterals_UnderscoreBoundary(t *testing.T) {
	// Upstream rejects '_' at the boundary of a digit run:
	// "'_' must be between digits". Test each form.
	// Trailing _ in integer or fractional part must fail. Cases where the '.'
	// is not followed by a digit (e.g. "1._") do not enter the float branch
	// at all and tokenize as Int + Dot + Ident, so they are not test cases
	// for the number lexer.
	bad := []string{
		`1_`,
		`1_.5`,  // trailing _ before decimal point
		`0x_`,   // no digits after hex prefix
		`0xFF_`, // trailing _ in hex
		`0b_`,   // no digits after binary prefix
		`0b1_`,  // trailing _ in binary
	}
	for _, src := range bad {
		_, err := Tokenize(src)
		if err == nil {
			t.Errorf("%q: expected tokenize error, got none", src)
		}
	}
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
