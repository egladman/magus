package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBuzzCmd_UnusedImportWarnsOnStderrAndExitsClean drives the actual `magus buzz`
// code path (buzzCmd, the same function main.go dispatches to) end-to-end: a script
// with an unused import must print the BZZ3001 warning to stderr and still exit
// clean (nil error, which main.go turns into exit 0) - the whole point of a warning
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
// warning line - a silent run should stay silent.
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
