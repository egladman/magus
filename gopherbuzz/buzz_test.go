package buzz

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/gopherbuzz/ast"
)

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
	prog, err := Parse(`const x = 42;`)
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
	src := `const f = fun(_args: [str]) void {};`
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
	src := `const m = {"key": "val"};`
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
	src := `magus.project.register(".", {});`
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
	if err := sess.Exec(context.Background(), `const x = "hello";`); err != nil {
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
const a = Box{ n = 1 };
const b = Box{ n = 2 };
const result = run(a, b);
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
	magus := NewMap()
	magus.MapSet("project", projectNS)
	sess.SetGlobal("magus", magus)

	src := `
import "magus";
magus.project.register(".");
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
	if _, ok := exports["test"]; !ok {
		t.Error("test target missing")
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

// TestExport_DeclIsExported verifies that an exported const appears in Exports().
func TestExport_DeclIsExported(t *testing.T) {
	ctx := context.Background()
	sess := NewSession(ctx)
	defer sess.Close()

	if err := sess.Exec(ctx, `export const version = "1.0";`); err != nil {
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
export const x = 42;
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
	if err := os.WriteFile(filepath.Join(dir, "util.bzz"), []byte(`const helper = 99;`), 0644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	sess := NewSession(ctx)
	defer sess.Close()
	sess.SetIncludeDirs([]string{dir})

	// import as alias: "util" loaded, bound under "u"
	if err := sess.Exec(ctx, `import "util" as u; const got = u.helper;`); err != nil {
		t.Fatalf("exec: %v", err)
	}
}

// TestCyclicImportTerminates verifies that mutually-importing .bzz files do
// not cause infinite recursion. loadedPaths in the Session guards the cycle.
func TestCyclicImportTerminates(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.bzz"), []byte(`import "b"; const from_a = 1;`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.bzz"), []byte(`import "a"; const from_b = 2;`), 0644); err != nil {
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
