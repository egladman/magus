package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSourceNewerThan is the whole probe: two stats and a verdict. The cutoff is an
// explicit time rather than a second write, because a filesystem with coarse timestamp
// granularity would make two adjacent writes indistinguishable and the test would pass for
// the wrong reason.
func TestSourceNewerThan(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	require.NoError(t, os.WriteFile(src, []byte("package main\n"), 0o644))
	now := time.Now()
	require.NoError(t, os.Chtimes(src, now, now))

	assert.True(t, sourceNewerThan(dir, time.Now().Add(-time.Hour)),
		"a source written after the index is what stale MEANS")
	assert.False(t, sourceNewerThan(dir, time.Now().Add(time.Hour)),
		"an index built after every source is current, and a banner there is noise")
}

// A pruned tree is not a source tree. Without this the probe reports stale forever in any
// workspace with a node_modules or a gen/ directory something touches.
func TestSourceNewerThanSkipsPrunedTrees(t *testing.T) {
	dir := t.TempDir()
	cutoff := time.Now().Add(-time.Hour)
	old := cutoff.Add(-time.Hour)

	src := filepath.Join(dir, "main.go")
	require.NoError(t, os.WriteFile(src, []byte("package main\n"), 0o644))
	require.NoError(t, os.Chtimes(src, old, old))

	for _, pruned := range []string{"node_modules", "gen", ".git"} {
		sub := filepath.Join(dir, pruned, "thing")
		require.NoError(t, os.MkdirAll(sub, 0o755))
		f := filepath.Join(sub, "x.js")
		require.NoError(t, os.WriteFile(f, []byte("recent"), 0o644))
	}

	assert.False(t, sourceNewerThan(dir, cutoff),
		"a freshly written node_modules must not report the index stale")
}

// TestCommandReadsGraph pins the guard-side trigger. It goes through the parser, so a
// wrapper resolves and a quoted word does not.
func TestCommandReadsGraph(t *testing.T) {
	for _, cmd := range []string{
		"magus refs Foo",
		"./magus query \"kind:target build\"",
		"magus explain target:.:ci",
		"mise exec -- magus path a b",
	} {
		assert.True(t, commandReadsGraph(cmd), cmd)
	}
	for _, cmd := range []string{
		"magus query output out1a2b3c",
		"magus run test .",
		"magus graph build",
		"grep -r refs .",
		"echo 'magus refs Foo'",
	} {
		assert.False(t, commandReadsGraph(cmd), cmd)
	}
}

// TestStaleIndexNotice: the banner names what is stale and the one command that fixes it,
// and says nothing at all when nothing is.
func TestStaleIndexNotice(t *testing.T) {
	got := staleIndexNotice([]string{"libs/api", "."})
	assert.Contains(t, got, "stale index")
	assert.Contains(t, got, "libs/api", "the reader has to know WHICH answers are short")
	assert.Contains(t, got, "magus graph build", "and the command that fixes it")
	assert.Contains(t, got, "projects changed", "two projects read as plural")

	assert.Contains(t, staleIndexNotice([]string{"."}), "a project changed")
	assert.Empty(t, staleIndexNotice(nil),
		"a current index draws silence; a banner on every lookup is one nobody reads")
}
