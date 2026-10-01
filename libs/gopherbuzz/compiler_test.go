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

func TestCompileWith_EmptyProgram(t *testing.T) {
	prog, err := ParseEmbedded("")
	require.NoError(t, err)
	chunk, err := CompileWith(prog, CompileOptions{})
	require.NoError(t, err)
	require.NotNil(t, chunk, "CompileWith returned nil chunk for empty program")
}
