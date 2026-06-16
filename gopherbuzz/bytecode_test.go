package buzz

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egladman/gopherbuzz/ast"
	vmpackage "github.com/egladman/gopherbuzz/vm"
)

// TestBytecodeRoundTrip compiles every non-error conformance fixture, marshals
// the resulting chunk to bytes, unmarshals it back, executes the recovered
// chunk, and asserts the result matches the fixture's @expect value. This
// exercises the serializer across scalars, strings, enums, objects (including
// field defaults), closures, fibers, and control flow.
func TestBytecodeRoundTrip(t *testing.T) {
	files, err := filepath.Glob("testdata/*.buzz")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no conformance test files found in testdata/")
	}
	for _, path := range files {
		name := strings.TrimSuffix(filepath.Base(path), ".buzz")
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
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
			if err != nil {
				t.Fatalf("compile: %v", err)
			}

			data, err := chunk.Marshal()
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			got, err := UnmarshalChunk(data)
			if err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if err := sess.ExecChunk(ctx, got); err != nil {
				t.Fatalf("exec recovered chunk: %v", err)
			}
			if r := sess.GetGlobal("__r"); r.String() != meta.expect {
				t.Errorf("__r = %q, want %q", r.String(), meta.expect)
			}
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
    fun describe() str {
        return this.label;
    }
}
final c = Config{};
final __r = c.describe() + " " + c.tags[0];
`
	ctx := context.Background()

	// Baseline: run directly.
	base := newSession(ctx)
	if err := base.Exec(ctx, src); err != nil {
		t.Fatalf("baseline exec: %v", err)
	}
	want := base.GetGlobal("__r").String()

	// Round-trip via ExecBytecode (exercises UnmarshalChunk + ExecChunk).
	sess := newSession(ctx)
	chunk, err := sess.Compile(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	data, err := chunk.Marshal()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := sess.ExecBytecode(ctx, data); err != nil {
		t.Fatalf("ExecBytecode: %v", err)
	}
	if r := sess.GetGlobal("__r").String(); r != want {
		t.Errorf("round-trip __r = %q, want %q", r, want)
	}
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
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if !slices.Contains(chunk.Exports, "mgs_getName") {
		t.Fatalf("compiled chunk Exports = %v, want to contain mgs_getName", chunk.Exports)
	}
	data, err := chunk.Marshal()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// Load into a fresh session so exports can only come from the bytecode.
	fresh := newSession(ctx)
	if err := fresh.ExecBytecode(ctx, data); err != nil {
		t.Fatalf("ExecBytecode: %v", err)
	}
	if _, ok := fresh.Exports()["mgs_getName"]; !ok {
		t.Fatalf("mgs_getName not exported after bytecode round-trip; exports=%v", fresh.Exports())
	}
}

// TestBytecodeDebugRoundTrip marshals a chunk's bytecode (.bo) and debug info
// (.bdb) separately, recovers the chunk from the .bo alone, and asserts
// AttachDebug folds the source lines back onto the function tree to match the
// originally compiled chunk.
func TestBytecodeDebugRoundTrip(t *testing.T) {
	src := `
fun add(a, b) { return a + b; }
final __r = add(2, 3);
`
	ctx := context.Background()
	sess := newSession(ctx)
	chunk, err := sess.Compile(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if chunk.Lines == nil {
		t.Fatal("expected Compile to populate debug lines")
	}

	bo, err := chunk.Marshal()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	bdb, err := chunk.Marshal(DebugOnly())
	if err != nil {
		t.Fatalf("marshal debug: %v", err)
	}

	got, err := UnmarshalChunk(bo)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Lines != nil {
		t.Fatal("bytecode-only chunk should carry no debug lines before AttachDebug")
	}
	if err := got.AttachDebug(bdb); err != nil {
		t.Fatalf("attach debug: %v", err)
	}
	if !reflect.DeepEqual(got.Lines, chunk.Lines) {
		t.Errorf("top-level lines = %v, want %v", got.Lines, chunk.Lines)
	}
	if len(got.Funs) != len(chunk.Funs) {
		t.Fatalf("funs len = %d, want %d", len(got.Funs), len(chunk.Funs))
	}
	for i := range got.Funs {
		if !reflect.DeepEqual(got.Funs[i].Lines, chunk.Funs[i].Lines) {
			t.Errorf("fun[%d] lines = %v, want %v", i, got.Funs[i].Lines, chunk.Funs[i].Lines)
		}
	}

	// A .bdb with the wrong magic must be rejected, not silently ignored.
	fresh, err := UnmarshalChunk(bo)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	bad := append([]byte(nil), bdb...)
	bad[0] ^= 0xFF
	if err := fresh.AttachDebug(bad); err == nil {
		t.Fatal("expected error for corrupt .bdb magic, got nil")
	}
}

func TestBytecodeVersionGuard(t *testing.T) {
	sess := newSession(context.Background())
	chunk, err := sess.Compile("final __r = 1 + 2;")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	data, err := chunk.Marshal()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	t.Run("bad magic", func(t *testing.T) {
		bad := append([]byte(nil), data...)
		bad[0] ^= 0xFF
		if _, err := UnmarshalChunk(bad); err == nil {
			t.Fatal("expected error for corrupted magic, got nil")
		}
	})

	t.Run("version mismatch", func(t *testing.T) {
		bad := append([]byte(nil), data...)
		// Version is the 2 bytes immediately after the 4-byte magic.
		bad[4] ^= 0xFF
		if _, err := UnmarshalChunk(bad); err == nil {
			t.Fatal("expected error for version mismatch, got nil")
		}
	})

	t.Run("truncated", func(t *testing.T) {
		if _, err := UnmarshalChunk(data[:3]); err == nil {
			t.Fatal("expected error for truncated data, got nil")
		}
	})

	t.Run("huge_count", func(t *testing.T) {
		// Valid header + empty Name, then Params count = 0xFFFFFFFF.
		// checkCount must reject this before make([]string, n) fires.
		var buf []byte
		buf = append(buf, 'B', 'Z', 'B', 'C')                              // magic
		buf = append(buf, byte(BytecodeVersion), byte(BytecodeVersion>>8)) // version LE
		buf = append(buf, 0, 0, 0, 0)                                      // Name: u32(0) = ""
		buf = append(buf, 0xFF, 0xFF, 0xFF, 0xFF)                          // Params count = 0xFFFFFFFF
		if _, err := UnmarshalChunk(buf); err == nil {
			t.Fatal("expected error for huge count, got nil")
		}
	})
}

func TestParser_Import(t *testing.T) {
	prog, err := Parse(`import "magus";`)
	if err != nil {
		t.Fatal(err)
	}
	if len(prog.Stmts) != 1 {
		t.Fatalf("want 1 stmt, got %d", len(prog.Stmts))
	}
	imp, ok := prog.Stmts[0].(*ast.ImportStmt)
	if !ok {
		t.Fatalf("want *ast.ImportStmt, got %T", prog.Stmts[0])
	}
	if imp.Path != "magus" {
		t.Errorf("path: got %q, want %q", imp.Path, "magus")
	}
}

func TestParser_ConstDecl(t *testing.T) {
	prog, err := Parse(`final x = 42;`)
	if err != nil {
		t.Fatal(err)
	}
	if len(prog.Stmts) != 1 {
		t.Fatalf("want 1 stmt, got %d", len(prog.Stmts))
	}
	d, ok := prog.Stmts[0].(*ast.DeclStmt)
	if !ok {
		t.Fatalf("want *ast.DeclStmt, got %T", prog.Stmts[0])
	}
	if !d.IsConst || d.Name != "x" {
		t.Errorf("decl: IsConst=%v Name=%q", d.IsConst, d.Name)
	}
}

func TestParser_FunExpr(t *testing.T) {
	src := `final f = fun(_args: [str]) void {};`
	prog, err := Parse(src)
	if err != nil {
		t.Fatalf("parse %q: %v", src, err)
	}
	if len(prog.Stmts) != 1 {
		t.Fatalf("want 1 stmt, got %d", len(prog.Stmts))
	}
	d, ok := prog.Stmts[0].(*ast.DeclStmt)
	if !ok {
		t.Fatalf("want *ast.DeclStmt, got %T", prog.Stmts[0])
	}
	if _, ok := d.Value.(*ast.FunExpr); !ok {
		t.Fatalf("want *ast.FunExpr, got %T", d.Value)
	}
}

func TestParser_MapLit(t *testing.T) {
	src := `final m = {"key": "val"};`
	prog, err := Parse(src)
	if err != nil {
		t.Fatalf("parse %q: %v", src, err)
	}
	d := prog.Stmts[0].(*ast.DeclStmt)
	m, ok := d.Value.(*ast.MapExpr)
	if !ok {
		t.Fatalf("want *ast.MapExpr, got %T", d.Value)
	}
	if len(m.Keys) != 1 || m.Keys[0].(*ast.StringLit).Val != "key" {
		t.Errorf("map keys: %v", m.Keys)
	}
}

func TestParser_CallChain(t *testing.T) {
	src := `host.project.register(".", {});`
	prog, err := Parse(src)
	if err != nil {
		t.Fatalf("parse %q: %v", src, err)
	}
	if len(prog.Stmts) != 1 {
		t.Fatalf("want 1 stmt, got %d", len(prog.Stmts))
	}
	es, ok := prog.Stmts[0].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("want *ast.ExprStmt, got %T", prog.Stmts[0])
	}
	call, ok := es.Expr.(*ast.CallExpr)
	if !ok {
		t.Fatalf("want *ast.CallExpr, got %T", es.Expr)
	}
	_ = call
}

func TestEval_ConstBinding(t *testing.T) {
	sess := newSession(context.Background())
	if err := sess.Exec(context.Background(), `final x = "hello";`); err != nil {
		t.Fatal(err)
	}
	v := sess.GetGlobal("x")
	if !v.IsStr() || v.AsString() != "hello" {
		t.Errorf("x: got %s %v, want str hello", v.Kind(), v.String())
	}
}

func TestEval_DirectCall(t *testing.T) {
	sess := newSession(context.Background())
	called := false
	sess.SetGlobal("fn", DirectValue("fn", func(_ context.Context, args []Value) (Value, error) {
		called = true
		if len(args) != 1 {
			t.Errorf("args: got %d, want 1", len(args))
		}
		return Null, nil
	}))
	if err := sess.Exec(context.Background(), `fn("hello");`); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Error("direct function was not called")
	}
}

func TestEval_MemberAccess(t *testing.T) {
	sess := newSession(context.Background())
	m := NewMap()
	m.MapSet("name", StrValue("test"))
	sess.SetGlobal("obj", m)

	var gotName Value
	sess.SetGlobal("capture", DirectValue("capture", func(_ context.Context, args []Value) (Value, error) {
		if len(args) > 0 {
			gotName = args[0]
		}
		return Null, nil
	}))

	if err := sess.Exec(context.Background(), `capture(obj.name);`); err != nil {
		t.Fatal(err)
	}
	if !gotName.IsStr() || gotName.AsString() != "test" {
		t.Errorf("obj.name: got %s %v, want str test", gotName.Kind(), gotName.String())
	}
}

// TestEval_NameCacheCrossInstance guards against the inline name cache serving
// one object instance's `this` to another instance's method. Both calls happen
// inside run()'s body with no intervening name op, so the cache (keyed by chunk)
// must also key on the resolving env or b.get() wrongly returns a's field.
func TestEval_NameCacheCrossInstance(t *testing.T) {
	sess := newSession(context.Background())
	src := `
object Box { n: int = 0, fun get() int { return this.n; } }
fun run(a, b) int {
    var x = a.get();
    var y = b.get();
    return x * 10 + y;
}
final a = Box{ n = 1 };
final b = Box{ n = 2 };
final result = run(a, b);
`
	if err := sess.Exec(context.Background(), src); err != nil {
		t.Fatal(err)
	}
	if got := sess.GetGlobal("result").AsInt(); got != 12 {
		t.Errorf("run(a,b): got %d, want 12 (a.get()=1, b.get()=2)", got)
	}
}

// TestEval_DoUntilCancellable guards against an infinite do..until loop ignoring
// context cancellation. The loop's back-edge is OpJumpFalse, which must poll the
// context like OpJump or the loop is unkillable.
func TestEval_DoUntilCancellable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled: the first back-edge poll must abort the loop
	sess := newSession(ctx)
	err := sess.Exec(ctx, `var i = 0; do { i = i + 1; } until (i < 0);`)
	if err == nil {
		t.Fatal("infinite do..until under cancelled ctx returned nil; loop is uncancellable")
	}
}

func TestEval_FunClosure(t *testing.T) {
	sess := newSession(context.Background())
	var stored Value
	sess.SetGlobal("register", DirectValue("register", func(_ context.Context, args []Value) (Value, error) {
		if len(args) >= 2 {
			stored = args[1]
		}
		return Null, nil
	}))

	if err := sess.Exec(context.Background(), `register("build", fun() void {});`); err != nil {
		t.Fatal(err)
	}
	if !stored.IsFun() {
		t.Errorf("stored: got %s, want fun", stored.Kind())
	}
}

func TestEval_TargetNew(t *testing.T) {
	sess := newSession(context.Background())
	targets := make(map[string]Callable)

	sess.SetGlobal("target_new", DirectValue("target_new", func(_ context.Context, args []Value) (Value, error) {
		if len(args) < 2 {
			return Null, nil
		}
		name := args[0].AsString()
		fn := args[1]
		targets[strings.ToLower(name)] = func(ctx context.Context, callArgs []Value) (Value, error) {
			return sess.CallValue(ctx, fn, callArgs)
		}
		return Null, nil
	}))

	if err := sess.Exec(context.Background(), `target_new("build", fun() void {});`); err != nil {
		t.Fatal(err)
	}
	if _, ok := targets["build"]; !ok {
		t.Error("build target was not registered")
	}
	_, err := targets["build"](context.Background(), nil)
	if err != nil {
		t.Errorf("invoke build: %v", err)
	}
}

func TestEval_MagusfilePattern(t *testing.T) {
	ctx := context.Background()
	sess := NewSession(ctx)
	defer sess.Close()

	registered := ""
	projectNS := NewMap()
	projectNS.MapSet("register", DirectValue("register", func(_ context.Context, args []Value) (Value, error) {
		if len(args) > 0 {
			registered = args[0].AsString()
		}
		return Null, nil
	}))
	host := NewMap()
	host.MapSet("project", projectNS)
	sess.SetGlobal("host", host)

	src := `
host.project.register(".");
export fun build(_args: [str]) > void {}
export fun test(_args: [str]) > void {}
`
	if err := sess.Exec(ctx, src); err != nil {
		t.Fatalf("exec: %v", err)
	}

	if registered != "." {
		t.Errorf("registered: got %q, want %q", registered, ".")
	}
	exports := sess.Exports()
	if _, ok := exports["build"]; !ok {
		t.Error("build target missing")
	}
	// `test` remains a valid identifier/target (contextual keyword): the magusfile
	// `export fun test` pattern must keep working.
	if _, ok := exports["test"]; !ok {
		t.Error("test target missing")
	}
}

// TestTestBlocks verifies the upstream `test "name" { ... }` construct: blocks
// register but do not run on a normal Exec, Session.Tests() exposes them in
// source order, and running a block surfaces a raised error as a failure.
func TestTestBlocks(t *testing.T) {
	ctx := context.Background()
	sess := NewSession(ctx)
	defer func() { _ = sess.Close() }()

	ran := 0
	sess.SetGlobal("touch", DirectValue("touch", func(_ context.Context, _ []Value) (Value, error) {
		ran++
		return Null, nil
	}))
	sess.SetGlobal("boom", DirectValue("boom", func(_ context.Context, _ []Value) (Value, error) {
		return Null, fmt.Errorf("kaboom")
	}))

	src := `
test "first" { touch(); }
test "second" { touch(); }
test "failing" { boom(); }
`
	if err := sess.Exec(ctx, src); err != nil {
		t.Fatalf("exec: %v", err)
	}

	// Bodies must not run during a normal execution.
	if ran != 0 {
		t.Errorf("test bodies ran during normal Exec: touch called %d times, want 0", ran)
	}

	tests := sess.Tests()
	if len(tests) != 3 {
		t.Fatalf("Tests() len = %d, want 3", len(tests))
	}
	if tests[0].Name != "first" || tests[1].Name != "second" || tests[2].Name != "failing" {
		t.Errorf("test names/order = %q,%q,%q", tests[0].Name, tests[1].Name, tests[2].Name)
	}

	// Running the blocks: the first two pass, the third surfaces its error.
	for _, tc := range tests[:2] {
		if _, err := sess.CallValue(ctx, tc.Fn, nil); err != nil {
			t.Errorf("test %q unexpectedly failed: %v", tc.Name, err)
		}
	}
	if _, err := sess.CallValue(ctx, tests[2].Fn, nil); err == nil {
		t.Error("failing test returned nil error, want failure")
	}
	if ran != 2 {
		t.Errorf("touch called %d times after running tests, want 2", ran)
	}
}

// TestTestKeywordIsContextual verifies `test` is a soft keyword: it introduces a
// test block in the `test "..." {` position yet remains usable as an ordinary
// identifier in the same program (the magus embedding relies on `test` targets).
func TestTestKeywordIsContextual(t *testing.T) {
	ctx := context.Background()
	sess := NewSession(ctx)
	defer func() { _ = sess.Close() }()

	src := `
final test = 7;
test "a block named like the variable" { }
`
	if err := sess.Exec(ctx, src); err != nil {
		t.Fatalf("exec: %v", err)
	}
	if v := sess.GetGlobal("test"); !v.IsInt() || v.AsInt() != 7 {
		t.Errorf("identifier `test` = %v, want int 7", v)
	}
	if tests := sess.Tests(); len(tests) != 1 || tests[0].Name != "a block named like the variable" {
		t.Errorf("Tests() = %+v, want one block", tests)
	}
}

// TestExport_FunIsExported verifies that an exported function appears in
// Session.Exports() while a non-exported helper does not.
func TestExport_FunIsExported(t *testing.T) {
	ctx := context.Background()
	sess := NewSession(ctx)
	defer sess.Close()

	src := `
export fun build(args: [str]) > void {}
fun helper() > void {}
`
	if err := sess.Exec(ctx, src); err != nil {
		t.Fatalf("exec: %v", err)
	}
	exports := sess.Exports()
	if exports == nil {
		t.Fatal("Exports() returned nil")
	}
	if _, ok := exports["build"]; !ok {
		t.Error("exported 'build' missing from Exports()")
	}
	if _, ok := exports["helper"]; ok {
		t.Error("non-exported 'helper' should not appear in Exports()")
	}
}

// TestExport_DeclIsExported verifies that an exported final appears in Exports().
func TestExport_DeclIsExported(t *testing.T) {
	ctx := context.Background()
	sess := NewSession(ctx)
	defer sess.Close()

	if err := sess.Exec(ctx, `export final version = "1.0";`); err != nil {
		t.Fatalf("exec: %v", err)
	}
	exports := sess.Exports()
	if v, ok := exports["version"]; !ok {
		t.Error("exported 'version' missing from Exports()")
	} else if v.String() != "1.0" {
		t.Errorf("version value: got %q, want %q", v.String(), "1.0")
	}
}

// TestNamespaceStmt verifies that a namespace declaration parses without error.
func TestNamespaceStmt(t *testing.T) {
	ctx := context.Background()
	sess := NewSession(ctx)
	defer sess.Close()

	src := `
namespace my\module;
export final x = 42;
`
	if err := sess.Exec(ctx, src); err != nil {
		t.Fatalf("namespace decl should not error, got: %v", err)
	}
	exports := sess.Exports()
	if _, ok := exports["x"]; !ok {
		t.Error("exported 'x' missing after namespace declaration")
	}
}

// TestImport_AsAlias verifies that `import "file" as alias` binds under the alias.
func TestImport_AsAlias(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "util.buzz"), []byte(`final helper = 99;`), 0644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	sess := NewSession(ctx)
	defer sess.Close()
	sess.SetIncludeDirs([]string{dir})

	// import as alias: "util" loaded, bound under "u"
	if err := sess.Exec(ctx, `import "util" as u; final got = u.helper;`); err != nil {
		t.Fatalf("exec: %v", err)
	}
}

// TestCyclicImportTerminates verifies that mutually-importing .buzz files do
// not cause infinite recursion. loadedPaths in the Session guards the cycle.
func TestCyclicImportTerminates(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.buzz"), []byte(`import "b"; final from_a = 1;`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.buzz"), []byte(`import "a"; final from_b = 2;`), 0644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	sess := NewSession(ctx)
	defer sess.Close()
	sess.SetIncludeDirs([]string{dir})

	if err := sess.Exec(ctx, `import "a";`); err != nil {
		t.Fatalf("cyclic import should not error, got: %v", err)
	}
}

// TestImport_SearchPathFullImportPath verifies the whole import path — not just
// its trailing segment — is substituted for `?` in a search-path template,
// matching upstream Buzz: import "lib/mod" resolves lib/mod.buzz, not mod.buzz.
func TestImport_SearchPathFullImportPath(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "lib", "mod.buzz"), []byte(`export final answer = 42;`), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	sess := NewSession(ctx, WithSearchPaths(filepath.Join(dir, "?.buzz")))
	defer sess.Close()

	v, err := sess.Eval(ctx, `import "lib/mod"; return answer;`)
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	if !v.IsInt() || v.AsInt() != 42 {
		t.Errorf("answer = %v, want 42", v.String())
	}
}

// TestImport_DefaultSearchPathsNested verifies an unconfigured session resolves
// the upstream `./?/main.buzz` layout relative to the working directory, and that
// WithSearchPaths() with no arguments leaves the session on DefaultSearchPaths.
func TestImport_DefaultSearchPathsNested(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "widget"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "widget", "main.buzz"), []byte(`export final tag = "w";`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir) // ./widget/main.buzz resolves relative to cwd

	ctx := context.Background()
	sess := NewSession(ctx, WithSearchPaths()) // no paths -> DefaultSearchPaths
	defer sess.Close()

	v, err := sess.Eval(ctx, `import "widget"; return tag;`)
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	if !v.IsStr() || v.AsString() != "w" {
		t.Errorf("tag = %v, want \"w\"", v.String())
	}
}

// TestFiberBasic covers the generator pattern: a fiber yields a sequence of
// values and then returns a final one. resume returns each yielded value, then
// null on completion; resolve returns the cached return value.
func TestFiberBasic(t *testing.T) {
	sess := newSession(context.Background())
	src := `
fun gen() int {
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
	if err := sess.Exec(context.Background(), src); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]int64{"a": 1, "b": 2} {
		if got := sess.GetGlobal(name).AsInt(); got != want {
			t.Errorf("%s = %d, want %d", name, got, want)
		}
	}
	if !sess.GetGlobal("c").IsNull() {
		t.Error("resume on fiber completion should return null")
	}
	if got := sess.GetGlobal("r").AsInt(); got != 3 {
		t.Errorf("resolve r = %d, want 3", got)
	}
}

// TestFiberArgsAndClosure checks that &fn(args) forwards arguments and that
// local state (loop counter) survives across yield/resume boundaries.
func TestFiberArgsAndClosure(t *testing.T) {
	sess := newSession(context.Background())
	src := `
fun counter(start) int {
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
	if err := sess.Exec(context.Background(), src); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]int64{"a": 10, "b": 11, "c": 12} {
		if got := sess.GetGlobal(name).AsInt(); got != want {
			t.Errorf("%s = %d, want %d", name, got, want)
		}
	}
	if !sess.GetGlobal("d").IsNull() {
		t.Error("resume on fiber completion should return null")
	}
	if got := sess.GetGlobal("r").AsInt(); got != -1 {
		t.Errorf("resolve r = %d, want -1", got)
	}
}

// TestFiberCancellation verifies a non-terminating fiber observes context
// cancellation when resumed (the loop's back-edge polls ctx).
func TestFiberCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	sess := newSession(ctx)
	if err := sess.Exec(context.Background(),
		`fun spin() int { var i = 0; while (i >= 0) { i = i + 1; } return i; }
final f = &spin();`); err != nil {
		t.Fatal(err)
	}
	if err := sess.Exec(ctx, `final r = resume f;`); err == nil {
		t.Fatal("resume of infinite-loop fiber under cancelled ctx did not error")
	}
}

// TestFiberRecursiveResumeGuard checks that resuming a fiber from within its own
// (still running) execution is rejected rather than corrupting the VM.
func TestFiberRecursiveResumeGuard(t *testing.T) {
	sess := newSession(context.Background())
	src := `
var f = null;
f = &(fun() int { return resume f; })();
final r = resume f;
`
	if err := sess.Exec(context.Background(), src); err == nil {
		t.Fatal("recursive resume of a running fiber should error")
	}
}

// TestFiberResumeDoneReturnsNull checks that resuming a completed fiber returns
// null (upstream parity: resume on a done fiber is not an error).
func TestFiberResumeDoneReturnsNull(t *testing.T) {
	sess := newSession(context.Background())
	src := `
fun gen() int { return 1; }
final f = &gen();
final a = resume f;
final b = resume f;
`
	if err := sess.Exec(context.Background(), src); err != nil {
		t.Fatal(err)
	}
	if !sess.GetGlobal("a").IsNull() {
		t.Error("first resume (completes gen) should return null")
	}
	if !sess.GetGlobal("b").IsNull() {
		t.Error("resume of a done fiber should return null")
	}
}

// TestFiberErrorResurfaced checks that a fiber whose VM errors caches the error
// so a *later* resume/resolve re-surfaces it, rather than swallowing it and
// returning null (the fiber is FiberDone after the failure, and the done-branch
// must report the cached error, not a zero return value).
func TestFiberErrorResurfaced(t *testing.T) {
	ctx := context.Background()
	t.Run("resolve then resolve", func(t *testing.T) {
		sess := newSession(ctx)
		if err := sess.Exec(ctx, `fun boom() { throw "boom"; } final f = &boom();`); err != nil {
			t.Fatal(err)
		}
		if err := sess.Exec(ctx, `final a = resolve f;`); err == nil {
			t.Fatal("first resolve of a throwing fiber should error")
		}
		if err := sess.Exec(ctx, `final b = resolve f;`); err == nil {
			t.Fatal("re-resolve of an errored fiber should re-surface the error, not return null")
		}
	})
	t.Run("resume then resolve", func(t *testing.T) {
		sess := newSession(ctx)
		if err := sess.Exec(ctx, `fun boom() { throw "boom"; } final f = &boom();`); err != nil {
			t.Fatal(err)
		}
		if err := sess.Exec(ctx, `final a = resume f;`); err == nil {
			t.Fatal("resume of a throwing fiber should error")
		}
		if err := sess.Exec(ctx, `final b = resolve f;`); err == nil {
			t.Fatal("resolve after a resume that errored should re-surface the error, not return null")
		}
	})
}

// TestYieldOutsideFiberDismissed checks that a top-level yield (no enclosing
// fiber) is silently dismissed and not an error (upstream parity).
func TestYieldOutsideFiberDismissed(t *testing.T) {
	sess := newSession(context.Background())
	if err := sess.Exec(context.Background(), `yield 1;`); err != nil {
		t.Fatalf("yield outside a fiber should not error, got: %v", err)
	}
}

// TestFiberDirectRejected checks that wrapping a direct (Go) callable with &
// is rejected: direct callables have no Buzz bytecode and cannot yield.
func TestFiberDirectRejected(t *testing.T) {
	sess := newSession(context.Background())
	sess.SetGlobal("nat", DirectValue("nat", func(_ context.Context, _ []Value) (Value, error) {
		return IntValue(42), nil
	}))
	if err := sess.Exec(context.Background(), `final f = &nat();`); err == nil {
		t.Fatal("& on a direct callable should error")
	}
}

// TestFiberDebugIntrospectsFiberStack verifies that Frames()/CallDepth() report
// the *fiber's* call stack (not the outer VM's) while a direct callable is executing
// inside a resumed fiber. Before the fix, curVM still pointed at the suspended
// outer VM so the debugger saw the wrong frames.
func TestFiberDebugIntrospectsFiberStack(t *testing.T) {
	sess := newSession(context.Background())

	type snapshot struct {
		depth  int
		frames []DebugFrame
	}
	var got snapshot

	// inner() is called from inside the fiber body so the fiber stack is at
	// least 2 frames deep (top-level + gen + inner). probe() captures the
	// session's view of the live stack at that moment.
	sess.SetGlobal("probe", DirectValue("probe", func(_ context.Context, _ []Value) (Value, error) {
		got.depth = sess.CallDepth()
		got.frames = sess.Frames()
		return Null, nil
	}))

	src := `
fun inner() int { probe(); return 1; }
fun gen() int {
    yield inner();
    return 0;
}
final f = &gen();
final v = resume f;
`
	if err := sess.Exec(context.Background(), src); err != nil {
		t.Fatal(err)
	}

	// probe() fires inside inner(), which is called from gen() running inside
	// the fiber. Expect at least 2 frames (inner + gen) and the innermost frame
	// to be named "inner".
	if got.depth < 2 {
		t.Errorf("CallDepth = %d, want >= 2 (fiber stack not visible)", got.depth)
	}
	if len(got.frames) == 0 {
		t.Fatal("Frames() returned empty inside fiber")
	}
	if got.frames[0].Name != "inner" {
		t.Errorf("innermost frame = %q, want %q", got.frames[0].Name, "inner")
	}
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
fun gen() int { yield 1; yield 2; return 7; }
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
		if errs[g] != nil {
			t.Fatalf("g%d: %v", g, errs[g])
		}
		if got[g] != 3 {
			t.Errorf("g%d sum = %d, want 3", g, got[g])
		}
	}
}

// runProgJIT compiles src and runs it with the JIT forced on or off, returning
// the top-level return value and any error.
func runProgJIT(t *testing.T, src string, jit bool) (Value, error) {
	t.Helper()
	prog, err := Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	chunk, err := CompileWith(prog, CompileOptions{})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	env := vmpackage.NewEnv()
	vmpackage.RegisterStdlib(env)
	vmpackage.SetJIT(jit)
	defer vmpackage.SetJIT(false)
	return vmpackage.NewVM(context.Background()).Run(chunk, env)
}

// jitPrograms are top-level integer loops the baseline JIT is meant to cover.
var jitPrograms = []struct {
	name string
	src  string
}{
	{"loopsum", `var sum = 0; var i = 0;
		while (i < 1000) { sum = sum + i; i = i + 1; } return sum;`},
	{"loopeq_even", `var count = 0; var i = 0;
		while (i < 1000) { if (i % 2 == 0) { count = count + 1; } i = i + 1; } return count;`},
	{"mul", `var acc = 1; var i = 1;
		while (i <= 10) { acc = acc * 2; i = i + 1; } return acc;`},
	{"nested", `var total = 0; var i = 0;
		while (i < 50) { var j = 0; while (j < 50) { total = total + 1; j = j + 1; } i = i + 1; }
		return total;`},
	{"sub_div_mod", `var x = 1000; var steps = 0;
		while (x > 1) { if (x % 2 == 0) { x = x / 2; } else { x = x - 1; } steps = steps + 1; }
		return steps;`},
	{"const_first", `var i = 0; var s = 0;
		while (1000 > i) { s = s + i; i = i + 1; } return s;`},
	{"float_sum", `var sum = 0.0; var i = 0.0;
		while (i < 1000.0) { sum = sum + i; i = i + 1.0; } return sum;`},
	{"float_mul", `var acc = 1.0; var i = 0.0;
		while (i < 10.0) { acc = acc * 2.0; i = i + 1.0; } return acc;`},
	{"float_div", `var x = 1024.0; var i = 0.0;
		while (i < 10.0) { x = x / 2.0; i = i + 1.0; } return x;`},
	{"float_cmp", `var c = 0.0; var i = 0.0;
		while (i < 1000.0) { if (i >= 500.0) { c = c + 1.0; } i = i + 1.0; } return c;`},
	{"mixed_promote", `var sum = 0.0; var i = 0;
		while (i < 100) { sum = sum + i; i = i + 1; } return sum;`},
	// Short-circuit operators in a loop condition compile to OpJumpFalsePeek /
	// OpJumpTruePeek (+ OpPop). These must JIT and match the interpreter.
	{"and_cond", `var i = 0; var s = 0;
		while (i < 1000 and s < 100000) { s = s + i; i = i + 1; } return s;`},
	{"or_cond", `var i = 0; var s = 0;
		while (i < 1000 or s > 0) { s = s + i; i = i + 1; } return s;`},
	// Mixed int*float promotion in the float path (px:int * 0.0125:float).
	{"mixed_mul", `var px = 0; var acc = 0.0;
		while (px < 100) { acc = acc + (px * 0.0125 - 1.5); px = px + 1; } return acc;`},
	// The Mandelbrot kernel itself: nested loops, an `and` escape condition, and
	// mixed int/float arithmetic — the whole reason this work exists.
	{"mandelbrot", `var checksum = 0; var py = 0;
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
		return checksum;`},
}

// TestJITMatchesInterpreter is the core differential test: every program must
// produce the identical result with the JIT on and off, and the JIT must
// actually engage on the top-level loop.
func TestJITMatchesInterpreter(t *testing.T) {
	for _, p := range jitPrograms {
		t.Run(p.name, func(t *testing.T) {
			want, err := runProgJIT(t, p.src, false)
			if err != nil {
				t.Fatalf("interp: %v", err)
			}
			vmpackage.ResetJITStats()
			got, err := runProgJIT(t, p.src, true)
			if err != nil {
				t.Fatalf("jit: %v", err)
			}
			if vmpackage.JITAvailable() && vmpackage.JITRunCount() == 0 {
				t.Fatalf("JIT did not engage for %s (chunk ineligible?)", p.name)
			}
			if got.String() != want.String() {
				t.Fatalf("%s: jit=%v interp=%v", p.name, got, want)
			}
		})
	}
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
	if (errWant == nil) != (errGot == nil) {
		t.Fatalf("error mismatch: interp=%v jit=%v", errWant, errGot)
	}
	if errWant == nil && got.String() != want.String() {
		t.Fatalf("value mismatch: jit=%v interp=%v", got, want)
	}
}

// TestJITCancellation confirms a JIT'd loop honors context cancellation via the
// back-edge poll: a long loop must return the cancellation error promptly
// instead of running to completion.
func TestJITCancellation(t *testing.T) {
	if !vmpackage.JITAvailable() {
		t.Skip("no JIT backend on this build")
	}
	prog, err := Parse(`var i = 0; while (i < 1000000000) { i = i + 1; } return i;`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	chunk, err := CompileWith(prog, CompileOptions{})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
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
		if runErr == nil {
			t.Fatal("expected cancellation error, got nil (loop ran to completion?)")
		}
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
	cases := map[string]struct {
		src  string
		want int64
	}{
		// read this.x/this.y (the BenchmarkMethodCall shape)
		"read fields": {`object P { x: int = 0, y: int = 0,
fun dist() int { return this.x * this.x + this.y * this.y; } }
final p = P{ x = 3, y = 4 };
final __r = p.dist();`, 25},
		// write this.field, then read it back
		"write then read": {`object C { n: int = 0,
mut fun bump() int { this.n = this.n + 1; this.n = this.n + 10; return this.n; } }
final c = mut C{};
final __r = c.bump();`, 11},
		// access fields in an order different from declaration order
		"out-of-order access": {`object T { a: int = 1, b: int = 2, c: int = 3,
fun mix() int { return this.c * 100 + this.a * 10 + this.b; } }
final t = T{ a = 4, b = 5, c = 6 };
final __r = t.mix();`, 645},
		// field whose value is mutated via an external setter still reads back
		// correctly inside a method (in-place update preserves slot order)
		"external set then method read": {`object Box { v: int = 0,
fun get() int { return this.v; } }
final b = mut Box{};
b.v = 99;
final __r = b.get();`, 99},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			sess := newSession(ctx)
			if err := sess.Exec(ctx, tc.src); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := sess.GetGlobal("__r").AsInt(); got != tc.want {
				t.Errorf("__r = %d, want %d", got, tc.want)
			}
		})
	}
}

func evalResult(t *testing.T, src string) Value {
	t.Helper()
	sess := newSession(context.Background())
	if err := sess.Exec(context.Background(), "final __r = "+src+";"); err != nil {
		t.Fatalf("exec %q: %v", src, err)
	}
	return sess.GetGlobal("__r")
}

func wantInt(t *testing.T, v Value, want int64) {
	t.Helper()
	if !v.IsInt() || v.AsInt() != want {
		t.Errorf("got %v (%s), want int %d", v.String(), v.Kind(), want)
	}
}

func wantStr(t *testing.T, v Value, want string) {
	t.Helper()
	if !v.IsStr() || v.AsString() != want {
		t.Errorf("got %v (%s), want str %q", v.String(), v.Kind(), want)
	}
}

func wantBool(t *testing.T, v Value, want bool) {
	t.Helper()
	if !v.IsBool() || v.AsBool() != want {
		t.Errorf("got %v (%s), want bool %v", v.String(), v.Kind(), want)
	}
}

func TestArithmetic(t *testing.T) {
	wantInt(t, evalResult(t, "1 + 2 * 3"), 7)
	wantInt(t, evalResult(t, "(1 + 2) * 3"), 9)
	wantInt(t, evalResult(t, "10 - 4 - 3"), 3) // left-assoc
	wantInt(t, evalResult(t, "17 % 5"), 2)
	wantInt(t, evalResult(t, "-5 + 8"), 3)
	if fv := evalResult(t, "3.0 / 2"); !fv.IsFloat() || fv.AsFloat() != 1.5 {
		t.Errorf("3.0/2: got %v", fv.String())
	}
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
	if err := sess.Exec(context.Background(), src); err != nil {
		t.Fatal(err)
	}
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
	if err := sess.Exec(context.Background(), src); err != nil {
		t.Fatal(err)
	}
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
	if err := sess.Exec(context.Background(), src); err != nil {
		t.Fatal(err)
	}
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
	if err := sess.Exec(context.Background(), src); err != nil {
		t.Fatal(err)
	}
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
	if err := sess.Exec(context.Background(), src); err != nil {
		t.Fatal(err)
	}
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
	if err := sess.Exec(context.Background(), src); err != nil {
		t.Fatal(err)
	}
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
	if err := sess.Exec(context.Background(), src); err != nil {
		t.Fatal(err)
	}
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
	if err := sess.Exec(context.Background(), src); err != nil {
		t.Fatal(err)
	}
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
	if err := sess.Exec(context.Background(), src); err != nil {
		t.Fatal(err)
	}
	wantInt(t, sess.GetGlobal("first"), 99)
	wantInt(t, sess.GetGlobal("my"), 2)
}

func TestNamedFunction(t *testing.T) {
	sess := newSession(context.Background())
	src := `
fun add(a, b) int {
    return a + b;
}
final result = add(3, 4);
`
	if err := sess.Exec(context.Background(), src); err != nil {
		t.Fatal(err)
	}
	wantInt(t, sess.GetGlobal("result"), 7)
}

func TestRecursion(t *testing.T) {
	sess := newSession(context.Background())
	src := `
fun fact(n) int {
    if (n <= 1) { return 1; }
    return n * fact(n - 1);
}
final result = fact(5);
`
	if err := sess.Exec(context.Background(), src); err != nil {
		t.Fatal(err)
	}
	wantInt(t, sess.GetGlobal("result"), 120)
}

func TestClosureCapture(t *testing.T) {
	sess := newSession(context.Background())
	src := `
fun makeAdder(n) fun(int) int {
    return fun(x) int { return x + n; };
}
final add5 = makeAdder(5);
final result = add5(10);
`
	if err := sess.Exec(context.Background(), src); err != nil {
		t.Fatal(err)
	}
	wantInt(t, sess.GetGlobal("result"), 15)
}

func TestObject(t *testing.T) {
	sess := newSession(context.Background())
	src := `
object Point {
    x: int = 0,
    y: int = 0,

    fun sum() int {
        return this.x + this.y;
    }
}
final p = Point{ x = 3, y = 4 };
final px = p.x;
final total = p.sum();
`
	if err := sess.Exec(context.Background(), src); err != nil {
		t.Fatal(err)
	}
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
	if err := sess.Exec(context.Background(), src); err != nil {
		t.Fatal(err)
	}
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
	if err := sess.Exec(context.Background(), src); err != nil {
		t.Fatal(err)
	}
	wantBool(t, sess.GetGlobal("isGreen"), true)
	wantBool(t, sess.GetGlobal("isRed"), false)
}

func TestParseReferenceConstructs(t *testing.T) {
	sess := newSession(context.Background())
	src := `
object Stack {
    items: [int] = [],

    fun push(v) void {
        this.items = this.items + [v];
    }

    fun size() int {
        return this.items.len;
    }
}

fun describe(n) str {
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
	if err := sess.Exec(context.Background(), src); err != nil {
		t.Fatal(err)
	}
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
	prog, err := Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	chunk, err := CompileWith(prog, opts)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
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
		`var x = 1; fun getx() int { return x; } x = 2; return getx();`,
		`var acc = 0; fun add(n) { acc = acc + n; } add(3); add(4); return acc;`,
		// Mix of promotable scratch and a captured config var in one chunk.
		`var cfg = 7; var t = 0; fun bump() { t = t + cfg; } bump(); bump(); var scratch = 0; foreach (k in 0..3) { scratch = scratch + k; } return t + scratch;`,
	}
	for _, src := range srcs {
		promote := runProg(t, src, promoteOpts)
		shared := runProg(t, src, sharedOpts)
		// RawEqual is raw-bits (pointer identity for heap values), so compare by
		// kind + rendered content to cover string results too.
		if promote.String() != shared.String() || promote.IsStr() != shared.IsStr() {
			t.Errorf("promote vs shared mismatch for %q: promote=%s shared=%s", src, promote.String(), shared.String())
		}
	}
}

// TestPromotePromotesChunkPrivate verifies that a chunk-private top-level var is
// actually slot-promoted: no OpDefName/OpLoadName/OpStoreName for it (those are the
// Env path), and the loop body uses the slot opcodes instead.
func TestPromotePromotesChunkPrivate(t *testing.T) {
	src := `var sum = 0; var i = 0; while (i < 1000) { sum = sum + i; i = i + 1; } return sum;`

	shared := countOps(t, src, sharedOpts)
	if shared[vmpackage.OpDefName] == 0 || shared[vmpackage.OpLoadName] == 0 {
		t.Fatalf("baseline SharedGlobals should use Env ops, got DefName=%d LoadName=%d", shared[vmpackage.OpDefName], shared[vmpackage.OpLoadName])
	}

	promote := countOps(t, src, promoteOpts)
	if promote[vmpackage.OpDefName] != 0 || promote[vmpackage.OpLoadName] != 0 || promote[vmpackage.OpStoreName] != 0 {
		t.Errorf("promoted chunk-private vars must not use Env ops: DefName=%d LoadName=%d StoreName=%d",
			promote[vmpackage.OpDefName], promote[vmpackage.OpLoadName], promote[vmpackage.OpStoreName])
	}
	if promote[vmpackage.OpSetLocal] == 0 || promote[vmpackage.OpGetLocal] == 0 {
		t.Errorf("promoted vars should use slot ops: SetLocal=%d GetLocal=%d", promote[vmpackage.OpSetLocal], promote[vmpackage.OpGetLocal])
	}
}

// TestPromoteKeepsCapturedInEnv verifies the closure carve-out: a top-level var
// referenced inside a function body is NOT promoted (stays an Env binding), so the
// closure keeps reading it live — and PromoteTopLevel does not silently flip it to
// a by-value upvalue snapshot.
func TestPromoteKeepsCapturedInEnv(t *testing.T) {
	src := `var x = 1; fun getx() int { return x; } x = 2; return getx();`
	ops := countOps(t, src, promoteOpts)
	if ops[vmpackage.OpDefName] == 0 {
		t.Errorf("captured top-level var must stay an Env binding (expected OpDefName), got none")
	}
	// Live-Env semantics: the post-definition mutation x = 2 is visible to getx().
	wantInt(t, runProg(t, src, promoteOpts), 2)
}

// TestPromoteKeepsExportedInEnv verifies exported top-level vars stay Env bindings
// (the cross-chunk/cross-module surface) even when promotion is on, and remain
// recorded in chunk.Exports.
func TestPromoteKeepsExportedInEnv(t *testing.T) {
	src := `export var version = 3; var scratch = 0; foreach (k in 0..3) { scratch = scratch + k; } return version + scratch;`
	prog, err := Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	chunk, err := CompileWith(prog, promoteOpts)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	hasExport := false
	for _, n := range chunk.Exports {
		if n == "version" {
			hasExport = true
		}
	}
	if !hasExport {
		t.Errorf("exported var should be recorded in chunk.Exports, got %v", chunk.Exports)
	}
	ops := countOps(t, src, promoteOpts)
	if ops[vmpackage.OpDefName] == 0 {
		t.Errorf("exported var must stay an Env binding (expected OpDefName), got none")
	}
}

// runProg compiles src with the given options and runs it against a fresh env
// that has the VM intrinsics available (spawn, zdef) via the Env fallback in
// slot mode, returning the program's top-level return value.
func runProg(t *testing.T, src string, opts CompileOptions) Value {
	t.Helper()
	prog, err := Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	chunk, err := CompileWith(prog, opts)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	env := vmpackage.NewEnv()
	vmpackage.RegisterStdlib(env)
	v, err := vmpackage.NewVM(context.Background()).Run(chunk, env)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
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
		if !slot.RawEqual(env) {
			t.Errorf("mode mismatch for %q: slot=%s env=%s", src, slot.String(), env.String())
		}
	}
}

// TestSlotTopLevelClosureCapture documents that a closure over a top-level
// variable in slot mode captures it as an upvalue — by value at closure
// creation, matching Buzz's existing nested-function closure semantics.
func TestSlotTopLevelClosureCapture(t *testing.T) {
	// Read-only capture: the closure sees the value present at creation.
	wantInt(t, runProg(t, `var x = 10;
fun getx() int { return x; }
return getx();`, CompileOptions{}), 10)

	// Snapshot semantics: mutating x after the closure is built does not change
	// what the closure returns (by-value upvalue). In SharedGlobals mode the
	// same source would observe the live global instead — that divergence is
	// the intended difference between the two models.
	wantInt(t, runProg(t, `var x = 1;
fun getx() int { return x; }
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
