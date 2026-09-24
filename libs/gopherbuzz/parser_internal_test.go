package buzz

import (
	"context"
	"maps"
	"slices"
	"sync"
	"testing"

	"github.com/egladman/magus/libs/gopherbuzz/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// padded appends comment lines until src passes minCachedSource, so the cache keeps
// it. Comments produce no tokens, so the program is unchanged.
func padded(src string) string {
	for len(src) < minCachedSource {
		src += "\n// padding to reach the cache's minimum source size"
	}
	return src + "\n"
}

func entrySize(t *testing.T, src string) int {
	t.Helper()
	toks, err := token.Tokenize(src)
	require.NoError(t, err)
	return len(src) + len(toks)*tokenSize
}

// TestParseCache_SharedAcrossSessions proves the second session READS the first
// one's entry rather than re-lexing: the entry is swapped for another program's
// tokens, and only a session that reads the cache runs that program.
func TestParseCache_SharedAcrossSessions(t *testing.T) {
	ctx := context.Background()
	src := padded("var answer = 42;")
	c := NewParseCache(1 << 20)

	first := NewSession(ctx, WithEmbedded(), WithParseCache(c))
	require.NoError(t, first.Exec(ctx, src))
	assert.Equal(t, int64(42), first.GetGlobal("answer").AsInt())
	require.Equal(t, []string{src}, slices.Collect(maps.Keys(c.tokens)))
	assert.Equal(t, entrySize(t, src), c.bytes)

	swapped, err := token.Tokenize("var answer = 7;")
	require.NoError(t, err)
	c.tokens[src] = swapped

	second := NewSession(ctx, WithEmbedded(), WithParseCache(c))
	require.NoError(t, second.Exec(ctx, src))
	assert.Equal(t, int64(7), second.GetGlobal("answer").AsInt(), "a session given the cache must read its entry")

	child := second.NewChild()
	require.NoError(t, child.Exec(ctx, src))
	assert.Equal(t, int64(7), child.GetGlobal("answer").AsInt(), "a child session must inherit the cache")

	uncached := NewSession(ctx, WithEmbedded())
	require.NoError(t, uncached.Exec(ctx, src))
	assert.Equal(t, int64(42), uncached.GetGlobal("answer").AsInt(), "a session without the option must not see any cache")
}

// TestParseCache_ParsingLeavesCachedTokensIntact pins the property that makes sharing
// safe: nothing downstream of the lexer writes to the token slice or to a token's
// interpolation parts, so what one session parses is what the next one reads.
func TestParseCache_ParsingLeavesCachedTokensIntact(t *testing.T) {
	ctx := context.Background()
	src := padded(`namespace demo\tokens;
/// greet renders a greeting.
fun greet(name: str) > str {
	return "hello {name}, from {"nested {name}"}";
}
var msg = greet("buzz");`)
	c := NewParseCache(1 << 20)
	toks, err := c.tokenize(src)
	require.NoError(t, err)
	want := make([]token.Token, len(toks))
	for i, tok := range toks {
		tok.Parts = slices.Clone(tok.Parts)
		want[i] = tok
	}

	for range 2 {
		s := NewSession(ctx, WithEmbedded(), WithParseCache(c))
		require.NoError(t, s.Exec(ctx, src))
		assert.Empty(t, s.Diagnostics(src))
	}

	got := c.tokens[src]
	assert.Equal(t, want, got)
	assert.Same(t, &toks[0], &got[0], "the entry must be the one first cached, not a replacement")
}

func TestParseCache_ParseMethodsMatchThePackageFunctions(t *testing.T) {
	// Top-level control flow: strict rejects it, embedded accepts it.
	src := padded("if (true) { var x = 1; }")
	c := NewParseCache(1 << 20)

	_, wantErr := Parse(src)
	require.Error(t, wantErr)
	_, err := c.Parse(src)
	require.Error(t, err)
	assert.Equal(t, wantErr.Error(), err.Error())

	want, err := ParseEmbedded(src)
	require.NoError(t, err)
	got, err := c.ParseEmbedded(src)
	require.NoError(t, err)
	assert.Equal(t, want, got)
	assert.Len(t, c.tokens, 1, "both modes lex through the one entry")

	var none *ParseCache
	got, err = none.ParseEmbedded(src)
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestParseCache_StartsOverAtTheBound(t *testing.T) {
	a, b := padded("var a = 1;"), padded("var b = 2;")
	c := NewParseCache(entrySize(t, a) + entrySize(t, b) - 1)

	_, err := c.tokenize(a)
	require.NoError(t, err)
	_, err = c.tokenize(b)
	require.NoError(t, err)

	assert.Equal(t, []string{b}, slices.Collect(maps.Keys(c.tokens)))
	assert.Equal(t, entrySize(t, b), c.bytes)
}

func TestParseCache_RetainsNothingItCannotHold(t *testing.T) {
	src := padded("var x = 1;")
	for name, c := range map[string]*ParseCache{
		"entry larger than the bound": NewParseCache(entrySize(t, src) - 1),
		"zero bound":                  NewParseCache(0),
		"negative bound":              NewParseCache(-1),
	} {
		t.Run(name, func(t *testing.T) {
			toks, err := c.tokenize(src)
			require.NoError(t, err)
			assert.NotEmpty(t, toks)
			assert.Empty(t, c.tokens)
			assert.Zero(t, c.bytes)
		})
	}
}

func TestParseCache_SkipsShortAndUnlexableSources(t *testing.T) {
	c := NewParseCache(1 << 20)

	_, err := c.tokenize("var x = 1;")
	require.NoError(t, err)
	_, err = c.tokenize(padded(`var s = "unterminated`))
	require.Error(t, err)

	assert.Empty(t, c.tokens)
}

func TestParseCache_NilLexesAfresh(t *testing.T) {
	var c *ParseCache
	src := padded("var x = 1;")

	a, err := c.tokenize(src)
	require.NoError(t, err)
	b, err := c.tokenize(src)
	require.NoError(t, err)

	assert.Equal(t, a, b)
	assert.NotSame(t, &a[0], &b[0])
}

// TestParseCache_ConcurrentSessions is meant for -race. The tight bound holds any one
// entry but never two, so sessions evict each other's entries while they read.
func TestParseCache_ConcurrentSessions(t *testing.T) {
	ctx := context.Background()
	srcs := []string{
		padded("var a = 1;"),
		padded(`namespace conc\b;` + "\nvar b = 2;"),
		padded(`fun c() > int { return 3; }`),
		padded(`var d = "{1 + 2}";`),
	}
	largest := 0
	for _, src := range srcs {
		largest = max(largest, entrySize(t, src))
	}
	for name, c := range map[string]*ParseCache{
		"roomy": NewParseCache(1 << 20),
		"tight": NewParseCache(largest * 3 / 2),
	} {
		t.Run(name, func(t *testing.T) {
			var wg sync.WaitGroup
			for range 16 {
				wg.Go(func() {
					for _, src := range srcs {
						s := NewSession(ctx, WithEmbedded(), WithParseCache(c))
						if err := s.Exec(ctx, src); err != nil {
							t.Error(err)
						}
					}
				})
			}
			wg.Wait()
			c.mu.RLock()
			defer c.mu.RUnlock()
			assert.LessOrEqual(t, c.bytes, c.maxBytes)
		})
	}
}

// TestSession_DeclaredNamespace checks the namespace a file declares, which upstream
// Buzz requires to be the first statement. Comments, doc comments and blank lines are
// not statements, and the lexer emits no token for them.
func TestSession_DeclaredNamespace(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []string
	}{
		{"first statement", "namespace a\\b;\nvar x = 1;", []string{"a", "b"}},
		{"after a line comment", "// header\n// more header\nnamespace a\\b;\nvar x = 1;", []string{"a", "b"}},
		{"after a doc comment", "/// module doc\nnamespace a\\b;\nvar x = 1;", []string{"a", "b"}},
		{"after a block comment", "/* license\n   text */\nnamespace a;\nvar x = 1;", []string{"a"}},
		{"after blank lines", "\n\n\nnamespace a;\nvar x = 1;", []string{"a"}},
		{"after an empty statement", ";\nnamespace a;\nvar x = 1;", []string{"a"}},
		{"after a standalone export", "export f;\nnamespace a;\nfun f() > void {}", []string{"a"}},
		{"with a parse error later in the file", "namespace a;\nvar = ;", []string{"a"}},
		{"after another statement", "import \"std\";\nnamespace a;", nil},
		{"none", "var x = 1;", nil},
		{"malformed", "namespace ;", nil},
		{"empty", "", nil},
	}
	ctx := context.Background()
	for _, tc := range cases {
		for _, src := range []string{tc.src, padded(tc.src)} {
			for mode, opts := range map[string][]Option{
				"strict":          nil,
				"embedded":        {WithEmbedded()},
				"strict cached":   {WithParseCache(NewParseCache(1 << 20))},
				"embedded cached": {WithEmbedded(), WithParseCache(NewParseCache(1 << 20))},
			} {
				t.Run(tc.name+"/"+mode, func(t *testing.T) {
					s := NewSession(ctx, opts...)
					assert.Equal(t, tc.want, s.declaredNamespace(src), "source %q", src)
				})
			}
		}
	}
}

// TestSession_DeclaredNamespace_AfterACommentBindsTheImport runs the case end to end:
// an imported module whose namespace follows a license header is reachable under it.
func TestSession_DeclaredNamespace_AfterACommentBindsTheImport(t *testing.T) {
	ctx := context.Background()
	s := NewSession(ctx, WithEmbedded(), WithParseCache(NewParseCache(1<<20)))
	s.SetModuleDecls("lib/util", padded("// SPDX-License-Identifier: MIT\n/// util helpers\nnamespace lib\\util;\nexport fun one() > int { return 1; }"))

	require.NoError(t, s.Exec(ctx, "import \"lib/util\";\nvar got = lib\\util\\one();"))
	assert.Equal(t, int64(1), s.GetGlobal("got").AsInt())
}
