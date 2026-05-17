package buzz

import (
	"context"
	"testing"
)

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
