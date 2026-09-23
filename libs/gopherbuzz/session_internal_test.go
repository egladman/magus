package buzz

import (
	"context"
	"strings"
	"testing"

	"github.com/egladman/magus/libs/diagnostics"
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

// TestNamespaceCache_AccountsKeyBytesTowardTheBound pins the fix for namespaceCache
// bounding itself by entry count alone: a key holds its whole source string alive, so
// a handful of huge modules could retain far more than a small entry count implies.
// Priming bytes near the bound and inserting one more entry exercises both the reset
// and the post-reset accounting, mirroring tokenCache's own test.
func TestNamespaceCache_AccountsKeyBytesTowardTheBound(t *testing.T) {
	origM, origBytes := namespaceCache.m, namespaceCache.bytes
	t.Cleanup(func() {
		namespaceCache.Lock()
		namespaceCache.m, namespaceCache.bytes = origM, origBytes
		namespaceCache.Unlock()
	})
	namespaceCache.Lock()
	namespaceCache.m = map[namespaceKey][]string{}
	namespaceCache.bytes = maxNamespaceCacheBytes - 10
	namespaceCache.Unlock()

	src := "namespace a\\b\\c;\n" + strings.Repeat("// pad\n", 40)
	s := NewSession(context.Background(), WithEmbedded())
	got := s.declaredNamespace(src)
	require.Equal(t, []string{"a", "b", "c"}, got)

	namespaceCache.Lock()
	defer namespaceCache.Unlock()
	assert.LessOrEqual(t, namespaceCache.bytes, maxNamespaceCacheBytes,
		"the accounted size must never exceed the bound that is supposed to trigger a reset")
	assert.Equal(t, len(src), namespaceCache.bytes,
		"the near-full cache must have reset before inserting, leaving exactly this entry's size")
}

// TestNamespaceCache_DoubleChecksBeforeInsert matches tokenCache's own guard: a value
// already inserted for key between the read-miss and the write lock must win over a
// concurrently recomputed one, rather than the second computation clobbering it (and,
// pre-fix, double-counting its bytes).
func TestNamespaceCache_DoubleChecksBeforeInsert(t *testing.T) {
	origM, origBytes := namespaceCache.m, namespaceCache.bytes
	t.Cleanup(func() {
		namespaceCache.Lock()
		namespaceCache.m, namespaceCache.bytes = origM, origBytes
		namespaceCache.Unlock()
	})
	namespaceCache.Lock()
	namespaceCache.m = map[namespaceKey][]string{}
	namespaceCache.bytes = 0
	namespaceCache.Unlock()

	src := "namespace x\\y;\n"
	key := namespaceKey{src: src, strict: false} // matches WithEmbedded's !s.embedded below
	sentinel := []string{"already", "here"}
	namespaceCache.Lock()
	namespaceCache.m[key] = sentinel
	namespaceCache.bytes = len(src)
	namespaceCache.Unlock()

	s := NewSession(context.Background(), WithEmbedded())
	got := s.declaredNamespace(src)
	assert.Equal(t, sentinel, got, "the already-cached value must win over recomputing it")

	namespaceCache.Lock()
	defer namespaceCache.Unlock()
	assert.Equal(t, len(src), namespaceCache.bytes, "the double-checked insert must not double-count the entry")
}
