package buzz_test

import (
	"testing"

	"github.com/egladman/gopherbuzz"
	"github.com/egladman/gopherbuzz/ast"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParse_ValidProgram(t *testing.T) {
	parseOK := func(t *testing.T, src string) {
		t.Helper()
		prog, err := buzz.ParseEmbedded(src)
		require.NoErrorf(t, err, "ParseEmbedded(%q): unexpected error", src)
		require.NotNil(t, prog, "Parse returned nil program without error")
	}

	t.Run("empty", func(t *testing.T) { parseOK(t, "") })
	t.Run("literal", func(t *testing.T) { parseOK(t, `var x: int = 42;`) })
	t.Run("function", func(t *testing.T) { parseOK(t, `fun add(a: int, b: int) > int { return a + b; }`) })
	t.Run("if statement", func(t *testing.T) { parseOK(t, `if (true) { var x: int = 1; }`) })
}

// A `::<T>` generic call argument must be captured on the CallExpr so the
// checker can use it as the call's result type (upstream Buzz semantics).
func TestParse_GenericCallTypeArg(t *testing.T) {
	prog, err := buzz.ParseEmbedded(`final x = b.readZAt::<double>(at: 0);`)
	require.NoError(t, err)
	decl, ok := prog.Stmts[0].(*ast.DeclStmt)
	require.Truef(t, ok, "stmt 0 is %T, want *ast.DeclStmt", prog.Stmts[0])
	call, ok := decl.Value.(*ast.CallExpr)
	require.Truef(t, ok, "decl value is %T, want *ast.CallExpr", decl.Value)
	assert.Equal(t, "double", call.TypeArg, "CallExpr.TypeArg")
}

func TestParse_InvalidSyntax(t *testing.T) {
	t.Run("incomplete function", func(t *testing.T) {
		_, err := buzz.ParseEmbedded(`fun (`)
		assert.Error(t, err)
	})
	t.Run("missing type", func(t *testing.T) {
		_, err := buzz.ParseEmbedded(`var x: = ;`)
		assert.Error(t, err)
	})
}
