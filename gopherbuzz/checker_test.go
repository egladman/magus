package buzz

import (
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
const x = 42;
const y = "hello";
const z = true;
const w = null;
`)
}

func TestCheck_UndefinedVar(t *testing.T) {
	checkErr(t, `const x = undeclared;`, "undefined: undeclared")
}

func TestCheck_ConstReassign(t *testing.T) {
	checkErr(t, `
const x = 1;
x = 2;
`, "cannot assign to const")
}

func TestCheck_VarReassign(t *testing.T) {
	checkOK(t, `
var x = 1;
x = 2;
`)
}

func TestCheck_TypeAnnotMismatch(t *testing.T) {
	checkErr(t, `const x: int = "hello";`, "cannot assign str to int")
}

func TestCheck_TypeAnnotOK(t *testing.T) {
	checkOK(t, `const x: int = 42;`)
	checkOK(t, `const x: str = "hello";`)
	checkOK(t, `const x: bool = true;`)
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
	checkOK(t, `fun g() { const x = 1; }`)
}

func TestCheck_FunctionArity(t *testing.T) {
	checkErr(t, `
fun add(a, b) int { return a + b; }
const r = add(1);
`, "wrong argument count: got 1, want 2")
}

func TestCheck_FunctionArityOK(t *testing.T) {
	checkOK(t, `
fun add(a, b) int { return a + b; }
const r = add(1, 2);
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
	checkOK(t, `const r = 1 + 2;`)
	checkOK(t, `const r = 1.5 + 2;`)
	checkOK(t, `const r = "a" + "b";`)
}

func TestCheck_ArithmeticInvalidType(t *testing.T) {
	checkErr(t, `const r = true + 1;`, "invalid type bool in arithmetic")
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
const p = Point{ x = 1, z = 2 };
`, `has no field "z"`)
}

func TestCheck_ObjectLitOK(t *testing.T) {
	checkOK(t, `
object Point {
    x: int = 0,
    y: int = 0,
}
const p = Point{ x = 1, y = 2 };
`)
}

func TestCheck_ObjectMemberAccess(t *testing.T) {
	checkOK(t, `
object Point {
    x: int = 0,
    y: int = 0,
    fun sum() int { return this.x + this.y; }
}
const p = Point{ x = 1, y = 2 };
const s = p.sum();
`)
}

func TestCheck_ObjectUnknownMember(t *testing.T) {
	checkErr(t, `
object Point { x: int = 0 }
const p = Point{ x = 1 };
const z = p.z;
`, `has no field or method "z"`)
}

func TestCheck_EnumDecl(t *testing.T) {
	checkOK(t, `
enum Color { Red, Green, Blue }
const c = Color.Green;
`)
}

func TestCheck_EnumUnknownCase(t *testing.T) {
	checkErr(t, `
enum Color { Red, Green, Blue }
const c = Color.Yellow;
`, `has no case "Yellow"`)
}

func TestCheck_ImportMagus(t *testing.T) {
	checkOK(t, `
import "magus";
magus.project.register(".");
`)
}

func TestCheck_ForEachList(t *testing.T) {
	checkOK(t, `
const items = [1, 2, 3];
var sum = 0;
foreach (x in items) {
    sum = sum + x;
}
`)
}

func TestCheck_ForEachMap(t *testing.T) {
	checkOK(t, `
const m = {"a": 1, "b": 2};
foreach (k, v in m) {
    const combined = k + v;
}
`)
}

func TestCheck_ListLen(t *testing.T) {
	checkOK(t, `
const xs = [1, 2, 3];
const n = xs.len;
`)
}

func TestCheck_NullCoalesce(t *testing.T) {
	checkOK(t, `const x = null ?? "fallback";`)
}

func TestCheck_Closure(t *testing.T) {
	checkOK(t, `
fun makeAdder(n) fun(int)int {
    return fun(x: int) int { return x + n; };
}
const add5 = makeAdder(5);
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
const xs = [10, 20, 30];
const a = xs[0];
const m = {"k": "v"};
const b = m["k"];
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
