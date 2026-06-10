package buzz

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/gopherbuzz/types"
)

func checkSrc(src string) []typeError {
	prog, err := Parse(src)
	if err != nil {
		return []typeError{{Line: 0, Col: 0, Msg: err.Error()}}
	}
	return checkWithGlobals(prog, nil, nil, nil)
}

// checkOK asserts that Check reports no errors for src.
func checkOK(t *testing.T, src string) {
	t.Helper()
	if errs := checkSrc(src); len(errs) != 0 {
		t.Errorf("expected no errors, got:\n%s", fmtErrors(errs))
	}
}

// checkErr asserts that Check reports at least one error containing substr.
func checkErr(t *testing.T, src, substr string) {
	t.Helper()
	errs := checkSrc(src)
	if len(errs) == 0 {
		t.Errorf("expected error containing %q, got none", substr)
		return
	}
	for _, e := range errs {
		if strings.Contains(e.Msg, substr) {
			return
		}
	}
	t.Errorf("expected error containing %q, got:\n%s", substr, fmtErrors(errs))
}

func fmtErrors(errs []typeError) string {
	var sb strings.Builder
	for _, e := range errs {
		sb.WriteString(e.Error())
		sb.WriteByte('\n')
	}
	return sb.String()
}

func TestCheck_ValidProgram(t *testing.T) {
	checkOK(t, `
final x = 42;
final y = "hello";
final z = true;
final w = null;
`)
}

func TestCheck_UndefinedVar(t *testing.T) {
	checkErr(t, `final x = undeclared;`, "undefined: undeclared")
}

func TestCheck_ConstReassign(t *testing.T) {
	checkErr(t, `
final x = 1;
x = 2;
`, "cannot assign to final")
}

func TestCheck_VarReassign(t *testing.T) {
	checkOK(t, `
var x = 1;
x = 2;
`)
}

func TestCheck_TypeAnnotMismatch(t *testing.T) {
	checkErr(t, `final x: int = "hello";`, "cannot assign str to int")
}

func TestCheck_TypeAnnotOK(t *testing.T) {
	checkOK(t, `final x: int = 42;`)
	checkOK(t, `final x: str = "hello";`)
	checkOK(t, `final x: bool = true;`)
}

// TestCheck_MapReturnType covers map-type return annotations, which require the
// parser to treat a '{' after the '>' arrow as a map type rather than the body.
func TestCheck_MapReturnType(t *testing.T) {
	checkOK(t, `fun f() > {str: int} { return {"a": 1}; }`)
	// A map of functions — github's mgs_listTargets shape.
	checkOK(t, `
fun op(p: {str: str}) > bool { return true; }
fun list() > {str: fun({str: str}) bool} { return {"op": op}; }
`)
	// Arrowless '{' is still the body (void return), not a map type.
	checkOK(t, `fun g() { final x = 1; }`)
}

func TestCheck_FunctionArity(t *testing.T) {
	checkErr(t, `
fun add(a, b) int { return a + b; }
final r = add(1);
`, "wrong argument count: got 1, want 2")
}

func TestCheck_FunctionArityOK(t *testing.T) {
	checkOK(t, `
fun add(a, b) int { return a + b; }
final r = add(1, 2);
`)
}

func TestCheck_ReturnTypeMismatch(t *testing.T) {
	checkErr(t, `
fun greet() int {
    return "hello";
}
`, "return type mismatch")
}

func TestCheck_ReturnTypeOK(t *testing.T) {
	checkOK(t, `
fun greet() str {
    return "hello";
}
`)
}

func TestCheck_ArithmeticTypes(t *testing.T) {
	checkOK(t, `final r = 1 + 2;`)
	checkOK(t, `final r = 1.5 + 2;`)
	checkOK(t, `final r = "a" + "b";`)
}

func TestCheck_ArithmeticInvalidType(t *testing.T) {
	checkErr(t, `final r = true + 1;`, "invalid type bool in arithmetic")
}

func TestCheck_IfCondition(t *testing.T) {
	checkOK(t, `if (true) { }`)
	checkOK(t, `if (1 < 2) { }`)
}

func TestCheck_IfConditionInvalid(t *testing.T) {
	checkErr(t, `if (42) { }`, "if condition must be bool")
}

func TestCheck_WhileConditionInvalid(t *testing.T) {
	checkErr(t, `while ("yes") { }`, "while condition must be bool")
}

func TestCheck_ObjectDecl(t *testing.T) {
	checkOK(t, `
object Point {
    x: int = 0,
    y: int = 0,
    fun sum() int {
        return this.x + this.y;
    }
}
`)
}

func TestCheck_ObjectLitUnknownField(t *testing.T) {
	checkErr(t, `
object Point {
    x: int = 0,
    y: int = 0,
}
final p = Point{ x = 1, z = 2 };
`, `has no field "z"`)
}

func TestCheck_ObjectLitOK(t *testing.T) {
	checkOK(t, `
object Point {
    x: int = 0,
    y: int = 0,
}
final p = Point{ x = 1, y = 2 };
`)
}

func TestCheck_ObjectMemberAccess(t *testing.T) {
	checkOK(t, `
object Point {
    x: int = 0,
    y: int = 0,
    fun sum() int { return this.x + this.y; }
}
final p = Point{ x = 1, y = 2 };
final s = p.sum();
`)
}

func TestCheck_ObjectUnknownMember(t *testing.T) {
	checkErr(t, `
object Point { x: int = 0 }
final p = Point{ x = 1 };
final z = p.z;
`, `has no field or method "z"`)
}

func TestCheck_EnumDecl(t *testing.T) {
	checkOK(t, `
enum Color { Red, Green, Blue }
final c = Color.Green;
`)
}

func TestCheck_EnumUnknownCase(t *testing.T) {
	checkErr(t, `
enum Color { Red, Green, Blue }
final c = Color.Yellow;
`, `has no case "Yellow"`)
}

func TestCheck_InjectedGlobalMemberCall(t *testing.T) {
	// A host embedding gopherbuzz can inject a namespace global (the magusfile
	// pattern, where the magus host binds `magus`). Such a global is pre-registered
	// as types.Any via extraGlobals, so member calls on it type-check. checkSrc
	// registers none, so exercise checkWithGlobals with a neutral injected name.
	prog, err := Parse(`host.project.register(".");`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if errs := checkWithGlobals(prog, []string{"host"}, nil, nil); len(errs) != 0 {
		t.Errorf("expected no errors, got:\n%s", fmtErrors(errs))
	}
}

func TestCheck_ForEachList(t *testing.T) {
	checkOK(t, `
final items = [1, 2, 3];
var sum = 0;
foreach (x in items) {
    sum = sum + x;
}
`)
}

func TestCheck_ForEachMap(t *testing.T) {
	checkOK(t, `
final m = {"a": 1, "b": 2};
foreach (k, v in m) {
    final combined = k + v;
}
`)
}

func TestCheck_ListLen(t *testing.T) {
	checkOK(t, `
final xs = [1, 2, 3];
final n = xs.len;
`)
}

func TestCheck_NullCoalesce(t *testing.T) {
	checkOK(t, `final x = null ?? "fallback";`)
}

func TestCheck_Closure(t *testing.T) {
	checkOK(t, `
fun makeAdder(n) fun(int)int {
    return fun(x: int) int { return x + n; };
}
final add5 = makeAdder(5);
`)
}

func TestCheck_MutualRecursion(t *testing.T) {
	checkOK(t, `
fun even(n) bool {
    if (n == 0) { return true; }
    return odd(n - 1);
}
fun odd(n) bool {
    if (n == 0) { return false; }
    return even(n - 1);
}
`)
}

func TestCheck_ForLoop(t *testing.T) {
	checkOK(t, `
var total = 0;
for (var i = 0; i < 10; i = i + 1) {
    total = total + i;
}
`)
}

func TestCheck_Indexing(t *testing.T) {
	checkOK(t, `
final xs = [10, 20, 30];
final a = xs[0];
final m = {"k": "v"};
final b = m["k"];
`)
}

func TestCheck_ParseAnnotInt(t *testing.T) {
	if types.ParseAnnot("int") != types.Int {
		t.Error("ParseAnnot(int)")
	}
}

func TestCheck_ParseAnnotList(t *testing.T) {
	lt, ok := types.ParseAnnot("[str]").(*types.ListType)
	if !ok || lt.Elem != types.Str {
		t.Error("ParseAnnot([str])")
	}
}

func TestCheck_ParseAnnotFun(t *testing.T) {
	ft, ok := types.ParseAnnot("fun(int)str").(*types.FuncType)
	if !ok || len(ft.Params) != 1 || ft.Params[0] != types.Int || ft.Ret != types.Str {
		t.Errorf("ParseAnnot(fun(int)str): %T %v", ft, ft)
	}
}

func TestCheck_ParseAnnotMap(t *testing.T) {
	mt, ok := types.ParseAnnot("{str:int}").(*types.MapType)
	if !ok || mt.Key != types.Str || mt.Val != types.Int {
		t.Errorf("ParseAnnot({str:int}): %T %v", mt, mt)
	}
}

// TestCheckTypeSoundness verifies that OpCheckType makes typed local slots
// runtime-sound: an `any` value laundered into a typed slot is asserted at the
// bind point, so a mismatch is a clear error instead of silently corrupting a
// slot that later reads (and future type-specialized opcodes) trust. Slot-based
// locals live in function bodies (and non-shared top-level); the session
// top-level runs in SharedGlobals (Env) mode, so the assignment cases are wrapped
// in a fun to exercise the slot path the optimization targets.
func TestCheckTypeSoundness(t *testing.T) {
	ctx := context.Background()

	// A string laundered through `any` into an int slot used to evaluate to
	// "hello1" (the str's heap pointer reinterpreted as an int, then concatenated).
	// The reassignment must now assert the type and error instead.
	t.Run("laundered str into int errors", func(t *testing.T) {
		sess := newSession(ctx)
		err := sess.Exec(ctx, `fun f() int { var i = 0; var a = "hello"; var b: any = a; i = b; return i + 1; }
final __r = f();`)
		if err == nil {
			t.Fatalf("expected a type error, got none (__r=%q)", sess.GetGlobal("__r").String())
		}
		if !strings.Contains(err.Error(), "expected int, got str") {
			t.Fatalf("error = %q, want it to mention expected int, got str", err.Error())
		}
	})

	// The matching case still works: an `any` that actually holds an int passes
	// the assertion and the program runs normally.
	t.Run("matching any into int passes", func(t *testing.T) {
		sess := newSession(ctx)
		if err := sess.Exec(ctx, `fun f() int { var i = 0; var a = 41; var b: any = a; i = b; return i + 1; }
final __r = f();`); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := sess.GetGlobal("__r").AsInt(); got != 42 {
			t.Errorf("__r = %d, want 42", got)
		}
	})

	// An annotated declaration from an any source is checked at the decl in either
	// mode (the assertion is emitted before the bind, slot or Env), so this fires
	// at the session top level.
	t.Run("annotated decl from any is checked", func(t *testing.T) {
		sess := newSession(ctx)
		err := sess.Exec(ctx, `var a = "x"; var u: any = a; var n: int = u; final __r = n;`)
		if err == nil || !strings.Contains(err.Error(), "expected int") {
			t.Fatalf("expected 'expected int' error, got %v", err)
		}
	})
}

// TestCheckTypeNoFalsePositives verifies the inserted checks never fire for
// well-typed code — the common path stays untouched and correct.
func TestCheckTypeNoFalsePositives(t *testing.T) {
	ctx := context.Background()
	cases := map[string]struct {
		src  string
		want int64
	}{
		"int loop":          {`var s = 0; var i = 0; while (i < 5) { s = s + i; i = i + 1; } final __r = s;`, 10},
		"typed int decl":    {`var x: int = 7; final __r = x + 1;`, 8},
		"int from typed fn": {`fun id(n: int) int { return n; } var x: int = id(5); final __r = x + 1;`, 6},
		"reassign int":      {`var x = 1; x = 2; x = x + 3; final __r = x;`, 5},
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

// writeModule drops a .bzz file into dir for an import test.
func writeModule(t *testing.T, dir, name, src string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0644); err != nil {
		t.Fatal(err)
	}
}

// TestImport_ExportedObjectType verifies a flat-imported module's exported
// object type resolves in the importer's annotations and literals — both the
// type-check (during Exec) and the runtime construction/field read.
func TestImport_ExportedObjectType(t *testing.T) {
	dir := t.TempDir()
	writeModule(t, dir, "lib.bzz", `export object Foo { n: int = 0 }`)

	ctx := context.Background()
	sess := NewSession(ctx)
	defer sess.Close()
	sess.SetIncludeDirs([]string{dir})

	src := `
import "lib";
fun make() > Foo { return Foo{ n = 7 }; }
export final result = make().n;
`
	if err := sess.Exec(ctx, src); err != nil {
		t.Fatalf("exec with imported object type: %v", err)
	}
	v, ok := sess.Exports()["result"]
	if !ok {
		t.Fatal("result not exported")
	}
	if !v.IsInt() || v.AsInt() != 7 {
		t.Errorf("result = %v, want 7", v.String())
	}
}

// TestImport_ExportedEnumType verifies a flat-imported module's exported enum
// type resolves in the importer (annotation + case access).
func TestImport_ExportedEnumType(t *testing.T) {
	dir := t.TempDir()
	writeModule(t, dir, "palette.bzz", `export enum Color { Red, Green, Blue }`)

	ctx := context.Background()
	sess := NewSession(ctx)
	defer sess.Close()
	sess.SetIncludeDirs([]string{dir})

	src := `
import "palette";
fun pick() > Color { return Color.Green; }
final c = pick();
`
	if err := sess.Exec(ctx, src); err != nil {
		t.Fatalf("exec with imported enum type: %v", err)
	}
}

// TestImport_CrossReferencingObjectTypes verifies imported object types that
// reference each other (a field typed as a sibling imported object) resolve.
func TestImport_CrossReferencingObjectTypes(t *testing.T) {
	dir := t.TempDir()
	writeModule(t, dir, "shapes.bzz", `
export object Inner { v: int = 0 }
export object Outer { inner: Inner = Inner{} }
`)

	ctx := context.Background()
	sess := NewSession(ctx)
	defer sess.Close()
	sess.SetIncludeDirs([]string{dir})

	src := `
import "shapes";
fun build() > Outer { return Outer{ inner = Inner{ v = 3 } }; }
export final got = build().inner.v;
`
	if err := sess.Exec(ctx, src); err != nil {
		t.Fatalf("exec with cross-referencing imported types: %v", err)
	}
	v, ok := sess.Exports()["got"]
	if !ok {
		t.Fatal("got not exported")
	}
	if !v.IsInt() || v.AsInt() != 3 {
		t.Errorf("got = %v, want 3", v.String())
	}
}

// TestSourceModule_ExportsTypes verifies a host-registered source module
// (embedded .bzz, no file on the include path) exposes its exported object/enum
// types to the importer — including object-typed and list field defaults, which
// the canonical magus/target module relies on.
func TestSourceModule_ExportsTypes(t *testing.T) {
	ctx := context.Background()
	sess := NewSession(ctx)
	defer sess.Close()
	sess.SetSourceModule("magus/lib", `
export object Strategy { name: str = "" }
export object Charm { name: str = "", args: [str] = [], strategy: Strategy = Strategy{} }
export object Target { name: str = "", charms: [Charm] = [] }
`)

	src := `
import "magus/lib";
fun pick() > Target {
    return Target{ name = "build", charms = [Charm{ name = "fast" }] };
}
export final tname = pick().name;
`
	if err := sess.Exec(ctx, src); err != nil {
		t.Fatalf("exec with source-module types: %v", err)
	}
	v, ok := sess.Exports()["tname"]
	if !ok || !v.IsStr() || v.AsString() != "build" {
		t.Errorf("tname = %v, want \"build\"", v.String())
	}
}

// TestImport_NonExportedObjectType_Errors verifies that only EXPORTED types
// cross the module boundary: a non-exported imported object is not visible to
// the importer's checker, so using it is a compile-time "undefined type" error.
func TestImport_NonExportedObjectType_Errors(t *testing.T) {
	dir := t.TempDir()
	writeModule(t, dir, "internal.bzz", `object Secret { n: int = 0 }`)

	ctx := context.Background()
	sess := NewSession(ctx)
	defer sess.Close()
	sess.SetIncludeDirs([]string{dir})

	err := sess.Exec(ctx, `import "internal"; final s = Secret{ n = 1 };`)
	if err == nil {
		t.Fatal("expected error using a non-exported imported type, got nil")
	}
	if !strings.Contains(err.Error(), "undefined type") {
		t.Errorf("error = %q, want it to mention \"undefined type\"", err.Error())
	}
}

func TestNamedArguments(t *testing.T) {
	checkOK(t, `
fun f(a: int, b: int) > int { return a - b; }
final r = f(b: 1, a: 2);
`)
	checkOK(t, `
fun f(a: int, b: int) > int { return a - b; }
final r = f(2, b: 1);
`)
	checkErr(t, `
fun f(a: int, b: int) > int { return a - b; }
final r = f(a: 2, c: 1);
`, `unknown argument name "c"`)
	checkErr(t, `
fun f(a: int, b: int) > int { return a - b; }
final r = f(a: 2, a: 1);
`, `given more than once`)
	checkErr(t, `
fun f(a: int, b: int) > int { return a - b; }
final r = f(b: 1, 2);
`, "positional argument after named argument")
	checkErr(t, `
fun f(a: int, b: int) > int { return a - b; }
final r = f(a: 2);
`, `missing argument "b"`)
}
