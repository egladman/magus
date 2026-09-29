package buzz

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/libs/diagnostics"
	"github.com/egladman/magus/libs/gopherbuzz/ast"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSession_Warnings_VisibleAfterExec reproduces the gap this fix closes: BZZ3001
// was computed by compileShared and then thrown away (a comment there says so
// explicitly), so nothing on the normal Exec path could ever see it; only the
// separate Diagnostics call could, and that one re-executes every import, which is
// unsafe to call after a real run. Warnings() must expose the SAME warning
// compileShared already computed for this Exec, without re-resolving anything.
func TestSession_Warnings_VisibleAfterExec(t *testing.T) {
	s := NewSession(context.Background(), WithEmbedded())
	s.SetNativeModule("unused/mod", vm.NewMap())

	err := s.Exec(context.Background(), `import "unused/mod";`)
	require.NoError(t, err, "a warning must never fail Exec")

	got := s.Warnings()
	require.Len(t, got, 1, "the unused import should surface as exactly one warning")
	assert.Equal(t, SeverityWarning, got[0].Severity)
	assert.Equal(t, diagnostics.Code("BZZ3001"), got[0].Code)
	assert.Contains(t, got[0].Msg, "unused/mod")
}

// TestSession_Warnings_LastCompileNotAccumulated verifies a second Exec call
// replaces, rather than appends to, the warning set. A Session is reused across many
// compiles (a session pool, NewChild sub-sessions, the REPL evaluating one line at a
// time), so accumulating would grow the slice unbounded over a long-lived session's
// life.
func TestSession_Warnings_LastCompileNotAccumulated(t *testing.T) {
	s := NewSession(context.Background(), WithEmbedded())
	s.SetNativeModule("unused/mod", vm.NewMap())

	require.NoError(t, s.Exec(context.Background(), `import "unused/mod";`))
	require.Len(t, s.Warnings(), 1)

	require.NoError(t, s.Exec(context.Background(), `var x = 1;`))
	assert.Empty(t, s.Warnings(), "a clean second compile must clear the prior warning, not append to it")
}

// TestSession_Warnings_NilBeforeAnyCompile pins the zero-cost/zero-value contract: a
// session that never compiles anything pays nothing beyond the nil slice.
func TestSession_Warnings_NilBeforeAnyCompile(t *testing.T) {
	s := NewSession(context.Background(), WithEmbedded())
	assert.Nil(t, s.Warnings())
}

// TestDiagnostic_String_MatchesErrorRenderStyle checks Warnings() results render the
// same "[CODE] buzz: line L:C: ...\n  see: <url>" shape typeError.Error() already
// uses for hard errors, so a printed warning reads consistently with them.
func TestDiagnostic_String_MatchesErrorRenderStyle(t *testing.T) {
	s := NewSession(context.Background(), WithEmbedded())
	s.SetNativeModule("unused/mod", vm.NewMap())
	require.NoError(t, s.Exec(context.Background(), `import "unused/mod";`))

	got := s.Warnings()
	require.Len(t, got, 1)
	line := got[0].String()
	assert.Contains(t, line, "[BZZ3001]")
	assert.Contains(t, line, "warning:")
	assert.Contains(t, line, "see: https://")
	assert.Contains(t, line, "buzz: line 1:1:", "with no File, the position renders as it always has")
}

// TestDiagnostic_String_NamesTheFileWhenSet covers the reason File exists: "line 37:66"
// alone leaves a reader grepping the tree for which file aired the warning. The rendered
// shape is <file>:<line>:<col>, which editors already jump to.
func TestDiagnostic_String_NamesTheFileWhenSet(t *testing.T) {
	d := Diagnostic{Line: 37, Col: 66, Msg: "something", Severity: SeverityWarning, File: "docs/lib/conventions.buzz"}

	assert.Equal(t, "buzz: docs/lib/conventions.buzz:37:66: warning: something", d.String())
}

// TestSession_Warnings_ReplSuppressed confirms a REPL session (WithREPL) still
// reports no warnings through the Exec path either, matching Diagnostics' existing
// REPL suppression (checkShared gates both on s.repl).
func TestSession_Warnings_ReplSuppressed(t *testing.T) {
	s := NewSession(context.Background(), WithEmbedded(), WithREPL())
	s.SetNativeModule("unused/mod", vm.NewMap())

	require.NoError(t, s.Exec(context.Background(), `import "unused/mod";`))
	assert.Empty(t, s.Warnings(), "a REPL session must not warn on an import unused so far")
}

func TestWithoutFileImports(t *testing.T) {
	ctx := context.Background()
	sess := NewSession(ctx, WithEmbedded(), WithoutFileImports())
	defer func() { _ = sess.Close() }()
	err := sess.Exec(ctx, `import "./helper.buzz";`)
	require.ErrorIs(t, err, UnresolvedImport)
	assert.ErrorContains(t, err, "file imports are unavailable")
}

func TestWithoutFFI(t *testing.T) {
	ctx := context.Background()
	sess := NewSession(ctx, WithEmbedded(), WithoutFFI())
	defer func() { _ = sess.Close() }()
	_, err := sess.CallValue(ctx, sess.GetGlobal("zdef"), nil)
	require.ErrorIs(t, err, FFIDisabled)
	assert.ErrorContains(t, err, "run the script through the Buzz CLI")
}

// The filter sees the entry alone: a module keeps what the entry's kept code
// calls into.
func TestEntryFilterSkipsImportedModules(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "lib.buzz"), []byte("fun helper() > int { return 2; }\nexport fun two() > int { return helper(); }\n"), 0o644))
	ctx := context.Background()
	sess := NewSession(ctx, WithEmbedded(), WithSearchPaths(filepath.Join(dir, "?.buzz")))
	defer func() { _ = sess.Close() }()
	var seen int
	sess.SetEntryFilter("test", func(prog *ast.Program, _ ImportLookup) {
		seen++
		kept := prog.Stmts[:0]
		for _, stmt := range prog.Stmts {
			if _, isFun := stmt.(*ast.FunDecl); !isFun {
				kept = append(kept, stmt)
			}
		}
		prog.Stmts = kept
	})
	require.NoError(t, sess.Exec(ctx, "import \"lib\";\nfun dropped() > void {}\nvar n = two();\n"))
	assert.Equal(t, 1, seen)
	assert.True(t, sess.GetGlobal("dropped").IsNull())
	assert.Equal(t, int64(2), sess.GetGlobal("n").AsInt())
}

func TestRejectImportMatchesEitherSpelling(t *testing.T) {
	rejected := errors.New("not offered here")
	for _, tc := range []struct{ rejected, imported string }{
		{"os", `import "os";`},
		{"os", `import "buzz:os";`},
		{"buzz:os", `import "os";`},
		{"buzz:os", `import "buzz:os";`},
	} {
		t.Run(tc.rejected+" "+tc.imported, func(t *testing.T) {
			ctx := context.Background()
			sess := NewSession(ctx, WithEmbedded())
			defer func() { _ = sess.Close() }()
			sess.RejectImport(tc.rejected, rejected)
			require.ErrorIs(t, sess.Exec(ctx, tc.imported), rejected)
		})
	}
}
