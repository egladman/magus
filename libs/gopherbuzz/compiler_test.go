package buzz

import (
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

func TestCompileWith_EmptyProgram(t *testing.T) {
	prog, err := ParseEmbedded("")
	require.NoError(t, err)
	chunk, err := CompileWith(prog, CompileOptions{})
	require.NoError(t, err)
	require.NotNil(t, chunk, "CompileWith returned nil chunk for empty program")
}
