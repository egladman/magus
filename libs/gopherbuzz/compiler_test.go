package buzz

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
