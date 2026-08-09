package buzz

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	vmpackage "github.com/egladman/magus/libs/gopherbuzz/vm"
)

// The tables in this file pin OBSERVABLE LANGUAGE SEMANTICS - the operator,
// method, and indexing behavior implemented by vm/operators.go - through whole
// programs, so each case exercises parse, compile, dispatch, and the method body
// together. Results are asserted as the value's full canonical rendering
// (Value.String()), which compares the entire structure - list contents, map
// ordering, float formatting - in one assertion instead of field-by-field.
// The rendering is display-style: string elements appear unquoted, so [a, b]
// is a two-string list.
//
// Error cases assert on a substring of the runtime error because the messages
// carry position/context prefixes that are not the behavior under test.

// evalString runs src and returns the result's canonical rendering.
func evalString(t *testing.T, src string) string {
	t.Helper()
	return runProg(t, src, CompileOptions{}).String()
}

// evalErr runs src expecting a runtime error, and returns it.
func evalErr(t *testing.T, src string) error {
	t.Helper()
	prog, err := ParseEmbedded(src)
	require.NoError(t, err, "parse")
	chunk, err := CompileWith(prog, CompileOptions{})
	require.NoError(t, err, "compile")
	env := vmpackage.NewEnv()
	vmpackage.RegisterStdlib(env)
	_, err = vmpackage.NewVM(context.Background()).Run(chunk, env)
	require.Error(t, err, "expected a runtime error from:\n%s", src)
	return err
}

func TestStringMethodSemantics(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"len counts runes not bytes", `return "héllo".len();`, "5"},
		{"upper", `return "hé11o".upper();`, "HÉ11O"},
		{"lower", `return "HÉLLO".lower();`, "héllo"},
		{"trim strips unicode space", "return \"\\t hi \\n\".trim();", "hi"},
		{"byte returns rune at index", `return "abc".byte(1);`, "98"},
		{"byte defaults to index 0", `return "abc".byte();`, "97"},
		{"indexOf found returns rune index", `return "héllo".indexOf("llo");`, "2"},
		{"indexOf missing returns null", `return "abc".indexOf("zzz");`, "null"},
		{"startsWith true", `return "hello".startsWith("he");`, "true"},
		{"startsWith false", `return "hello".startsWith("lo");`, "false"},
		{"endsWith true", `return "hello".endsWith("lo");`, "true"},
		{"endsWith false", `return "hello".endsWith("he");`, "false"},
		// replace substitutes EVERY occurrence. These two lines asserted the opposite
		// until 2026-08-09, justified as "matching upstream Buzz" - which was simply
		// untrue: upstream's src/builtin/str.zig calls Zig's std.mem.replaceOwned, and
		// that walks the whole string. The wrong claim is why the bug outlived review,
		// so it is worth stating where the answer comes from rather than asserting it.
		{"replace replaces every occurrence", `return "a-a-a".replace("a", "b");`, "b-b-b"},
		// An empty needle has NO upstream answer to match: std.mem.replace advances its
		// cursor by needle.len, so a zero-length needle never advances and upstream spins
		// rather than returning. gopherbuzz takes Go's (and JavaScript's) reading -
		// insert at every boundary - because it terminates and is the answer a reader
		// coming from another language already expects.
		{"replace with empty needle inserts at every boundary", `return "ab".replace("", "x");`, "xaxbx"},
		{"split on separator", `return "a,b,,c".split(",");`, "[a, b, , c]"},
		{"split with no separator splits on whitespace runs", "return \" a  b\\tc \".split();", "[a, b, c]"},
		{"split separator absent yields whole string", `return "abc".split(",");`, "[abc]"},
		{"sub start only", `return "hello".sub(1);`, "ello"},
		{"sub start and len", `return "hello".sub(1, 3);`, "ell"},
		{"sub negative start clamps to 0", `return "hello".sub(0 - 2, 2);`, "he"},
		{"sub start past end is empty", `return "hi".sub(10);`, ""},
		{"sub len past end clamps", `return "hi".sub(1, 99);`, "i"},
		{"sub negative len is empty", `return "hello".sub(1, 0 - 1);`, ""},
		{"sub on non-ascii uses rune indexes", `return "héllo".sub(1, 2);`, "él"},
		{"repeat", `return "ab".repeat(3);`, "ababab"},
		{"repeat zero is empty", `return "ab".repeat(0);`, ""},
		{"repeat negative clamps to empty", `return "ab".repeat(0 - 1);`, ""},
		{"encodeBase64", `return "hi".encodeBase64();`, "aGk="},
		{"decodeBase64 round trip", `return "hi".encodeBase64().decodeBase64();`, "hi"},
		{"hex", `return "hi".hex();`, "6869"},
		{"bin round trip", `return "hi".hex().bin();`, "hi"},
		{"utf8Len", `return "héllo".utf8Len();`, "5"},
		{"utf8Valid true", `return "héllo".utf8Valid();`, "true"},
		{"chained methods", `return " Hello ".trim().lower().sub(0, 4);`, "hell"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			require.Equal(t, c.want, evalString(t, c.src))
		})
	}

	errCases := []struct {
		name    string
		src     string
		wantSub string
	}{
		{"byte out of range", `return "ab".byte(5);`, "out of range"},
		{"byte negative", `return "ab".byte(0 - 1);`, "out of range"},
		{"indexOf without needle", `return "ab".indexOf();`, "requires a str needle"},
		{"replace without args", `return "ab".replace();`, "requires (str needle, str with)"},
		{"decodeBase64 invalid input", `return "!!!".decodeBase64();`, "decodeBase64"},
		{"bin invalid hex", `return "zz".bin();`, "bin"},
	}
	for _, c := range errCases {
		t.Run(c.name, func(t *testing.T) {
			require.ErrorContains(t, evalErr(t, c.src), c.wantSub)
		})
	}
}

func TestListMethodSemantics(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"len", `return [1, 2, 3].len();`, "3"},
		{"len empty", `return [].len();`, "0"},
		{"append returns the value and mutates", `var l = mut [1]; l.append(2); return l;`, "[1, 2]"},
		{"insert at index", `var l = mut [1, 3]; l.insert(1, 2); return l;`, "[1, 2, 3]"},
		{"remove at index returns removed", `var l = mut [1, 2, 3]; final r = l.remove(1); return [r, l.len()];`, "[2, 2]"},
		{"pop removes and returns last", `var l = mut [1, 2, 3]; final p = l.pop(); return [p, l.len()];`, "[3, 2]"},
		{"pop on empty returns null", `var l = mut []; return l.pop();`, "null"},
		{"sub start only", `return [1, 2, 3, 4].sub(2);`, "[3, 4]"},
		{"sub start and len", `return [1, 2, 3, 4].sub(1, 2);`, "[2, 3]"},
		{"indexOf found", `return [10, 20, 30].indexOf(20);`, "1"},
		{"indexOf missing returns null", `return [10].indexOf(99);`, "null"},
		{"join", `return ["a", "b"].join("-");`, "a-b"},
		{"map callback receives index and element", `return [1, 2, 3].map(fun (i: int, x: int) > int { return x * 2; });`, "[2, 4, 6]"},
		{"map index argument is the position", `return [9, 9, 9].map(fun (i: int, x: int) > int { return i; });`, "[0, 1, 2]"},
		{"filter keeps matching elements", `return [1, 2, 3, 4].filter(fun (i: int, x: int) > bool { return x % 2 == 0; });`, "[2, 4]"},
		{"reduce folds with initial, acc last", `return [1, 2, 3].reduce(fun (i: int, x: int, acc: int) > int { return acc + x; }, 10);`, "16"},
		{"sort with a less-than comparator, in place", `var l = mut [3, 1, 2]; l.sort(fun (a: int, b: int) > bool { return a < b; }); return l;`, "[1, 2, 3]"},
		{"sort descending", `var l = mut [1, 3, 2]; l.sort(fun (a: int, b: int) > bool { return a > b; }); return l;`, "[3, 2, 1]"},
		{"reverse", `return [1, 2, 3].reverse();`, "[3, 2, 1]"},
		{"fill", `var l = mut [1, 2, 3]; l.fill(0); return l;`, "[0, 0, 0]"},
		{"cloneMutable is a distinct mutable copy", `final a = mut [1]; var b = a.cloneMutable(); b.append(2); return [a.len(), b.len()];`, "[1, 2]"},
		{"concat with + builds a fresh list", `final a = [1]; final b = [2]; return a + b;`, "[1, 2]"},
		{"forEach visits elements in order", `var s = mut [0]; [1, 2, 3].forEach(fun (i: int, x: int) > void { s[0] = s[0] * 10 + x; }); return s[0];`, "123"},
		{"nested list renders fully", `return [[1, 2], [3]];`, "[[1, 2], [3]]"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			require.Equal(t, c.want, evalString(t, c.src))
		})
	}

	errCases := []struct {
		name    string
		src     string
		wantSub string
	}{
		{"append on immutable list", `final l = [1]; l.append(2); return l;`, "immutable"},
		{"fill on immutable list", `final l = [1]; l.fill(0); return l;`, "immutable"},
		{"map without callback", `return [1].map();`, "requires a callback"},
		{"clone yields an immutable copy", `final a = mut [1]; var b = a.clone(); b.append(2); return b;`, "immutable"},
		{"reduce without initial", `return [1].reduce(fun (a: int, x: int) > int { return a; });`, "requires (callback, initial)"},
	}
	for _, c := range errCases {
		t.Run(c.name, func(t *testing.T) {
			require.ErrorContains(t, evalErr(t, c.src), c.wantSub)
		})
	}
}

func TestMapMethodSemantics(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"len", `return {"a": 1, "b": 2}.len();`, "2"},
		{"size aliases len", `return {"a": 1}.size();`, "1"},
		{"keys preserve insertion order", `return {"b": 1, "a": 2}.keys();`, "[b, a]"},
		{"values preserve insertion order", `return {"b": 1, "a": 2}.values();`, "[1, 2]"},
		{"hasKey present", `return {"a": 1}.hasKey("a");`, "true"},
		{"hasKey absent", `return {"a": 1}.hasKey("z");`, "false"},
		{"remove deletes the key", `var m = mut {"a": 1, "b": 2}; m.remove("a"); return m.keys();`, "[b]"},
		{"cloneMutable is a distinct mutable copy", `final a = mut {"k": 1}; var b = a.cloneMutable(); b["x"] = 2; return [a.len(), b.len()];`, "[1, 2]"},
		{"merge with + right wins on duplicates", `return ({"a": 1, "b": 1} + {"b": 2})["b"];`, "2"},
		{"merge with + leaves operands untouched", `final a = {"a": 1}; final unused = a + {"b": 2}; return a.len();`, "1"},
		{"filter", `return {"a": 1, "b": 2}.filter(fun (k: str, v: int) > bool { return v > 1; }).keys();`, "[b]"},
		{"reduce", `return {"a": 1, "b": 2}.reduce(fun (k: str, v: int, acc: int) > int { return acc + v; }, 0);`, "3"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			require.Equal(t, c.want, evalString(t, c.src))
		})
	}

	errCases := []struct {
		name    string
		src     string
		wantSub string
	}{
		{"hasKey without argument", `return {"a": 1}.hasKey();`, "requires 1 argument"},
		{"diff without a map", `return {"a": 1}.diff(3);`, "requires a map"},
		{"intersect without a map", `return {"a": 1}.intersect(3);`, "requires a map"},
	}
	for _, c := range errCases {
		t.Run(c.name, func(t *testing.T) {
			require.ErrorContains(t, evalErr(t, c.src), c.wantSub)
		})
	}
}

// TestRangeSemantics pins the range contract documented on rngMethod: a range
// runs from Lo TOWARD Hi and stops before it, in either direction, and
// low()/high() report the operands as written, not min/max.
func TestRangeSemantics(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"low reports the left operand", `return (3..7).low();`, "3"},
		{"high reports the right operand", `return (3..7).high();`, "7"},
		{"descending low stays as written", `return (7..3).low();`, "7"},
		{"len ascending excludes high", `return (0..4).len();`, "4"},
		{"len descending", `return (10..0).len();`, "10"},
		{"len empty range", `return (3..3).len();`, "0"},
		{"toList ascending", `return (1..4).toList();`, "[1, 2, 3]"},
		{"toList descending stops before high", `return (3..0).toList();`, "[3, 2, 1]"},
		{"foreach iterates the range", `var s = 0; foreach (i in 1..5) { s = s + i; } return s;`, "10"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			require.Equal(t, c.want, evalString(t, c.src))
		})
	}
}

// TestArithmeticSemantics pins arith()/intArith()/floatArith(): the type matrix
// (int op int stays int, any float promotes), the mixed-type error, and the
// division/modulo error cases. These are the interpreter-side semantics the JIT
// backends are differentially tested against, so a drift here invalidates that
// suite's baseline.
func TestArithmeticSemantics(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"int + int stays int", `return 2 + 3;`, "5"},
		{"int / int truncates toward zero", `return 7 / 2;`, "3"},
		{"negative int / truncates toward zero", `return (0 - 7) / 2;`, "-3"},
		{"int % takes dividend sign", `return (0 - 7) % 3;`, "-1"},
		{"int % positive", `return 7 % 3;`, "1"},
		{"float + float", `return 1.5 + 2.25;`, "3.75"},
		{"int + float promotes", `return 1 + 0.5;`, "1.5"},
		{"float * int promotes", `return 0.5 * 4;`, "2"},
		{"float / keeps fraction", `return 7.0 / 2.0;`, "3.5"},
		{"str + str concatenates", `return "ab" + "cd";`, "abcd"},
		{"str + int stringifies right", `return "n=" + 3;`, "n=3"},
		{"str + float stringifies right", `return "x=" + 1.5;`, "x=1.5"},
		{"str + bool stringifies right", `return "b=" + true;`, "b=true"},
		{"bitwise and", `return 12 & 10;`, "8"},
		{"bitwise or", `return 12 | 10;`, "14"},
		{"bitwise xor", `return 12 ^ 10;`, "6"},
		{"shift left", `return 1 << 4;`, "16"},
		{"shift right", `return 256 >> 4;`, "16"},
		{"unary minus", `final x = 5; return 0 - x;`, "-5"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			require.Equal(t, c.want, evalString(t, c.src))
		})
	}

	errCases := []struct {
		name    string
		src     string
		wantSub string
	}{
		{"int division by zero", `final d = 0; return 1 / d;`, "division by zero"},
		{"int modulo by zero", `final d = 0; return 1 % d;`, "zero"},
		{"float division by zero", `final d = 0.0; return 1.0 / d;`, "division by zero"},
		{"arith on null", `final n = null; return 1 + n;`, "cannot apply"},
		{"arith on bool", `return 1 + true;`, "cannot apply"},
		{"arith on list and int", `return [1] + 1;`, "cannot apply"},
	}
	for _, c := range errCases {
		t.Run(c.name, func(t *testing.T) {
			require.ErrorContains(t, evalErr(t, c.src), c.wantSub)
		})
	}
}

// TestComparisonSemantics pins compare()/cmpResult() across the type matrix,
// including every sense at its equality boundary (where >= vs > and <= vs <
// actually differ).
func TestComparisonSemantics(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"int equality boundary lt", `return 3 < 3;`, "false"},
		{"int equality boundary le", `return 3 <= 3;`, "true"},
		{"int equality boundary gt", `return 3 > 3;`, "false"},
		{"int equality boundary ge", `return 3 >= 3;`, "true"},
		{"mixed int float compare", `return 1 < 1.5;`, "true"},
		{"mixed equality", `return 2 == 2.0;`, "true"},
		{"string ordering", `return "abc" < "abd";`, "true"},
		{"string equality", `return "ab" == "ab";`, "true"},
		{"string inequality", `return "ab" != "ba";`, "true"},
		{"bool equality", `return true == true;`, "true"},
		{"null equals null", `return null == null;`, "true"},
		{"null not equal to zero", `return null == 0;`, "false"},
		{"list equality is identity, not structure", `return [1, [2]] == [1, [2]];`, "false"},
		{"a list equals itself", `final a = [1, [2]]; return a == a;`, "true"},
		{"map equality is identity, not structure", `return {"a": 1} == {"a": 1};`, "false"},
		{"a map equals itself", `final m = {"a": 1}; return m == m;`, "true"},
		{"cross-type equality is false not an error", `return 1 == "1";`, "false"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			require.Equal(t, c.want, evalString(t, c.src))
		})
	}
}

// TestIndexingSemantics pins indexGet/setIndex: list, map, and string indexing,
// bounds behavior, and the immutability rules.
func TestIndexingSemantics(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"list index get", `return [10, 20][1];`, "20"},
		{"list index set", `var l = mut [1, 2]; l[1] = 9; return l;`, "[1, 9]"},
		{"map index get", `return {"a": 5}["a"];`, "5"},
		{"map index missing is null", `return {"a": 5}["z"];`, "null"},
		{"map index set inserts", `var m = mut {"a": 1}; m["b"] = 2; return m.keys();`, "[a, b]"},
		{"map index set overwrites", `var m = mut {"a": 1}; m["a"] = 9; return m["a"];`, "9"},
		{"string index yields one-rune string", `return "héllo"[1];`, "é"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			require.Equal(t, c.want, evalString(t, c.src))
		})
	}

	errCases := []struct {
		name    string
		src     string
		wantSub string
	}{
		{"list index out of range", `return [1][5];`, "out of"},
		{"list negative index", `return [1][0 - 1];`, "out of"},
		{"set on immutable list", `final l = [1]; l[0] = 2; return l;`, "immutable"},
		{"set on immutable map", `final m = {"a": 1}; m["a"] = 2; return m;`, "immutable"},
		{"index a non-indexable", `final n = 5; return n[0];`, "index"},
	}
	for _, c := range errCases {
		t.Run(c.name, func(t *testing.T) {
			require.ErrorContains(t, evalErr(t, c.src), c.wantSub)
		})
	}
}
