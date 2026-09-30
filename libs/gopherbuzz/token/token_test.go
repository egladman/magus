package token

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"unsafe"

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
	tokenizedDoc := func(t *testing.T, src, ident string) string {
		t.Helper()
		toks, err := Tokenize(src)
		require.NoError(t, err, "tokenize")
		return docOfIdent(toks, ident)
	}

	t.Run("single line comment attaches to next token", func(t *testing.T) {
		assert.Equal(t, "builds the thing", tokenizedDoc(t, "// builds the thing\nbuild", "build"))
	})
	t.Run("contiguous lines join", func(t *testing.T) {
		assert.Equal(t, "line one\nline two", tokenizedDoc(t, "// line one\n// line two\nbuild", "build"))
	})
	t.Run("blank line breaks the block", func(t *testing.T) {
		assert.Equal(t, "", tokenizedDoc(t, "// not a doc\n\nbuild", "build"))
	})
	t.Run("only the last contiguous block attaches after a gap", func(t *testing.T) {
		assert.Equal(t, "fresh", tokenizedDoc(t, "// stale\n\n// fresh\nbuild", "build"))
	})
	t.Run("block comment attaches", func(t *testing.T) {
		assert.Equal(t, "a block doc", tokenizedDoc(t, "/* a block doc */\nbuild", "build"))
	})
	t.Run("trailing comment on a line does not attach to the next token", func(t *testing.T) {
		assert.Equal(t, "", tokenizedDoc(t, "x // trailing\nbuild", "build"))
	})
	t.Run("a leading empty comment line is dropped", func(t *testing.T) {
		assert.Equal(t, "body", tokenizedDoc(t, "//\n// body\nbuild", "build"))
	})
	t.Run("inner and trailing empty comment lines are kept", func(t *testing.T) {
		assert.Equal(t, "a\n\nb\n", tokenizedDoc(t, "// a\n//\n// b\n//\nbuild", "build"))
	})
	t.Run("an empty comment alone is no doc", func(t *testing.T) {
		assert.Equal(t, "", tokenizedDoc(t, "//\nbuild", "build"))
	})
	t.Run("an empty comment line does not bridge a gap", func(t *testing.T) {
		assert.Equal(t, "fresh", tokenizedDoc(t, "//\n\n// fresh\nbuild", "build"))
	})
	t.Run("a block comment joins the line comments above it", func(t *testing.T) {
		assert.Equal(t, "lead\ntail", tokenizedDoc(t, "// lead\n/* tail */\nbuild", "build"))
	})

	cases := []struct {
		name, src, ident, want string
	}{
		{"line comments join the block comment above them", "/* head */\n// tail\nbuild", "build", "head\ntail"},
		{"a multi-line block comment attaches from its closing line", "/* a\n   b */\nbuild", "build", "a\n   b"},
		{"a blank line after a block comment starts a fresh block", "/* a */\n\n// b\nbuild", "build", "b"},
		{"a blank line between blocks and token attaches neither", "// a\n\n// b\n\nbuild", "build", ""},
		{"indented comments attach", "    // a\n    // b\n    build", "build", "a\nb"},
		{"CRLF line comments attach without the carriage return", "// one\r\n// two\r\nbuild", "build", "one\ntwo"},
		{"a trailing block comment does not attach", "x /* t */\nbuild", "build", ""},
		{"only the first token below the block takes it", "// a\nx build", "build", ""},
		{"a block consumed across a gap does not reach a later token", "// a\n\nx\nbuild", "build", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, tokenizedDoc(t, c.src, c.ident))
		})
	}

	t.Run("a block above the end of input attaches to nothing", func(t *testing.T) {
		toks, err := Tokenize("x\n// dangling\n/* and this */")
		require.NoError(t, err)
		require.Len(t, toks, 2)
		assert.Equal(t, "", toks[0].Doc)
		assert.Equal(t, Token{Kind: EOF, Line: 3, Col: toks[1].Col}, toks[1])
	})
}

// TestTokenize_Positions pins exact tokens for the edge shapes of a source:
// nothing at all, a single token, CRLF line ends, and identifiers outside ASCII,
// whose Col counts bytes.
func TestTokenize_Positions(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []Token
	}{
		{"empty source", "", []Token{{Kind: EOF, Line: 1, Col: 1}}},
		{"whitespace only", "  \n \t", []Token{{Kind: EOF, Line: 2, Col: 3}}},
		{"one identifier", "x", []Token{{Kind: Ident, Val: "x", Line: 1, Col: 1}, {Kind: EOF, Line: 1, Col: 2}}},
		{"one keyword", "fun", []Token{{Kind: Fun, Val: "fun", Line: 1, Col: 1}, {Kind: EOF, Line: 1, Col: 4}}},
		{"one number", "42", []Token{{Kind: Int, Val: "42", Line: 1, Col: 1}, {Kind: EOF, Line: 1, Col: 3}}},
		{"one string", `"s"`, []Token{{Kind: String, Val: "s", Line: 1, Col: 1}, {Kind: EOF, Line: 1, Col: 4}}},
		{"CRLF line ends", "a\r\nb\r\n", []Token{
			{Kind: Ident, Val: "a", Line: 1, Col: 1},
			{Kind: Ident, Val: "b", Line: 2, Col: 1},
			{Kind: EOF, Line: 3, Col: 1},
		}},
		{"CRLF inside a string is kept", "\"a\r\nb\" c", []Token{
			{Kind: String, Val: "a\r\nb", Line: 1, Col: 1},
			{Kind: Ident, Val: "c", Line: 2, Col: 4},
			{Kind: EOF, Line: 2, Col: 5},
		}},
		{"accented identifier", "héllo x", []Token{
			{Kind: Ident, Val: "héllo", Line: 1, Col: 1},
			{Kind: Ident, Val: "x", Line: 1, Col: 8},
			{Kind: EOF, Line: 1, Col: 9},
		}},
		{"CJK identifier", "日本 = 1", []Token{
			{Kind: Ident, Val: "日本", Line: 1, Col: 1},
			{Kind: Assign, Line: 1, Col: 8},
			{Kind: Int, Val: "1", Line: 1, Col: 10},
			{Kind: EOF, Line: 1, Col: 11},
		}},
		{"non-ASCII digit continues an identifier", "x٣", []Token{
			{Kind: Ident, Val: "x٣", Line: 1, Col: 1},
			{Kind: EOF, Line: 1, Col: 4},
		}},
		{"free identifier keeps its spelling", `@"fun name"`, []Token{
			{Kind: Ident, Val: "fun name", Raw: true, Line: 1, Col: 1},
			{Kind: EOF, Line: 1, Col: 12},
		}},
		{"newline inside an interpolation starts its line at col 1", "\"{a\n}\" x", []Token{
			{Kind: InterpStr, Parts: []StringPart{{IsExpr: true, Text: "a\n"}}, Line: 1, Col: 1},
			{Kind: Ident, Val: "x", Line: 2, Col: 4},
			{Kind: EOF, Line: 2, Col: 5},
		}},
		{"escaped newline in a string inside an interpolation counts a line", "\"{\"a\\\nb\"}\"\nx", []Token{
			{Kind: InterpStr, Parts: []StringPart{{IsExpr: true, Text: "\"a\\\nb\""}}, Line: 1, Col: 1},
			{Kind: Ident, Val: "x", Line: 3, Col: 1},
			{Kind: EOF, Line: 3, Col: 2},
		}},
		{"line comment advances the column", "x // c", []Token{
			{Kind: Ident, Val: "x", Line: 1, Col: 1},
			{Kind: EOF, Line: 1, Col: 7},
		}},
		{"escaped newline in a string counts a line", "\"a\\\nb\" x", []Token{
			{Kind: String, Val: "a\\\nb", Line: 1, Col: 1},
			{Kind: Ident, Val: "x", Line: 2, Col: 4},
			{Kind: EOF, Line: 2, Col: 5},
		}},
		{"escaped newline in a pattern counts a line", "$\"a\\\nb\" x", []Token{
			{Kind: Pat, Val: "a\\\nb", Line: 1, Col: 1},
			{Kind: Ident, Val: "x", Line: 2, Col: 4},
			{Kind: EOF, Line: 2, Col: 5},
		}},
		{"raw newline in a char literal counts a line", "'\n' x", []Token{
			{Kind: Int, Val: "10", Line: 1, Col: 1},
			{Kind: Ident, Val: "x", Line: 2, Col: 3},
			{Kind: EOF, Line: 2, Col: 4},
		}},
		{"quote inside a nested string's interpolation", `"{"x{"}"}"}" y`, []Token{
			{Kind: InterpStr, Parts: []StringPart{{IsExpr: true, Text: `"x{"}"}"`}}, Line: 1, Col: 1},
			{Kind: Ident, Val: "y", Line: 1, Col: 14},
			{Kind: EOF, Line: 1, Col: 15},
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			toks, err := Tokenize(c.src)
			require.NoError(t, err)
			assert.Equal(t, c.want, toks)
			assert.Equal(t, len(toks), cap(toks))
		})
	}
}

// pooledScratch lexes src and takes a buffer back out of the pool. sync.Pool may
// drop a Put, and does at random under -race, so it retries until one comes back.
func pooledScratch(t *testing.T, src string) []Token {
	t.Helper()
	for range 100 {
		_, _ = Tokenize(src)
		buf := scratch.Get().(*[]Token)
		got := slices.Clone((*buf)[:cap(*buf)])
		scratch.Put(buf)
		if len(got) > 0 {
			return got
		}
	}
	t.Fatal("the pool never returned a used buffer")
	return nil
}

// TestTokenize_PooledBufferIsCleared pins that a buffer waiting in the pool holds
// no token, so it pins no source text or doc string, whether the lex it served
// succeeded or failed.
func TestTokenize_PooledBufferIsCleared(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"after a successful lex", "// doc\nfun a() > void { \"text {x}\"; }\n"},
		{"after a failed lex", "// doc\nfun a() > void { \"unterminated"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for i, tok := range pooledScratch(t, c.src) {
				require.Equalf(t, Token{}, tok, "pooled buffer slot %d", i)
			}
		})
	}
}

// TestTokenize_PoolCapFallback lexes modules past maxScratch tokens: the result is
// complete and exact, and the oversized buffer never enters the pool.
func TestTokenize_PoolCapFallback(t *testing.T) {
	cases := []struct {
		name  string
		line  string
		lines int
	}{
		{"presized past the cap", "abcdefgh\n", maxScratch + 4000},
		{"grown past the cap mid-lex", "a\n", maxScratch + 4000},
		{"grown under the cap", "a\n", maxScratch / 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			toks, err := Tokenize(strings.Repeat(c.line, c.lines))
			require.NoError(t, err)
			require.Len(t, toks, c.lines+1)
			assert.Equal(t, len(toks), cap(toks))
			word := strings.TrimSuffix(c.line, "\n")
			assert.Equal(t, Token{Kind: Ident, Val: word, Line: c.lines, Col: 1}, toks[c.lines-1])
			assert.Equal(t, Token{Kind: EOF, Line: c.lines + 1, Col: 1}, toks[c.lines])

			var held []*[]Token
			for range 8 {
				buf := scratch.Get().(*[]Token)
				assert.LessOrEqual(t, cap(*buf), maxScratch)
				held = append(held, buf)
			}
			for _, buf := range held {
				scratch.Put(buf)
			}
		})
	}
}

// TestTokenize_ConcurrentCallersSharePool is meant for -race: callers lexing
// different sources at once through one pool each get exactly their own tokens.
func TestTokenize_ConcurrentCallersSharePool(t *testing.T) {
	srcs := []string{
		"// doc\nfun a() > void { return \"a {b} \\t c\"; }\n",
		"x",
		strings.Repeat("a\n", maxScratch+10),
		"`raw {q}` // trailing\n",
		"\"unterminated",
	}
	want := make([][]Token, len(srcs))
	wantErr := make([]error, len(srcs))
	for i, src := range srcs {
		want[i], wantErr[i] = Tokenize(src)
	}
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Go(func() {
			for i := range 40 {
				k := (g + i) % len(srcs)
				got, err := Tokenize(srcs[k])
				assert.Equal(t, wantErr[k], err)
				assert.Equal(t, want[k], got)
			}
		})
	}
	wg.Wait()
}

// TestValidRunes pins that valid text comes back uncopied and each invalid byte
// becomes U+FFFD, as ranging over the string decodes it.
func TestValidRunes(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"empty", "", ""},
		{"ascii", "abc", "abc"},
		{"multibyte", "héllo 日本", "héllo 日本"},
		{"lone invalid byte", "\xff", "�"},
		{"invalid between valid", "a\xffb\xfec", "a�b�c"},
		{"truncated sequence", "a\xe2\x82", "a��"},
		{"surrogate half", "\xed\xa0\x80", "���"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := validRunes(c.in)
			assert.Equal(t, c.want, got)
			if c.in == c.want && c.in != "" {
				assert.Same(t, unsafe.StringData(c.in), unsafe.StringData(got), "valid text must not be copied")
			}
		})
	}
}

// FuzzTokenize lexes every Buzz program under the package's testdata and examples,
// plus hostile shapes. A success ends in EOF, keeps positions in source order and
// inside the source, and matches a second lex exactly, which a leak through the
// pooled buffer would break. A failure is the lexer's own error, never a panic.
func FuzzTokenize(f *testing.F) {
	for _, root := range []string{"../testdata", "../examples"} {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || filepath.Ext(path) != ".buzz" {
				return err
			}
			src, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			f.Add(string(src))
			return nil
		})
		require.NoError(f, err)
	}
	for _, src := range []string{
		"",
		"\"unterminated",
		"`unterminated",
		"\"{unterminated",
		"\"{\"nested",
		"/* unterminated",
		"\"a {\"b {c}\"} d\"",
		"\"{ {\"{`}`}\"} }\"",
		"`{`{`{x}`}`}`",
		"\"\xff\"",
		"`\xff{x}`",
		"\"\\\xff\"",
		"\"{\xff}\"",
		"// \xff\nx",
		"\xff",
		"a\r\nb",
		"// doc\r\n// more\r\nfun a() > void {}\r\n",
		"\"a\r\nb {x} \\t\"\r\ny",
		"/* \xff */\r\nx",
		"\"\\256\"",
		"\"\\0ab\\999\"",
		"\"{a}{b}{\"{c}\"}\"",
		"\"{\"{\"{`{x}`}\"}\"}\"",
		"@\"\xff\"",
		"@\"{x}\"",
		"h\xffllo",
		"\xef\xbb\xbfx",
		"$\"\\d+\\\"\"",
		"'\\n' 'a' '\\q'",
		"\"{a\n}\" x",
		"\"{\"a\\\nb\"}\"\nx",
		"x // c",
		"\"a\\\nb\" x",
		"$\"a\\\nb\" x",
		"'\n' x",
		"\"{`a\\`}\" x",
		"\"{\"x{\"}\"}\"}\" y",
		"\"", "`", "{", "}", "@", "$",
		strings.Repeat("a ", maxScratch+4000),
		strings.Repeat("// doc\nx\n", maxScratch),
		strings.Repeat("\"a {b} c\\t\"\n", maxScratch/2),
	} {
		f.Add(src)
	}
	errType := reflect.TypeOf(errors.New(""))

	f.Fuzz(func(t *testing.T, src string) {
		toks, err := Tokenize(src)
		if err != nil {
			require.Nil(t, toks)
			require.Equal(t, errType, reflect.TypeOf(err))
			require.True(t, strings.HasPrefix(err.Error(), "buzz: "), err.Error())
			return
		}
		require.NotEmpty(t, toks)
		require.Equal(t, EOF, toks[len(toks)-1].Kind)
		lines := strings.Split(src, "\n")
		prev := Token{Line: 1, Col: 1}
		for i, tok := range toks {
			require.Truef(t, tok.Line > prev.Line || tok.Line == prev.Line && tok.Col >= prev.Col,
				"token %d at %d:%d precedes %d:%d", i, tok.Line, tok.Col, prev.Line, prev.Col)
			require.LessOrEqualf(t, tok.Line, len(lines), "token %d line", i)
			require.GreaterOrEqualf(t, tok.Col, 1, "token %d col", i)
			require.LessOrEqualf(t, tok.Col, len(lines[tok.Line-1])+1, "token %d at %d:%d is past its line", i, tok.Line, tok.Col)
			prev = tok
		}
		again, err := Tokenize(src)
		require.NoError(t, err)
		require.Equal(t, toks, again)
	})
}

// TestTokenSize pins Kind packed beside Raw: every cached module holds one Token
// per lexeme, so a field that adds padding costs the whole cache. The unpacked
// layout, an int Kind first and Raw after Col, is one word larger on any target.
func TestTokenSize(t *testing.T) {
	type unpacked struct {
		Kind  int
		Val   string
		Parts []StringPart
		Line  int
		Col   int
		Raw   bool
		Doc   string
	}
	assert.Equal(t, unsafe.Sizeof(Kind(0)), unsafe.Offsetof(Token{}.Raw))
	assert.Equal(t, unsafe.Sizeof(unpacked{})-unsafe.Sizeof(0), unsafe.Sizeof(Token{}))
}

// TestTokenize_ReusedBufferLeaksNothing lexes a doc-heavy source and then a bare
// one: the second must not see a doc, value or extra token from the buffer the
// first returned to the pool, and each result is exactly its token count.
func TestTokenize_ReusedBufferLeaksNothing(t *testing.T) {
	first, err := Tokenize("// doc\nfun a() > void {}\n// more\nfun b() > void {}")
	require.NoError(t, err)
	second, err := Tokenize("x")
	require.NoError(t, err)

	assert.Equal(t, []Token{
		{Kind: Ident, Val: "x", Line: 1, Col: 1},
		{Kind: EOF, Line: 1, Col: 2},
	}, second)
	assert.Equal(t, len(first), cap(first))
	assert.Equal(t, len(second), cap(second))
	assert.Equal(t, "doc", first[0].Doc)
}

// TestKindStringExhaustive drives Kind.String across every declared kind, Ident
// through EOF. An unnamed kind surfaces in parse errors as a bare integer, which
// is exactly the message quality regression this guards against, and iterating
// the full range means a kind added without a String case fails here instead of
// in a user's error message.
func TestKindStringExhaustive(t *testing.T) {
	for k := Ident; k <= EOF; k++ {
		name := k.String()
		require.NotEmptyf(t, name, "Kind(%d) has no String name", int(k))
		require.NotContainsf(t, name, "Kind(", "Kind(%d) fell through to a raw integer rendering: %q", int(k), name)
	}
}

// TestKeywordsRoundTrip pins the keyword table's two views against each other:
// every word Keywords() lists must satisfy IsKeyword, and must tokenize to
// something other than a plain identifier.
func TestKeywordsRoundTrip(t *testing.T) {
	words := Keywords()
	require.NotEmpty(t, words)
	for _, w := range words {
		require.Truef(t, IsKeyword(w), "Keywords() lists %q but IsKeyword(%q) is false", w, w)
		toks, err := Tokenize(w)
		require.NoErrorf(t, err, "Tokenize(%q)", w)
		require.NotEmptyf(t, toks, "Tokenize(%q) produced no tokens", w)
		require.NotEqualf(t, Ident, toks[0].Kind, "keyword %q tokenized as a plain identifier", w)
	}
	require.False(t, IsKeyword("definitelyNotAKeyword"))
}

// TestTokenizeOperators sweeps every operator and punctuation spelling to its
// kind, asserting the whole Token per case. Operator tokens carry an EMPTY Val
// (the kind is the spelling; only literals and identifiers carry text), so a
// lexer that recognized `<<=` as `<<` + `=` fails on the token count and on the
// second token's kind.
func TestTokenizeOperators(t *testing.T) {
	cases := []struct {
		src  string
		want Kind
	}{
		{"(", LParen}, {")", RParen}, {"{", LBrace}, {"}", RBrace},
		{"[", LBracket}, {"]", RBracket}, {",", Comma}, {";", Semicolon},
		{":", Colon}, {".", Dot}, {"=", Assign}, {"?", Question},
		{"??", Coalesce}, {"!", Bang}, {"+", Plus}, {"-", Minus},
		{"*", Star}, {"/", Slash}, {"%", Percent}, {"==", Eq},
		{"!=", Neq}, {"<", Lt}, {">", Gt}, {"<=", Le}, {">=", Ge},
		{"..", DotDot}, {"!>", ErrArrow}, {"*>", YieldArrow}, {"=>", FatArrow},
		{"->", Arrow}, {"\\", Backslash}, {"&", Amp}, {"|", Pipe},
		{"^", Caret}, {"~", Tilde}, {"<<", Shl}, {">>", Shr},
		{"+=", PlusAssign}, {"-=", MinusAssign}, {"*=", StarAssign},
		{"/=", SlashAssign}, {"%=", PercentAssign}, {"&=", AmpAssign},
		{"|=", PipeAssign}, {"^=", CaretAssign}, {"<<=", ShlAssign}, {">>=", ShrAssign},
	}
	for _, c := range cases {
		t.Run(c.src, func(t *testing.T) {
			toks, err := Tokenize(c.src)
			require.NoError(t, err)
			require.Len(t, toks, 2, "want exactly the operator token plus EOF")
			require.Equal(t, Token{Kind: c.want, Line: 1, Col: 1}, toks[0])
			require.Equal(t, EOF, toks[1].Kind)
		})
	}
}

// TestTokenizeStringEscapes pins the escape table inside string literals.
func TestTokenizeStringEscapes(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"newline", `"a\nb"`, "a\nb"},
		{"tab", `"a\tb"`, "a\tb"},
		{"quote", `"a\"b"`, `a"b`},
		{"backslash", `"a\\b"`, `a\b`},
		{"carriage return", `"a\rb"`, "a\rb"},
		{"plain", `"plain text"`, "plain text"},
		{"empty", `""`, ""},
		{"escape after a run", `"run then \\ tail"`, `run then \ tail`},
		{"decimal byte escape", `"\065B"`, "AB"},
		{"unknown escape kept", `"a\qb"`, `a\qb`},
		{"escaped braces", `"\{x\}"`, "{x}"},
		{"multiline", "\"a\nb\"", "a\nb"},
		{"invalid utf-8 byte", "\"a\xffb\"", "a\uFFFDb"},
		{"invalid utf-8 after escape", "\"\\t\xff\"", "\t\uFFFD"},
		{"raw", "`a\\nb`", `a\nb`},
		{"raw escaped brace", "`a\\{b\\}`", "a{b}"},
		{"raw keeps invalid utf-8", "`a\xffb`", "a\xffb"},
		{"escaped open brace", `"a\{b"`, "a{b"},
		{"escaped close brace", `"a\}b"`, "a}b"},
		{"decimal byte escape zero", `"\000"`, "\x00"},
		{"decimal byte escape max", `"\255"`, "\xff"},
		{"digit escape without three digits kept", `"\0ab"`, `\0ab`},
		{"digit escape at the end kept", `"\12"`, `\12`},
		{"unknown multibyte escape kept", `"\é"`, `\é`},
		{"escaped invalid byte", "\"\\\xff\"", "\\\uFFFD"},
		{"invalid utf-8 before an escape", "\"\xff\\t\"", "\uFFFD\t"},
		{"newline after an escape", "\"\\t\nb\"", "\t\nb"},
		{"CRLF", "\"a\r\nb\"", "a\r\nb"},
		{"non-ASCII text", `"héllo 日本"`, "héllo 日本"},
		{"raw multiline", "`a\nb`", "a\nb"},
		{"raw CRLF", "`a\r\nb`", "a\r\nb"},
		{"raw trailing backslash", "`a\\`", `a\`},
		{"raw backslash before other text kept", "`a\\db`", `a\db`},
		{"raw newline after an escaped brace", "`\\{\nz`", "{\nz"},
		{"raw invalid utf-8 after an escaped brace", "`\\}\xff`", "}\xff"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			toks, err := Tokenize(c.src)
			require.NoError(t, err)
			require.Len(t, toks, 2)
			require.Equal(t, String, toks[0].Kind)
			require.Equal(t, c.want, toks[0].Val)
		})
	}
}

// TestTokenizeInterpolationParts pins how literal runs and expressions split,
// including a nested string whose braces must not close the expression.
func TestTokenizeInterpolationParts(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []StringPart
	}{
		{"runs around an expression", `"a {x} b"`, []StringPart{{Text: "a "}, {IsExpr: true, Text: "x"}, {Text: " b"}}},
		{"adjacent expressions", `"{a}{b}"`, []StringPart{{IsExpr: true, Text: "a"}, {IsExpr: true, Text: "b"}}},
		{"escape in a run", `"\t{x}\n"`, []StringPart{{Text: "\t"}, {IsExpr: true, Text: "x"}, {Text: "\n"}}},
		{"nested braces and string", `"{f({"k": "}"})} end"`, []StringPart{{IsExpr: true, Text: `f({"k": "}"})`}, {Text: " end"}}},
		{"escaped quote in nested string", `"{g("a\"}")}"`, []StringPart{{IsExpr: true, Text: `g("a\"}")`}}},
		{"invalid utf-8 in an expression", "\"{h(\"\xff\")}\"", []StringPart{{IsExpr: true, Text: "h(\"\uFFFD\")"}}},
		{"raw", "`p {q} r`", []StringPart{{Text: "p "}, {IsExpr: true, Text: "q"}, {Text: " r"}}},
		{"empty expression", `"{}"`, []StringPart{{IsExpr: true, Text: ""}}},
		{"nested braces", `"{ {a} }"`, []StringPart{{IsExpr: true, Text: " {a} "}}},
		{"nested interpolation stays in the expression", `"{"in {x}"} out"`, []StringPart{{IsExpr: true, Text: `"in {x}"`}, {Text: " out"}}},
		{"raw string inside an expression", "\"{`}`}\"", []StringPart{{IsExpr: true, Text: "`}`"}}},
		{"escaped backslash ends a nested string", `"{f("a\\")}"`, []StringPart{{IsExpr: true, Text: `f("a\\")`}}},
		{"newline inside an expression", "\"{a +\nb}\"", []StringPart{{IsExpr: true, Text: "a +\nb"}}},
		{"newline inside a nested string", "\"{f(\"a\nb\")}\"", []StringPart{{IsExpr: true, Text: "f(\"a\nb\")"}}},
		{"run after an escape and an expression", `"\t{x}y"`, []StringPart{{Text: "\t"}, {IsExpr: true, Text: "x"}, {Text: "y"}}},
		{"invalid utf-8 in a run", "\"\xff{x}\xfe\"", []StringPart{{Text: "�"}, {IsExpr: true, Text: "x"}, {Text: "�"}}},
		{"non-ASCII runs", `"é{x}日"`, []StringPart{{Text: "é"}, {IsExpr: true, Text: "x"}, {Text: "日"}}},
		{"raw keeps invalid utf-8 in a run", "`\xff{x}`", []StringPart{{Text: "\xff"}, {IsExpr: true, Text: "x"}}},
		{"raw escaped brace then an expression", "`\\{a{x}b`", []StringPart{{Text: "{a"}, {IsExpr: true, Text: "x"}, {Text: "b"}}},
		{"raw multiline run", "`a\n{x}`", []StringPart{{Text: "a\n"}, {IsExpr: true, Text: "x"}}},
		{"raw leaves a brace in a nested string unmatched", "`{\"a {b\"}`", []StringPart{{IsExpr: true, Text: `"a {b"`}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			toks, err := Tokenize(c.src)
			require.NoError(t, err)
			require.Len(t, toks, 2)
			require.Equal(t, InterpStr, toks[0].Kind)
			require.Equal(t, c.want, toks[0].Parts)
		})
	}
}

// TestTokenizeErrors pins the lexer's rejection of malformed input: each must
// return an error rather than a silent partial token stream.
func TestTokenizeErrors(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"unterminated string", `"abc`},
		{"unterminated pattern", `$"abc`},
		{"lone dollar", `$x`},
		{"dangling escape", `"a\`},
		{"decimal byte escape past 255", `"\256"`},
		{"unterminated raw string", "`abc"},
		{"unterminated interpolation", `"a {b`},
		{"unterminated string inside an interpolation", `"{"abc`},
		{"unterminated raw interpolation", "`{b"},
		{"free identifier that interpolates", `@"{x}"`},
		{"unterminated free identifier", `@"x`},
		{"unexpected character", "#"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Tokenize(c.src)
			require.Errorf(t, err, "Tokenize(%q) accepted malformed input", c.src)
		})
	}
}

// TestTokenize_NestedRawStringBackslashEscapesBacktick pins upstream's rule inside
// an interpolation: \` does not close a nested raw string, so the outer brace is
// never reached. Upstream reports the same input as an unterminated string.
func TestTokenize_NestedRawStringBackslashEscapesBacktick(t *testing.T) {
	_, err := Tokenize("\"{`a\\`}\" x")
	require.EqualError(t, err, "buzz: unterminated interpolation at line 1:1")
}
