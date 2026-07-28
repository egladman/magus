package buzz_test

import (
	"context"
	"strconv"
	"testing"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/ast"
	buzzstd "github.com/egladman/magus/libs/gopherbuzz/std"
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

// TestNumericLiteralEval covers the non-decimal integer literals and underscore
// separators the lexer accepts (matching upstream Buzz: 0x/0b prefixes and _
// separators, no 0o/exponent/uppercase). Each source snippet must evaluate to
// the expected int64.
func TestNumericLiteralEval(t *testing.T) {
	ctx := context.Background()
	cases := map[string]int64{
		"return 0x1a;":       26,
		"return 0xFF_FF;":    65535,
		"return 0b1010;":     10,
		"return 1_000_000;":  1000000,
		"return 0xDEADBEEF;": 3735928559,
	}
	for src, want := range cases {
		sess := buzz.NewSession(ctx, buzz.WithEmbedded())
		buzzstd.Register(sess)
		v, err := sess.Eval(ctx, src)
		if err != nil {
			t.Errorf("%q: eval err: %v", src, err)
			continue
		}
		if got := v.String(); got != strconv.FormatInt(want, 10) {
			t.Errorf("%q: got %s, want %d", src, got, want)
		}
	}
}
