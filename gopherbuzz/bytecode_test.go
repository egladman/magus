package buzz

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egladman/gopherbuzz/ast"
	vmpackage "github.com/egladman/gopherbuzz/vm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBytecodeRoundTrip compiles every non-error conformance fixture, marshals
// the resulting chunk to bytes, unmarshals it back, executes the recovered
// chunk, and asserts the result matches the fixture's @expect value. This
// exercises the serializer across scalars, strings, enums, objects (including
// field defaults), closures, fibers, and control flow.
func TestBytecodeRoundTrip(t *testing.T) {
	files, err := filepath.Glob("testdata/*.buzz")
	require.NoError(t, err)
	require.NotEmpty(t, files, "no conformance test files found in testdata/")
	for _, path := range files {
		name := strings.TrimSuffix(filepath.Base(path), ".buzz")
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(path)
			require.NoErrorf(t, err, "read %s", path)
			meta := parseConformanceMeta(string(src))
			if meta.skip != "" {
				t.Skipf("skip: %s", meta.skip)
			}
			// Only round-trip programs that compile and run cleanly with an
			// expected value; error fixtures have nothing to serialize.
			// Skip fixtures that use `import "std"` since newSession does not
			// register the std synthetic module (use TestConformance for that).
			if meta.errStr != "" || meta.expect == "" || containsStdImport(string(src)) {
				t.Skip("not a value-producing fixture or requires std import")
			}

			ctx := context.Background()
			sess := newSession(ctx)
			chunk, err := sess.Compile(string(src))
			require.NoError(t, err, "compile")

			data, err := chunk.Marshal()
			require.NoError(t, err, "marshal")
			got, err := UnmarshalChunk(data)
			require.NoError(t, err, "unmarshal")
			require.NoError(t, sess.ExecChunk(ctx, got), "exec recovered chunk")
			assert.Equal(t, meta.expect, sess.GetGlobal("__r").String())
		})
	}
}

// TestBytecodeObjectDefault round-trips an object whose field defaults are
// non-trivial expressions (an interpolated string and a list literal),
// exercising the AST-node codec path that backs tagObjDecl constants.
func TestBytecodeObjectDefault(t *testing.T) {
	src := `
final WHO = "world";
object Config {
    label: str = "hi {WHO}",
    tags: [str] = ["a", "b"],
    fun describe() > str {
        return this.label;
    }
}
final c = Config{};
final __r = c.describe() + " " + c.tags[0];
`
	ctx := context.Background()

	// Baseline: run directly.
	base := newSession(ctx)
	require.NoError(t, base.Exec(ctx, src), "baseline exec")
	want := base.GetGlobal("__r").String()

	// Round-trip via ExecBytecode (exercises UnmarshalChunk + ExecChunk).
	sess := newSession(ctx)
	chunk, err := sess.Compile(src)
	require.NoError(t, err, "compile")
	data, err := chunk.Marshal()
	require.NoError(t, err, "marshal")
	require.NoError(t, sess.ExecBytecode(ctx, data), "ExecBytecode")
	assert.Equal(t, want, sess.GetGlobal("__r").String(), "round-trip __r")
}

// TestBytecodeExportsRoundTrip asserts a SharedGlobals module's exported names
// survive marshal/unmarshal: ExecChunk repopulates the session's export set from
// chunk.Exports, so a spell module loaded from bytecode still exposes its mgs_
// contract. Regression for the v2 format addition — before it, exports were
// dropped and a bytecode-loaded spell exported nothing.
func TestBytecodeExportsRoundTrip(t *testing.T) {
	const src = `export fun mgs_getName() > str { return "go"; }`
	ctx := context.Background()
	sess := newSession(ctx)
	chunk, err := sess.Compile(src)
	require.NoError(t, err, "compile")
	require.Contains(t, chunk.Exports, "mgs_getName", "compiled chunk Exports want to contain mgs_getName")
	data, err := chunk.Marshal()
	require.NoError(t, err, "marshal")
	// Load into a fresh session so exports can only come from the bytecode.
	fresh := newSession(ctx)
	require.NoError(t, fresh.ExecBytecode(ctx, data), "ExecBytecode")
	require.Contains(t, fresh.Exports(), "mgs_getName", "mgs_getName not exported after bytecode round-trip")
}

// TestBytecodeDebugRoundTrip marshals a chunk's bytecode (.bo) and debug info
// (.bdb) separately, recovers the chunk from the .bo alone, and asserts
// AttachDebug folds the source lines back onto the function tree to match the
// originally compiled chunk.
func TestBytecodeDebugRoundTrip(t *testing.T) {
	src := `
fun add(a: int, b: int) { return a + b; }
final __r = add(2, 3);
`
	ctx := context.Background()
	sess := newSession(ctx)
	chunk, err := sess.Compile(src)
	require.NoError(t, err, "compile")
	require.NotNil(t, chunk.Lines, "expected Compile to populate debug lines")

	bo, err := chunk.Marshal()
	require.NoError(t, err, "marshal")
	bdb, err := chunk.Marshal(DebugOnly())
	require.NoError(t, err, "marshal debug")

	got, err := UnmarshalChunk(bo)
	require.NoError(t, err, "unmarshal")
	require.Nil(t, got.Lines, "bytecode-only chunk should carry no debug lines before AttachDebug")
	require.NoError(t, got.AttachDebug(bdb), "attach debug")
	assert.Equal(t, chunk.Lines, got.Lines, "top-level lines")
	require.Len(t, got.Funs, len(chunk.Funs), "funs len")
	for i := range got.Funs {
		assert.Equalf(t, chunk.Funs[i].Lines, got.Funs[i].Lines, "fun[%d] lines", i)
	}

	// A .bdb with the wrong magic must be rejected, not silently ignored.
	fresh, err := UnmarshalChunk(bo)
	require.NoError(t, err, "unmarshal")
	bad := append([]byte(nil), bdb...)
	bad[0] ^= 0xFF
	assert.Error(t, fresh.AttachDebug(bad), "expected error for corrupt .bdb magic")
}

func TestBytecodeVersionGuard(t *testing.T) {
	sess := newSession(context.Background())
	chunk, err := sess.Compile("final __r = 1 + 2;")
	require.NoError(t, err, "compile")
	data, err := chunk.Marshal()
	require.NoError(t, err, "marshal")

	t.Run("bad magic", func(t *testing.T) {
		bad := append([]byte(nil), data...)
		bad[0] ^= 0xFF
		_, err := UnmarshalChunk(bad)
		assert.Error(t, err, "expected error for corrupted magic")
	})

	t.Run("version mismatch", func(t *testing.T) {
		bad := append([]byte(nil), data...)
		// Version is the 2 bytes immediately after the 4-byte magic.
		bad[4] ^= 0xFF
		_, err := UnmarshalChunk(bad)
		assert.Error(t, err, "expected error for version mismatch")
	})

	t.Run("truncated", func(t *testing.T) {
		_, err := UnmarshalChunk(data[:3])
		assert.Error(t, err, "expected error for truncated data")
	})

	t.Run("huge_count", func(t *testing.T) {
		// Valid header + empty Name, then Params count = 0xFFFFFFFF.
		// checkCount must reject this before make([]string, n) fires.
		var buf []byte
		buf = append(buf, 'B', 'Z', 'B', 'C')                                                  // magic
		buf = append(buf, byte(vmpackage.BytecodeVersion), byte(vmpackage.BytecodeVersion>>8)) // version LE
		buf = append(buf, 0, 0, 0, 0)                                                          // Name: u32(0) = ""
		buf = append(buf, 0xFF, 0xFF, 0xFF, 0xFF)                                              // Params count = 0xFFFFFFFF
		_, err := UnmarshalChunk(buf)
		assert.Error(t, err, "expected error for huge count")
	})
}

func TestParser_Import(t *testing.T) {
	prog, err := ParseEmbedded(`import "magus";`)
	require.NoError(t, err)
	require.Len(t, prog.Stmts, 1)
	imp, ok := prog.Stmts[0].(*ast.ImportStmt)
	require.Truef(t, ok, "want *ast.ImportStmt, got %T", prog.Stmts[0])
	assert.Equal(t, "magus", imp.Path, "path")
}

func TestParser_ConstDecl(t *testing.T) {
	prog, err := ParseEmbedded(`final x = 42;`)
	require.NoError(t, err)
	require.Len(t, prog.Stmts, 1)
	d, ok := prog.Stmts[0].(*ast.DeclStmt)
	require.Truef(t, ok, "want *ast.DeclStmt, got %T", prog.Stmts[0])
	assert.True(t, d.IsConst, "IsConst")
	assert.Equal(t, "x", d.Name, "Name")
}

func TestParser_FunExpr(t *testing.T) {
	src := `final f = fun(_args: [str]) > void {};`
	prog, err := ParseEmbedded(src)
	require.NoErrorf(t, err, "parse %q", src)
	require.Len(t, prog.Stmts, 1)
	d, ok := prog.Stmts[0].(*ast.DeclStmt)
	require.Truef(t, ok, "want *ast.DeclStmt, got %T", prog.Stmts[0])
	_, ok = d.Value.(*ast.FunExpr)
	require.Truef(t, ok, "want *ast.FunExpr, got %T", d.Value)
}

func TestParser_MapLit(t *testing.T) {
	src := `final m = {"key": "val"};`
	prog, err := ParseEmbedded(src)
	require.NoErrorf(t, err, "parse %q", src)
	d := prog.Stmts[0].(*ast.DeclStmt)
	m, ok := d.Value.(*ast.MapExpr)
	require.Truef(t, ok, "want *ast.MapExpr, got %T", d.Value)
	require.Len(t, m.Keys, 1, "map keys")
	assert.Equal(t, "key", m.Keys[0].(*ast.StringLit).Val, "map key")
}

func TestParser_CallChain(t *testing.T) {
	src := `host.project.register(".", {});`
	prog, err := ParseEmbedded(src)
	require.NoErrorf(t, err, "parse %q", src)
	require.Len(t, prog.Stmts, 1)
	es, ok := prog.Stmts[0].(*ast.ExprStmt)
	require.Truef(t, ok, "want *ast.ExprStmt, got %T", prog.Stmts[0])
	_, ok = es.Expr.(*ast.CallExpr)
	require.Truef(t, ok, "want *ast.CallExpr, got %T", es.Expr)
}

func TestEval_ConstBinding(t *testing.T) {
	sess := newSession(context.Background())
	require.NoError(t, sess.Exec(context.Background(), `final x = "hello";`))
	v := sess.GetGlobal("x")
	assert.True(t, v.IsStr(), "x should be str")
	assert.Equal(t, "hello", v.AsString(), "x")
}

func TestEval_DirectCall(t *testing.T) {
	sess := newSession(context.Background())
	called := false
	sess.SetGlobal("fn", vmpackage.DirectValue("fn", func(_ context.Context, args []vmpackage.Value) (vmpackage.Value, error) {
		called = true
		assert.Len(t, args, 1, "args")
		return vmpackage.Null, nil
	}))
	require.NoError(t, sess.Exec(context.Background(), `fn("hello");`))
	assert.True(t, called, "direct function was not called")
}

func TestEval_MemberAccess(t *testing.T) {
	sess := newSession(context.Background())
	m := vmpackage.NewMap()
	m.MapSet("name", vmpackage.StrValue("test"))
	sess.SetGlobal("obj", m)

	var gotName vmpackage.Value
	sess.SetGlobal("capture", vmpackage.DirectValue("capture", func(_ context.Context, args []vmpackage.Value) (vmpackage.Value, error) {
		if len(args) > 0 {
			gotName = args[0]
		}
		return vmpackage.Null, nil
	}))

	require.NoError(t, sess.Exec(context.Background(), `capture(obj.name);`))
	assert.True(t, gotName.IsStr(), "obj.name should be str")
	assert.Equal(t, "test", gotName.AsString(), "obj.name")
}

// TestEval_NameCacheCrossInstance guards against the inline name cache serving
// one object instance's `this` to another instance's method. Both calls happen
// inside run()'s body with no intervening name op, so the cache (keyed by chunk)
// must also key on the resolving env or b.get() wrongly returns a's field.
func TestEval_NameCacheCrossInstance(t *testing.T) {
	sess := newSession(context.Background())
	src := `
object Box { n: int = 0, fun get() > int { return this.n; } }
fun run(a: int, b: int) > int {
    var x = a.get();
    var y = b.get();
    return x * 10 + y;
}
final a = Box{ n = 1 };
final b = Box{ n = 2 };
final result = run(a, b);
`
	require.NoError(t, sess.Exec(context.Background(), src))
	assert.Equal(t, int64(12), sess.GetGlobal("result").AsInt(), "run(a,b) want 12 (a.get()=1, b.get()=2)")
}

// TestEval_DoUntilCancellable guards against an infinite do..until loop ignoring
// context cancellation. The loop's back-edge is OpJumpFalse, which must poll the
// context like OpJump or the loop is unkillable.
func TestEval_DoUntilCancellable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled: the first back-edge poll must abort the loop
	sess := newSession(ctx)
	err := sess.Exec(ctx, `var i = 0; do { i = i + 1; } until (i < 0);`)
	assert.Error(t, err, "infinite do..until under cancelled ctx returned nil; loop is uncancellable")
}

func TestEval_FunClosure(t *testing.T) {
	sess := newSession(context.Background())
	var stored vmpackage.Value
	sess.SetGlobal("register", vmpackage.DirectValue("register", func(_ context.Context, args []vmpackage.Value) (vmpackage.Value, error) {
		if len(args) >= 2 {
			stored = args[1]
		}
		return vmpackage.Null, nil
	}))

	require.NoError(t, sess.Exec(context.Background(), `register("build", fun() > void {});`))
	assert.True(t, stored.IsFun(), "stored should be fun")
}

func TestEval_TargetNew(t *testing.T) {
	sess := newSession(context.Background())
	targets := make(map[string]vmpackage.Callable)

	sess.SetGlobal("target_new", vmpackage.DirectValue("target_new", func(_ context.Context, args []vmpackage.Value) (vmpackage.Value, error) {
		if len(args) < 2 {
			return vmpackage.Null, nil
		}
		name := args[0].AsString()
		fn := args[1]
		targets[strings.ToLower(name)] = func(ctx context.Context, callArgs []vmpackage.Value) (vmpackage.Value, error) {
			return sess.CallValue(ctx, fn, callArgs)
		}
		return vmpackage.Null, nil
	}))

	require.NoError(t, sess.Exec(context.Background(), `target_new("build", fun() > void {});`))
	require.Contains(t, targets, "build", "build target was not registered")
	_, err := targets["build"](context.Background(), nil)
	assert.NoError(t, err, "invoke build")
}

func TestEval_MagusfilePattern(t *testing.T) {
	ctx := context.Background()
	sess := NewSession(ctx, WithEmbedded())
	defer sess.Close()

	registered := ""
	projectNS := vmpackage.NewMap()
	projectNS.MapSet("register", vmpackage.DirectValue("register", func(_ context.Context, args []vmpackage.Value) (vmpackage.Value, error) {
		if len(args) > 0 {
			registered = args[0].AsString()
		}
		return vmpackage.Null, nil
	}))
	host := vmpackage.NewMap()
	host.MapSet("project", projectNS)
	sess.SetGlobal("host", host)

	src := `
host.project.register(".");
export fun build(_args: [str]) > void {}
export fun test(_args: [str]) > void {}
`
	require.NoError(t, sess.Exec(ctx, src), "exec")

	assert.Equal(t, ".", registered, "registered")
	exports := sess.Exports()
	assert.Contains(t, exports, "build", "build target missing")
	// `test` remains a valid identifier/target (contextual keyword): the magusfile
	// `export fun test` pattern must keep working.
	assert.Contains(t, exports, "test", "test target missing")
}

// TestTestBlocks verifies the upstream `test "name" { ... }` construct: blocks
// register but do not run on a normal Exec, Session.Tests() exposes them in
// source order, and running a block surfaces a raised error as a failure.
func TestTestBlocks(t *testing.T) {
	ctx := context.Background()
	sess := NewSession(ctx, WithEmbedded())
	defer func() { _ = sess.Close() }()

	ran := 0
	sess.SetGlobal("touch", vmpackage.DirectValue("touch", func(_ context.Context, _ []vmpackage.Value) (vmpackage.Value, error) {
		ran++
		return vmpackage.Null, nil
	}))
	sess.SetGlobal("boom", vmpackage.DirectValue("boom", func(_ context.Context, _ []vmpackage.Value) (vmpackage.Value, error) {
		return vmpackage.Null, fmt.Errorf("kaboom")
	}))

	src := `
test "first" { touch(); }
test "second" { touch(); }
test "failing" { boom(); }
`
	require.NoError(t, sess.Exec(ctx, src), "exec")

	// Bodies must not run during a normal execution.
	assert.Zero(t, ran, "test bodies ran during normal Exec")

	tests := sess.Tests()
	require.Len(t, tests, 3, "Tests() len")
	assert.Equal(t, "first", tests[0].Name)
	assert.Equal(t, "second", tests[1].Name)
	assert.Equal(t, "failing", tests[2].Name)

	// Running the blocks: the first two pass, the third surfaces its error.
	for _, tc := range tests[:2] {
		_, err := sess.CallValue(ctx, tc.Fn, nil)
		assert.NoErrorf(t, err, "test %q unexpectedly failed", tc.Name)
	}
	_, err := sess.CallValue(ctx, tests[2].Fn, nil)
	assert.Error(t, err, "failing test returned nil error, want failure")
	assert.Equal(t, 2, ran, "touch called wrong number of times after running tests")
}

// TestTestKeywordIsContextual verifies `test` is a soft keyword: it introduces a
// test block in the `test "..." {` position yet remains usable as an ordinary
// identifier in the same program (the magus embedding relies on `test` targets).
func TestTestKeywordIsContextual(t *testing.T) {
	ctx := context.Background()
	sess := NewSession(ctx, WithEmbedded())
	defer func() { _ = sess.Close() }()

	src := `
final test = 7;
test "a block named like the variable" { }
`
	require.NoError(t, sess.Exec(ctx, src), "exec")
	v := sess.GetGlobal("test")
	assert.True(t, v.IsInt(), "identifier `test` should be int")
	assert.Equal(t, int64(7), v.AsInt(), "identifier `test`")
	tests := sess.Tests()
	require.Len(t, tests, 1, "Tests() want one block")
	assert.Equal(t, "a block named like the variable", tests[0].Name)
}

// TestExport_FunIsExported verifies that an exported function appears in
// Session.Exports() while a non-exported helper does not.
func TestExport_FunIsExported(t *testing.T) {
	ctx := context.Background()
	sess := NewSession(ctx, WithEmbedded())
	defer sess.Close()

	src := `
export fun build(args: [str]) > void {}
fun helper() > void {}
`
	require.NoError(t, sess.Exec(ctx, src), "exec")
	exports := sess.Exports()
	require.NotNil(t, exports, "Exports() returned nil")
	assert.Contains(t, exports, "build", "exported 'build' missing from Exports()")
	assert.NotContains(t, exports, "helper", "non-exported 'helper' should not appear in Exports()")
}

// TestExport_DeclIsExported verifies that an exported final appears in Exports().
func TestExport_DeclIsExported(t *testing.T) {
	ctx := context.Background()
	sess := NewSession(ctx, WithEmbedded())
	defer sess.Close()

	require.NoError(t, sess.Exec(ctx, `export final version = "1.0";`), "exec")
	exports := sess.Exports()
	v, ok := exports["version"]
	require.True(t, ok, "exported 'version' missing from Exports()")
	assert.Equal(t, "1.0", v.String(), "version value")
}

// TestNamespaceStmt verifies that a namespace declaration parses without error.
func TestNamespaceStmt(t *testing.T) {
	ctx := context.Background()
	sess := NewSession(ctx, WithEmbedded())
	defer sess.Close()

	src := `
namespace my\module;
export final x = 42;
`
	require.NoError(t, sess.Exec(ctx, src), "namespace decl should not error")
	exports := sess.Exports()
	assert.Contains(t, exports, "x", "exported 'x' missing after namespace declaration")
}

// TestImport_AsAlias verifies that `import "file" as alias` binds under the alias.
func TestImport_AsAlias(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "util.buzz"), []byte(`final helper = 99;`), 0644))

	ctx := context.Background()
	sess := NewSession(ctx, WithEmbedded())
	defer sess.Close()
	sess.SetIncludeDirs([]string{dir})

	// import as alias: "util" loaded, bound under "u"
	require.NoError(t, sess.Exec(ctx, `import "util" as u; final got = u.helper;`), "exec")
}

// TestCyclicImportTerminates verifies that mutually-importing .buzz files do
// not cause infinite recursion. loadedPaths in the Session guards the cycle.
func TestCyclicImportTerminates(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.buzz"), []byte(`import "b"; final from_a = 1;`), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "b.buzz"), []byte(`import "a"; final from_b = 2;`), 0644))

	ctx := context.Background()
	sess := NewSession(ctx, WithEmbedded())
	defer sess.Close()
	sess.SetIncludeDirs([]string{dir})

	require.NoError(t, sess.Exec(ctx, `import "a";`), "cyclic import should not error")
}

// TestImport_SearchPathFullImportPath verifies the whole import path — not just
// its trailing segment — is substituted for `?` in a search-path template,
// matching upstream Buzz: import "lib/mod" resolves lib/mod.buzz, not mod.buzz.
func TestImport_SearchPathFullImportPath(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "lib"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "lib", "mod.buzz"), []byte(`export final answer = 42;`), 0o644))

	ctx := context.Background()
	sess := NewSession(ctx, WithEmbedded(), WithSearchPaths(filepath.Join(dir, "?.buzz")))
	defer sess.Close()

	v, err := sess.Eval(ctx, `import "lib/mod"; return answer;`)
	require.NoError(t, err, "eval")
	assert.True(t, v.IsInt(), "answer should be int")
	assert.Equal(t, int64(42), v.AsInt(), "answer")
}

// TestImport_DefaultSearchPathsNested verifies an unconfigured session resolves
// the upstream `./?/main.buzz` layout relative to the working directory, and that
// WithSearchPaths() with no arguments leaves the session on DefaultSearchPaths.
func TestImport_DefaultSearchPathsNested(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "widget"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "widget", "main.buzz"), []byte(`export final tag = "w";`), 0o644))
	t.Chdir(dir) // ./widget/main.buzz resolves relative to cwd

	ctx := context.Background()
	sess := NewSession(ctx, WithEmbedded(), WithSearchPaths()) // no paths -> DefaultSearchPaths
	defer sess.Close()

	v, err := sess.Eval(ctx, `import "widget"; return tag;`)
	require.NoError(t, err, "eval")
	assert.True(t, v.IsStr(), "tag should be str")
	assert.Equal(t, "w", v.AsString(), "tag")
}

// TestFiberBasic covers the generator pattern: a fiber yields a sequence of
// values and then returns a final one. resume returns each yielded value, then
// null on completion; resolve returns the cached return value.
func TestFiberBasic(t *testing.T) {
	sess := newSession(context.Background())
	src := `
fun gen() > int {
    yield 1;
    yield 2;
    return 3;
}
final f = &gen();
final a = resume f;
final b = resume f;
final c = resume f;
final r = resolve f;
`
	require.NoError(t, sess.Exec(context.Background(), src))
	for name, want := range map[string]int64{"a": 1, "b": 2} {
		assert.Equalf(t, want, sess.GetGlobal(name).AsInt(), "%s", name)
	}
	assert.True(t, sess.GetGlobal("c").IsNull(), "resume on fiber completion should return null")
	assert.Equal(t, int64(3), sess.GetGlobal("r").AsInt(), "resolve r")
}

// TestFiberArgsAndClosure checks that &fn(args) forwards arguments and that
// local state (loop counter) survives across yield/resume boundaries.
func TestFiberArgsAndClosure(t *testing.T) {
	sess := newSession(context.Background())
	src := `
fun counter(start: int) > int {
    var i = start;
    while (i < start + 3) {
        yield i;
        i = i + 1;
    }
    return -1;
}
final f = &counter(10);
final a = resume f;
final b = resume f;
final c = resume f;
final d = resume f;
final r = resolve f;
`
	require.NoError(t, sess.Exec(context.Background(), src))
	for name, want := range map[string]int64{"a": 10, "b": 11, "c": 12} {
		assert.Equalf(t, want, sess.GetGlobal(name).AsInt(), "%s", name)
	}
	assert.True(t, sess.GetGlobal("d").IsNull(), "resume on fiber completion should return null")
	assert.Equal(t, int64(-1), sess.GetGlobal("r").AsInt(), "resolve r")
}

// TestFiberCancellation verifies a non-terminating fiber observes context
// cancellation when resumed (the loop's back-edge polls ctx).
func TestFiberCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	sess := newSession(ctx)
	require.NoError(t, sess.Exec(context.Background(),
		`fun spin() > int { var i = 0; while (i >= 0) { i = i + 1; } return i; }
final f = &spin();`))
	assert.Error(t, sess.Exec(ctx, `final r = resume f;`), "resume of infinite-loop fiber under cancelled ctx did not error")
}

// TestFiberRecursiveResumeGuard checks that resuming a fiber from within its own
// (still running) execution is rejected rather than corrupting the VM.
func TestFiberRecursiveResumeGuard(t *testing.T) {
	sess := newSession(context.Background())
	src := `
var f = null;
f = &(fun() > int { return resume f; })();
final r = resume f;
`
	assert.Error(t, sess.Exec(context.Background(), src), "recursive resume of a running fiber should error")
}

// TestFiberResumeDoneReturnsNull checks that resuming a completed fiber returns
// null (upstream parity: resume on a done fiber is not an error).
func TestFiberResumeDoneReturnsNull(t *testing.T) {
	sess := newSession(context.Background())
	src := `
fun gen() > int { return 1; }
final f = &gen();
final a = resume f;
final b = resume f;
`
	require.NoError(t, sess.Exec(context.Background(), src))
	assert.True(t, sess.GetGlobal("a").IsNull(), "first resume (completes gen) should return null")
	assert.True(t, sess.GetGlobal("b").IsNull(), "resume of a done fiber should return null")
}

// TestFiberErrorResurfaced checks that a fiber whose VM errors caches the error
// so a *later* resume/resolve re-surfaces it, rather than swallowing it and
// returning null (the fiber is FiberDone after the failure, and the done-branch
// must report the cached error, not a zero return value).
func TestFiberErrorResurfaced(t *testing.T) {
	ctx := context.Background()
	t.Run("resolve then resolve", func(t *testing.T) {
		sess := newSession(ctx)
		require.NoError(t, sess.Exec(ctx, `fun boom() { throw "boom"; } final f = &boom();`))
		assert.Error(t, sess.Exec(ctx, `final a = resolve f;`), "first resolve of a throwing fiber should error")
		assert.Error(t, sess.Exec(ctx, `final b = resolve f;`), "re-resolve of an errored fiber should re-surface the error, not return null")
	})
	t.Run("resume then resolve", func(t *testing.T) {
		sess := newSession(ctx)
		require.NoError(t, sess.Exec(ctx, `fun boom() { throw "boom"; } final f = &boom();`))
		assert.Error(t, sess.Exec(ctx, `final a = resume f;`), "resume of a throwing fiber should error")
		assert.Error(t, sess.Exec(ctx, `final b = resolve f;`), "resolve after a resume that errored should re-surface the error, not return null")
	})
}

// TestYieldOutsideFiberDismissed checks that a top-level yield (no enclosing
// fiber) is silently dismissed and not an error (upstream parity).
func TestYieldOutsideFiberDismissed(t *testing.T) {
	sess := newSession(context.Background())
	require.NoError(t, sess.Exec(context.Background(), `yield 1;`), "yield outside a fiber should not error")
}

// TestFiberDirectRejected checks that wrapping a direct (Go) callable with &
// is rejected: direct callables have no Buzz bytecode and cannot yield.
func TestFiberDirectRejected(t *testing.T) {
	sess := newSession(context.Background())
	sess.SetGlobal("nat", vmpackage.DirectValue("nat", func(_ context.Context, _ []vmpackage.Value) (vmpackage.Value, error) {
		return vmpackage.IntValue(42), nil
	}))
	assert.Error(t, sess.Exec(context.Background(), `final f = &nat();`), "& on a direct callable should error")
}

// TestFiberDebugIntrospectsFiberStack verifies that Frames()/CallDepth() report
// the *fiber's* call stack (not the outer VM's) while a direct callable is executing
// inside a resumed fiber. Before the fix, curVM still pointed at the suspended
// outer VM so the debugger saw the wrong frames.
func TestFiberDebugIntrospectsFiberStack(t *testing.T) {
	sess := newSession(context.Background())

	type snapshot struct {
		depth  int
		frames []vmpackage.DebugFrame
	}
	var got snapshot

	// inner() is called from inside the fiber body so the fiber stack is at
	// least 2 frames deep (top-level + gen + inner). probe() captures the
	// session's view of the live stack at that moment.
	sess.SetGlobal("probe", vmpackage.DirectValue("probe", func(_ context.Context, _ []vmpackage.Value) (vmpackage.Value, error) {
		got.depth = sess.CallDepth()
		got.frames = sess.Frames()
		return vmpackage.Null, nil
	}))

	src := `
fun inner() > int { probe(); return 1; }
fun gen() > int {
    yield inner();
    return 0;
}
final f = &gen();
final v = resume f;
`
	require.NoError(t, sess.Exec(context.Background(), src))

	// probe() fires inside inner(), which is called from gen() running inside
	// the fiber. Expect at least 2 frames (inner + gen) and the innermost frame
	// to be named "inner".
	assert.GreaterOrEqual(t, got.depth, 2, "CallDepth want >= 2 (fiber stack not visible)")
	require.NotEmpty(t, got.frames, "Frames() returned empty inside fiber")
	assert.Equal(t, "inner", got.frames[0].Name, "innermost frame")
}

// TestFiberConcurrentSessions mirrors the magus pool model: N goroutines, each
// owning its own Session, each running a fiber generator. Sessions share no
// mutable state, so this must be race-free under -race.
func TestFiberConcurrentSessions(t *testing.T) {
	const n = 16
	var wg sync.WaitGroup
	errs := make([]error, n)
	got := make([]int64, n)
	for g := 0; g < n; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			sess := newSession(context.Background())
			src := `
fun gen() > int { yield 1; yield 2; return 7; }
final f = &gen();
var sum = 0;
sum = sum + resume f;
sum = sum + resume f;
resume f;
`
			if err := sess.Exec(context.Background(), src); err != nil {
				errs[g] = err
				return
			}
			got[g] = sess.GetGlobal("sum").AsInt()
		}(g)
	}
	wg.Wait()
	for g := 0; g < n; g++ {
		require.NoErrorf(t, errs[g], "g%d", g)
		assert.Equalf(t, int64(3), got[g], "g%d sum", g)
	}
}

// runProgJIT compiles src and runs it with the JIT forced on or off, returning
// the top-level return value and any error.
func runProgJIT(t *testing.T, src string, jit bool) (vmpackage.Value, error) {
	t.Helper()
	prog, err := ParseEmbedded(src)
	require.NoError(t, err, "parse")
	chunk, err := CompileWith(prog, CompileOptions{})
	require.NoError(t, err, "compile")
	env := vmpackage.NewEnv()
	vmpackage.RegisterStdlib(env)
	vmpackage.SetJIT(jit)
	defer vmpackage.SetJIT(false)
	return vmpackage.NewVM(context.Background()).Run(chunk, env)
}

// assertJITMatchesInterp runs src with the JIT off then on and requires both
// engines agree, plus that the JIT actually engaged on the top-level loop.
func assertJITMatchesInterp(t *testing.T, src string) {
	t.Helper()
	want, err := runProgJIT(t, src, false)
	require.NoError(t, err, "interp")
	vmpackage.ResetJITStats()
	got, err := runProgJIT(t, src, true)
	require.NoError(t, err, "jit")
	if vmpackage.JITAvailable() {
		require.NotZero(t, vmpackage.JITRunCount(), "JIT did not engage (chunk ineligible?)")
	}
	require.Equal(t, want.String(), got.String(), "jit vs interp")
}

// TestJITMatchesInterpreter is the core differential test: every program must
// produce the identical result with the JIT on and off, and the JIT must
// actually engage on the top-level loop. Each program is a top-level integer or
// float loop the baseline JIT is meant to cover.
func TestJITMatchesInterpreter(t *testing.T) {
	t.Run("loopsum", func(t *testing.T) {
		assertJITMatchesInterp(t, `var sum = 0; var i = 0;
			while (i < 1000) { sum = sum + i; i = i + 1; } return sum;`)
	})
	t.Run("loopeq_even", func(t *testing.T) {
		assertJITMatchesInterp(t, `var count = 0; var i = 0;
			while (i < 1000) { if (i % 2 == 0) { count = count + 1; } i = i + 1; } return count;`)
	})
	t.Run("mul", func(t *testing.T) {
		assertJITMatchesInterp(t, `var acc = 1; var i = 1;
			while (i <= 10) { acc = acc * 2; i = i + 1; } return acc;`)
	})
	t.Run("nested", func(t *testing.T) {
		assertJITMatchesInterp(t, `var total = 0; var i = 0;
			while (i < 50) { var j = 0; while (j < 50) { total = total + 1; j = j + 1; } i = i + 1; }
			return total;`)
	})
	t.Run("sub_div_mod", func(t *testing.T) {
		assertJITMatchesInterp(t, `var x = 1000; var steps = 0;
			while (x > 1) { if (x % 2 == 0) { x = x / 2; } else { x = x - 1; } steps = steps + 1; }
			return steps;`)
	})
	t.Run("const_first", func(t *testing.T) {
		assertJITMatchesInterp(t, `var i = 0; var s = 0;
			while (1000 > i) { s = s + i; i = i + 1; } return s;`)
	})
	t.Run("float_sum", func(t *testing.T) {
		assertJITMatchesInterp(t, `var sum = 0.0; var i = 0.0;
			while (i < 1000.0) { sum = sum + i; i = i + 1.0; } return sum;`)
	})
	t.Run("float_mul", func(t *testing.T) {
		assertJITMatchesInterp(t, `var acc = 1.0; var i = 0.0;
			while (i < 10.0) { acc = acc * 2.0; i = i + 1.0; } return acc;`)
	})
	t.Run("float_div", func(t *testing.T) {
		assertJITMatchesInterp(t, `var x = 1024.0; var i = 0.0;
			while (i < 10.0) { x = x / 2.0; i = i + 1.0; } return x;`)
	})
	t.Run("float_cmp", func(t *testing.T) {
		assertJITMatchesInterp(t, `var c = 0.0; var i = 0.0;
			while (i < 1000.0) { if (i >= 500.0) { c = c + 1.0; } i = i + 1.0; } return c;`)
	})
	t.Run("mixed_promote", func(t *testing.T) {
		assertJITMatchesInterp(t, `var sum = 0.0; var i = 0;
			while (i < 100) { sum = sum + i; i = i + 1; } return sum;`)
	})
	t.Run("and_cond", func(t *testing.T) {
		// Short-circuit operators in a loop condition compile to OpJumpFalsePeek /
		// OpJumpTruePeek (+ OpPop). These must JIT and match the interpreter.
		assertJITMatchesInterp(t, `var i = 0; var s = 0;
			while (i < 1000 and s < 100000) { s = s + i; i = i + 1; } return s;`)
	})
	t.Run("or_cond", func(t *testing.T) {
		assertJITMatchesInterp(t, `var i = 0; var s = 0;
			while (i < 1000 or s > 0) { s = s + i; i = i + 1; } return s;`)
	})
	t.Run("mixed_mul", func(t *testing.T) {
		// Mixed int*float promotion in the float path (px:int * 0.0125:float).
		assertJITMatchesInterp(t, `var px = 0; var acc = 0.0;
			while (px < 100) { acc = acc + (px * 0.0125 - 1.5); px = px + 1; } return acc;`)
	})
	t.Run("mandelbrot", func(t *testing.T) {
		// The Mandelbrot kernel itself: nested loops, an `and` escape condition, and
		// mixed int/float arithmetic — the whole reason this work exists.
		assertJITMatchesInterp(t, `var checksum = 0; var py = 0;
			while (py < 30) {
			  var px = 0;
			  while (px < 30) {
			    var x0 = px * 0.0625 - 1.5; var y0 = py * 0.05 - 1.0;
			    var zx = 0.0; var zy = 0.0; var iter = 0;
			    while (iter < 100 and zx * zx + zy * zy <= 4.0) {
			      var tmp = zx * zx - zy * zy + x0; zy = 2.0 * zx * zy + y0; zx = tmp; iter = iter + 1;
			    }
			    checksum = checksum + iter; px = px + 1;
			  }
			  py = py + 1;
			}
			return checksum;`)
	})
}

// TestJITDeopt forces a guard miss: an `any`-typed value laundered into the loop
// variable makes an int op see a non-int operand. The JIT must deopt to the
// interpreter and produce the exact same outcome (here, a runtime type error)
// as the pure-interpreter run.
func TestJITDeopt(t *testing.T) {
	// `bad` is any-typed holding a string; `n + bad` inside the loop forces the
	// fused int op to see a non-int and deopt. Both engines must agree.
	src := `var bad: any = "x"; var n = 0; var i = 0;
		while (i < 5) { n = n + i; i = i + 1; }
		n = n + bad; return n;`
	want, errWant := runProgJIT(t, src, false)
	vmpackage.ResetJITStats()
	got, errGot := runProgJIT(t, src, true)
	require.Equalf(t, errWant == nil, errGot == nil, "error mismatch: interp=%v jit=%v", errWant, errGot)
	if errWant == nil {
		assert.Equal(t, want.String(), got.String(), "value mismatch jit vs interp")
	}
}

// TestJITCancellation confirms a JIT'd loop honors context cancellation via the
// back-edge poll: a long loop must return the cancellation error promptly
// instead of running to completion.
func TestJITCancellation(t *testing.T) {
	if !vmpackage.JITAvailable() {
		t.Skip("no JIT backend on this build")
	}
	prog, err := ParseEmbedded(`var i = 0; while (i < 1000000000) { i = i + 1; } return i;`)
	require.NoError(t, err, "parse")
	chunk, err := CompileWith(prog, CompileOptions{})
	require.NoError(t, err, "compile")
	env := vmpackage.NewEnv()
	vmpackage.RegisterStdlib(env)
	vmpackage.SetJIT(true)
	defer vmpackage.SetJIT(false)

	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()

	done := make(chan struct{})
	var runErr error
	go func() {
		_, runErr = vmpackage.NewVM(ctx).Run(chunk, env)
		close(done)
	}()
	select {
	case <-done:
		assert.Error(t, runErr, "expected cancellation error, got nil (loop ran to completion?)")
	case <-time.After(5 * time.Second):
		t.Fatal("JIT'd loop did not honor cancellation within 5s")
	}
}

// TestThisFieldAccess exercises the OpGetField/OpSetField path: reading and
// writing object fields through `this` inside method bodies, including a method
// that mutates `this` and one whose fields are declared out of access order, to
// confirm the decl-index inline cache resolves correctly.
func TestThisFieldAccess(t *testing.T) {
	ctx := context.Background()
	run := func(t *testing.T, src string, want int64) {
		t.Helper()
		sess := newSession(ctx)
		require.NoError(t, sess.Exec(ctx, src), "unexpected error")
		assert.Equal(t, want, sess.GetGlobal("__r").AsInt(), "__r")
	}

	// read this.x/this.y (the BenchmarkMethodCall shape)
	t.Run("read fields", func(t *testing.T) {
		run(t, `object P { x: int = 0, y: int = 0,
fun dist() > int { return this.x * this.x + this.y * this.y; } }
final p = P{ x = 3, y = 4 };
final __r = p.dist();`, 25)
	})
	// write this.field, then read it back
	t.Run("write then read", func(t *testing.T) {
		run(t, `object C { n: int = 0,
mut fun bump() > int { this.n = this.n + 1; this.n = this.n + 10; return this.n; } }
final c = mut C{};
final __r = c.bump();`, 11)
	})
	// access fields in an order different from declaration order
	t.Run("out-of-order access", func(t *testing.T) {
		run(t, `object T { a: int = 1, b: int = 2, c: int = 3,
fun mix() > int { return this.c * 100 + this.a * 10 + this.b; } }
final t = T{ a = 4, b = 5, c = 6 };
final __r = t.mix();`, 645)
	})
	// field whose value is mutated via an external setter still reads back
	// correctly inside a method (in-place update preserves slot order)
	t.Run("external set then method read", func(t *testing.T) {
		run(t, `object Box { v: int = 0,
fun get() > int { return this.v; } }
final b = mut Box{};
b.v = 99;
final __r = b.get();`, 99)
	})
}

func evalResult(t *testing.T, src string) vmpackage.Value {
	t.Helper()
	sess := newSession(context.Background())
	require.NoErrorf(t, sess.Exec(context.Background(), "final __r = "+src+";"), "exec %q", src)
	return sess.GetGlobal("__r")
}

func wantInt(t *testing.T, v vmpackage.Value, want int64) {
	t.Helper()
	assert.Truef(t, v.IsInt(), "got %v (%s), want int %d", v.String(), v.Kind(), want)
	assert.Equalf(t, want, v.AsInt(), "got %v (%s), want int %d", v.String(), v.Kind(), want)
}

func wantStr(t *testing.T, v vmpackage.Value, want string) {
	t.Helper()
	assert.Truef(t, v.IsStr(), "got %v (%s), want str %q", v.String(), v.Kind(), want)
	assert.Equalf(t, want, v.AsString(), "got %v (%s), want str %q", v.String(), v.Kind(), want)
}

func wantBool(t *testing.T, v vmpackage.Value, want bool) {
	t.Helper()
	assert.Truef(t, v.IsBool(), "got %v (%s), want bool %v", v.String(), v.Kind(), want)
	assert.Equalf(t, want, v.AsBool(), "got %v (%s), want bool %v", v.String(), v.Kind(), want)
}

// TestListIndexOfConformance pins list.indexOf to the upstream buzz semantics
// (validated against the 0.6.0-dev binary): strings match by content, lists and
// maps match by reference identity only, a missing element returns null. This
// source runs under every value representation (nanbox, buzz_safe, buzz_unsafe)
// and must agree in all three - the pre-fix RawEqual path made any same-tag heap
// needle match the first element under buzz_safe/buzz_unsafe.
func TestListIndexOfConformance(t *testing.T) {
	wantNull := func(v vmpackage.Value, msg string) {
		t.Helper()
		assert.Truef(t, v.IsNull(), "%s: got %v (%s), want null", msg, v.String(), v.Kind())
	}

	// String needle matches by content, including a runtime-built (concatenated)
	// string rather than the literal.
	wantInt(t, runProg(t, `return ["a", "b"].indexOf("b");`, CompileOptions{}), 1)
	wantInt(t, runProg(t, `var n = "b" + ""; return ["a", "b"].indexOf(n);`, CompileOptions{}), 1)
	wantNull(runProg(t, `return ["a", "b"].indexOf("z");`, CompileOptions{}), "missing string needle")

	// List needle matches by reference identity: a fresh content-equal literal is
	// not found, but the actual element (via a variable) is.
	wantNull(runProg(t, `return [[1], [2]].indexOf([2]);`, CompileOptions{}), "fresh content-equal list needle")
	wantInt(t, runProg(t, `var xs = [[1], [2]]; var e = xs[1]; return xs.indexOf(e);`, CompileOptions{}), 1)

	// Map needle: same reference-identity rule as lists.
	wantNull(runProg(t, `return [{"a": 1}].indexOf({"a": 1});`, CompileOptions{}), "fresh content-equal map needle")
	wantInt(t, runProg(t, `var m = {"a": 1}; return [m].indexOf(m);`, CompileOptions{}), 0)

	// Numbers compare by value; a missing number returns null.
	wantNull(runProg(t, `return [1, 2, 3].indexOf(99);`, CompileOptions{}), "missing number needle")
}

func TestArithmetic(t *testing.T) {
	wantInt(t, evalResult(t, "1 + 2 * 3"), 7)
	wantInt(t, evalResult(t, "(1 + 2) * 3"), 9)
	wantInt(t, evalResult(t, "10 - 4 - 3"), 3) // left-assoc
	wantInt(t, evalResult(t, "17 % 5"), 2)
	wantInt(t, evalResult(t, "-5 + 8"), 3)
	fv := evalResult(t, "3.0 / 2")
	assert.True(t, fv.IsFloat(), "3.0/2 should be float")
	assert.Equal(t, 1.5, fv.AsFloat(), "3.0/2")
}

func TestStringConcat(t *testing.T) {
	wantStr(t, evalResult(t, `"foo" + "bar"`), "foobar")
}

func TestComparison(t *testing.T) {
	wantBool(t, evalResult(t, "1 < 2"), true)
	wantBool(t, evalResult(t, "2 <= 2"), true)
	wantBool(t, evalResult(t, "3 > 5"), false)
	wantBool(t, evalResult(t, `"a" < "b"`), true)
	wantBool(t, evalResult(t, "1 == 1"), true)
	wantBool(t, evalResult(t, "1 != 2"), true)
}

func TestLogical(t *testing.T) {
	wantBool(t, evalResult(t, "true and false"), false)
	wantBool(t, evalResult(t, "true or false"), true)
	wantBool(t, evalResult(t, "!false"), true)
	wantBool(t, evalResult(t, "1 < 2 and 2 < 3"), true)
}

func TestNullCoalesce(t *testing.T) {
	wantStr(t, evalResult(t, `null ?? "fallback"`), "fallback")
	wantStr(t, evalResult(t, `"value" ?? "fallback"`), "value")
}

func TestStringInterpolation(t *testing.T) {
	sess := newSession(context.Background())
	src := `
final name = "world";
final n = 42;
final greeting = "hello {name}, n+1 = {n + 1}!";
`
	require.NoError(t, sess.Exec(context.Background(), src))
	wantStr(t, sess.GetGlobal("greeting"), "hello world, n+1 = 43!")
}

func TestIfElse(t *testing.T) {
	sess := newSession(context.Background())
	src := `
var result = "";
final x = 5;
if (x > 10) {
    result = "big";
} else if (x > 3) {
    result = "medium";
} else {
    result = "small";
}
`
	require.NoError(t, sess.Exec(context.Background(), src))
	wantStr(t, sess.GetGlobal("result"), "medium")
}

func TestWhileLoop(t *testing.T) {
	sess := newSession(context.Background())
	src := `
var sum = 0;
var i = 1;
while (i <= 5) {
    sum = sum + i;
    i = i + 1;
}
`
	require.NoError(t, sess.Exec(context.Background(), src))
	wantInt(t, sess.GetGlobal("sum"), 15)
}

func TestForLoop(t *testing.T) {
	sess := newSession(context.Background())
	src := `
var total = 0;
for (var i = 0; i < 10; i = i + 1) {
    total = total + i;
}
`
	require.NoError(t, sess.Exec(context.Background(), src))
	wantInt(t, sess.GetGlobal("total"), 45)
}

func TestBreakContinue(t *testing.T) {
	sess := newSession(context.Background())
	src := `
var sum = 0;
for (var i = 0; i < 100; i = i + 1) {
    if (i == 3) { continue; }
    if (i >= 6) { break; }
    sum = sum + i;
}
`
	// i in {0,1,2,4,5} => 0+1+2+4+5 = 12
	require.NoError(t, sess.Exec(context.Background(), src))
	wantInt(t, sess.GetGlobal("sum"), 12)
}

func TestForEachList(t *testing.T) {
	sess := newSession(context.Background())
	src := `
final items = [10, 20, 30];
var sum = 0;
foreach (x in items) {
    sum = sum + x;
}
`
	require.NoError(t, sess.Exec(context.Background(), src))
	wantInt(t, sess.GetGlobal("sum"), 60)
}

func TestForEachMap(t *testing.T) {
	sess := newSession(context.Background())
	src := `
final m = {"a": 1, "b": 2, "c": 3};
var keys = "";
var sum = 0;
foreach (k, v in m) {
    keys = keys + k;
    sum = sum + v;
}
`
	require.NoError(t, sess.Exec(context.Background(), src))
	wantStr(t, sess.GetGlobal("keys"), "abc") // insertion order preserved
	wantInt(t, sess.GetGlobal("sum"), 6)
}

func TestIndexing(t *testing.T) {
	sess := newSession(context.Background())
	src := `
final list = [100, 200, 300];
final m = {"key": "val"};
final a = list[1];
final b = m["key"];
`
	require.NoError(t, sess.Exec(context.Background(), src))
	wantInt(t, sess.GetGlobal("a"), 200)
	wantStr(t, sess.GetGlobal("b"), "val")
}

func TestIndexAssign(t *testing.T) {
	sess := newSession(context.Background())
	src := `
final list = mut [1, 2, 3];
list[0] = 99;
final m = mut {"x": 1};
m["y"] = 2;
final first = list[0];
final my = m["y"];
`
	require.NoError(t, sess.Exec(context.Background(), src))
	wantInt(t, sess.GetGlobal("first"), 99)
	wantInt(t, sess.GetGlobal("my"), 2)
}

func TestNamedFunction(t *testing.T) {
	sess := newSession(context.Background())
	src := `
fun add(a: int, b: int) > int {
    return a + b;
}
final result = add(3, 4);
`
	require.NoError(t, sess.Exec(context.Background(), src))
	wantInt(t, sess.GetGlobal("result"), 7)
}

func TestRecursion(t *testing.T) {
	sess := newSession(context.Background())
	src := `
fun fact(n: int) > int {
    if (n <= 1) { return 1; }
    return n * fact(n - 1);
}
final result = fact(5);
`
	require.NoError(t, sess.Exec(context.Background(), src))
	wantInt(t, sess.GetGlobal("result"), 120)
}

func TestClosureCapture(t *testing.T) {
	sess := newSession(context.Background())
	src := `
fun makeAdder(n: int) > fun(int) > int {
    return fun(x: int) > int { return x + n; };
}
final add5 = makeAdder(5);
final result = add5(10);
`
	require.NoError(t, sess.Exec(context.Background(), src))
	wantInt(t, sess.GetGlobal("result"), 15)
}

func TestObject(t *testing.T) {
	sess := newSession(context.Background())
	src := `
object Point {
    x: int = 0,
    y: int = 0,

    fun sum() > int {
        return this.x + this.y;
    }
}
final p = Point{ x = 3, y = 4 };
final px = p.x;
final total = p.sum();
`
	require.NoError(t, sess.Exec(context.Background(), src))
	wantInt(t, sess.GetGlobal("px"), 3)
	wantInt(t, sess.GetGlobal("total"), 7)
}

func TestObjectDefaults(t *testing.T) {
	sess := newSession(context.Background())
	src := `
object Config {
    name: str = "default",
    count: int = 1,
}
final c = Config{ name = "custom" };
final cn = c.name;
final cc = c.count;
`
	require.NoError(t, sess.Exec(context.Background(), src))
	wantStr(t, sess.GetGlobal("cn"), "custom")
	wantInt(t, sess.GetGlobal("cc"), 1)
}

func TestEnum(t *testing.T) {
	sess := newSession(context.Background())
	src := `
enum Color {
    Red,
    Green,
    Blue,
}
final c = Color.Green;
final isGreen = c == Color.Green;
final isRed = c == Color.Red;
`
	require.NoError(t, sess.Exec(context.Background(), src))
	wantBool(t, sess.GetGlobal("isGreen"), true)
	wantBool(t, sess.GetGlobal("isRed"), false)
}

func TestParseReferenceConstructs(t *testing.T) {
	sess := newSession(context.Background())
	src := `
object Stack {
    items: [int] = [],

    fun push(v: int) > void {
        this.items = this.items + [v];
    }

    fun size() > int {
        return this.items.len;
    }
}

fun describe(n: int) > str {
    if (n == 0) {
        return "empty";
    }
    return "has {n} items";
}

final labels = ["a", "b", "c"];
var joined = "";
foreach (i, label in labels) {
    joined = joined + label;
}
final msg = describe(3);
`
	require.NoError(t, sess.Exec(context.Background(), src))
	wantStr(t, sess.GetGlobal("joined"), "abc")
	wantStr(t, sess.GetGlobal("msg"), "has 3 items")
}

// promoteOpts is SharedGlobals with top-level slot promotion enabled — the mode
// the magusfile entrypoint path uses. sharedOpts is
// the same without promotion (the REPL/incremental path).
var (
	promoteOpts = CompileOptions{SharedGlobals: true, PromoteTopLevel: true}
	sharedOpts  = CompileOptions{SharedGlobals: true}
)

// countOps tallies how many times each opcode appears in a freshly compiled chunk.
func countOps(t *testing.T, src string, opts CompileOptions) map[vmpackage.OpCode]int {
	t.Helper()
	prog, err := ParseEmbedded(src)
	require.NoError(t, err, "parse")
	chunk, err := CompileWith(prog, opts)
	require.NoError(t, err, "compile")
	got := map[vmpackage.OpCode]int{}
	for _, ins := range chunk.Code {
		got[ins.Op]++
	}
	return got
}

// TestPromoteEquivalence is the core soundness gate: enabling PromoteTopLevel must
// never change a single chunk's result versus plain SharedGlobals. Promotion only
// changes where a chunk-private var is stored (slot vs Env), not what it computes,
// and captured/exported names are left on the Env path, so the two modes must agree
// for every program — including ones with closures over top-level state.
func TestPromoteEquivalence(t *testing.T) {
	srcs := []string{
		`var a = 3; var b = 4; return a * a + b * b;`,
		`var sum = 0; var i = 0; while (i < 1000) { sum = sum + i; i = i + 1; } return sum;`,
		`var s = ""; var i = 0; while (i < 5) { s = "item {i}"; i = i + 1; } return s;`,
		`var s = 0; for (var i = 0; i < 5; i = i + 1) { s = s + i; } return s;`,
		`var total = 0; foreach (x in 0..10) { total = total + x; } return total;`,
		`var n = 0; { var n = 41; } return n + 1;`, // inner block shadows
		// Closures over top-level state: must stay live-Env, identical in both modes.
		`var x = 1; fun getx() > int { return x; } x = 2; return getx();`,
		`var acc = 0; fun add(n: int) { acc = acc + n; } add(3); add(4); return acc;`,
		// Mix of promotable scratch and a captured config var in one chunk.
		`var cfg = 7; var t = 0; fun bump() { t = t + cfg; } bump(); bump(); var scratch = 0; foreach (k in 0..3) { scratch = scratch + k; } return t + scratch;`,
	}
	for _, src := range srcs {
		promote := runProg(t, src, promoteOpts)
		shared := runProg(t, src, sharedOpts)
		// RawEqual is raw tag+num bits - heap reference identity holds only in
		// the nanbox build; under buzz_safe/buzz_unsafe any two same-tag heap
		// values compare equal - so compare by kind + rendered content instead.
		assert.Equalf(t, shared.String(), promote.String(), "promote vs shared mismatch for %q", src)
		assert.Equalf(t, shared.IsStr(), promote.IsStr(), "promote vs shared kind mismatch for %q", src)
	}
}

// TestPromotePromotesChunkPrivate verifies that a chunk-private top-level var is
// actually slot-promoted: no OpDefName/OpLoadName/OpStoreName for it (those are the
// Env path), and the loop body uses the slot opcodes instead.
func TestPromotePromotesChunkPrivate(t *testing.T) {
	src := `var sum = 0; var i = 0; while (i < 1000) { sum = sum + i; i = i + 1; } return sum;`

	shared := countOps(t, src, sharedOpts)
	require.NotZero(t, shared[vmpackage.OpDefName], "baseline SharedGlobals should use Env op OpDefName")
	require.NotZero(t, shared[vmpackage.OpLoadName], "baseline SharedGlobals should use Env op OpLoadName")

	promote := countOps(t, src, promoteOpts)
	assert.Zero(t, promote[vmpackage.OpDefName], "promoted chunk-private vars must not use Env op OpDefName")
	assert.Zero(t, promote[vmpackage.OpLoadName], "promoted chunk-private vars must not use Env op OpLoadName")
	assert.Zero(t, promote[vmpackage.OpStoreName], "promoted chunk-private vars must not use Env op OpStoreName")
	assert.NotZero(t, promote[vmpackage.OpSetLocal], "promoted vars should use slot op OpSetLocal")
	assert.NotZero(t, promote[vmpackage.OpGetLocal], "promoted vars should use slot op OpGetLocal")
}

// TestPromoteKeepsCapturedInEnv verifies the closure carve-out: a top-level var
// referenced inside a function body is NOT promoted (stays an Env binding), so the
// closure keeps reading it live — and PromoteTopLevel does not silently flip it to
// a by-value upvalue snapshot.
func TestPromoteKeepsCapturedInEnv(t *testing.T) {
	src := `var x = 1; fun getx() > int { return x; } x = 2; return getx();`
	ops := countOps(t, src, promoteOpts)
	assert.NotZero(t, ops[vmpackage.OpDefName], "captured top-level var must stay an Env binding (expected OpDefName), got none")
	// Live-Env semantics: the post-definition mutation x = 2 is visible to getx().
	wantInt(t, runProg(t, src, promoteOpts), 2)
}

// TestPromoteKeepsExportedInEnv verifies exported top-level vars stay Env bindings
// (the cross-chunk/cross-module surface) even when promotion is on, and remain
// recorded in chunk.Exports.
func TestPromoteKeepsExportedInEnv(t *testing.T) {
	src := `export var version = 3; var scratch = 0; foreach (k in 0..3) { scratch = scratch + k; } return version + scratch;`
	prog, err := ParseEmbedded(src)
	require.NoError(t, err, "parse")
	chunk, err := CompileWith(prog, promoteOpts)
	require.NoError(t, err, "compile")
	assert.Contains(t, chunk.Exports, "version", "exported var should be recorded in chunk.Exports")
	ops := countOps(t, src, promoteOpts)
	assert.NotZero(t, ops[vmpackage.OpDefName], "exported var must stay an Env binding (expected OpDefName), got none")
}

// runProg compiles src with the given options and runs it against a fresh env
// that has the VM intrinsics available (spawn, zdef) via the Env fallback in
// slot mode, returning the program's top-level return value.
func runProg(t *testing.T, src string, opts CompileOptions) vmpackage.Value {
	t.Helper()
	prog, err := ParseEmbedded(src)
	require.NoError(t, err, "parse")
	chunk, err := CompileWith(prog, opts)
	require.NoError(t, err, "compile")
	env := vmpackage.NewEnv()
	vmpackage.RegisterStdlib(env)
	v, err := vmpackage.NewVM(context.Background()).Run(chunk, env)
	require.NoError(t, err, "run")
	return v
}

// TestSlotTopLevelLoop is the workload behind BenchmarkLoopSum: a tight
// top-level arithmetic loop whose variables become stack slots in slot mode.
func TestSlotTopLevelLoop(t *testing.T) {
	src := `var sum = 0;
var i = 0;
while (i < 1000) { sum = sum + i; i = i + 1; }
return sum;`
	wantInt(t, runProg(t, src, CompileOptions{}), 499500)
}

// TestSlotEnvEquivalence asserts the two compile modes agree on results for
// programs that don't depend on cross-Run global sharing.
func TestSlotEnvEquivalence(t *testing.T) {
	srcs := []string{
		`var a = 3; var b = 4; return a * a + b * b;`,
		`var s = 0; for (var i = 0; i < 5; i = i + 1) { s = s + i; } return s;`,
		`var total = 0; foreach (x in 0..10) { total = total + x; } return total;`,
		`var n = 0; { var n = 41; } return n + 1;`, // inner block shadows; outer unchanged
	}
	for _, src := range srcs {
		slot := runProg(t, src, CompileOptions{})
		env := runProg(t, src, CompileOptions{SharedGlobals: true})
		assert.Truef(t, slot.Equal(env), "mode mismatch for %q: slot=%s env=%s", src, slot.String(), env.String())
	}
}

// TestSlotTopLevelClosureCapture documents that a closure over a top-level
// variable in slot mode captures it as an upvalue — by value at closure
// creation, matching Buzz's existing nested-function closure semantics.
func TestSlotTopLevelClosureCapture(t *testing.T) {
	// Read-only capture: the closure sees the value present at creation.
	wantInt(t, runProg(t, `var x = 10;
fun getx() > int { return x; }
return getx();`, CompileOptions{}), 10)

	// Snapshot semantics: mutating x after the closure is built does not change
	// what the closure returns (by-value upvalue). In SharedGlobals mode the
	// same source would observe the live global instead — that divergence is
	// the intended difference between the two models.
	wantInt(t, runProg(t, `var x = 1;
fun getx() > int { return x; }
x = 2;
return getx();`, CompileOptions{}), 1)
}

// testConformanceMeta holds the directives parsed from a conformance fixture.
// Shared by bytecode_test.go and conformance_test.go (package buzz_test version).
type testConformanceMeta struct {
	expect string
	errStr string
	skip   string
}

// parseConformanceMeta reads the leading comment block of src for @expect,
// @error, and @skip directives. Used by both the conformance and bytecode
// round-trip tests.
func parseConformanceMeta(src string) testConformanceMeta {
	var m testConformanceMeta
	for _, line := range splitLines(src) {
		if len(line) == 0 || line[0] != '/' {
			break
		}
		rest := line[2:] // strip "//"
		if len(rest) > 0 && rest[0] == ' ' {
			rest = rest[1:]
		}
		if v, ok := cutPrefix(rest, "@expect:"); ok {
			m.expect = trimSpace(v)
		} else if v, ok := cutPrefix(rest, "@error:"); ok {
			m.errStr = trimSpace(v)
		} else if v, ok := cutPrefix(rest, "@skip:"); ok {
			m.skip = trimSpace(v)
		}
	}
	return m
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

func cutPrefix(s, prefix string) (string, bool) {
	if len(s) >= len(prefix) && s[:len(prefix)] == prefix {
		return s[len(prefix):], true
	}
	return s, false
}

// containsStdImport reports whether src contains any `import "<module>"` for
// the Buzz standard library modules that require buzzstd.Register to resolve.
func containsStdImport(src string) bool {
	stdModules := []string{`"std"`, `"math"`, `"fs"`, `"os"`, `"crypto"`, `"gc"`, `"debug"`, `"io"`, `"serialize"`, `"buffer"`, `"ffi"`}
	for _, mod := range stdModules {
		if contains(src, "import "+mod) {
			return true
		}
	}
	return false
}

func contains(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func trimSpace(s string) string {
	i, j := 0, len(s)
	for i < j && (s[i] == ' ' || s[i] == '\t' || s[i] == '\r' || s[i] == '\n') {
		i++
	}
	for j > i && (s[j-1] == ' ' || s[j-1] == '\t' || s[j-1] == '\r' || s[j-1] == '\n') {
		j--
	}
	return s[i:j]
}
