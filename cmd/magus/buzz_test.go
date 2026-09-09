package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus"
	sandboxapply "github.com/egladman/magus/internal/sandbox/apply"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBuzzCmd_UnusedImportWarnsOnStderrAndExitsClean drives the actual `magus buzz`
// code path (buzzCmd, the same function main.go dispatches to) end-to-end: a script
// with an unused import must print the BZZ3001 warning to stderr and still exit
// clean (nil error, which main.go turns into exit 0): the whole point of a warning
// is that it never fails the run.
func TestBuzzCmd_UnusedImportWarnsOnStderrAndExitsClean(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "unused.buzz")
	require.NoError(t, os.WriteFile(path, []byte("import \"fs\";\nvar x = 1;\n"), 0o644))

	prevQuiet, prevSilent := global.quiet, global.silent
	global.quiet, global.silent = false, false
	t.Cleanup(func() { global.quiet, global.silent = prevQuiet, prevSilent })

	var runErr error
	stderr := captureStderr(t, func() {
		stdout := captureStdout(t, func() {
			runErr = buzzCmd(context.Background(), "", []string{path})
		})
		assert.NotContains(t, stdout, "BZZ3001", "stdout carries structured output only; a warning must never land there")
	})

	require.NoError(t, runErr, "a warning must never fail the run")
	assert.Contains(t, stderr, "BZZ3001")
	assert.Contains(t, stderr, "fs")
	assert.Contains(t, stderr, "warning:")
}

// TestBuzzCmd_SilentSuppressesUnusedImportWarning verifies -s/--silent, the flag
// this command's own progress/error output already respects, also gates the new
// warning line: a silent run should stay silent.
func TestBuzzCmd_SilentSuppressesUnusedImportWarning(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "unused.buzz")
	require.NoError(t, os.WriteFile(path, []byte("import \"fs\";\nvar x = 1;\n"), 0o644))

	prevSilent := global.silent
	t.Cleanup(func() { global.silent = prevSilent })

	var runErr error
	stderr := captureStderr(t, func() {
		runErr = buzzCmd(context.Background(), "", []string{"-s", path})
	})

	require.NoError(t, runErr)
	assert.NotContains(t, stderr, "BZZ3001", "-s/--silent must suppress the warning line")
}

// buzzSandboxTarget is what the fixture script removes. It has to sit outside $TMPDIR as
// well as outside the workspace, because the default policy grants read+write on both, so
// a t.TempDir() would be allowed and the test would pass with no policy attached at all.
// Nothing is created there: checkWrite runs before the removal, so the deny fires on a
// path that does not exist, and a permitted run removes nothing.
const buzzSandboxTarget = "/magus-buzz-sandbox-test/victim"

// buzzSandboxWorkspace builds a workspace whose magus.yaml sets the sandbox as asked,
// opens it, and returns it on the context loadMagus reads so buzzCmd never touches the
// process singleton.
func buzzSandboxWorkspace(t *testing.T, sandboxEnabled bool) (context.Context, *magus.Magus, string) {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"),
		[]byte("import \"magus\";\n\nmagus.project({})\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "magus.yaml"),
		fmt.Appendf(nil, "sandbox:\n  enabled: %t\n", sandboxEnabled), 0o644))

	m, err := magus.Open(t.Context(), root)
	require.NoError(t, err, "fixture workspace must open")
	t.Cleanup(func() { _ = m.Close() })

	script := filepath.Join(root, "wipe.buzz")
	require.NoError(t, os.WriteFile(script,
		fmt.Appendf(nil, "import \"fs\";\n\nfun main(args: [str]) > void !> any {\n    fs\\removeAll(\"%s\");\n}\n", buzzSandboxTarget), 0o644))

	return withMagus(t.Context(), m), m, script
}

// TestBuzzCmd_ScriptRunsUnderTheWorkspaceSandbox pins the reason `magus buzz` is not a
// hole in the sandbox. The agent guard allows `magus buzz -` outright and cannot read a
// script body, so a script that magus never sandboxed was an unrestricted fs/proc/network
// surface in a workspace that had asked for one.
//
// MarkAppliedExternally puts the apply layer in its attach-only mode: landlock is
// permanent and process-wide, so applying it here would confine every later test in this
// binary. The policy still reaches ctx, which is what the binding checks read.
func TestBuzzCmd_ScriptRunsUnderTheWorkspaceSandbox(t *testing.T) {
	sandboxapply.MarkAppliedExternally("magus buzz sandbox test")
	ctx, m, script := buzzSandboxWorkspace(t, true)

	err := buzzCmd(ctx, "", []string{"-s", script})

	require.Error(t, err, "a write outside the workspace must be refused")
	assert.ErrorContains(t, err, string(types.PathWriteDenied))

	events, rerr := trail.ReadRecent(m.CacheDir(), 10)
	require.NoError(t, rerr)
	require.Len(t, events, 1, "the refusal must be readable on the trail, as a target's is")
	assert.Equal(t, trail.KindSandboxDenial, events[0].Kind)
}

// TestBuzzCmd_SandboxDisabledLeavesTheScriptUnrestricted holds the other half: the
// sandbox is off by default, and a script in a workspace that never asked for one keeps
// writing wherever it could before.
func TestBuzzCmd_SandboxDisabledLeavesTheScriptUnrestricted(t *testing.T) {
	ctx, m, script := buzzSandboxWorkspace(t, false)

	require.NoError(t, buzzCmd(ctx, "", []string{"-s", script}))

	events, rerr := trail.ReadRecent(m.CacheDir(), 10)
	require.NoError(t, rerr)
	assert.Empty(t, events, "nothing was refused, so nothing belongs on the trail")
}
