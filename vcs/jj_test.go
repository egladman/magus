package vcs

import (
	"os/exec"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParseJJConflicts pins the `jj resolve --list` parse against output captured from
// jj 0.44.0. The "including 1 deletion" clause is load-bearing: it is the only signal
// that a side has no content, and getting it wrong would settle a modify/delete as a
// content conflict, which no regeneration can fix.
func TestParseJJConflicts(t *testing.T) {
	got := parseJJConflicts("f.txt       2-sided conflict\ngone.txt    2-sided conflict including 1 deletion\n")
	require.Len(t, got, 2)
	assert.Equal(t, types.Conflict{Path: "f.txt", Kind: types.ConflictKindContent}, got[0])
	assert.Equal(t, types.Conflict{Path: "gone.txt", Kind: types.ConflictKindDeleted}, got[1])

	t.Run("a path with spaces keeps them", func(t *testing.T) {
		got := parseJJConflicts("docs/my notes.md    2-sided conflict\n")
		require.Len(t, got, 1)
		assert.Equal(t, "docs/my notes.md", got[0].Path)
	})
	t.Run("clean tree yields none", func(t *testing.T) {
		assert.Empty(t, parseJJConflicts(""))
	})
	t.Run("higher arity still parses", func(t *testing.T) {
		got := parseJJConflicts("a.txt    3-sided conflict including 2 deletions\n")
		require.Len(t, got, 1)
		assert.Equal(t, types.ConflictKindDeleted, got[0].Kind)
	})
}

// TestConflictsDoesNotSwallowRealFailures pins the stderr-based discriminator.
//
// Reading `out` (STDOUT) cannot discriminate: vcsOutputRaw returns ("", err) on any
// failure, so `strings.Contains(out, "No conflicts") || out == ""` is unconditionally true on
// the error path and the real-failure branch below it is unreachable. jj missing from PATH, a
// directory that is not a jj repo, a cancelled context and a permission error would all report
// "no conflicts", and `magus vcs resolve` would then call the merge settled.
//
// A plain temp directory is the cheapest way to provoke a genuine failure; jj writes its
// "No conflicts found" notice to stderr, which is where the discriminator looks.
func TestConflictsDoesNotSwallowRealFailures(t *testing.T) {
	if _, err := exec.LookPath("jj"); err != nil {
		t.Skip("jj not available")
	}
	_, err := jjVCS{}.Conflicts(t.Context(), t.TempDir())
	require.Error(t, err, "a directory that is not a jj repo must not report 'no conflicts'")
}

// jj keeps a repository's config under the user's config directory, so the registration is
// read back through jj rather than from the working copy. It routes jj resolve's
// arguments, the output file included, to the merge driver, leaves ui.merge-editor alone,
// and a second install changes nothing.
func TestJJMergeDriverRegistersTheToolInTheRepoConfig(t *testing.T) {
	if _, err := exec.LookPath("jj"); err != nil {
		t.Skip("jj not available")
	}
	isolateUserConfig(t)
	dir := t.TempDir()
	jjInitRepo(t, dir, map[string]string{"a.txt": "a\n"})
	ctx := t.Context()
	globs := types.MergeDriverGlobs{AutoResolve: []string{"CHANGELOG.md"}}

	changed, err := jjVCS{}.EnsureMergeDriver(ctx, dir, types.MergeDriverGlobs{Outputs: []string{"gen/**"}})
	require.NoError(t, err)
	assert.False(t, changed, "only auto-resolution has a use for the tool on jj")

	changed, err = jjVCS{}.EnsureMergeDriver(ctx, dir, globs)
	require.NoError(t, err)
	assert.True(t, changed)
	listed, err := vcsOutput(ctx, dir, "jj", "config", "list", "--repo")
	require.NoError(t, err)
	assert.Equal(t, `merge-tools.magus.program = "magus"`+"\n"+
		`merge-tools.magus.merge-args = ["vcs", "merge-driver", "$base", "$left", "$right", "$marker_length", "$path", "$output"]`+"\n"+
		`merge-tools.magus.merge-conflict-exit-codes = [1]`+"\n"+
		`merge-tools.magus.merge-tool-edits-conflict-markers = true`, listed)
	assert.NotContains(t, listed, "ui.merge-editor")

	registered, err := jjVCS{}.CheckMergeDriver(ctx, dir)
	require.NoError(t, err)
	assert.True(t, registered)
	command, err := jjVCS{}.MergeDriverCommand(ctx, dir)
	require.NoError(t, err)
	assert.Equal(t, "magus vcs merge-driver $base $left $right $marker_length $path $output", command)

	changed, err = jjVCS{}.EnsureMergeDriver(ctx, dir, globs)
	require.NoError(t, err)
	assert.False(t, changed, "installing twice is idempotent")
}
