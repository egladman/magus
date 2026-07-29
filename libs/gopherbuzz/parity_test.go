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
		{name: "no out yields null", body: `return from { final unused = 1; };`, want: nil},
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
