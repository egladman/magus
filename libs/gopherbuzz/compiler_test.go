package buzz

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	vmpackage "github.com/egladman/magus/libs/gopherbuzz/vm"
)

// BenchmarkCompileImportedTypes compiles a module of many small functions in a
// session that has flat-imported a large types bundle, as a magusfile does.
func BenchmarkCompileImportedTypes(b *testing.B) {
	types, err := ParseEmbedded(hostShapedDecls("T", 150, 0))
	require.NoError(b, err)
	var src strings.Builder
	for i := range 60 {
		fmt.Fprintf(&src, "fun f%d(x: int) > int { final g = fun (y: int) > int => y + x; return g(%d); }\n", i, i)
	}
	prog, err := ParseEmbedded(src.String())
	require.NoError(b, err)
	opts := CompileOptions{SharedGlobals: true, DebugLines: true, ImportedTypes: types.Stmts}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := CompileWith(prog, opts); err != nil {
			b.Fatal(err)
		}
	}
}

func TestCompileWith_SimpleFunction(t *testing.T) {
	prog, err := ParseEmbedded(`fun add(a: int, b: int) > int { return a + b; }`)
	require.NoError(t, err)
	chunk, err := CompileWith(prog, CompileOptions{})
	require.NoError(t, err)
	require.NotNil(t, chunk, "CompileWith returned nil chunk")
	assert.NotEmpty(t, chunk.Code, "compiled chunk has no instructions")
}

// TestCompileWith_PrivateTypesDoNotCollideWithImporter pins upstream Buzz at
// 294d8f9, which prints "fig:inner:1:made:0 main:outer:3:2" for this pair: the
// module's private Node and Color stay its own after the importer declares types
// of the same names. Resolved to the importer's Node, the module's literal would
// leave anchor null.
func TestCompileWith_PrivateTypesDoNotCollideWithImporter(t *testing.T) {
	ctx := context.Background()
	s := NewSession(ctx, WithEmbedded())
	s.SetModuleDecls("fig", `
namespace fig;
enum Color { teal, plum }
object Node {
    anchor: str,
    color: Color = Color.plum,
    static fun make() > Node { return Node{ anchor = "made" }; }
}
fun describe() > str {
    final n = Node{ anchor = "inner" };
    final m = Node.make();
    return "fig:{n.anchor}:{n.color.value}:{m.anchor}:{Color.teal.value}";
}
export describe;
`)
	v, err := s.Eval(ctx, `
import "fig";
enum Color { red, green, blue }
object Node { label: str, weight: int }
final mine = Node{ label = "outer", weight = 3 };
return "{fig\describe()} main:{mine.label}:{mine.weight}:{Color.blue.value}";
`)
	require.NoError(t, err)
	assert.Equal(t, "fig:inner:1:made:0 main:outer:3:2", v.String())
}

// TestCompileWith_TypeTestsTellSameNamedTypesApart pins upstream Buzz at 294d8f9,
// which prints "main-is-theirs:false main-as-theirs:false main-is-mine:true
// mod-is:false mod-as:false mod-is:true mod-as:true" for the object half,
// "enum-is-theirs:false enum-is-mine:true" for the enum half, and "caught:as-any"
// for the typed catch: `is`, `as?` and a catch clause match the type in scope,
// never another module's type of the same name.
func TestCompileWith_TypeTestsTellSameNamedTypesApart(t *testing.T) {
	ctx := context.Background()
	s := NewSession(ctx, WithEmbedded())
	s.SetModuleDecls("idmod", `
namespace idmod;
object Node { anchor: str = "inner" }
enum Color { teal }
object Oops { why: str = "theirs" }
fun make() > any { return Node{}; }
fun makeColor() > any { return Color.teal; }
fun fail() > void !> Oops { throw Oops{}; }
fun check(v: any) > str {
    final asNode = v as? Node;
    return "mod-is:{v is Node} mod-as:{asNode != null}";
}
export make;
export makeColor;
export fail;
export check;
`)
	v, err := s.Eval(ctx, `
import "idmod";
object Node { label: str = "outer" }
enum Color { red }
object Oops { why: str = "mine" }
final theirs = idmod\make();
final mine: any = Node{};
final asNode = theirs as? Node;
final theirColor = idmod\makeColor();
final myColor: any = Color.red;
var caught = "none";
try {
    idmod\fail();
} catch (e: Oops) {
    caught = "as-mine";
} catch (e: any) {
    caught = "as-any";
}
return "main-is-theirs:{theirs is Node} main-as-theirs:{asNode != null} main-is-mine:{mine is Node} {idmod\check(mine)} {idmod\check(theirs)}"
    + " enum-is-theirs:{theirColor is Color} enum-is-mine:{myColor is Color} caught:{caught}";
`)
	require.NoError(t, err)
	assert.Equal(t, "main-is-theirs:false main-as-theirs:false main-is-mine:true mod-is:false mod-as:false mod-is:true mod-as:true"+
		" enum-is-theirs:false enum-is-mine:true caught:as-any", v.String())
}

// TestCompileWith_QualifiedLiteralBuildsTheExport pins upstream Buzz at 294d8f9,
// which prints "qualified:cmod:9 bare:amod": inside a module with a private Node,
// `cmod\Node{...}` builds and type-checks as the Node cmod exports.
func TestCompileWith_QualifiedLiteralBuildsTheExport(t *testing.T) {
	ctx := context.Background()
	s := NewSession(ctx, WithEmbedded())
	s.SetModuleDecls("cmod", `
namespace cmod;
export object Node { who: str = "cmod", extra: int = 7 }
`)
	s.SetModuleDecls("amod", `
namespace amod;
import "cmod";
object Node { who: str = "amod" }
fun both() > str {
    final b = cmod\Node{ extra = 9 };
    final a = Node{};
    return "qualified:{b.who}:{b.extra} bare:{a.who}";
}
export both;
`)
	v, err := s.Eval(ctx, `import "amod"; return amod\both();`)
	require.NoError(t, err)
	assert.Equal(t, "qualified:cmod:9 bare:amod", v.String())
}

// TestCompileWith_MatchTypeArmTellsSameNamedTypesApart pins upstream Buzz at
// 294d8f9, which prints "main-other main-node mod-node mod-other": a `<Node>` arm
// selects only the Node in scope where the match is written.
func TestCompileWith_MatchTypeArmTellsSameNamedTypesApart(t *testing.T) {
	ctx := context.Background()
	s := NewSession(ctx, WithEmbedded())
	s.SetModuleDecls("mmod", `
namespace mmod;
object Node { anchor: str = "inner" }
fun make() > any { return Node{}; }
fun check(v: any) > str {
    return match (v) { <Node> -> "mod-node", else -> "mod-other" };
}
export make;
export check;
`)
	v, err := s.Eval(ctx, `
import "mmod";
object Node { label: str = "outer" }
fun which(v: any) > str {
    return match (v) { <Node> -> "main-node", else -> "main-other" };
}
final theirs = mmod\make();
final mine: any = Node{};
return "{which(theirs)} {which(mine)} {mmod\check(theirs)} {mmod\check(mine)}";
`)
	require.NoError(t, err)
	assert.Equal(t, "main-other main-node mod-node mod-other", v.String())
}

// TestCompileWith_ImporterTypeKeepsOffAnExport pins upstream Buzz at 294d8f9,
// which prints "q:xmod made:xmod mine:outer main-is-theirs:true main-is-mine:true
// xmod-is:false xmod-is:true": the importer's own Node and the Node xmod exports
// stay two types, so neither replaces the other in xmod's code or the importer's.
func TestCompileWith_ImporterTypeKeepsOffAnExport(t *testing.T) {
	ctx := context.Background()
	s := NewSession(ctx, WithEmbedded())
	s.SetModuleDecls("xmod", `
namespace xmod;
export object Node { who: str = "xmod" }
export fun make() > Node { return Node{}; }
export fun describe(v: any) > str { return "xmod-is:{v is Node}"; }
`)
	v, err := s.Eval(ctx, `
import "xmod";
object Node { label: str = "outer" }
final q = xmod\Node{};
final made = xmod\make();
final mine = Node{};
final mineAny: any = mine;
return "q:{q.who} made:{made.who} mine:{mine.label} main-is-theirs:{made is xmod\Node} main-is-mine:{mineAny is Node} {xmod\describe(mine)} {xmod\describe(made)}";
`)
	require.NoError(t, err)
	assert.Equal(t, "q:xmod made:xmod mine:outer main-is-theirs:true main-is-mine:true xmod-is:false xmod-is:true", v.String())
}

// TestCompileWith_QualifiedLiteralDefaultResolvesInTheExporter pins upstream Buzz
// at 294d8f9, which prints "default:1 local:2": dmod's `Shade.light` default
// names dmod's private Shade even when the importer declares a Shade of its own.
func TestCompileWith_QualifiedLiteralDefaultResolvesInTheExporter(t *testing.T) {
	ctx := context.Background()
	s := NewSession(ctx, WithEmbedded())
	s.SetModuleDecls("dmod", `
namespace dmod;
enum Shade { dark, light }
export object Node { shade: Shade = Shade.light }
`)
	v, err := s.Eval(ctx, `
import "dmod";
enum Shade { red, green, blue }
final n = dmod\Node{};
return "default:{n.shade.value} local:{Shade.blue.value}";
`)
	require.NoError(t, err)
	assert.Equal(t, "default:1 local:2", v.String())
}

// TestCompileWith_ImporterFunAndVarKeepOffExports pins upstream Buzz at 294d8f9,
// which prints "inside:fmod-tag:fmod-label qualified:fmod-tag:fmod-label
// mine:main-tag:main-label": a script's own `tag` and `label` leave the ones fmod
// exports, and fmod's code calls, untouched. The host reads the script's own.
func TestCompileWith_ImporterFunAndVarKeepOffExports(t *testing.T) {
	ctx := context.Background()
	s := NewSession(ctx, WithEmbedded())
	s.SetModuleDecls("fmod", `
namespace fmod;
export final label = "fmod-label";
export fun tag() > str { return "fmod-tag"; }
export fun describe() > str { return "{tag()}:{label}"; }
`)
	v, err := s.Eval(ctx, `
import "fmod";
final label = "main-label";
fun tag() > str { return "main-tag"; }
return "inside:{fmod\describe()} qualified:{fmod\tag()}:{fmod\label} mine:{tag()}:{label}";
`)
	require.NoError(t, err)
	assert.Equal(t, "inside:fmod-tag:fmod-label qualified:fmod-tag:fmod-label mine:main-tag:main-label", v.String())
	assert.Equal(t, "main-label", s.GetGlobal("label").String(), "GetGlobal answers with the script's binding")
}

// TestCompileWith_LaterChunkReachesTheEntrysQualifiedType covers the REPL shape,
// which upstream has no counterpart for: a chunk declares a Node an import also
// exports, and a later chunk naming Node reaches the declaring chunk's type.
func TestCompileWith_LaterChunkReachesTheEntrysQualifiedType(t *testing.T) {
	ctx := context.Background()
	s := NewSession(ctx, WithEmbedded())
	s.SetModuleDecls("xmod", `
namespace xmod;
export object Node { who: str = "xmod" }
export fun make() > Node { return Node{}; }
`)
	_, err := s.Eval(ctx, `import "xmod"; object Node { label: str = "outer" }`)
	require.NoError(t, err)
	v, err := s.Eval(ctx, `final n: any = Node{}; return "{(n as? Node) != null}:{xmod\make().who}";`)
	require.NoError(t, err)
	assert.Equal(t, "true:xmod", v.String())
	v, err = s.Eval(ctx, `return Node{}.label;`)
	require.NoError(t, err)
	assert.Equal(t, "outer", v.String())
}

// TestCompileWith_RefusesAHostTypeNothingDefines covers a gopherbuzz host API with
// no upstream counterpart: a type declared for the checker alone cannot be built,
// so its literal fails to compile and names the type, even in a branch that would
// never run.
func TestCompileWith_RefusesAHostTypeNothingDefines(t *testing.T) {
	ctx := context.Background()
	s := NewSession(ctx, WithEmbedded())
	s.DeclareModuleTypes("host", `export object Ghost { shade: int = 1 }`)
	_, err := s.Eval(ctx, `fun never() > Ghost { return Ghost{}; } return 1;`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Ghost is declared for type checking only and has no definition to construct")
}

func TestCompileWith_EmptyProgram(t *testing.T) {
	prog, err := ParseEmbedded("")
	require.NoError(t, err)
	chunk, err := CompileWith(prog, CompileOptions{})
	require.NoError(t, err)
	require.NotNil(t, chunk, "CompileWith returned nil chunk for empty program")
}

// The Semantics tables below pin OBSERVABLE LANGUAGE SEMANTICS (the operator,
// method, and indexing behavior implemented by vm/operators.go) through whole
// programs, so each case exercises parse, compile, dispatch, and the method body
// together. Results are asserted as the value's full canonical rendering
// (Value.String()), which compares the entire structure (list contents, map
// ordering, float formatting) in one assertion instead of field-by-field.
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
		{"len counts BYTES, as upstream does", `return "héllo".len();`, "6"},
		{"upper", `return "hé11o".upper();`, "HÉ11O"},
		{"lower", `return "HÉLLO".lower();`, "héllo"},
		{"trim strips unicode space", "return \"\\t hi \\n\".trim();", "hi"},
		{"byte returns rune at index", `return "abc".byte(1);`, "98"},
		{"byte defaults to index 0", `return "abc".byte();`, "97"},
		{"indexOf returns a BYTE index", `return "héllo".indexOf("llo");`, "3"},
		{"indexOf missing returns null", `return "abc".indexOf("zzz");`, "null"},
		{"startsWith true", `return "hello".startsWith("he");`, "true"},
		{"startsWith false", `return "hello".startsWith("lo");`, "false"},
		{"endsWith true", `return "hello".endsWith("lo");`, "true"},
		{"endsWith false", `return "hello".endsWith("he");`, "false"},
		// replace substitutes EVERY occurrence. These two lines asserted the opposite
		// until 2026-08-09, justified as "matching upstream Buzz", which was simply
		// untrue: upstream's src/builtin/str.zig calls Zig's std.mem.replaceOwned, and
		// that walks the whole string. The wrong claim is why the bug outlived review,
		// so it is worth stating where the answer comes from rather than asserting it.
		{"replace replaces every occurrence", `return "a-a-a".replace("a", "b");`, "b-b-b"},
		// An empty needle has NO upstream answer to match: std.mem.replace advances its
		// cursor by needle.len, so a zero-length needle never advances and upstream spins
		// rather than returning. gopherbuzz takes Go's (and JavaScript's) reading:
		// insert at every boundary, because it terminates and is the answer a reader
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
		{"sub takes BYTE offsets", `return "héllo".sub(1, 2);`, "é"},
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
		{"string index yields one-byte string", `return "héllo"[1].byte(0);`, "195"},
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
		{"list index out of range", `return [1][5];`, "Out of bound list access"},
		{"list negative index", `return [1][0 - 1];`, "Out of bound list access"},
		{"list set out of range", `var l = mut [1]; l[5] = 2; return l;`, "Out of bound list access"},
		{"string index past the last byte", `return "aé"[3];`, "Out of bound str access"},
		{"string negative index", `return "ab"[0 - 1];`, "Out of bound string access"},
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

var _benchCtx = context.Background()

// benchSession creates a session and defines src once; returns a precompiled
// chunk for the "hot" portion and the session's env so globals are available.
func benchSetup(b *testing.B, init, hot string) (*vmpackage.Chunk, *vmpackage.Env) {
	b.Helper()
	sess := newSession(_benchCtx)
	if init != "" {
		if err := sess.Exec(_benchCtx, init); err != nil {
			b.Fatalf("bench setup: %v", err)
		}
	}
	prog, err := ParseEmbedded(hot)
	if err != nil {
		b.Fatalf("bench parse: %v", err)
	}
	chunk, err := CompileWith(prog, CompileOptions{})
	if err != nil {
		b.Fatalf("bench compile: %v", err)
	}
	return chunk, sess.env
}

// benchRun times b.N executions of an already-compiled chunk on a fresh VM, the
// protocol every benchmark here uses.
func benchRun(b *testing.B, chunk *vmpackage.Chunk, env *vmpackage.Env) {
	b.Helper()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm := vmpackage.NewVM(_benchCtx)
		if _, err := vm.Run(chunk, env); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkFib measures recursive fibonacci(30) — call/return overhead, int
// arithmetic, and conditional branching.
func BenchmarkFib(b *testing.B) {
	chunk, env := benchSetup(b,
		`fun fibonacci(n: int) > int {
    if (n <= 1) { return n; }
    return fibonacci(n - 1) + fibonacci(n - 2);
}`,
		`final __r = fibonacci(30);`,
	)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm := vmpackage.NewVM(_benchCtx)
		if _, err := vm.Run(chunk, env); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkLoopSum measures a tight while-loop summing 1 000 000 ints —
// local variables, integer arithmetic, backward jumps, context-cancel poll.
func BenchmarkLoopSum(b *testing.B) {
	chunk, env := benchSetup(b, "", `
var sum = 0;
var i = 0;
while (i < 1000000) {
    sum = sum + i;
    i = i + 1;
}
`)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm := vmpackage.NewVM(_benchCtx)
		if _, err := vm.Run(chunk, env); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkLoopSumFloat is BenchmarkLoopSum with double operands — it exercises
// the float arithmetic path.
func BenchmarkLoopSumFloat(b *testing.B) {
	chunk, env := benchSetup(b, "", `
var sum = 0.0;
var i = 0.0;
while (i < 1000000.0) {
    sum = sum + i;
    i = i + 1.0;
}
`)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm := vmpackage.NewVM(_benchCtx)
		if _, err := vm.Run(chunk, env); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkLoopSumShared measures the same tight while-loop as BenchmarkLoopSum
// but compiled in SharedGlobals mode — the Env-based top-level path magus uses
// for magusfiles. Here sum/i are runtime Env bindings accessed via
// OpLoadName/OpStoreName (with the VM name cache), not stack slots, so this is
// the benchmark that exercises the Env load/store hot path.
func BenchmarkLoopSumShared(b *testing.B) {
	sess := newSession(_benchCtx)
	prog, err := ParseEmbedded(`
var sum = 0;
var i = 0;
while (i < 1000000) {
    sum = sum + i;
    i = i + 1;
}
`)
	if err != nil {
		b.Fatalf("bench parse: %v", err)
	}
	chunk, err := CompileWith(prog, CompileOptions{SharedGlobals: true})
	if err != nil {
		b.Fatalf("bench compile: %v", err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm := vmpackage.NewVM(_benchCtx)
		if _, err := vm.Run(chunk, sess.env); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkLoopSumPromoted is BenchmarkLoopSumShared compiled with PromoteTopLevel:
// sum/i are chunk-private (never captured, never exported), so they slot-promote
// even though the chunk runs against the session Env. This is the win the
// magusfile entrypoint path unlocks — it should approach the slot-based
// BenchmarkLoopSum rather than the Env-bound BenchmarkLoopSumShared.
func BenchmarkLoopSumPromoted(b *testing.B) {
	sess := newSession(_benchCtx)
	prog, err := ParseEmbedded(`
var sum = 0;
var i = 0;
while (i < 1000000) {
    sum = sum + i;
    i = i + 1;
}
`)
	if err != nil {
		b.Fatalf("bench parse: %v", err)
	}
	chunk, err := CompileWith(prog, CompileOptions{SharedGlobals: true, PromoteTopLevel: true})
	if err != nil {
		b.Fatalf("bench compile: %v", err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm := vmpackage.NewVM(_benchCtx)
		if _, err := vm.Run(chunk, sess.env); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkLoopEq measures a tight while-loop whose body is dominated by
// integer equality tests (OpEqual), counting how many i in [0,1e6) are even via
// i % 2 == 0. Gates the OpEqual/OpNotEqual int fast paths.
func BenchmarkLoopEq(b *testing.B) {
	chunk, env := benchSetup(b, "", `
var count = 0;
var i = 0;
while (i < 1000000) {
    if (i % 2 == 0) { count = count + 1; }
    i = i + 1;
}
`)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm := vmpackage.NewVM(_benchCtx)
		if _, err := vm.Run(chunk, env); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkForeachList measures list iteration and element access.
func BenchmarkForeachList(b *testing.B) {
	chunk, env := benchSetup(b,
		`var items = mut []; var k = 0; while (k < 1000) { items.append(k); k = k + 1; }`,
		`var sum = 0;
foreach (x in items) { sum = sum + x; }`,
	)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm := vmpackage.NewVM(_benchCtx)
		if _, err := vm.Run(chunk, env); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkForeachMap measures map iteration (insertion-ordered keys).
func BenchmarkForeachMap(b *testing.B) {
	chunk, env := benchSetup(b,
		`final m = {"a": 1, "b": 2, "c": 3, "d": 4, "e": 5,
                    "f": 6, "g": 7, "h": 8, "i": 9, "j": 10};`,
		`var sum = 0;
foreach (k, v in m) { sum = sum + v; }`,
	)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm := vmpackage.NewVM(_benchCtx)
		if _, err := vm.Run(chunk, env); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkStringInterp measures string interpolation in a loop.
func BenchmarkStringInterp(b *testing.B) {
	chunk, env := benchSetup(b, "", `
var s = "";
var i = 0;
while (i < 100) {
    s = "item {i} of 100";
    i = i + 1;
}
`)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm := vmpackage.NewVM(_benchCtx)
		if _, err := vm.Run(chunk, env); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkStringInterpPromoted is BenchmarkStringInterp compiled with
// PromoteTopLevel: s/i slot-promote, isolating how much of the interpolation-loop
// cost was the top-level Env access path versus the string building itself.
func BenchmarkStringInterpPromoted(b *testing.B) {
	sess := newSession(_benchCtx)
	prog, err := ParseEmbedded(`
var s = "";
var i = 0;
while (i < 100) {
    s = "item {i} of 100";
    i = i + 1;
}
`)
	if err != nil {
		b.Fatalf("bench parse: %v", err)
	}
	chunk, err := CompileWith(prog, CompileOptions{SharedGlobals: true, PromoteTopLevel: true})
	if err != nil {
		b.Fatalf("bench compile: %v", err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm := vmpackage.NewVM(_benchCtx)
		if _, err := vm.Run(chunk, sess.env); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkCall measures the overhead of calling a simple Buzz function
// repeatedly — frame allocation, parameter binding, and return.
func BenchmarkCall(b *testing.B) {
	chunk, env := benchSetup(b,
		`fun add(a: int, b: int) > int { return a + b; }`,
		`final __r = add(1, 2);`,
	)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm := vmpackage.NewVM(_benchCtx)
		if _, err := vm.Run(chunk, env); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkMethodCall measures a tight loop calling an object method 100 000
// times. Each call exercises: OpGetMember method binding (copies the funObj +
// sets This), the OpCall this-env path (newEnv + define("this")), and
// OpLoadName "this" inside the method body. This is the primary benchmark for
// the method-call deallocation work (plan items 2.1/2.2).
func BenchmarkMethodCall(b *testing.B) {
	chunk, env := benchSetup(b,
		`object Point {
    x: int = 0,
    y: int = 0,
    fun dist() > int {
        return this.x * this.x + this.y * this.y;
    }
}
final p = Point{ x = 3, y = 4 };`,
		`var sum = 0;
var i = 0;
while (i < 100000) {
    sum = sum + p.dist();
    i = i + 1;
}`,
	)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm := vmpackage.NewVM(_benchCtx)
		if _, err := vm.Run(chunk, env); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkFieldAccess measures a tight loop reading and writing a single
// object field 1 000 000 times. Exercises OpGetMember and OpSetMember →
// mapObj.get/set, which is the primary benchmark for the small-map fast path
// (plan item 1).
func BenchmarkFieldAccess(b *testing.B) {
	chunk, env := benchSetup(b,
		`object Counter {
    n: int = 0,
}
final c = mut Counter{};`,
		`var i = 0;
while (i < 1000000) {
    c.n = c.n + 1;
    i = i + 1;
}`,
	)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm := vmpackage.NewVM(_benchCtx)
		if _, err := vm.Run(chunk, env); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkFieldAccessLocal measures a tight loop reading and writing a single
// object field on a LOCAL variable (not a global) 1 000 000 times. Unlike
// BenchmarkFieldAccess (which uses a global loaded via OpLoadName+mcache),
// here `c` is a slot-local inside a fun body, so A3 (slotObjFields) kicks in:
// the compiler emits OpGetField/OpSetField instead of OpGetMember/OpSetMember.
// Counter must be in the same compilation unit so it lands in typeDecls before
// the function body is compiled.
func BenchmarkFieldAccessLocal(b *testing.B) {
	chunk, env := benchSetup(b,
		"",
		`object Counter {
    n: int = 0,
}
fun run() {
    var c = mut Counter{};
    var i = 0;
    while (i < 1000000) {
        c.n = c.n + 1;
        i = i + 1;
    }
}
run();`,
	)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm := vmpackage.NewVM(_benchCtx)
		if _, err := vm.Run(chunk, env); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkDirectCall measures a tight loop calling a direct (Go) callable
// 1 000 000 times. Each call exercises the OpCall tagDirect path, which does
// args := make([]Value, argCount); copy(...) — the target for plan item 4
// (drop direct-call per-call allocation). A direct callable is injected from Go
// (the old `len`/`range` globals were moved into the std module), so the bench no
// longer depends on `import "std"`.
func BenchmarkDirectCall(b *testing.B) {
	sess := newSession(_benchCtx)
	sess.SetGlobal("nat", vmpackage.DirectValue("nat", func(_ context.Context, args []vmpackage.Value) (vmpackage.Value, error) {
		return vmpackage.IntValue(int64(len(args))), nil
	}))
	prog, err := ParseEmbedded(`var sum = 0;
var i = 0;
while (i < 1000000) {
    sum = sum + nat(i);
    i = i + 1;
}`)
	if err != nil {
		b.Fatalf("bench parse: %v", err)
	}
	chunk, err := CompileWith(prog, CompileOptions{})
	if err != nil {
		b.Fatalf("bench compile: %v", err)
	}
	env := sess.env
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm := vmpackage.NewVM(_benchCtx)
		if _, err := vm.Run(chunk, env); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkThreeReg measures a tight loop where each iteration performs a
// true 3-address operation (dst ≠ src1 ≠ src2): `c = a + b` with all three
// variables being distinct stack slots. Before A2, the compiler emitted
// OpBinLL (3-instr fusion, C=0) + OpSetLocal — two dispatches. After
// A2 Pass 1L absorbs the SetLocal at compile time into OpBinLL with
// C=dst+1 (4-instr fusion), saving one dispatch and one push/pop round-trip.
func BenchmarkThreeReg(b *testing.B) {
	chunk, env := benchSetup(b, "", `
var a = 1;
var b = 2;
var c = 0;
var i = 0;
while (i < 1000000) {
    c = a + b;
    i = i + 1;
}
`)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm := vmpackage.NewVM(_benchCtx)
		if _, err := vm.Run(chunk, env); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkLoopSumSharedScoped measures the same SharedGlobals tight loop as
// BenchmarkLoopSumShared but the body is wrapped in a block scope — this
// exercises OpPushScope/OpPopScope on every iteration, which invalidates the
// VM name cache and forces ncache re-population. Establishes a baseline for
// any per-entry invalidation optimization (REC-19).
func BenchmarkLoopSumSharedScoped(b *testing.B) {
	sess := newSession(_benchCtx)
	prog, err := ParseEmbedded(`
var sum = 0;
var i = 0;
while (i < 1000000) {
    {
        sum = sum + i;
        i = i + 1;
    }
}
`)
	if err != nil {
		b.Fatalf("bench parse: %v", err)
	}
	chunk, err := CompileWith(prog, CompileOptions{SharedGlobals: true})
	if err != nil {
		b.Fatalf("bench compile: %v", err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm := vmpackage.NewVM(_benchCtx)
		if _, err := vm.Run(chunk, sess.env); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkStrIndexOfLateHit measures indexOf on a large ASCII haystack where the
// needle sits near the END. This is the docs site generator's hot shape: lib/glossary
// and lib/html scan a rendered page (tens of KB) for markers, so any per-call cost
// proportional to the prefix makes a marker-by-marker walk quadratic in the page size.
func BenchmarkStrIndexOfLateHit(b *testing.B) {
	chunk, env := benchSetup(b,
		`final haystack = "x".repeat(200000) + "NEEDLE";`,
		`final __r = haystack.indexOf("NEEDLE");`,
	)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm := vmpackage.NewVM(_benchCtx)
		if _, err := vm.Run(chunk, env); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkStrByteScan measures a character-at-a-time walk of a large ASCII string,
// the shape lib/glossary's word-boundary check and lib/conventions' fence scanner use.
// Each byte(i) must be O(1) or the walk turns quadratic.
func BenchmarkStrByteScan(b *testing.B) {
	chunk, env := benchSetup(b,
		`final haystack = "abcdefghij".repeat(2000);`,
		`var n = 0;
var i = 0;
while (i < haystack.len()) {
    if (haystack.byte(i) == 99) { n = n + 1; }
    i = i + 1;
}`,
	)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm := vmpackage.NewVM(_benchCtx)
		if _, err := vm.Run(chunk, env); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkStrByteScanMultibyte is BenchmarkStrByteScan's twin for a non-ASCII string,
// which must cost the same: 31 of the magus docs' 200 Markdown sources contain an
// accented character, and a CPU profile of the site render once attributed 14% of the
// whole build to str.byte when one such character slowed every lookup in a document.
func BenchmarkStrByteScanMultibyte(b *testing.B) {
	chunk, env := benchSetup(b,
		`final haystack = "abcdéfghij".repeat(2000);`,
		`var n = 0;
var i = 0;
while (i < haystack.len()) {
    if (haystack.byte(i) == 99) { n = n + 1; }
    i = i + 1;
}`,
	)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm := vmpackage.NewVM(_benchCtx)
		if _, err := vm.Run(chunk, env); err != nil {
			b.Fatal(err)
		}
	}
}

// The benchmarks below cover the language features gopherbuzz gained after the set
// above was written (fibers, match, closures over cells, optionals, mut
// collections, higher-order collection methods, static dispatch and tuples), so the
// benchmark set tracks what conformance now tests rather than lagging it.

// BenchmarkFiberForeach drives a generator fiber 1 000 times through foreach, the
// shape upstream's own examples use. Each yield suspends the fiber's private VM and
// resumes the driver, so this measures fiber switch cost, not loop cost.
func BenchmarkFiberForeach(b *testing.B) {
	chunk, env := benchSetup(b,
		`fun squares(n: int) > void *> int? {
    foreach (i in 0..n) {
        _ = yield (i * i);
    }
}`,
		`var sum = 0;
foreach (v in &squares(1000)) {
    sum = sum + v;
}`,
	)
	benchRun(b, chunk, env)
}

// BenchmarkFiberResume measures explicit resume/resolve rather than foreach: the
// handle is bound, so each step pays the OpResume path and the status check.
func BenchmarkFiberResume(b *testing.B) {
	chunk, env := benchSetup(b,
		`fun counter(n: int) > int *> int? {
    var i = 0;
    while (i < n) {
        _ = yield i;
        i = i + 1;
    }
    return i;
}`,
		`final f = &counter(1000);
var sum = 0;
foreach (v in f) {
    sum = sum + v;
}
final __r = resolve f;`,
	)
	benchRun(b, chunk, env)
}

// BenchmarkMatchEnum measures an exhaustive match over an enum subject: the arm
// comparison is enum-case identity, and no else arm is present.
func BenchmarkMatchEnum(b *testing.B) {
	chunk, env := benchSetup(b,
		`enum Kind { one, two, three, four }

fun label(k: Kind) > str {
    return match (k) {
        .one -> "one",
        .two -> "two",
        .three -> "three",
        .four -> "four",
    };
}
final kinds = [Kind.one, Kind.two, Kind.three, Kind.four];`,
		`var n = 0;
var i = 0;
while (i < 25000) {
    if (label(kinds[i % 4]).len() > 2) { n = n + 1; }
    i = i + 1;
}`,
	)
	benchRun(b, chunk, env)
}

// BenchmarkMatchRange measures range arms, which dispatch on CONTAINMENT rather
// than equality: a different comparison path from the enum and literal arms.
func BenchmarkMatchRange(b *testing.B) {
	chunk, env := benchSetup(b, "",
		`var n = 0;
var i = 0;
while (i < 25000) {
    final band = match (i % 100) {
        0..25 -> 1,
        25..50 -> 2,
        50..75 -> 3,
        else -> 4,
    };
    n = n + band;
    i = i + 1;
}`,
	)
	benchRun(b, chunk, env)
}

// BenchmarkMatchPattern measures pattern (regex) arms, where each arm runs the
// compiled pattern against the subject string.
func BenchmarkMatchPattern(b *testing.B) {
	chunk, env := benchSetup(b,
		`final subjects = ["hello joe", "1234", "  ", "zz-99"];`,
		`var n = 0;
var i = 0;
while (i < 5000) {
    final kind = match (subjects[i % 4]) {
        $"^[a-z]+ [a-z]+$" -> 1,
        $"^\d+$" -> 2,
        $"^\s+$" -> 3,
        else -> 4,
    };
    n = n + kind;
    i = i + 1;
}`,
	)
	benchRun(b, chunk, env)
}

// BenchmarkTryCatchThrow measures a throw caught one frame up, the cross-frame
// unwind path. The loop throws on half its iterations so both the raising and the
// non-raising arm are represented.
func BenchmarkTryCatchThrow(b *testing.B) {
	chunk, env := benchSetup(b,
		`fun risky(n: int) > int !> str {
    if (n % 2 == 0) { throw "even"; }
    return n;
}`,
		`var caught = 0;
var ok = 0;
var i = 0;
while (i < 20000) {
    try {
        ok = ok + risky(i);
    } catch (e: str) {
        caught = caught + e.len();
    }
    i = i + 1;
}`,
	)
	benchRun(b, chunk, env)
}

// BenchmarkClosureUpvalue measures a closure assigning to a captured local. Capture
// is by reference through a shared cell, so every read and write in both the owning
// frame and the closure goes through OpGetLocalCell/OpSetLocalCell.
func BenchmarkClosureUpvalue(b *testing.B) {
	chunk, env := benchSetup(b, "",
		`var sum = 0;
final add = fun (n: int) > void { sum = sum + n; };
var i = 0;
while (i < 100000) {
    add(i);
    i = i + 1;
}`,
	)
	benchRun(b, chunk, env)
}

// BenchmarkForeachRange measures foreach over a range, which iterates without
// materialising a list.
func BenchmarkForeachRange(b *testing.B) {
	chunk, env := benchSetup(b, "",
		`var sum = 0;
foreach (n in 0..200000) {
    sum = sum + n;
}`,
	)
	benchRun(b, chunk, env)
}

// BenchmarkForeachStr measures foreach over a string. Buzz iterates a string
// BYTEWISE, so this walks the raw bytes rather than decoding runes.
func BenchmarkForeachStr(b *testing.B) {
	chunk, env := benchSetup(b,
		`final doc = "abcdefghij".repeat(2000);`,
		`var n = 0;
foreach (c in doc) {
    n = n + c.len();
}`,
	)
	benchRun(b, chunk, env)
}

// BenchmarkOptionalUnwrap measures the optional operators together: null-coalesce
// on a null subject, force-unwrap on a bound one, and optional subscript.
func BenchmarkOptionalUnwrap(b *testing.B) {
	chunk, env := benchSetup(b,
		`final present: int? = 7;
final absent: int? = null;
final xs: [int]? = [1, 2, 3];`,
		`var n = 0;
var i = 0;
while (i < 50000) {
    n = n + present! + (absent ?? 1) + (xs?[i % 3] ?? 0);
    i = i + 1;
}`,
	)
	benchRun(b, chunk, env)
}

// BenchmarkListHigherOrder measures map/filter/reduce over a list. Each call
// invokes a Buzz closure per element, so this is dominated by callback dispatch
// rather than by the traversal.
func BenchmarkListHigherOrder(b *testing.B) {
	chunk, env := benchSetup(b,
		`var seed = mut [<int>];
foreach (i in 0..2000) {
    seed.append(i);
}`,
		`final doubled = seed.map(fun (_: int, v: int) > int => v * 2);
final evens = doubled.filter(fun (_: int, v: int) > bool => v % 4 == 0);
final __r = evens.reduce::<int>(fun (_: int, v: int, acc: int) > int => acc + v, initial: 0);`,
	)
	benchRun(b, chunk, env)
}

// BenchmarkMutListAppend measures repeated append to a mut list, so the growth of
// the backing slice is on the clock alongside the mutability check each call makes.
func BenchmarkMutListAppend(b *testing.B) {
	chunk, env := benchSetup(b, "",
		`final xs = mut [<int>];
var i = 0;
while (i < 20000) {
    xs.append(i);
    i = i + 1;
}`,
	)
	benchRun(b, chunk, env)
}

// BenchmarkStaticCall measures a static method call, which resolves on the TYPE
// value and binds no receiver: a different dispatch path from BenchmarkMethodCall.
func BenchmarkStaticCall(b *testing.B) {
	chunk, env := benchSetup(b,
		`object Point {
    x: int = 0,
    y: int = 0,
    static fun at(x: int, y: int) > Point { return Point{ x = x, y = y }; }
}`,
		`var n = 0;
var i = 0;
while (i < 20000) {
    n = n + Point.at(x: i, y: i).x;
    i = i + 1;
}`,
	)
	benchRun(b, chunk, env)
}

// BenchmarkTupleAccess measures building and reading a tuple, which is an anonymous
// object keyed by each element's decimal index. The first element is parenthesised
// because a bare identifier in `.{ }` is the `name = name` field shorthand.
func BenchmarkTupleAccess(b *testing.B) {
	chunk, env := benchSetup(b, "",
		`var n = 0;
var i = 0;
while (i < 20000) {
    final t = .{ (i), i + 1, i + 2 };
    n = n + t.0 + t.1 + t.2;
    i = i + 1;
}`,
	)
	benchRun(b, chunk, env)
}

// BenchmarkCompile measures parse + compile throughput.
func BenchmarkCompile(b *testing.B) {
	src := `
import "host";
fun helper(x: int) > int { return x * x + 1; }
object Config {
    name: str = "default",
    count: int = 0,
}
enum Status { Ok, Err, Unknown }
host\project.register(".");
export fun build(_args: [str]) > void {}
`
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		prog, err := ParseEmbedded(src)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := CompileWith(prog, CompileOptions{}); err != nil {
			b.Fatal(err)
		}
	}
}
