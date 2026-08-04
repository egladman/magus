package buzz_test

import (
	"context"
	"strconv"
	"strings"
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

// TestIntLiteralOverflowIsRejected pins that a literal wider than buzz's 48-bit
// int is a parse error rather than a silently truncated value.
//
// vm.IntValue keeps only the low 48 bits of its int64, so before this check
// `1786000000000000000` (a UnixNano timestamp) evaluated to 41272770887680, and
// a literal just past 2^47 came out NEGATIVE. Nothing reported either. Upstream
// scans integer literals straight into its i48 and reports "int overflow" on the
// same input, so rejecting here is conformance, not a new restriction.
func TestIntLiteralOverflowIsRejected(t *testing.T) {
	rejected := []string{
		"1786000000000000000", // UnixNano timestamp: used to evaluate to 41272770887680
		"140737488355328",     // 2^47, one past the max: used to evaluate to -140737488355328
		"0x8000_0000_0000",    // the same bound written in hex
	}
	for _, lit := range rejected {
		_, err := buzz.ParseEmbedded("final x = " + lit + ";")
		require.Errorf(t, err, "literal %s must be rejected", lit)
		assert.Containsf(t, err.Error(), "int overflow", "literal %s: %v", lit, err)
	}

	// The boundary itself still parses and still evaluates to itself. Note that
	// vm.MinInt has no literal spelling: `-140737488355328` is unary minus applied
	// to an out-of-range positive literal, which upstream rejects the same way.
	ctx := context.Background()
	sess := buzz.NewSession(ctx, buzz.WithEmbedded())
	v, err := sess.Eval(ctx, "return 140737488355327;")
	require.NoError(t, err, "max int literal")
	assert.Equal(t, "140737488355327", v.String())
}

// TestDeepNestingIsDiagnosedNotFatal pins the nesting limit on every recursive
// shape, not just parentheses.
//
// maxParseDepth used to be enforced only on the `(` case of parsePrimary, so a
// list, map, or call-argument chain recursed unguarded and overflowed Go's stack
// at roughly 40 000 levels. A stack overflow is a FATAL error, not a panic: no
// recover catches it, and the input is a magusfile in a cloned repo, which magus
// parses on every command.
func TestDeepNestingIsDiagnosedNotFatal(t *testing.T) {
	const depth = 50000
	for name, src := range map[string]string{
		"list":  "var x = " + strings.Repeat("[", depth) + "1" + strings.Repeat("]", depth) + ";",
		"map":   "var x = " + strings.Repeat(`{"a":`, depth) + "1" + strings.Repeat("}", depth) + ";",
		"call":  "var x = f" + strings.Repeat("(f", depth) + "(1" + strings.Repeat(")", depth+1) + ";",
		"paren": "var x = " + strings.Repeat("(", depth) + "1" + strings.Repeat(")", depth) + ";",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := buzz.Parse(src)
			require.Error(t, err, "must diagnose, not overflow the stack")
			assert.Contains(t, err.Error(), "nested too deeply")
		})
	}
}
