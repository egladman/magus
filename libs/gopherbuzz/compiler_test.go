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

func TestCompileWith_EmptyProgram(t *testing.T) {
	prog, err := ParseEmbedded("")
	require.NoError(t, err)
	chunk, err := CompileWith(prog, CompileOptions{})
	require.NoError(t, err)
	require.NotNil(t, chunk, "CompileWith returned nil chunk for empty program")
}
