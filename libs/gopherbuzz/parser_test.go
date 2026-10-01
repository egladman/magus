package buzz

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/egladman/magus/libs/gopherbuzz/ast"
	"github.com/egladman/magus/libs/gopherbuzz/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParse_ValidProgram(t *testing.T) {
	parseOK := func(t *testing.T, src string) {
		t.Helper()
		prog, err := ParseEmbedded(src)
		require.NoErrorf(t, err, "ParseEmbedded(%q): unexpected error", src)
		require.NotNil(t, prog, "Parse returned nil program without error")
	}

	t.Run("empty", func(t *testing.T) { parseOK(t, "") })
	t.Run("literal", func(t *testing.T) { parseOK(t, `var x: int = 42;`) })
	t.Run("function", func(t *testing.T) { parseOK(t, `fun add(a: int, b: int) > int { return a + b; }`) })
	t.Run("if statement", func(t *testing.T) { parseOK(t, `if (true) { var x: int = 1; }`) })
}

// A `::<T>` generic call argument must be captured on the CallExpr so the
// checker can use it as the call's result type (upstream Buzz semantics).
func TestParse_GenericCallTypeArg(t *testing.T) {
	prog, err := ParseEmbedded(`final x = b.readZAt::<double>(at: 0);`)
	require.NoError(t, err)
	decl, ok := prog.Stmts[0].(*ast.DeclStmt)
	require.Truef(t, ok, "stmt 0 is %T, want *ast.DeclStmt", prog.Stmts[0])
	call, ok := decl.Value.(*ast.CallExpr)
	require.Truef(t, ok, "decl value is %T, want *ast.CallExpr", decl.Value)
	assert.Equal(t, "double", call.TypeArg, "CallExpr.TypeArg")
}

func TestParse_InvalidSyntax(t *testing.T) {
	t.Run("incomplete function", func(t *testing.T) {
		_, err := ParseEmbedded(`fun (`)
		assert.Error(t, err)
	})
	t.Run("missing type", func(t *testing.T) {
		_, err := ParseEmbedded(`var x: = ;`)
		assert.Error(t, err)
	})
}

// TestNumericLiteralEval covers the non-decimal integer literals and underscore
// separators the lexer accepts (matching upstream Buzz: 0x/0b prefixes and _
// separators, no 0o/exponent/uppercase). Each source snippet must evaluate to
// the expected int64.
func TestNumericLiteralEval(t *testing.T) {
	ctx := context.Background()
	cases := map[string]int64{
		"return 0x1a;":       26,
		"return 0xFF_FF;":    65535,
		"return 0b1010;":     10,
		"return 1_000_000;":  1000000,
		"return 0xDEADBEEF;": 3735928559,
	}
	for src, want := range cases {
		sess := NewSession(ctx, WithEmbedded())
		v, err := sess.Eval(ctx, src)
		if err != nil {
			t.Errorf("%q: eval err: %v", src, err)
			continue
		}
		if got := v.String(); got != strconv.FormatInt(want, 10) {
			t.Errorf("%q: got %s, want %d", src, got, want)
		}
	}
}

// TestStandaloneExport covers upstream's `export name;` form, where a declaration is
// written plainly and exported by a separate statement: the shape upstream's own
// tests/utils/testing.buzz uses.
//
// It used to parse and then VANISH. `export` parses the statement that follows and
// sets IsExported on it, but a bare name parses as an expression statement, matched
// none of the declaration cases, and fell through with the export silently dropped:
// the name stayed invisible to importers and nothing said why. That is the failure
// this pins: a silently-ignored export is worse than an unsupported one.
func TestStandaloneExport(t *testing.T) {
	// The declaration may come BEFORE the export statement...
	prog, err := ParseEmbedded("object Foo { n: int = 1 }\nexport Foo;\n")
	require.NoError(t, err)
	requireExported(t, prog, "Foo")

	// ...or after it, which is why resolution waits until the file is fully parsed.
	prog, err = ParseEmbedded("export Bar;\nfun Bar() > int { return 1; }\n")
	require.NoError(t, err)
	requireExported(t, prog, "Bar")

	// Naming something that does not exist is an error, not a no-op.
	_, err = ParseEmbedded("export Missing;\n")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no declaration named Missing")

	// `export X as Y;` re-exports a value under a new name. It DESUGARS to
	// `export final Y = X`, so the declaration export path carries it and no runtime
	// machinery is needed. Upstream's tests/utils/testing.buzz uses this form, and
	// tests/behavior/run-file.buzz carries one too.
	prog, err = ParseEmbedded("import \"std\";\nexport std\\assert as assert;\n")
	require.NoError(t, err)
	requireExportedDecl(t, prog, "assert")

	// The modifier form is untouched.
	prog, err = ParseEmbedded("export fun Baz() > int { return 1; }\n")
	require.NoError(t, err)
	requireExported(t, prog, "Baz")
}

func requireExported(t *testing.T, prog *ast.Program, name string) {
	t.Helper()
	for _, st := range prog.Stmts {
		switch d := st.(type) {
		case *ast.FunDecl:
			if d.Name == name {
				assert.True(t, d.IsExported, "fun %s must be exported", name)
				return
			}
		case *ast.ObjectDecl:
			if d.Name == name {
				assert.True(t, d.IsExported, "object %s must be exported", name)
				return
			}
		}
	}
	t.Fatalf("no declaration named %s in the parsed program", name)
}

// requireExportedDecl asserts a `final`/`var` declaration of the given name is exported.
func requireExportedDecl(t *testing.T, prog *ast.Program, name string) {
	t.Helper()
	for _, st := range prog.Stmts {
		if d, ok := st.(*ast.DeclStmt); ok && d.Name == name {
			assert.True(t, d.IsExported, "decl %s must be exported", name)
			return
		}
	}
	t.Fatalf("no declaration named %s in the parsed program", name)
}

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

// TestInspectKitchenSink drives ast.Inspect over a REAL parse of a program using
// most statement and expression forms, then asserts the node census: which
// kinds were visited and that traversal reached inside each construct. The
// hand-built trees in ast/walk_test.go verify the visitor mechanics; this pins
// the traversal switch against what the parser actually produces, so a node
// kind whose children Inspect forgets to descend into shows up as a missing
// census entry rather than as a silently shallower walk.
func TestInspectKitchenSink(t *testing.T) {
	src := `
fun helper(n: int) > int {
	if (n > 1) { return n * 2; } else { return 0 - n; }
}

object Point {
	x: int,
	y: int,
	fun sum(this: Point) > int { return this.x + this.y; }
}

enum Color { red, green }

fun main() > void {
	var total = 0;
	final list = [1, 2, 3];
	final m = {"k": 1};
	while (total < 10) { total = total + 1; }
	do { total = total - 1; } until (total < 5)
	foreach (x in 1..3) { total = total + x; }
	foreach (item in list) { total = total + item; }
	final p = Point{ x = 1, y = 2 };
	total = total + p.sum() + m["k"] + list[0];
	final anon = fun (a: int) > int { return a + 1; };
	total = anon(total);
	final interp = "total is {total}";
	final cond = total > 0 and total < 1000 or false;
	if (cond) { total = helper(total); }
	if (!cond) { total = 0; }
	helper(total);
	final safe = helper(1) catch 0;
	try {
		throw "boom";
	} catch (e: str) {
		total = total + 1;
	}
}
`
	prog, err := ParseEmbedded(src)
	require.NoError(t, err, "parse")

	census := map[string]int{}
	for _, decl := range prog.Stmts {
		ast.Inspect(decl, func(n ast.Node) bool {
			census[fmt.Sprintf("%T", n)]++
			return true
		})
	}

	// Every construct the source spells must have been VISITED, and the counts
	// for the unambiguous ones must be exact: an off-by-one there means Inspect
	// double-visited or skipped a nesting level.
	wantExact := map[string]int{
		"*ast.FunDecl":     3, // helper, main, and Point.sum
		"*ast.ObjectDecl":  1,
		"*ast.WhileStmt":   1,
		"*ast.DoStmt":      1,
		"*ast.ForEachStmt": 2,
		"*ast.TryStmt":     1,
		"*ast.ThrowStmt":   1,
		"*ast.FunExpr":     1, // the anonymous fun
		"*ast.RangeExpr":   1, // 1..3
		"*ast.InterpExpr":  1, // "total is {total}"
		"*ast.ObjectLit":   1, // Point{...}
		"*ast.MapExpr":     1,
	}
	for kind, want := range wantExact {
		require.Equalf(t, want, census[kind], "census[%s]", kind)
	}

	// Present with parser-dependent counts: assert reached, not how many times.
	wantPresent := []string{
		"*ast.IfStmt", "*ast.ReturnStmt", "*ast.DeclStmt", "*ast.AssignStmt",
		"*ast.ExprStmt", "*ast.BlockStmt", "*ast.BinaryExpr", "*ast.UnaryExpr",
		"*ast.CallExpr", "*ast.MemberExpr", "*ast.IndexExpr", "*ast.ListExpr",
		"*ast.CatchExpr",
	}
	for _, kind := range wantPresent {
		require.Containsf(t, census, kind, "Inspect never visited a %s; either the "+
			"source no longer produces one or the traversal does not descend to it", kind)
	}

	// The early-stop contract on a real tree: stopping at every FunDecl must
	// suppress everything nested inside function bodies.
	stopped := map[string]int{}
	for _, decl := range prog.Stmts {
		ast.Inspect(decl, func(n ast.Node) bool {
			stopped[fmt.Sprintf("%T", n)]++
			_, isFun := n.(*ast.FunDecl)
			return !isFun
		})
	}
	require.Zero(t, stopped["*ast.WhileStmt"], "returning false at FunDecl must prune its body")
	require.Equal(t, 3, stopped["*ast.FunDecl"], "the pruned nodes themselves are still visited")
}

// BenchmarkParse measures lexer + parser throughput on a realistic magusfile.
func BenchmarkParse(b *testing.B) {
	src := `
import "host";
fun helper(x: int) > int {
    return x * x + 1;
}
object Config {
    name: str = "default",
    count: int = 0,
    fun describe() > str {
        return "Config({this.name}, {this.count})";
    }
}
enum Status { Ok, Err, Unknown }
host.project.register(".");
export fun build(_args: [str]) > void {}
export fun test(_args: [str]) > void {}
`
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := ParseEmbedded(src); err != nil {
			b.Fatal(err)
		}
	}
}

// benchModule is a spell-shaped module: doc comment blocks, a detached license
// header, interpolated strings and nested calls, repeated to the size of the
// larger spells magus ships.
func benchModule(repeat int) string {
	const unit = `
// Package-level settings for the build.
// They are read once per session and never mutated,
// so every target sees the same values.
object Settings{n} {
    name: str = "default",
    count: int = 0,

    // describe renders a one-line summary.
    fun describe() > str {
        return "Settings({this.name}, {this.count}) at {host\env\get("HOME")}";
    }
}

// Section: helpers.

/* squared returns x*x+1; it exists for the benchmark only. */
fun squared{n}(x: int) > int {
    return x * x + 1; // trailing comment is not a doc
}

export fun build{n}(args: [str]) > void {
    final res = proc\run(["go", "build", "-o", "bin/{args[0]}", "./..."], cwd: ".", env: {"CGO_ENABLED": "0"});
    if (res.code != 0) {
        throw "build failed: {res.stderr} ({res.code})";
    }
}
`
	var sb strings.Builder
	sb.WriteString("// Copyright header, detached by the blank line below.\n\nimport \"host\";\n")
	for i := range repeat {
		sb.WriteString(strings.ReplaceAll(unit, "{n}", strconv.Itoa(i)))
	}
	return sb.String()
}

// BenchmarkTokenize measures the lexer alone on a module the size of a large spell.
func BenchmarkTokenize(b *testing.B) {
	src := benchModule(30)
	b.SetBytes(int64(len(src)))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := token.Tokenize(src); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkParseCacheMiss measures a first parse through a ParseCache: the lex,
// the copy the cache retains, and the parse, as every module costs once per process.
func BenchmarkParseCacheMiss(b *testing.B) {
	src := benchModule(30)
	b.SetBytes(int64(len(src)))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := NewParseCache(64 << 20).ParseEmbedded(src); err != nil {
			b.Fatal(err)
		}
	}
}
