package buzz

import (
	"context"
	"testing"

	"github.com/egladman/magus/libs/diagnostics"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSession_Warnings_VisibleAfterExec reproduces the gap this fix closes: BZZ3001
// was computed by compileShared and then thrown away (a comment there says so
// explicitly), so nothing on the normal Exec path could ever see it - only the
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
