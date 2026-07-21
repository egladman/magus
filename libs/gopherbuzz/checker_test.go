package buzz

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/libs/gopherbuzz/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func checkSrc(src string) []typeError {
	prog, err := ParseEmbedded(src)
	if err != nil {
		return []typeError{{Line: 0, Col: 0, Msg: err.Error()}}
	}
	return checkWithGlobals(prog, nil, nil, nil, nil)
}

// checkOK asserts that Check reports no errors for src.
func checkOK(t *testing.T, src string) {
	t.Helper()
	errs := checkSrc(src)
	assert.Emptyf(t, errs, "expected no errors, got:\n%s", fmtErrors(errs))
}

// checkErr asserts that Check reports at least one error containing substr.
func checkErr(t *testing.T, src, substr string) {
	t.Helper()
	errs := checkSrc(src)
	if !assert.NotEmptyf(t, errs, "expected error containing %q, got none", substr) {
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
fun add(a: int, b: int) > int { return a + b; }
final r = add(1);
`, "wrong argument count: got 1, want 2")
}

func TestCheck_FunctionArityOK(t *testing.T) {
	checkOK(t, `
fun add(a: int, b: int) > int { return a + b; }
final r = add(1, 2);
`)
}

func TestCheck_ReturnTypeMismatch(t *testing.T) {
	checkErr(t, `
fun greet() > int {
    return "hello";
}
`, "return type mismatch")
}

func TestCheck_ReturnTypeOK(t *testing.T) {
	checkOK(t, `
fun greet() > str {
    return "hello";
}
`)
}

// Empty typed-list literal `[<T>]` must infer `[T]`, not `[any]`, so a list
// accumulated with `+` and returned keeps its element type. Mirrors upstream
// Buzz; see splitCommands in examples/bubblegum/config.buzz.
func TestCheck_TypedEmptyListReturn(t *testing.T) {
	checkOK(t, `
fun build() > [str] {
    var res = [<str>];
    res = res + ["a"];
    return res;
}
`)
}

func TestCheck_TypedEmptyListElemMismatch(t *testing.T) {
	checkErr(t, `
fun build() > [str] {
    var res = [<int>];
    return res;
}
`, "return type mismatch")
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
    fun sum() > int {
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
    fun sum() > int { return this.x + this.y; }
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
	prog, err := ParseEmbedded(`host.project.register(".");`)
	require.NoError(t, err, "parse")
	errs := checkWithGlobals(prog, []string{"host"}, nil, nil, nil)
	assert.Emptyf(t, errs, "expected no errors, got:\n%s", fmtErrors(errs))
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
fun makeAdder(n: int) > fun(int) > int {
    return fun(x: int) > int { return x + n; };
}
final add5 = makeAdder(5);
`)
}

func TestCheck_MutualRecursion(t *testing.T) {
	checkOK(t, `
fun even(n: int) > bool {
    if (n == 0) { return true; }
    return odd(n - 1);
}
fun odd(n: int) > bool {
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
	assert.Equal(t, types.Int, types.ParseAnnot("int"), "ParseAnnot(int)")
}

func TestCheck_ParseAnnotList(t *testing.T) {
	lt, ok := types.ParseAnnot("[str]").(*types.ListType)
	require.True(t, ok, "ParseAnnot([str])")
	assert.Equal(t, types.Str, lt.Elem, "ParseAnnot([str])")
}

func TestCheck_ParseAnnotFun(t *testing.T) {
	ft, ok := types.ParseAnnot("fun(int)str").(*types.FuncType)
	require.Truef(t, ok, "ParseAnnot(fun(int)str): %T %v", ft, ft)
	require.Lenf(t, ft.Params, 1, "ParseAnnot(fun(int)str): %T %v", ft, ft)
	assert.Equalf(t, types.Int, ft.Params[0], "ParseAnnot(fun(int)str): %T %v", ft, ft)
	assert.Equalf(t, types.Str, ft.Ret, "ParseAnnot(fun(int)str): %T %v", ft, ft)
}

func TestCheck_ParseAnnotMap(t *testing.T) {
	mt, ok := types.ParseAnnot("{str:int}").(*types.MapType)
	require.Truef(t, ok, "ParseAnnot({str:int}): %T %v", mt, mt)
	assert.Equalf(t, types.Str, mt.Key, "ParseAnnot({str:int}): %T %v", mt, mt)
	assert.Equalf(t, types.Int, mt.Val, "ParseAnnot({str:int}): %T %v", mt, mt)
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
		err := sess.Exec(ctx, `fun f() > int { var i = 0; var a = "hello"; var b: any = a; i = b; return i + 1; }
final __r = f();`)
		require.Errorf(t, err, "expected a type error, got none (__r=%q)", sess.GetGlobal("__r").String())
		assert.Containsf(t, err.Error(), "expected int, got str", "error = %q, want it to mention expected int, got str", err.Error())
	})

	// The matching case still works: an `any` that actually holds an int passes
	// the assertion and the program runs normally.
	t.Run("matching any into int passes", func(t *testing.T) {
		sess := newSession(ctx)
		require.NoError(t, sess.Exec(ctx, `fun f() > int { var i = 0; var a = 41; var b: any = a; i = b; return i + 1; }
final __r = f();`))
		assert.Equal(t, int64(42), sess.GetGlobal("__r").AsInt(), "__r")
	})

	// An annotated declaration from an any source is checked at the decl in either
	// mode (the assertion is emitted before the bind, slot or Env), so this fires
	// at the session top level.
	t.Run("annotated decl from any is checked", func(t *testing.T) {
		sess := newSession(ctx)
		err := sess.Exec(ctx, `var a = "x"; var u: any = a; var n: int = u; final __r = n;`)
		require.Error(t, err, "expected 'expected int' error")
		assert.Contains(t, err.Error(), "expected int", "expected 'expected int' error")
	})
}

// TestCheckTypeNoFalsePositives verifies the inserted checks never fire for
// well-typed code — the common path stays untouched and correct.
func TestCheckTypeNoFalsePositives(t *testing.T) {
	ctx := context.Background()
	noFalsePositive := func(t *testing.T, src string, want int64) {
		t.Helper()
		sess := newSession(ctx)
		require.NoError(t, sess.Exec(ctx, src), "unexpected error")
		assert.Equal(t, want, sess.GetGlobal("__r").AsInt(), "__r")
	}

	t.Run("int loop", func(t *testing.T) {
		noFalsePositive(t, `var s = 0; var i = 0; while (i < 5) { s = s + i; i = i + 1; } final __r = s;`, 10)
	})
	t.Run("typed int decl", func(t *testing.T) {
		noFalsePositive(t, `var x: int = 7; final __r = x + 1;`, 8)
	})
	t.Run("int from typed fn", func(t *testing.T) {
		noFalsePositive(t, `fun id(n: int) > int { return n; } var x: int = id(5); final __r = x + 1;`, 6)
	})
	t.Run("reassign int", func(t *testing.T) {
		noFalsePositive(t, `var x = 1; x = 2; x = x + 3; final __r = x;`, 5)
	})
}

// writeModule drops a .buzz file into dir for an import test.
func writeModule(t *testing.T, dir, name, src string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(src), 0644))
}

// TestImport_ExportedObjectType verifies a flat-imported module's exported
// object type resolves in the importer's annotations and literals — both the
// type-check (during Exec) and the runtime construction/field read.
func TestImport_ExportedObjectType(t *testing.T) {
	dir := t.TempDir()
	writeModule(t, dir, "lib.buzz", `export object Foo { n: int = 0 }`)

	ctx := context.Background()
	sess := NewSession(ctx, WithEmbedded())
	defer sess.Close()
	sess.SetIncludeDirs([]string{dir})

	src := `
import "lib";
fun make() > Foo { return Foo{ n = 7 }; }
export final result = make().n;
`
	require.NoError(t, sess.Exec(ctx, src), "exec with imported object type")
	v, ok := sess.Exports()["result"]
	require.True(t, ok, "result not exported")
	require.True(t, v.IsInt(), "result = %v, want 7", v.String())
	assert.Equal(t, int64(7), v.AsInt(), "result = %v, want 7", v.String())
}

// TestImport_ExportedEnumType verifies a flat-imported module's exported enum
// type resolves in the importer (annotation + case access).
func TestImport_ExportedEnumType(t *testing.T) {
	dir := t.TempDir()
	writeModule(t, dir, "palette.buzz", `export enum Color { Red, Green, Blue }`)

	ctx := context.Background()
	sess := NewSession(ctx, WithEmbedded())
	defer sess.Close()
	sess.SetIncludeDirs([]string{dir})

	src := `
import "palette";
fun pick() > Color { return Color.Green; }
final c = pick();
`
	require.NoError(t, sess.Exec(ctx, src), "exec with imported enum type")
}

// TestImport_CrossReferencingObjectTypes verifies imported object types that
// reference each other (a field typed as a sibling imported object) resolve.
func TestImport_CrossReferencingObjectTypes(t *testing.T) {
	dir := t.TempDir()
	writeModule(t, dir, "shapes.buzz", `
export object Inner { v: int = 0 }
export object Outer { inner: Inner = Inner{} }
`)

	ctx := context.Background()
	sess := NewSession(ctx, WithEmbedded())
	defer sess.Close()
	sess.SetIncludeDirs([]string{dir})

	src := `
import "shapes";
fun build() > Outer { return Outer{ inner = Inner{ v = 3 } }; }
export final got = build().inner.v;
`
	require.NoError(t, sess.Exec(ctx, src), "exec with cross-referencing imported types")
	v, ok := sess.Exports()["got"]
	require.True(t, ok, "got not exported")
	require.True(t, v.IsInt(), "got = %v, want 3", v.String())
	assert.Equal(t, int64(3), v.AsInt(), "got = %v, want 3", v.String())
}

// TestSourceModule_ExportsTypes verifies a host-registered source module
// (embedded .buzz, no file on the include path) exposes its exported object/enum
// types to the importer — including object-typed and list field defaults, which
// the canonical magus/target module relies on.
func TestSourceModule_ExportsTypes(t *testing.T) {
	ctx := context.Background()
	sess := NewSession(ctx, WithEmbedded())
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
	require.NoError(t, sess.Exec(ctx, src), "exec with source-module types")
	v, ok := sess.Exports()["tname"]
	require.True(t, ok, "tname not exported")
	require.True(t, v.IsStr(), "tname = %v, want \"build\"", v.String())
	assert.Equal(t, "build", v.AsString(), "tname")
}

// TestImport_NonExportedObjectType_Errors verifies that only EXPORTED types
// cross the module boundary: a non-exported imported object is not visible to
// the importer's checker, so using it is a compile-time "undefined type" error.
func TestImport_NonExportedObjectType_Errors(t *testing.T) {
	dir := t.TempDir()
	writeModule(t, dir, "internal.buzz", `object Secret { n: int = 0 }`)

	ctx := context.Background()
	sess := NewSession(ctx, WithEmbedded())
	defer sess.Close()
	sess.SetIncludeDirs([]string{dir})

	err := sess.Exec(ctx, `import "internal"; final s = Secret{ n = 1 };`)
	require.Error(t, err, "expected error using a non-exported imported type, got nil")
	assert.Containsf(t, err.Error(), "undefined type", "error = %q, want it to mention \"undefined type\"", err.Error())
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

// Conformance suite for advanced language semantics — fibers, closures/upvalues,
// scope, and recursion — the bug-prone areas surfaced while writing the
// bubblegum example. Every expected value here was differentially verified
// against the upstream Buzz interpreter (zig build of buzz-language/buzz, run via
// `buzz run-script` / `buzz test`): a program was run on BOTH runtimes and the
// outputs compared. A divergence is therefore a real conformance defect, not a
// guess at intended semantics.
//
// Programs are driven through globals (resume/resolve results stored in finals)
// and asserted with GetGlobal, mirroring TestFiberBasic, so no main()/auto-main
// or stdout capture is involved.
//
// Reproduce the differential check (with upstream built at ~/.local/bin/buzz):
//
//	{ echo 'import "buzz:std";'; cat body.buzz; } | buzz run-script /dev/stdin
//	{ echo 'import "std";';      cat body.buzz; } | go run ./cmd/buzz /dev/stdin

// confRejects asserts that src is REJECTED (lenient syntax upstream forbids must
// not slip through gopherbuzz's checker) and the error mentions wantSubstr. This
// is the strict-parity direction: every leniency removed from the parser/checker
// gets a test here proving the lenient form now fails. Each snippet was confirmed
// to error on the upstream Buzz interpreter too.
func confRejects(t *testing.T, src, wantSubstr string) {
	t.Helper()
	sess := newSession(context.Background())
	defer func() { _ = sess.Close() }()
	err := sess.Exec(context.Background(), src)
	require.Error(t, err, "expected lenient program to be rejected")
	assert.Contains(t, err.Error(), wantSubstr)
}

// TestConformance_StrictParityRejections pins the leniencies gopherbuzz no longer
// accepts, matching upstream Buzz. Each case is valid upstream-Buzz-rejected and
// gopherbuzz-rejected.
func TestConformance_StrictParityRejections(t *testing.T) {
	t.Run("non-optional fiber yield type", func(t *testing.T) {
		confRejects(t, `fun g() > void *> int { _ = yield 1; } final f = &g();`,
			"expected optional type or void")
	})
	t.Run("untyped parameter", func(t *testing.T) {
		confRejects(t, `fun f(n) > int { return n; } final r = f(1);`,
			"must have a type annotation")
	})
	t.Run("untyped lambda parameter", func(t *testing.T) {
		confRejects(t, `final g = fun(x) > int { return x; }; final r = g(1);`,
			"must have a type annotation")
	})
	t.Run("return type without >", func(t *testing.T) {
		confRejects(t, `fun f() int { return 1; } final r = f();`,
			"return type must be preceded by '>'")
	})
	t.Run("bare void return type without >", func(t *testing.T) {
		confRejects(t, `fun f() void { } f();`,
			"return type must be preceded by '>'")
	})
	t.Run("function-type return without >", func(t *testing.T) {
		confRejects(t, `fun mk() fun(int) > int { return fun(x: int) > int { return x; }; } final r = mk();`,
			"return type must be preceded by '>'")
	})
	t.Run("reserved word as variable name", func(t *testing.T) {
		confRejects(t, `final out = 1; final r = out;`,
			"reserved word")
	})
	t.Run("reserved word as parameter name", func(t *testing.T) {
		confRejects(t, `fun f(type: int) > int { return type; } final r = f(1);`,
			"reserved word")
	})
	t.Run("reserved word as function name", func(t *testing.T) {
		confRejects(t, `fun match() > int { return 1; } final r = match();`,
			"reserved word")
	})
}

// confStrictRejects asserts src parses fine in the embedding-lenient mode but is
// rejected by strict Parse (the default script-conformance mode), and the strict error
// mentions wantSubstr. These are the leniencies that cannot be removed from the
// default path without breaking the REPL/eval/magusfile embedding, so they are
// enforced only in strict (portable-script) mode.
func confStrictRejects(t *testing.T, src, wantSubstr string) {
	t.Helper()
	if _, err := ParseEmbedded(src); err != nil {
		t.Fatalf("expected lenient ParseEmbedded to accept, got: %v", err)
	}
	_, err := Parse(src)
	require.Error(t, err, "expected strict Parse to reject")
	assert.Contains(t, err.Error(), wantSubstr)
}

// TestConformance_StrictModeRejections pins the leniencies enforced only in
// strict Parse (upstream script conformance): no top-level control flow, and
// labeled call arguments. Each is accepted by the lenient embedding parser.
func TestConformance_StrictModeRejections(t *testing.T) {
	t.Run("top-level if", func(t *testing.T) {
		confStrictRejects(t, `if (true) { final a = 1; }`, "not allowed at the top level")
	})
	t.Run("top-level foreach", func(t *testing.T) {
		confStrictRejects(t, `foreach (i in 0..3) { final a = i; }`, "not allowed at the top level")
	})
	t.Run("top-level while", func(t *testing.T) {
		confStrictRejects(t, `var i = 0; while (i < 3) { i = i + 1; }`, "not allowed at the top level")
	})
	t.Run("top-level return", func(t *testing.T) {
		confStrictRejects(t, `return;`, "not allowed at the top level")
	})
	t.Run("unlabeled second argument (literal)", func(t *testing.T) {
		confStrictRejects(t, `fun f(a: int, b: int) > int { return a + b; } final r = f(1, 2);`,
			"must be labeled")
	})
	t.Run("top-level call is still allowed", func(t *testing.T) {
		if _, err := Parse(`fun f() > void {} f();`); err != nil {
			t.Fatalf("strict mode should allow top-level calls, got: %v", err)
		}
	})
	t.Run("bare-identifier arg is still allowed (implicit label)", func(t *testing.T) {
		if _, err := Parse(`fun f(a: int, b: int) > int { return a + b; } final b = 5; final r = f(1, b);`); err != nil {
			t.Fatalf("strict mode should allow bare-identifier args, got: %v", err)
		}
	})
}

func conf(t *testing.T, src string) *Session {
	t.Helper()
	sess := newSession(context.Background())
	t.Cleanup(func() { _ = sess.Close() })
	require.NoError(t, sess.Exec(context.Background(), src))
	return sess
}

// --- Fibers -----------------------------------------------------------------

// A resumed `yield X` expression evaluates to the value it yielded (upstream:
// `final a = yield 7;` binds a == 7). Regression test for the OpYield fix that
// previously pushed Null as the expression result.
func TestConformance_YieldExpressionValue(t *testing.T) {
	s := conf(t, `
fun g() > int *> int? { final a = yield 7; return a; }
final f = &g();
final y = resume f;    // 7 (the yielded value)
final done = resume f; // null (fiber completed)
final r = resolve f;   // 7 (return value == a, proving the yield expr was 7)
`)
	assert.Equal(t, int64(7), s.GetGlobal("y").AsInt(), "resume returns yielded value")
	assert.True(t, s.GetGlobal("done").IsNull(), "resume after completion is null")
	assert.Equal(t, int64(7), s.GetGlobal("r").AsInt(), "yield expression evaluated to the yielded value")
}

// foreach over &fn() drives the fiber, binding each yielded value in turn.
func TestConformance_ForeachOverFiber(t *testing.T) {
	s := conf(t, `
fun squares(n: int) > void *> int? { foreach (i in 1..n) { _ = yield (i * i); } }
var sum = 0;
foreach (v in &squares(5)) { sum = sum + v; }
final total = sum; // 1+4+9+16 = 30
`)
	assert.Equal(t, int64(30), s.GetGlobal("total").AsInt())
}

// Two instances of the same fiber function keep independent local state.
func TestConformance_FiberInstancesIndependent(t *testing.T) {
	s := conf(t, `
fun ticker() > int *> int? { var i = 0; while (true) { _ = yield i; i = i + 1; } return 0; }
final a = &ticker();
final b = &ticker();
final a0 = resume a; final a1 = resume a; // 0, 1
final b0 = resume b;                        // 0 (independent)
final a2 = resume a;                        // 2
`)
	assert.Equal(t, int64(0), s.GetGlobal("a0").AsInt())
	assert.Equal(t, int64(1), s.GetGlobal("a1").AsInt())
	assert.Equal(t, int64(0), s.GetGlobal("b0").AsInt(), "second fiber instance has its own state")
	assert.Equal(t, int64(2), s.GetGlobal("a2").AsInt())
}

// A fiber may drive another fiber (nested resume).
func TestConformance_NestedFibers(t *testing.T) {
	s := conf(t, `
fun inner() > int *> int? { _ = yield 10; _ = yield 20; return 0; }
fun outer() > int *> int? {
    final g = &inner();
    _ = yield (resume g) ?? -1;
    _ = yield (resume g) ?? -1;
    return 0;
}
final f = &outer();
final x = resume f; // 10
final y = resume f; // 20
`)
	assert.Equal(t, int64(10), s.GetGlobal("x").AsInt())
	assert.Equal(t, int64(20), s.GetGlobal("y").AsInt())
}

// Ordinary recursion inside a fiber body works across yields.
func TestConformance_RecursionInFiber(t *testing.T) {
	s := conf(t, `
fun fact(n: int) > int { if (n <= 1) { return 1; } return n * fact(n - 1); }
fun gen() > int *> int? { _ = yield fact(5); _ = yield fact(6); return 0; }
final f = &gen();
final a = resume f; // 120
final b = resume f; // 720
`)
	assert.Equal(t, int64(120), s.GetGlobal("a").AsInt())
	assert.Equal(t, int64(720), s.GetGlobal("b").AsInt())
}

// --- Closures / upvalues ----------------------------------------------------

// A closure captures and mutates an upvalue; calls share the same cell.
func TestConformance_ClosureCounter(t *testing.T) {
	s := conf(t, `
fun makeCounter() > fun () > int {
    var n = 0;
    return fun () > int { n = n + 1; return n; };
}
final c = makeCounter();
final a = c(); final b = c(); final d = c(); // 1, 2, 3
`)
	assert.Equal(t, int64(1), s.GetGlobal("a").AsInt())
	assert.Equal(t, int64(2), s.GetGlobal("b").AsInt())
	assert.Equal(t, int64(3), s.GetGlobal("d").AsInt())
}

// Each loop iteration's closure captures the iteration's value (inside a fn).
func TestConformance_LoopVarCaptureInFunction(t *testing.T) {
	s := conf(t, `
fun build() > int {
    var sum = 0;
    foreach (i in 0..3) {
        final f = fun () > int { return i; };
        sum = sum + f();
    }
    return sum; // 0+1+2 = 3
}
final total = build();
`)
	assert.Equal(t, int64(3), s.GetGlobal("total").AsInt())
}

// Nested closures resolve upvalues through multiple enclosing scopes.
func TestConformance_NestedClosureUpvalues(t *testing.T) {
	s := conf(t, `
fun outer(x: int) > fun (y: int) > int {
    return fun (y: int) > int {
        final inner = fun (z: int) > int { return x + y + z; };
        return inner(100);
    };
}
final r = outer(1)(20); // 1 + 20 + 100 = 121
`)
	assert.Equal(t, int64(121), s.GetGlobal("r").AsInt())
}

// --- Scope: global vs local -------------------------------------------------

// A local shadows a global; mutating the local leaves the global untouched.
func TestConformance_LocalShadowsGlobal(t *testing.T) {
	s := conf(t, `
var g = 100;
fun f() > int { var g = 5; g = g + 1; return g; }
final local = f(); // 6
final global = g;  // 100 (unchanged)
`)
	assert.Equal(t, int64(6), s.GetGlobal("local").AsInt())
	assert.Equal(t, int64(100), s.GetGlobal("global").AsInt())
}

// A function with no shadowing mutates the global directly.
func TestConformance_GlobalMutationFromFunction(t *testing.T) {
	s := conf(t, `
var counter = 0;
fun bump() > void { counter = counter + 1; }
bump(); bump(); bump();
final result = counter; // 3
`)
	assert.Equal(t, int64(3), s.GetGlobal("result").AsInt())
}

// --- Recursion --------------------------------------------------------------

func TestConformance_Recursion(t *testing.T) {
	s := conf(t, `
fun fact(n: int) > int { if (n <= 1) { return 1; } return n * fact(n - 1); }
fun isEven(n: int) > bool { if (n == 0) { return true; } return isOdd(n - 1); }
fun isOdd(n: int) > bool { if (n == 0) { return false; } return isEven(n - 1); }
final f = fact(6);    // 720
final e = isEven(10); // true
final o = isOdd(7);   // true
`)
	assert.Equal(t, int64(720), s.GetGlobal("f").AsInt())
	assert.True(t, s.GetGlobal("e").AsBool())
	assert.True(t, s.GetGlobal("o").AsBool())
}

// --- Known conformance gaps (differentially confirmed; tracked, not fixed) --
//
// These are documented divergences from upstream Buzz, each confirmed by running
// the same source on both runtimes. They are skipped rather than deleted so the
// expected (upstream) behavior is recorded and the tests start passing once the
// underlying defect is fixed.

// GAP: yield operand precedence. Upstream parses `yield a + b` as `(yield a) + b`
// (yield is a .Primary-precedence prefix); gopherbuzz consumes the whole
// expression as `yield (a + b)`. Fixing the parser is blocked on the mid-
// expression resume gap below, so it is held to preserve the superset invariant.
func TestConformance_GAP_YieldPrecedence(t *testing.T) {
	t.Skip("known gap: `yield a + b` parses as yield(a+b); upstream is (yield a)+b. See parser.go yield note.")
	s := conf(t, `
fun g() > void *> int? { _ = yield 2 + 10; }
final f = &g();
final y = resume f; // upstream: 2  (gopherbuzz currently: 12)
`)
	assert.Equal(t, int64(2), s.GetGlobal("y").AsInt())
}

// GAP: resuming a fiber whose `yield` sits mid-expression. Upstream evaluates
// `(yield 5) + 3` to 8 after resume; gopherbuzz yields null for the resumed
// sub-expression and errors ("null in arithmetic"). Blocks the precedence fix.
func TestConformance_GAP_YieldMidExpressionResume(t *testing.T) {
	t.Skip("known gap: resuming a fiber with pending stack ops after the yield loses the resumed value")
	s := conf(t, `
fun g() > int *> int? { final r = (yield 5) + 3; return r; }
final f = &g();
final y = resume f;  // 5
final _c = resume f; // completes
final r = resolve f; // upstream: 8
`)
	assert.Equal(t, int64(5), s.GetGlobal("y").AsInt())
	assert.Equal(t, int64(8), s.GetGlobal("r").AsInt())
}

// GAP: `=>` expression-body (arrow) functions/lambdas. Upstream supports
// `fun (n: int) > int => n * 2`; gopherbuzz has no `=>` token and requires a
// `{ return ...; }` block body.
func TestConformance_GAP_ArrowLambda(t *testing.T) {
	t.Skip("known gap: gopherbuzz lacks `=>` expression-body lambdas; use a block body")
	s := conf(t, `final double = fun (n: int) > int => n * 2; final r = double(21);`)
	assert.Equal(t, int64(42), s.GetGlobal("r").AsInt())
}
