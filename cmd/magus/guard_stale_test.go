package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// magusTree lays out the two markers staleGuardNotice identifies its own workspace
// by, plus one guard source, and returns the root.
func magusTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "cmd", "magus"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte("\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "cmd", "magus", "guard.go"), []byte("package main\n"), 0o644))
	return root
}

func TestNewestGuardSourceIgnoresUnrelatedGo(t *testing.T) {
	root := magusTree(t)
	future := time.Now().Add(time.Hour)

	unrelated := filepath.Join(root, "cmd", "magus", "run.go")
	require.NoError(t, os.WriteFile(unrelated, []byte("package main\n"), 0o644))
	require.NoError(t, os.Chtimes(unrelated, future, future))

	newest, path := newestGuardSource(root)
	assert.Equal(t, filepath.Join("cmd", "magus", "guard.go"), path,
		"only sources that change what the guard DECIDES count")
	assert.True(t, newest.Before(future))
}

// The rule sources are what matter, wherever they live.
func TestNewestGuardSourceSeesEveryRuleSource(t *testing.T) {
	root := magusTree(t)
	future := time.Now().Add(time.Hour)

	require.NoError(t, os.MkdirAll(filepath.Join(root, "internal", "agent"), 0o755))
	rule := filepath.Join(root, "internal", "agent", "guard.go")
	require.NoError(t, os.WriteFile(rule, []byte("package agent\n"), 0o644))
	require.NoError(t, os.Chtimes(rule, future, future))

	_, path := newestGuardSource(root)
	assert.Equal(t, filepath.Join("internal", "agent", "guard.go"), path)
}

// A tree with no guard sources yields no time, which the caller reads as "nothing to
// compare" rather than as "stale".
func TestNewestGuardSourceIsZeroWithoutSources(t *testing.T) {
	newest, path := newestGuardSource(t.TempDir())
	assert.True(t, newest.IsZero())
	assert.Empty(t, path)
}

// A binary on PATH judges other people's workspaces and has no tree to be stale
// against, so the notice must never fire there.
func TestStaleGuardNoticeIsSilentOutsideTheMagusTree(t *testing.T) {
	assert.Empty(t, staleGuardNotice(),
		"the test binary is not a ./magus sitting in the magus tree")
}

// A test is not linked into the binary, so editing one cannot make a verdict stale.
// Including them made this notice ride on every tool call of a normal session.
func TestNewestGuardSourceIgnoresTests(t *testing.T) {
	root := magusTree(t)
	future := time.Now().Add(time.Hour)

	test := filepath.Join(root, "cmd", "magus", "guard_gate_test.go")
	require.NoError(t, os.WriteFile(test, []byte("package main\n"), 0o644))
	require.NoError(t, os.Chtimes(test, future, future))

	newest, path := newestGuardSource(root)
	assert.Equal(t, filepath.Join("cmd", "magus", "guard.go"), path)
	assert.True(t, newest.Before(future), "a test edit must not read as a stale binary")
}
