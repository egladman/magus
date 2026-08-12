package buzz_test

// Hermetic coverage for the upstream-parity language features. TestUpstreamConformance
// is the broader gate, but it needs a pinned foreign checkout and is deliberately
// opt-in (see magusfile.buzz: the `conformance` target is not part of `ci`), so on
// its own it protects none of this in CI. These cases run in strict (upstream-parity)
// mode with no host wiring, so a regression in any feature below fails `go test`.

import (
	"context"
	"testing"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
	buzzstd "github.com/egladman/magus/libs/gopherbuzz/std"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// evalParity execs src in a strict session and returns the result of calling
// probe(), the zero-argument function every case declares. Strict mode is the
// point: these features must parse under upstream's rules, not the lenient
// embedded ones. It calls probe through the session rather than eval-ing a
// `return`, which strict mode rightly rejects at the top level.
func evalParity(t *testing.T, src string) vm.Value {
	t.Helper()
	ctx := context.Background()
	s := buzz.NewSession(ctx)
	t.Cleanup(func() { _ = s.Close() })
	require.NoError(t, s.Exec(ctx, src), "Exec")
	probe, ok := s.Globals()["probe"]
	require.True(t, ok, "source must declare a zero-argument probe()")
	v, err := s.CallValue(ctx, probe, nil)
	require.NoError(t, err, "probe()")
	return v
}

func TestParity_ForLoopMultipleClauses(t *testing.T) {
	v := evalParity(t, `
fun probe() > int {
    var sum = 0;
    for (i: int = 0, j: int = 9; i < 10 and j >= 0; i = i + 1, j = j - 1) {
        sum = sum + i + j;
    }
    return sum;
}`)
	assert.Equal(t, int64(90), v.AsInt(), "each clause list runs every iteration")
}

func TestParity_NullableDeclarationDefaultsToNull(t *testing.T) {
	v := evalParity(t, `
fun probe() > bool {
    final hello: int?;
    return hello == null;
}`)
	assert.True(t, v.AsBool(), "a nullable declaration may omit its initializer")
}

func TestParity_ObjectLiteralFieldPunning(t *testing.T) {
	v := evalParity(t, `
object Person {
    name: str,
    age: int,
    sex: bool,
}

fun probe() > bool {
    final name = "joe";
    final age = 24;
    final person = Person{
        name,
        age,
        sex = true,
    };
    return person.name == "joe" and person.age == 24 and person.sex;
}`)
	assert.True(t, v.AsBool(), "a bare field name puns the same-named variable")
}

func TestParity_AnonymousObjectLiteralFieldPunning(t *testing.T) {
	v := evalParity(t, `
fun probe() > str {
    final data = "hello";
    final info = .{ data };
    return info.data;
}`)
	assert.Equal(t, "hello", v.AsString(), "punning works in a .{} literal too")
}

func TestParity_VoidArrowBodyAndUnannotatedReturn(t *testing.T) {
	// The arrow body is an expression statement, not a return, so `> void` does
	// not reject its own sugar; and `fun ()` must accept a `fun () > void`.
	v := evalParity(t, `
fun upvals() > fun () {
    final upvalue = 12;
    return fun () > void => upvalue + 1;
}

fun probe() > bool {
    upvals()();
    return true;
}`)
	assert.True(t, v.AsBool(), "a void arrow body evaluates for effect")
}

func TestParity_EnumStrBackingType(t *testing.T) {
	v := evalParity(t, `
enum<str> StrEnum {
    one,
    two,
}

fun probe() > str {
    return "{StrEnum.one.value}/{StrEnum.two.value}";
}`)
	assert.Equal(t, "one/two", v.AsString(), "a str-backed case takes its own name as its value")
}

func TestParity_EnumIntExplicitCaseValues(t *testing.T) {
	v := evalParity(t, `
enum<int> IntEnum {
    one = 1,
    two = 2,
    three = 3,
}

fun probe() > int {
    return IntEnum.one.value + IntEnum.two.value + IntEnum.three.value;
}`)
	assert.Equal(t, int64(6), v.AsInt(), "an explicit case value wins over the ordinal")
}

func TestParity_PlainEnumKeepsOrdinalValues(t *testing.T) {
	v := evalParity(t, `
enum NaturalEnum {
    zero,
    one,
    two,
}

fun probe() > int {
    return NaturalEnum.zero.value + NaturalEnum.one.value + NaturalEnum.two.value;
}`)
	assert.Equal(t, int64(3), v.AsInt(), "an unbacked enum still numbers its cases from zero")
}

func TestParity_OptionalChaining(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want any
	}{
		{"member on non-null", `final o: [int]? = [1, 2, 3]; return o?.len();`, int64(3)},
		{"method call on null", `final o: [int]? = null; return o?.len();`, nil},
		{"subscript on non-null", `final o: [int]? = [7, 8]; return o?[1];`, int64(8)},
		{"subscript on null", `final o: [int]? = null; return o?[1];`, nil},
		{"chained hops short-circuit", `final o: {str: [int]}? = null; return o?["k"]?.len();`, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := evalParity(t, "fun probe() > any {\n"+tc.src+"\n}")
			if tc.want == nil {
				assert.True(t, v.IsNull(), "a null receiver yields null, it does not error")
				return
			}
			assert.Equal(t, tc.want, v.AsInt(), "a non-null receiver passes straight through")
		})
	}
}

func TestParity_DefaultArguments(t *testing.T) {
	const decls = `
fun hey(name: str = "Joe", age: int = 12, father: str?, fourth: int = 1) > str
    => "Hello {name} you're {age} {father} {fourth}";
`
	cases := []struct {
		name string
		call string
		want string
	}{
		{"one positional", `hey("John")`, "Hello John you're 12 null 1"},
		{"labeled middle", `hey(age: 25)`, "Hello Joe you're 25 null 1"},
		{"nullable parameter defaults to null", `hey(father: "Doe")`, "Hello Joe you're 12 Doe 1"},
		{"labeled last", `hey(fourth: 42)`, "Hello Joe you're 12 null 42"},
		{"labels out of order", `hey(fourth: 12, age: 44)`, "Hello Joe you're 44 null 12"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := evalParity(t, decls+"\nfun probe() > str { return "+tc.call+"; }")
			assert.Equal(t, tc.want, v.AsString(), "omitted slots take their declared defaults")
		})
	}
}

func TestParity_MissingArgumentWithoutDefaultStillErrors(t *testing.T) {
	// Defaults must not turn arity checking off: a parameter with no default and
	// no nullable type is still required.
	s := buzz.NewSession(context.Background())
	t.Cleanup(func() { _ = s.Close() })
	err := s.Exec(context.Background(), `
fun need(a: int = 1, b: str) > str => "{a}{b}";
fun probe() > str { return need(); }`)
	require.Error(t, err, "a required parameter cannot be omitted")
	assert.Contains(t, err.Error(), `missing argument "b"`)
}

func TestParity_FunctionTypeErrorSetAndYieldSuffix(t *testing.T) {
	// `!>` and `*>` may follow a function TYPE in parameter position. They are
	// consumed but kept out of the annotation text, so the parameter's type is
	// still `fun(int)int` and an ordinary function is assignable to it.
	v := evalParity(t, `
fun twice(value: int) > int {
    return value * 2;
}

fun callDynamicWithCatch(fn: fun (value: int) > int !>
    str, value: int) > int {
    return fn(value) catch 99;
}

fun probe() > int {
    return callDynamicWithCatch(twice, value: 21);
}`)
	assert.Equal(t, int64(42), v.AsInt(), "the call runs; 99 would mean it threw instead")
}

func TestParity_MultipleTypedCatchClauses(t *testing.T) {
	const decls = `
object SomeError {}

fun willFail(kind: str) > void !> str {
    if (kind == "str") {
        throw "boom";
    }
    throw SomeError{};
}
`
	cases := []struct {
		name string
		kind string
		want string
	}{
		{"first clause matches", "str", "str"},
		{"later clause matches", "obj", "obj"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := evalParity(t, decls+`
fun probe() > str {
    try {
        willFail("`+tc.kind+`");
    } catch (_: str) {
        return "str";
    } catch (_: SomeError) {
        return "obj";
    }
    return "none";
}`)
			assert.Equal(t, tc.want, v.AsString(), "the first clause whose type matches runs")
		})
	}
}

func TestParity_CatchAnyMatchesEverything(t *testing.T) {
	v := evalParity(t, `
fun willFail() > void !> str {
    throw "Yolo";
}

fun probe() > bool {
    var caught = false;
    try {
        willFail();
    } catch (error: any) {
        caught = error is str;
    }
    return caught;
}`)
	assert.True(t, v.AsBool(), "`any` is inhabited by every value, so it catches everything")
}

func TestParity_UnmatchedCatchRethrows(t *testing.T) {
	// A try whose clauses do not claim the error must not swallow it: the error
	// has to reach the enclosing handler.
	v := evalParity(t, `
fun willFail() > void !> str {
    throw "boom";
}

fun inner() > str !> str {
    try {
        willFail();
    } catch (_: int) {
        return "wrong";
    }
    return "fell through";
}

fun probe() > str {
    return inner() catch "rethrown";
}`)
	assert.Equal(t, "rethrown", v.AsString(), "an unclaimed error propagates outward")
}

func TestParity_LabeledLoops(t *testing.T) {
	cases := []struct {
		name string
		body string
		want int64
	}{
		{
			name: "labeled break",
			body: `
    var i = 0;
    while (i < 100) :here {
        i = i + 1;
        if (i == 10) {
            break here;
        }
    }
    return i;`,
			want: 10,
		},
		{
			// The foreach's iterator state has to come off the stack on the way
			// out, or every later stack offset is wrong.
			name: "labeled break out of a nested loop",
			body: `
    var i = 0;
    foreach (_ in 0..100) :here {
        while (i < 100) {
            i = i + 1;
            if (i == 10) {
                break here;
            }
        }
    }
    return i;`,
			want: 10,
		},
		{
			name: "labeled break out of a deeply nested loop",
			body: `
    var i = 0;
    foreach (j in 0..100) :here {
        while (j < 100) {
            while (i < 100) {
                i = i + 1;
                if (i == 10) {
                    break here;
                }
            }
        }
    }
    return i;`,
			want: 10,
		},
		{
			name: "labeled continue",
			body: `
    var i = 0;
    foreach (j in 0..10) :here {
        if (j == 3) {
            continue here;
        }
        i = i + j;
    }
    return i;`,
			want: 42,
		},
		{
			// The strongest witness that the iterator state is discarded: the
			// OUTER foreach's next step peeks the top of the stack, so an inner
			// state left behind is the one it would advance.
			name: "labeled break leaves the inner iterator state balanced",
			body: `
    var acc = 0;
    foreach (_ in 0..3) {
        foreach (j in 0..3) :inner {
            if (j == 1) {
                break inner;
            }
            acc = acc + 1;
        }
    }
    return acc;`,
			want: 3,
		},
		{
			name: "unlabeled break still targets the innermost loop",
			body: `
    var i = 0;
    foreach (_ in 0..10) :here {
        while (true) {
            i = i + 1;
            break;
        }
    }
    return i;`,
			want: 10,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := evalParity(t, "fun probe() > int {"+tc.body+"\n}")
			assert.Equal(t, tc.want, v.AsInt(), "the label selects which loop is left")
		})
	}
}

func TestParity_BreakWithUnknownLabelIsAnError(t *testing.T) {
	s := buzz.NewSession(context.Background())
	t.Cleanup(func() { _ = s.Close() })
	err := s.Exec(context.Background(), `
fun probe() > int {
    while (true) :here {
        break elsewhere;
    }
    return 0;
}`)
	require.Error(t, err, "a label naming no enclosing loop cannot compile")
	assert.Contains(t, err.Error(), "elsewhere")
}

func TestParity_BlockExpressionWithoutOutIsRejected(t *testing.T) {
	// This case used to live in TestParity_BlockExpression asserting the opposite:
	// that `from { final unused = 1; }` evaluates to null. Upstream rejects it
	// (tests/compile_errors/block-expression-partial-out.buzz, "All block expression
	// paths must end with `out`"), and a block expression that silently produces null
	// is the dangerous kind of divergence - the value is consumed somewhere. Nothing
	// in this repo or in magus used the form, so the rule was adopted.
	ctx := context.Background()
	s := buzz.NewSession(ctx)
	t.Cleanup(func() { _ = s.Close() })
	err := s.Exec(ctx, `fun probe() > any { return from { final unused = 1; }; }`)
	require.Error(t, err, "a block expression with no out must be rejected")
	assert.Contains(t, err.Error(), "must end with `out`")
}

func TestParity_BlockExpression(t *testing.T) {
	cases := []struct {
		name string
		body string
		want any
	}{
		{name: "straight out", body: `return from { out "my value"; };`, want: "my value"},
		{
			name: "out from either branch",
			body: `
    final flag = false;
    return from {
        if (flag) {
            out "then";
        } else {
            out "else";
        }
    };`,
			want: "else",
		},
		{
			// The early out must skip the fallback, not fall through to it.
			name: "early out beats the fallback out",
			body: `
    final flag = true;
    return from {
        if (flag) {
            out "early";
        }
        out "fallback";
    };`,
			want: "early",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := tc.body
			if body == "" {
				body = tc.name
			}
			v := evalParity(t, "fun probe() > any {\n"+body+"\n}")
			if tc.want == nil {
				assert.True(t, v.IsNull(), "a block that never outs is null")
				return
			}
			assert.Equal(t, tc.want, v.AsString(), "the block's value is the out that ran")
		})
	}
}

func TestParity_NestedBlockExpressionOutBindsInnermost(t *testing.T) {
	// Each `out` leaves the block it sits in, so the inner block's value feeds
	// the outer one rather than escaping past it.
	v := evalParity(t, `
fun probe() > str {
    return from {
        final inner = from {
            out "in";
        };
        out "{inner}/out";
    };
}`)
	assert.Equal(t, "in/out", v.AsString(), "out targets the innermost enclosing from block")
}

func TestParity_FreeIdentifiers(t *testing.T) {
	v := evalParity(t, `
object A {
    @"type": str,
}

fun probe() > str {
    final @"non-standard-identifier" = "hello";
    final a = A{
        @"type" = "world",
    };
    return "{@"non-standard-identifier"} {a.@"type"}";
}`)
	assert.Equal(t, "hello world", v.AsString(), "@\"...\" names a binding, a field, and a member")
}

func TestParity_FreeIdentifierMayBeAReservedWord(t *testing.T) {
	// The quotes are the whole point: `type` is reserved, `@"type"` is not.
	v := evalParity(t, `
fun probe() > int {
    final @"type" = 7;
    return @"type";
}`)
	assert.Equal(t, int64(7), v.AsInt(), "the reserved-word rule does not reach a raw identifier")
}

func TestParity_BareReservedWordIsStillRejected(t *testing.T) {
	s := buzz.NewSession(context.Background())
	t.Cleanup(func() { _ = s.Close() })
	err := s.Exec(context.Background(), `fun probe() > int { final type = 7; return type; }`)
	require.Error(t, err, "only the quoted form escapes the reserved-word rule")
	assert.Contains(t, err.Error(), "reserved word")
}

func TestParity_GenericObjectDeclaration(t *testing.T) {
	// Type parameters are erased, so the object's identity stays its bare name:
	// `payload is Payload::<str, int>` has to test against `Payload`.
	v := evalParity(t, `
object Payload::<K, V> {
    data: mut {K: V},
}

fun probe() > bool {
    final payload = Payload::<str, int>{
        data = mut { "one": 1 },
    };
    payload.data["two"] = 2;
    return payload is Payload::<str, int> and payload.data["two"] == 2;
}`)
	assert.True(t, v.AsBool(), "a generic object declares, instantiates, and type-tests")
}

func TestParity_InlineIfExpression(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{name: "constant condition", body: `return if (true) "then" else "else";`, want: "then"},
		{name: "computed condition", body: `return if ("hello".len() == 2) "then" else "else";`, want: "else"},
		{
			name: "else-if chain",
			body: `
    final value = 12;
    return if (value == 14)
        "hello"
    else if (value == 12)
        "yolo"
    else
        "fallback";`,
			want: "yolo",
		},
		{
			name: "nested in a larger expression",
			body: `
    final value = 12;
    return (if (value == 14) "hello" else "yolo") + "!";`,
			want: "yolo!",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := evalParity(t, "fun probe() > str {\n"+tc.body+"\n}")
			assert.Equal(t, tc.want, v.AsString(), "only the selected branch's value survives")
		})
	}
}

func TestParity_InlineIfRequiresBothBranches(t *testing.T) {
	// An expression has to produce a value on every path, so unlike the statement
	// form the else is mandatory.
	s := buzz.NewSession(context.Background())
	t.Cleanup(func() { _ = s.Close() })
	err := s.Exec(context.Background(), `fun probe() > int { return if (true) 1; }`)
	require.Error(t, err, "an inline if without else cannot compile")
}

func TestParity_InlineCatchVoid(t *testing.T) {
	v := evalParity(t, `
fun willFailVoid() > void !> str {
    throw "i'm failing";
}

fun willFail() > int !> str {
    throw "i'm failing";
}

fun probe() > int {
    willFailVoid() catch void;
    return willFail() catch 7;
}`)
	assert.Equal(t, int64(7), v.AsInt(), "catch void swallows the error and yields nothing")
}

func TestParity_InferredEnumCaseFromExpectedType(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want int64
	}{
		{
			name: "call argument",
			src: `
enum Suit { hearts, spades }
fun pick(s: Suit) > int => s.value;
fun probe() > int { return pick(.spades); }`,
			want: 1,
		},
		{
			name: "parameter default",
			src: `
enum Suit { hearts, spades }
fun pick(s: Suit = .spades) > int => s.value;
fun probe() > int { return pick(); }`,
			want: 1,
		},
		{
			// The enum is declared AFTER the object, so the method's signature is
			// built before the enum exists and has to be refreshed before the
			// default can resolve against it.
			name: "method default with a forward-declared enum",
			src: `
object Defaults {
    static fun pick(s: Suit = .spades) > int => s.value;
}
enum Suit { hearts, spades }
fun probe() > int { return Defaults.pick(); }`,
			want: 1,
		},
		{
			name: "anonymous object literal field",
			src: `
enum Suit { hearts, spades }
object Setting { suit: Suit }
fun probe() > int {
    final s: Setting = .{ suit = .spades };
    return s.suit.value;
}`,
			want: 1,
		},
		{
			name: "nested in an annotated map literal",
			src: `
enum Suit { hearts, spades }
object Setting { suit: Suit }
fun probe() > int {
    final m: {str: Setting} = { "a": .{ suit = .spades } };
    return m["a"]?.suit.value ?? -1;
}`,
			want: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := evalParity(t, tc.src)
			assert.Equal(t, tc.want, v.AsInt(), "the expected type says which enum a bare .case names")
		})
	}
}

func TestParity_AnnotatedMapLiteralIsStillAMap(t *testing.T) {
	// The anonymous-object path must not swallow a real map literal: `.{}` and
	// `{}` are different forms and only the first fills an object's fields.
	v := evalParity(t, `
fun probe() > int {
    final m: {str: int} = { "a": 1, "b": 2 };
    return m["a"] ?? 0;
}`)
	assert.Equal(t, int64(1), v.AsInt(), "a brace literal stays a map")
}

func TestParity_EnumFromValue(t *testing.T) {
	v := evalParity(t, `
enum Suit { hearts, spades }

fun probe() > str {
    final found = Suit(1);
    final missing = Suit(9);
    return "{found?.name}/{missing == null}";
}`)
	assert.Equal(t, "spades/true", v.AsString(), "calling an enum looks a case up by value, or yields null")
}

func TestParity_EnumFromValueUsesBackingValuesNotOrdinals(t *testing.T) {
	v := evalParity(t, `
enum<str> Suit { hearts, spades }

fun probe() > str {
    return "{Suit("spades")?.name}/{Suit(1) == null}";
}`)
	assert.Equal(t, "spades/true", v.AsString(), "the lookup is by the case's value, not its position")
}

func TestParity_RangeBindsTighterThanComparison(t *testing.T) {
	// `range == 0..10` must parse as `range == (0..10)`. At a looser precedence
	// it becomes `(range == 0)..10`, which ranges a bool.
	v := evalParity(t, `
fun probe() > bool {
    final limit = 10;
    final r = 0..limit;
    return r == 0..10;
}`)
	assert.True(t, v.AsBool(), "..  binds tighter than ==")
}

func TestParity_RangeEqualityIsStructural(t *testing.T) {
	v := evalParity(t, `
fun probe() > bool {
    return 0..10 == 0..10 and !(0..10 == 10..0);
}`)
	assert.True(t, v.AsBool(), "two ranges are equal when both operands match")
}

func TestParity_RangeMethods(t *testing.T) {
	cases := []struct {
		name string
		expr string
		want int64
	}{
		// low/high report the operands as written, not the smaller and larger.
		{"low", `(0..10).low()`, 0},
		{"high", `(0..10).high()`, 10},
		{"inverted low", `(10..0).low()`, 10},
		{"inverted high", `(10..0).high()`, 0},
		{"len", `(0..10).len()`, 10},
		{"inverted len", `(10..0).len()`, 10},
		{"toList length", `(0..10).toList().len()`, 10},
		{"inverted toList length", `(10..0).toList().len()`, 10},
		{"toList is ascending", `(0..10).toList()[0]`, 0},
		{"inverted toList descends", `(10..0).toList()[0]`, 10},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := evalParity(t, "fun probe() > int { return "+tc.expr+"; }")
			assert.Equal(t, tc.want, v.AsInt(), "a range runs from low toward high and stops before it")
		})
	}
}

func TestParity_RangeInvertAndContains(t *testing.T) {
	v := evalParity(t, `
fun probe() > bool {
    return (0..10).invert() == 10..0
        and (0..10).contains(0)
        and (0..10).contains(9)
        and !(0..10).contains(10)
        and !(0..10).contains(-1)
        and (10..0).contains(10)
        and !(10..0).contains(0);
}`)
	assert.True(t, v.AsBool(), "contains matches exactly what foreach would yield")
}

func TestParity_RangeMethodsMatchForeach(t *testing.T) {
	// len/toList/contains all have to agree with iteration, in both directions.
	v := evalParity(t, `
fun probe() > str {
    var up = 0;
    foreach (n in 0..10) {
        up = up + n;
    }
    var down = 0;
    foreach (n in 10..0) {
        down = down + n;
    }
    return "{up}/{down}";
}`)
	assert.Equal(t, "45/55", v.AsString(), "0..10 yields 0-9 and 10..0 yields 10-1")
}

func TestParity_VoidArrowBodyAcceptsAnAssignment(t *testing.T) {
	// A `> void` arrow body is a statement position. An assignment is not an
	// expression in Buzz, so this only parses because the void path reads a
	// statement rather than an expression.
	v := evalParity(t, `
fun probe() > int {
    var sum = 0;
    final add = fun (n: int) > void => sum = sum + n;
    add(5);
    return sum;
}`)
	// 5: closures capture by REFERENCE, as upstream does, so the assignment reaches
	// the enclosing sum. This pinned 0 while capture was by value.
	assert.Equal(t, int64(5), v.AsInt(), "the body parses and runs, and the write reaches the enclosing local")
}

// TestParity_MatchCompoundTypeCondition pins that a match arm whose condition is a
// COMPOUND type value actually matches. A type value carries the canonical spelling
// ("[str]"), while the runtime type test compares against the reduced shape ("list"),
// so passing the canonical name straight through made every such arm silently dead:
// the arm fell to `else` even where the equivalent `is` answered true. Upstream's own
// match.buzz only exercises simple arms (`<int>`, `<str>`, a named object), all of
// which reduce to themselves, so it cannot catch this.
//
// testdata/65_regressions.buzz covers the same ground through the bytecode codec now
// that a type-value constant serializes (v21); this keeps a direct, readable assertion
// on the reduction itself.
func TestParity_MatchCompoundTypeCondition(t *testing.T) {
	v := evalParity(t, `
fun probe() > str {
    final xs = [ "a", "b" ];
    final byList = match (xs) { <[str]> -> "L", else -> "none" };
    final byMap = match ({ "k": 1 }) { <{str: int}> -> "M", else -> "none" };
    final mismatch = match (xs) { <{str: int}> -> "M", else -> "none" };
    return "{byList}{byMap}{mismatch}";
}`)
	assert.Equal(t, "LMnone", v.String(), "compound type arms match, and a wrong shape still falls to else")
}

func TestParity_ClosureUpvaluesAreCapturedByReference(t *testing.T) {
	// Matches upstream: a captured local is one shared cell, not a per-closure copy,
	// so a closure assigning to it updates the variable itself. This asserted 0 while
	// gopherbuzz captured by value.
	v := evalParity(t, `
fun probe() > int {
    var sum = 0;
    final add = fun (n: int) > void { sum = sum + n; };
    add(5);
    return sum;
}`)
	assert.Equal(t, int64(5), v.AsInt(), "a closure mutating an enclosing local updates that local")
}

func TestParity_AnonymousObjectLiteralBecomesTheExpectedObject(t *testing.T) {
	// Resolved to a named object, `.{ ... }` gains that type's methods and the
	// defaults of the fields it does not mention. As a plain map it would answer
	// field reads but have neither.
	v := evalParity(t, `
object Payload {
    data: str,
    tag: str = "default",

    fun describe() > str => "{this.data}/{this.tag}";
}

fun take(p: Payload) > str => p.describe();

fun probe() > str {
    return take(.{ data = "hello" });
}`)
	assert.Equal(t, "hello/default", v.AsString(), "the literal is built as the object, not as a map")
}

func TestParity_AnonymousObjectLiteralStaysAMapWithoutAnExpectedObject(t *testing.T) {
	v := evalParity(t, `
fun probe() > str {
    final info = .{ name = "joe" };
    return info.name;
}`)
	assert.Equal(t, "joe", v.AsString(), "with no object to fill it remains a map")
}

func TestParity_CallWithoutParenthesesAroundAnObjectLiteral(t *testing.T) {
	v := evalParity(t, `
object Payload {
    data: str,

    fun len() => this.data.len();

    fun join(other: Payload) => "{this.data}:{other.data}";
}

fun callMe(payload: Payload) => payload.len();

fun probe() > str {
    final len = callMe .{
        data = "hello",
    };
    final payload = Payload{ data = "hello" };
    final joined = payload.join .{
        data = "world",
    };
    return "{len}/{joined}";
}`)
	assert.Equal(t, "5/hello:world", v.AsString(), "a lone object-literal argument may drop its parentheses")
}

func TestParity_DotStillMeansMemberAccess(t *testing.T) {
	// The paren-free call only triggers on `.` followed by `{`; ordinary member
	// access must be untouched.
	v := evalParity(t, `
object P { data: str }

fun probe() > str {
    final p = P{ data = "ok" };
    return p.data;
}`)
	assert.Equal(t, "ok", v.AsString(), "a dot before an identifier is still member access")
}

// TestParity_ExternFunDeclaresASignature covers `extern fun name(...) > T;`, the
// body-less forward declaration upstream uses to give its NATIVE stdlib types
// (src/lib/debug.buzz: `export extern fun dump(value: any) > void;`). Before it,
// `extern` was a reserved word the parser knew only well enough to refuse as a
// binding name, so every host-provided function typed as Unknown.
//
// It emits no code by design: the implementation is whatever the host already
// bound to that name, so a declaration must not shadow it with an empty closure.
func TestParity_ExternFunDeclaresASignature(t *testing.T) {
	s := buzz.NewSession(context.Background())
	t.Cleanup(func() { _ = s.Close() })
	require.NoError(t, s.Exec(context.Background(), `
extern fun native(n: int) > str;
export extern fun exported(value: any) > void;

fun probe() > int { return 1; }`), "a body-less extern declaration parses")

	_, bound := s.Globals()["native"]
	assert.False(t, bound, "an extern declaration must not bind a value: the host owns the implementation")
}

// TestParity_ExternReturnTypeIsChecked is the reason the declaration exists. The
// signature has to reach call sites, or it is decoration: a host call's result
// must type as its declared return, not as Unknown.
func TestParity_ExternReturnTypeIsChecked(t *testing.T) {
	s := buzz.NewSession(context.Background())
	t.Cleanup(func() { _ = s.Close() })
	err := s.Exec(context.Background(), `
extern fun native(n: int) > str;
fun probe() > int { return native(1); }`)
	require.Error(t, err, "the declared return type must reach the call site")
	assert.Contains(t, err.Error(), "return type mismatch")
}

// TestParity_ExternRejectsABody pins the shape: `extern` means the
// implementation is elsewhere, so a body is a contradiction rather than an
// extra. Catching it at the parser keeps the compiler's "extern emits nothing"
// rule from silently discarding real code.
func TestParity_ExternRejectsABody(t *testing.T) {
	s := buzz.NewSession(context.Background())
	t.Cleanup(func() { _ = s.Close() })
	err := s.Exec(context.Background(), `
extern fun native(n: int) > str { return "x"; }
fun probe() > int { return 1; }`)
	require.Error(t, err, "an extern declaration must not carry a body")
}

// TestParity_ExternStaysAnOrdinaryIdentifier guards the contextual lookahead:
// `extern` introduces a declaration only when `fun` follows it. Upstream reserves
// the word from BINDING positions (so an object field named extern is rightly
// refused) while leaving the non-binding ones open, and the new lookahead must
// not narrow that further.
func TestParity_ExternStaysAnOrdinaryIdentifier(t *testing.T) {
	v := evalParity(t, `
fun probe() > str {
    final m = {"extern": "key"};
    return m["extern"] ?? "missing";
}`)
	assert.Equal(t, "key", v.AsString(), "`extern` is only a keyword directly before `fun`")
}

// TestParity_MapMerge covers `{...} + {...}`, the map counterpart of list
// concatenation (upstream tests/behavior/composite-assign.buzz). The checker
// rejected it outright before, so `m += {...}` could not compile.
func TestParity_MapMerge(t *testing.T) {
	v := evalParity(t, `
fun probe() > str {
    final a = {"one": 1};
    final b = {"one": 9, "two": 2};
    final m = a + b;
    return "{m.size()}/{m["one"] ?? 0}/{a.size()}";
}`)
	// Right wins the duplicate key, and the left operand is untouched: `+` is an
	// expression, so it must copy rather than mutate in place.
	assert.Equal(t, "2/9/1", v.AsString(), "map merge takes the right operand's value and leaves the left alone")
}

// TestParity_FloatModulo covers `%` on doubles, which raised "not supported for
// float operands". Upstream's composite-assign.buzz asserts `4.0 %= 2.0` is 0,
// so this is fmod, not an integer-only operator.
func TestParity_FloatModulo(t *testing.T) {
	v := evalParity(t, `
fun probe() > double {
    var b = 4.0;
    b %= 2.0;
    return b + (5.5 % 2.0);
}`)
	assert.InDelta(t, 1.5, v.AsFloat(), 1e-9, "4.0 % 2.0 is 0.0 and 5.5 % 2.0 is 1.5")
}

// TestParity_ModuloByZeroReports guards the divisor check added alongside float
// modulo: math.Mod would return NaN, which would propagate silently instead of
// reporting where it went wrong.
func TestParity_ModuloByZeroReports(t *testing.T) {
	s := buzz.NewSession(context.Background())
	t.Cleanup(func() { _ = s.Close() })
	err := s.Exec(context.Background(), `fun probe() > double { return 5.5 % 0.0; }
final __r = probe();`)
	require.Error(t, err, "a zero divisor must report, not yield NaN")
	assert.Contains(t, err.Error(), "modulo by zero")
}

// TestParity_NullCoalescingBindsTighterThanTerm pins `??` at upstream's
// Precedence.NullCoalescing, which sits between Term (`+`/`-`) and Bitwise.
// gopherbuzz had it above `or`, looser than every binary operator, so the
// upstream idiom of coalescing a nullable where it is used raised at runtime:
// the `+` saw the null before `??` could replace it.
func TestParity_NullCoalescingBindsTighterThanTerm(t *testing.T) {
	v := evalParity(t, `
fun probe() > int {
    final n: int? = null;
    return 1 + n ?? 0;
}`)
	assert.Equal(t, int64(1), v.AsInt(), "`1 + n ?? 0` is `1 + (n ?? 0)`")
}

// TestParity_NullCoalescingStillLooserThanBitwise pins the other side of the
// level: moving `??` down must not push it past Bitwise.
func TestParity_NullCoalescingStillLooserThanBitwise(t *testing.T) {
	v := evalParity(t, `
fun probe() > int {
    final n: int? = null;
    return n ?? 1 | 2;
}`)
	assert.Equal(t, int64(3), v.AsInt(), "`n ?? 1 | 2` is `n ?? (1 | 2)`")
}

// TestParity_TypeValues covers `<T>` and `typeof`, upstream's type-as-value pair.
//
// The load-bearing property is that typeof is STATIC. `[]` and `[]` annotated
// `[str]` are the same empty list at runtime, so an implementation that probed
// the value could not tell them apart; upstream compares the type DEFS the
// compiler resolved, and so does this.
func TestParity_TypeValues(t *testing.T) {
	v := evalParity(t, `
fun probe() > str {
    final list = [];
    final slist: [str] = [];
    final map = {};
    final smap: {str: int} = {};
    return "{typeof list}/{typeof slist}/{typeof map}/{typeof smap}/{typeof 1}";
}`)
	assert.Equal(t, "<[any]>/<[str]>/<{any: any}>/<{str: int}>/<int>", v.AsString(),
		"an unannotated empty collection is [any]/{any: any}; an annotated one keeps its declared types")
}

// TestParity_TypeValueEquality pins the comparison. Two type values are built
// independently (one from a literal, one from typeof), so equality has to be
// structural on the canonical spelling - reference equality would make every
// `typeof x == <T>` false.
func TestParity_TypeValueEquality(t *testing.T) {
	v := evalParity(t, `
fun probe() > bool {
    final slist: [str] = [];
    return typeof slist == <[str]> and <int> != <str> and <{str: int}> == <{str: int}>;
}`)
	assert.True(t, v.AsBool(), "type values compare by what they denote, not by identity")
}

// TestParity_TypeOfDoesNotEvaluateItsOperand guards the static-ness directly: a
// typeof whose operand has a side effect must not run it.
func TestParity_TypeOfDoesNotEvaluateItsOperand(t *testing.T) {
	v := evalParity(t, `
var calls = 0;

fun bump() > int {
    calls = calls + 1;
    return 1;
}

fun probe() > int {
    _ = typeof bump();
    return calls;
}`)
	assert.Equal(t, int64(0), v.AsInt(), "typeof reads a type, it does not call anything")
}

// TestParity_CollectionCloneAliases covers upstream's copyMutable/copyImmutable,
// which obj.zig declares as ALIASES of cloneMutable/cloneImmutable rather than
// as separate operations. Missing them made `list.copyImmutable()` a null call.
func TestParity_CollectionCloneAliases(t *testing.T) {
	v := evalParity(t, `
fun probe() > str {
    final l = [1, 2, 3];
    final m = {"a": 1};
    return "{l.copyImmutable().len()}/{m.copyImmutable().size()}/{l.cloneMutable().len()}";
}`)
	assert.Equal(t, "3/1/3", v.AsString(), "the copy* names are the clone* operations under upstream's spelling")
}

// TestParity_MapHasKey covers map.hasKey, which upstream declares on the map
// object and gopherbuzz did not implement at all.
func TestParity_MapHasKey(t *testing.T) {
	v := evalParity(t, `
fun probe() > bool {
    final m = {"hello": "world"};
    return m.hasKey("hello") and !m.hasKey("absent");
}`)
	assert.True(t, v.AsBool(), "hasKey reports presence, not the value")
}

// TestParity_ListFillRange covers fill's start/len window. Filling the whole list
// regardless passes the simple case and silently corrupts the windowed one, which
// is why this asserts the UNTOUCHED neighbours rather than only the filled span.
func TestParity_ListFillRange(t *testing.T) {
	v := evalParity(t, `
fun probe() > str {
    final all = (mut [1, 2, 3]).fill(42);
    final some = (mut [0, 1, 2, 3, 4, 5]).fill(42, start: 2, len: 3);
    return "{all[0]}{all[2]}/{some[1]}{some[2]}{some[4]}{some[5]}";
}`)
	assert.Equal(t, "4242/142425", v.AsString(), "fill without a window covers everything; with one it covers exactly [start, start+len)")
}

// TestParity_ListRemoveOutOfRangeIsNull pins remove's miss behaviour. Upstream
// documents "or null when out of bounds" and asserts `list.remove(12) == null`;
// raising instead made a miss unrecoverable.
func TestParity_ListRemoveOutOfRangeIsNull(t *testing.T) {
	v := evalParity(t, `
fun probe() > bool {
    final l = mut ["a", "b", "c"];
    return l.remove(12) == null and l.len() == 3 and l.remove(1) == "b";
}`)
	assert.True(t, v.AsBool(), "an out-of-range remove yields null and changes nothing")
}

// ── Features this branch added ───────────────────────────────────────────────
//
// Everything below is covered by the upstream behavior suite too, but that suite is
// opt-in (magusfile.buzz keeps `conformance` out of `ci`, since it needs a foreign
// checkout and the network). Without these, protocols, match, the boxed-upvalue
// capture, the typed host modules and the raw-string rules would ship with no gate
// that a plain `go test` runs -- which is how parseProtocolDecl reached 0% coverage.

// evalWithStd is evalParity with the bundled stdlib registered, for the features that
// reach a host module (crypto, io, fs). The plain helper deliberately wires nothing.
func evalWithStd(t *testing.T, src string) vm.Value {
	t.Helper()
	ctx := context.Background()
	s := buzz.NewSession(ctx)
	t.Cleanup(func() { _ = s.Close() })
	buzzstd.Register(s)
	require.NoError(t, s.Exec(ctx, src), "Exec")
	probe, ok := s.Globals()["probe"]
	require.True(t, ok, "source must declare a zero-argument probe()")
	v, err := s.CallValue(ctx, probe, nil)
	require.NoError(t, err, "probe()")
	return v
}

func TestParity_ProtocolDeclarationAndConformance(t *testing.T) {
	v := evalParity(t, `
protocol Nameable {
    mut fun rename(name: str) > void;
}

protocol Sized {
    fun size() > int;
}

object<Nameable, Sized> Pet {
    name: str,

    mut fun rename(name: str) > void {
        this.name = name;
    }

    fun size() > int => 4;
}

fun probe() > str {
    final bandit = mut Pet{ name = "bandit" };
    // A protocol-typed binding accepts a declared conformer, and dispatch on it is
    // ordinary dynamic dispatch on the object actually there.
    final named: Nameable = bandit;
    named.rename("Chili");
    final sized: [Sized] = [ bandit ];
    return "{bandit.name}:{sized[0].size()}";
}`)
	assert.Equal(t, "Chili:4", v.AsString(), "a protocol declares signatures; the object supplies them")
}

func TestParity_ProtocolRejectsUndeclaredConformer(t *testing.T) {
	// Conformance is DECLARED, not structural: an object with a matching method set
	// that never named the protocol is not assignable to it.
	s := buzz.NewSession(context.Background())
	t.Cleanup(func() { _ = s.Close() })
	err := s.Exec(context.Background(), `
protocol Nameable {
    fun rename(name: str) > void;
}

object Impostor {
    fun rename(name: str) > void {}
}

fun probe() > bool {
    final x: Nameable = Impostor{};
    return true;
}`)
	require.Error(t, err, "structural match alone must not satisfy a protocol")
}

func TestParity_MatchStatementAndExpressionForms(t *testing.T) {
	v := evalParity(t, `
fun classify(n: int) > str {
    return match (n) {
        1, 2 -> "low",
        3 -> "three",
        else -> "high",
    };
}

fun probe() > str {
    // Statement form with block bodies; sibling arms get their own scopes.
    var tag = "unset";
    match (2) {
        1 -> {
            final local = "one";
            tag = local;
        },
        2 -> {
            final local = "two";
            tag = local;
        },
        else -> {
            tag = "other";
        },
    }
    return "{classify(1)}/{classify(2)}/{classify(3)}/{classify(9)}/{tag}";
}`)
	assert.Equal(t, "low/low/three/high/two", v.AsString(),
		"a multi-condition arm matches any of its conditions; a block body runs its statements")
}

func TestParity_MatchComparisonRules(t *testing.T) {
	// Upstream picks the comparison from the operand kinds rather than always using
	// ==: a range tests containment, a pattern against a string tests a regex match
	// (both directions), and a type value tests `is`.
	cases := []struct{ name, src, want string }{
		{"range contains an int", `match (7) { 0..5 -> "low", 5..10 -> "mid", else -> "high" }`, "mid"},
		{"range contains a double", `match (3.14) { 0..3 -> "low", 3..4 -> "mid", else -> "high" }`, "mid"},
		{"pattern condition against a string", `match ("hello joe") { $"hello [a-z]+" -> "rx", else -> "no" }`, "rx"},
		{"string condition against a pattern", `match ($"hello [a-z]+") { "hello joe" -> "rx", else -> "no" }`, "rx"},
		{"type condition tests is", `match ("s") { <int> -> "i", <str> -> "s", else -> "no" }`, "s"},
		{"type subject tests the condition", `match (<str>) { "hello" -> "inhabits", else -> "no" }`, "inhabits"},
		{"equality is the fallback", `match (true) { true -> "yes", false -> "no" }`, "yes"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := evalParity(t, "fun probe() > str { return "+tc.src+"; }")
			assert.Equal(t, tc.want, v.AsString(), "the rule is chosen by the operand kinds")
		})
	}
}

func TestParity_MatchInfersEnumCaseFromSubject(t *testing.T) {
	v := evalParity(t, `
enum Axis { up, down }

fun probe() > str {
    final a = Axis.down;
    // A bare case as a CONDITION resolves against the subject's type; a bare case as
    // a branch VALUE resolves against the match's expected type.
    final byCase = match (a) { .up -> "u", .down -> "d" };
    final asValue: Axis? = match (1) { 1 -> .up, else -> null };
    return "{byCase}{asValue == Axis.up}";
}`)
	assert.Equal(t, "dtrue", v.AsString(), "both positions resolve without naming the enum")
}

func TestParity_MatchWithoutElseIsRejected(t *testing.T) {
	// This test used to assert the opposite of what it now does. It ran
	// `match (99) { 1 -> "one", 2 -> "two" }` and pinned that falling off the last
	// arm yields null - correct as a description of the VM, but NOT parity, which
	// is what the name claimed. Upstream rejects that source outright
	// (tests/compile_errors/match-non-exhaustive.buzz, "Non-exhaustive `match`,
	// `else` branch required"), so upstream never lets the null be observed. The
	// checker now rejects it too, and the guarantee worth pinning is the rejection.
	//
	// The VM's fall-through-yields-null behaviour is deliberately UNCHANGED: a `.bo`
	// blob compiled before this check exists still runs, and the compiler still
	// emits the trailing push.
	ctx := context.Background()
	s := buzz.NewSession(ctx)
	t.Cleanup(func() { _ = s.Close() })
	err := s.Exec(ctx, `
fun probe() > bool {
    final r = match (99) { 1 -> "one", 2 -> "two" };
    return r == null;
}`)
	require.Error(t, err, "a non-enum, non-bool match with no else must be rejected")
	assert.Contains(t, err.Error(), "non-exhaustive match")
}

func TestParity_MatchExhaustiveWithoutElse(t *testing.T) {
	// The flip side: a subject with a finite case set needs no else. Upstream's
	// match.buzz asserts this for bool in so many words ("boolean match can be
	// exhaustive without else"), and the same reasoning covers a fully-named enum.
	v := evalParity(t, `
enum Side { left, right }

fun probe() > bool {
    final b = match (true) { true -> "y", false -> "n" };
    final e = match (Side.left) { Side.left -> "l", Side.right -> "r" };
    return b == "y" and e == "l";
}`)
	assert.True(t, v.AsBool(), "bool and fully-covered enum matches need no else")
}

func TestParity_MatchEvaluatesSubjectOnce(t *testing.T) {
	// The subject goes into a temp, so a side-effecting subject must not re-run per
	// arm -- otherwise an iterator or counter advances once per condition tested.
	v := evalParity(t, `
var calls = 0;

fun bump() > int {
    calls = calls + 1;
    return 3;
}

fun probe() > int {
    _ = match (bump()) { 1 -> "a", 2 -> "b", 3 -> "c", else -> "d" };
    return calls;
}`)
	assert.Equal(t, int64(1), v.AsInt(), "the subject is evaluated once, not once per arm")
}

func TestParity_ClosureCaptureIsShared(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want int64
	}{
		{"writer visible to reader", `
    var n = 0;
    final set = fun () > void { n = 2; };
    final get = fun () > int => n;
    set();
    return get();`, 2},
		{"captured parameter keeps its argument then mutates", `
    return bumped(5);`, 9},
		{"transitive capture through a frame that never names it", `
    return outer();`, 7},
		{"capture inside a match arm", `
    var hit = 0;
    match (1) { 1 -> { final f = fun () > void { hit = 7; }; f(); }, else -> {} }
    return hit;`, 7},
		{"capture inside a catch body", `
    var seen = 0;
    try {
        throw "x";
    } catch (e: str) {
        final f = fun () > void { seen = 4; };
        f();
    }
    return seen;`, 4},
		{"foreach body captures the loop variable", `
    var last = 0;
    final fns = mut [];
    foreach (i in [1, 2, 3]) {
        _ = fns.append(fun () > void { last = i; });
    }
    fns[0]();
    return last;`, 1},
	}
	const decls = `
fun bumped(start: int) > int {
    final f = fun () > void { start = start + 4; };
    f();
    return start;
}

fun outer() > int {
    var target = 0;
    final middle = fun () > void {
        final inner = fun () > void { target = 7; };
        inner();
    };
    middle();
    return target;
}
`
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := evalParity(t, decls+"\nfun probe() > int {"+tc.src+"\n}")
			assert.Equal(t, tc.want, v.AsInt(), "a captured local is one shared cell")
		})
	}
}

func TestParity_RawStringRules(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"interpolates an expression", "`sum={1 + 2}`", "sum=3"},
		{"escaped brace stays literal", "`\\{literal\\}`", "{literal}"},
		{"mustache survives as literal text", "`{{name}}`", "{{name}}"},
		{"nested raw string inside an interpolation", "`outer {`inner`} end`", "outer inner end"},
		{"newlines are literal", "`a\nb`", "a\nb"},
		{"regex classes pass through", "`[0-9]+`", "[0-9]+"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := evalParity(t, "fun probe() > str { return "+tc.src+"; }")
			assert.Equal(t, tc.want, v.AsString(), "a raw string interpolates but unescapes only \\{ and \\}")
		})
	}
}

func TestParity_ZdefBlockIsNotInterpolated(t *testing.T) {
	// A zdef declaration block is a raw string full of Zig braces. Upstream reads it
	// from the raw token and never interpolates it; the two positional arguments are
	// also exempt from the strict argument-labeling rule.
	s := buzz.NewSession(context.Background())
	t.Cleanup(func() { _ = s.Close() })
	err := s.Exec(context.Background(), `
zdef(
    "tests/utils/libforeign",
    `+"`"+`
        const Data = extern struct {
            msg: [*:0]const u8,
            id: i32,
        };
    `+"`"+`
);

fun probe() > bool => true;`)
	// The library cannot be opened in a test environment; what matters is that the
	// block PARSED (braces intact, no labeling error) rather than how dlopen fared.
	if err != nil {
		assert.NotContains(t, err.Error(), "must be labeled", "zdef args are exempt from labeling")
		assert.NotContains(t, err.Error(), "interpolation", "a zdef block is never interpolated")
	}
}

func TestParity_TypedCollectionLiteralsResolveTheirElements(t *testing.T) {
	v := evalParity(t, `
enum Locale { fr, en, it }

fun probe() > bool {
    // The annotation is a hint, not an element: list[0] is the first VALUE after it.
    final list = [ <Locale>, .it, .fr ];
    final map = { <str: Locale>, "it": .it };
    return list[0] == Locale.it and list[1] == Locale.fr and map["it"] == Locale.it;
}`)
	assert.True(t, v.AsBool(), "an explicit element type resolves a bare enum case inside the literal")
}

func TestParity_AnnotatedDiscardAndAsBinding(t *testing.T) {
	v := evalParity(t, `
fun probe() > str {
    // An annotated discard asserts a shape without binding.
    _: obj{ name: str } = .{ name = "joe" };
    // `+"`"+`as name: Type`+"`"+` binds the cast value only when the cast succeeds.
    final anything: any = "hello";
    var hit = "none";
    if (anything as s: str) {
        hit = s;
    }
    var missed = "none";
    if (anything as n: int) {
        missed = "int";
    }
    return "{hit}/{missed}";
}`)
	assert.Equal(t, "hello/none", v.AsString(), "the branch is taken only when the cast holds")
}

func TestParity_CheckedOptionalCastDoesNotCoerce(t *testing.T) {
	v := evalParity(t, `
fun probe() > bool {
    final n: any = 12;
    // as? is a type TEST, not a conversion: an int is not a str.
    return (n as? int) == 12 and (n as? str) == null;
}`)
	assert.True(t, v.AsBool(), "as? yields null on a type mismatch instead of stringifying")
}

func TestParity_StructuralAnonymousObjectTest(t *testing.T) {
	v := evalParity(t, `
fun probe() > bool {
    final x = .{ f = "a", g = 1 };
    return (x is obj{ f: str })
        and (x is obj{ f: str, g: int })
        and !(x is obj{ f: str, missing: int })
        // A function-typed member contains a bare `+"`"+`>`+"`"+` that must not be read as a
        // bracket closer, or the fields after it are dropped and this answers true.
        and !(x is obj{ f: fun () > str, missing: int });
}`)
	assert.True(t, v.AsBool(), "the structural test checks every named field")
}

func TestParity_TypeValuesAndTypeof(t *testing.T) {
	v := evalParity(t, `
fun probe() > bool {
    // typeof is STATIC: it reports the checker's inferred type, so an annotated
    // empty list and a bare one disagree even though both are empty at run time.
    final bare = [];
    final typed: [str] = [];
    return typeof 3 == <int>
        and typeof "s" == <str>
        and typeof bare == <[any]>
        and typeof typed == <[str]>
        and <int> != <str>;
}`)
	assert.True(t, v.AsBool(), "a type value compares by canonical spelling")
}

func TestParity_SelectiveAndFlatImports(t *testing.T) {
	// A selective import binds exactly the named members, unprefixed; a flat `as _`
	// binds all of them. Both are checked AND executed: the checker must see the
	// signatures or an inferred enum case in an argument cannot resolve.
	v := evalWithStd(t, `
import print, assert from "buzz:std";
import "buzz:crypto" as _;

fun probe() > str {
    assert(true, message: "selective import binds the named member");
    return hash(.Md5, data: "abc").hex();
}`)
	assert.Equal(t, "900150983cd24fb0d6963f7d28e17f72", v.AsString(),
		"the flat import binds hash and HashAlgorithm, and the signature resolves .Md5")
}

func TestParity_HostModuleSignaturesResolveInferredEnumCases(t *testing.T) {
	// The io module's typed declaration is what lets `mode: .write` resolve; a host
	// value alone carries no parameter types.
	v := evalWithStd(t, `
import "buzz:std";
import "buzz:io";
import "buzz:fs";

fun probe() > str {
    final f = io\File.open("./parity_probe.txt", mode: .write);
    f.write("hi");
    f.close();
    final r = io\File.open("./parity_probe.txt", mode: .read);
    final got = r.readAll();
    r.close();
    fs\deleteFile("./parity_probe.txt");
    return "{got}:{fs\exists("./parity_probe.txt")}";
}`)
	assert.Equal(t, "hi:false", v.AsString(), "File.open resolves .write/.read, and deleteFile removes it")
}

func TestParity_FsDeleteDistinguishesFilesFromDirectories(t *testing.T) {
	// deleteFile and deleteDirectory are upstream's two narrow deletes; each refuses
	// the wrong kind of path rather than acting like the broader `delete`.
	v := evalWithStd(t, `
import "buzz:fs";

fun probe() > str {
    fs\makeDirectory("./parity_dir");
    // deleteFile must refuse a directory, so it is still there afterwards. An fs call
    // returns null on success too, so existence is the signal, not the return value.
    _ = fs\deleteFile("./parity_dir") catch null;
    final survived = fs\exists("./parity_dir");
    fs\deleteDirectory("./parity_dir");
    return "{survived}:{fs\exists("./parity_dir")}";
}`)
	assert.Equal(t, "true:false", v.AsString(), "deleteFile refuses a directory; deleteDirectory removes it")
}

func TestParity_MapAndListFunctionalMethods(t *testing.T) {
	v := evalParity(t, `
fun probe() > str {
    // sort reorders the RECEIVER in place and returns it.
    final l = mut [ 3, 1, 2 ];
    _ = l.sort(fun (a: int, b: int) => a < b);
    final m = mut { "b": 2, "a": 1 };
    _ = m.sort(fun (x: str, y: str) => x < y);
    // map builds a new map from {key, value} records.
    final inverted = { "a": 1 }.map(fun (k: str, v: int) => .{ key = "{v}", value = k });
    return "{l[0]}{l[1]}{l[2]}:{m.keys()[0]}:{inverted["1"]}";
}`)
	assert.Equal(t, "123:a:a", v.AsString(), "sort is in place; map rebuilds from records")
}

func TestParity_ImmutableCollectionsRejectSorting(t *testing.T) {
	// This used to run the program and assert that `sort` refused at RUNTIME via
	// `catch null`. The checker now rejects an in-place mutator on a receiver it can
	// see is immutable, so the source no longer compiles - a strictly earlier failure
	// for the same rule, and what upstream does (compile_errors/immutable-list.buzz).
	//
	// The VM's own guard is unchanged and still load-bearing: it catches a receiver
	// whose mutability the checker cannot determine, which is why errImmutable is not
	// dead code.
	ctx := context.Background()
	s := buzz.NewSession(ctx)
	t.Cleanup(func() { _ = s.Close() })
	err := s.Exec(ctx, `
fun probe() > bool {
    final l = [ 2, 1 ];
    _ = l.sort(fun (a: int, b: int) => a < b);
    return true;
}`)
	require.Error(t, err, "sorting an immutable list must be rejected")
	assert.Contains(t, err.Error(), "requires a mutable list")
}
func TestParity_StoredMapKeyWinsOverBuiltinMethod(t *testing.T) {
	// An anonymous object literal is represented as a map, so a field whose name
	// collides with a map builtin must still read as the field.
	v := evalParity(t, `
fun probe() > bool {
    final rec = .{ map = "field", keys = "also a field" };
    return rec.map == "field" and rec.keys == "also a field";
}`)
	assert.True(t, v.AsBool(), "a stored key shadows the same-named builtin")
}

func TestParity_CryptoHashAlgorithms(t *testing.T) {
	// One case per HashAlgorithm the enum declares, so the native switch cannot lose
	// or mis-wire an arm silently. Digests are the published test vectors for "abc".
	cases := []struct{ algo, want string }{
		{"Md5", "900150983cd24fb0d6963f7d28e17f72"},
		{"Sha1", "a9993e364706816aba3e25717850c26c9cd0d89d"},
		{"Sha224", "23097d223405d8228642a477bda255b32aadbce4bda0b3f7e36c9da7"},
		{"Sha256", "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"},
		{"Sha384", "cb00753f45a35e8bb5a03d699ac65007272c32ab0eded1631a8b605a43ff5bed8086072ba1e7cc2358baeca134c825a7"},
		{"Sha512", "ddaf35a193617abacc417349ae20413112e6fa4e89a97ea20a9eeee64b55d39a2192992a274fc1a836ba3c23a3feebbd454d4423643ce80e2a9ac94fa54ca49f"},
		{"Sha3256", "3a985da74fe225b2045c172d6bd390bd855f086e3e9d525b46bfe24511431532"},
		{"Sha3512", "b751850b1a57168a5693cd924b6b096e08f621827444f70d884f5d0240d2712e10e116e9192af3c91a7ec57647e3934057340b4cf408d5a56592f8274eec53f0"},
	}
	for _, tc := range cases {
		t.Run(tc.algo, func(t *testing.T) {
			v := evalWithStd(t, `
import "buzz:crypto";

fun probe() > str {
    return crypto\hash(crypto\HashAlgorithm.`+tc.algo+`, data: "abc").hex();
}`)
			assert.Equal(t, tc.want, v.AsString(), "the digest matches the published vector")
		})
	}
}

// ── Checker false positives, each measured against upstream ──────────────────
//
// Every case below is a program `~/Repos/buzz/zig-out/bin/buzz` compiles clean and
// gopherbuzz rejected. They are grouped because they share a failure MODE that the
// allowlists cannot catch: a strictness check that over-claims rejects correct
// source, and neither upstream suite contains the shape, so both stayed green while
// magus's own corpus failed to load.

// TestParity_WriteThroughANameJustifiesItsVar covers the var-not-assigned check's
// blind spot. It fired only for an *ast.IdentExpr target, so `digests[p] = h` and
// `point.x = 2` both read as "never assigned" and the declaration was rejected -
// which is what stopped tools/drift.buzz from loading. Upstream accepts both; where
// it comments at all (W102 on an index-assigned `var`) it warns rather than errors.
func TestParity_WriteThroughANameJustifiesItsVar(t *testing.T) {
	cases := []struct{ name, body string }{
		{"index assignment", `
    var digests: mut {str: str} = mut {<str: str>};
    digests["k"] = "v";
    return digests["k"] ?? "";`},
		{"field assignment", `
    var p = mut Point{ x = 1 };
    p.x = 2;
    return "{p.x}";`},
		{"nested target marks the root", `
    var box = mut Box{ inner = mut Point{ x = 1 } };
    box.inner.x = 9;
    return "{box.inner.x}";`},
	}
	const decls = `
object Point { x: int }
object Box { inner: mut Point }
`
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := evalParity(t, decls+"\nfun probe() > str {"+tc.body+"\n}")
			assert.NotEmpty(t, v.AsString(), "the program compiles and runs")
		})
	}
}

// TestParity_BreakOutOfADoUntilLeavesLiveCode pins the DoStmt terminal-flow guard.
// `do { ... } until (cond)` runs its body once, so a body that transfers control
// away does end the loop - but a break that exits the DO lands on the statement
// after it, exactly as for while and for. Without the guard everything following a
// `do { break; } until (...)` was reported unreachable.
func TestParity_BreakOutOfADoUntilLeavesLiveCode(t *testing.T) {
	v := evalParity(t, `
fun probe() > int {
    var i = 0;
    do {
        i = i + 1;
        break;
    } until (i > 10)
    // Dead by the old analysis; upstream compiles this clean and reaches it.
    i = i + 100;
    return i;
}`)
	assert.Equal(t, int64(101), v.AsInt(), "the break exits the do and the next statement runs")
}

// TestParity_ClosureMayReuseAnEnclosingFunctionsLocalName pins the shadowing rule's
// scope. It walked every enclosing local scope, so a nested closure could not reuse
// an outer name; upstream scopes the rule to the CURRENT function's locals, so a new
// frame starts the name space over.
func TestParity_ClosureMayReuseAnEnclosingFunctionsLocalName(t *testing.T) {
	v := evalParity(t, `
fun probe() > int {
    final name = 1;
    final f = fun () > int {
        // A different frame, so this is a fresh name rather than a shadow.
        final name = 2;
        return name;
    };
    return name + f();
}`)
	assert.Equal(t, int64(3), v.AsInt(), "a closure's local is its own, and the outer one is untouched")
}

// TestParity_ShadowingWithinOneFunctionIsStillRejected is the other side of that
// boundary: narrowing the walk must not switch the check off. A nested BLOCK in the
// same function is still the same frame.
func TestParity_ShadowingWithinOneFunctionIsStillRejected(t *testing.T) {
	s := buzz.NewSession(context.Background())
	t.Cleanup(func() { _ = s.Close() })
	err := s.Exec(context.Background(), `
fun probe() > int {
    final name = 1;
    if (true) {
        final name = 2;
        return name;
    }
    return name;
}`)
	require.Error(t, err, "a block in the same function still shadows")
	assert.Contains(t, err.Error(), "already exists in an enclosing scope")
}

// TestParity_MutatorThroughAnImmutableAnnotationIsRejected pins the rule that forced
// magus's own 126-site migration, because the corpus was what was wrong. Upstream
// rejects this source in the same words ("Method `append` requires mutable list"):
// the ANNOTATION narrows the type, so appending through it is a type error however
// the value was built. Recorded as a test so nobody relaxes the rule to spare a
// corpus again.
func TestParity_MutatorThroughAnImmutableAnnotationIsRejected(t *testing.T) {
	s := buzz.NewSession(context.Background())
	t.Cleanup(func() { _ = s.Close() })
	err := s.Exec(context.Background(), `
fun probe() > int {
    final files: [str] = mut [<str>];
    files.append("x");
    return files.len();
}`)
	require.Error(t, err, "a mut value does not widen an immutable annotation")
	assert.Contains(t, err.Error(), "requires a mutable list")
}

// TestParity_MutAnnotationAcceptsTheMutator is the migration's target shape, and the
// reason the migration is safe: `mut [str]` takes the mut value AND permits the
// mutator, and stays assignable where a plain `[str]` is wanted.
func TestParity_MutAnnotationAcceptsTheMutator(t *testing.T) {
	v := evalParity(t, `
fun consume(xs: [str]) > int => xs.len();

fun probe() > int {
    final files: mut [str] = mut [<str>];
    files.append("x");
    // mut T is assignable to T, never the reverse, so widening the declaration
    // cannot break a call that wanted the immutable one.
    return consume(files);
}`)
	assert.Equal(t, int64(1), v.AsInt(), "the mut annotation permits the write and still satisfies [str]")
}

// TestParity_CollectorKeepsReachableObjects covers the reachability sweep in
// vm/gc_collect.go, which had NO test at all - which is how both holes below
// survived. The dangerous direction of this check is a false COLLECT: calling a
// live object's collect() is unrecoverable, where missing a dead one merely
// delays it.
func TestParity_CollectorKeepsReachableObjects(t *testing.T) {
	cases := []struct{ name, body string }{
		{
			// The list literal is reachable ONLY through the iterator state: it is
			// bound to no local and sits on no frame's env. markReachable had no
			// tagIterState case, so collecting mid-loop reclaimed the elements the
			// loop had not reached yet.
			name: "the collection under a foreach",
			body: `
    foreach (t in [ Tracked{ id = 1 }, Tracked{ id = 2 }, Tracked{ id = 3 } ]) {
        gc\collect();
        _ = t.id;
    }
    return collected;`,
		},
		{
			// markReachable walked mo.Vals but not mo.keyVals, so an object used as
			// a KEY was invisible to the mark while the map still keyed on it.
			name: "an object used as a map key",
			body: `
    final m = mut {};
    m[Tracked{ id = 1 }] = "v";
    gc\collect();
    return collected;`,
		},
	}
	const decls = `
import "gc";

var collected = 0;

object Tracked {
    id: int,

    fun collect() > void {
        collected = collected + 1;
    }
}
`
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := evalWithStd(t, decls+"\nfun probe() > int {"+tc.body+"\n}")
			assert.Equal(t, int64(0), v.AsInt(), "a reachable object must not be collected")
		})
	}
}

func TestParity_CryptoHashReturnsRawBytes(t *testing.T) {
	// Upstream returns the raw digest and leaves rendering to `.hex()`. Returning hex
	// directly made upstream's own `hash(...).hex()` double-encode.
	v := evalWithStd(t, `
import "buzz:crypto";

fun probe() > bool {
    final raw = crypto\hash(crypto\HashAlgorithm.Md5, data: "abc");
    // The digest is NOT already hex (the regression this guards), and renders to 32
    // characters. Deliberately not asserting a raw BYTE count: str.len() counts runes,
    // so a binary digest measures shorter than its 16 bytes.
    return raw != raw.hex() and raw.hex().len() == 32;
}`)
	assert.True(t, v.AsBool(), "hash returns the digest itself; .hex() renders it as 32 characters")
}
