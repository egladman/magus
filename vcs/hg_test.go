package vcs

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParseHgConflicts pins the `hg resolve --list` parse. Only U is a conflict: an R
// line is a path already settled, and re-resolving one would clobber a resolution the
// user (or an earlier pass of this command) already made.
func TestParseHgConflicts(t *testing.T) {
	got := parseHgConflicts("U MAGUS.md\nR docs/index.md\nU gen/graph.json\n")
	require.Len(t, got, 2)
	assert.Equal(t, types.Conflict{Path: "MAGUS.md", Kind: types.ConflictKindContent}, got[0])
	assert.Equal(t, types.Conflict{Path: "gen/graph.json", Kind: types.ConflictKindContent}, got[1])

	t.Run("no merge in progress yields none", func(t *testing.T) {
		assert.Empty(t, parseHgConflicts(""))
	})
	t.Run("paths with spaces survive", func(t *testing.T) {
		got := parseHgConflicts("U docs/my notes.md\n")
		require.Len(t, got, 1)
		assert.Equal(t, "docs/my notes.md", got[0].Path)
	})
	t.Run("CRLF and malformed lines are skipped, not mis-parsed", func(t *testing.T) {
		got := parseHgConflicts("U a.md\r\ngarbage\nX b.md\n\n")
		require.Len(t, got, 1)
		assert.Equal(t, "a.md", got[0].Path)
	})
}

// TestParseHgRemovalCandidates pins the modify/delete detection against real
// `hg debugmergestate` output (mercurial 7.2.3).
//
// This guards a bug that shipped and was caught only by running hg: the first
// implementation probed `hg status -nd`, reasoning that a deleted file would show as
// missing. It does not. During a merge Mercurial keeps the local side in the working
// tree, so the file EXISTS and `status -nd` reports nothing - every modify/delete was
// silently classified as a content conflict, which regeneration cannot settle.
func TestParseHgRemovalCandidates(t *testing.T) {
	const out = `local (working copy): 1503fcb585e5
other (merge rev): 13f4d1a8ece2
file: f.txt (state "u")
  local path: f.txt (hash 7ad4af83b511, flags "")
  other path: f.txt (node 56086f771253)
  extra: merged = yes
file: gone.txt (state "u")
  local path: gone.txt (hash 0ce54e727589, flags "")
  other path: gone.txt (node 0000000000000000000000000000000000000000)
  extra: merge-removal-candidate = yes
  extra: merged = yes
`
	got := parseHgRemovalCandidates(out)
	assert.True(t, got["gone.txt"], "the side with no content is a removal candidate")
	assert.False(t, got["f.txt"], "a two-sided content conflict is not a removal")

	t.Run("unparseable output degrades to no deletions", func(t *testing.T) {
		assert.Empty(t, parseHgRemovalCandidates("debugmergestate: unknown command"))
	})
}

// TestBaseNamesTheMainlineNotTip is the regression test for hg's default base ref.
//
// tip is the newest commit in the repository, so once you have committed anything it is
// YOUR commit: ChangedFiles then compared the checkout against itself, `magus affected`
// reported nothing affected, and a working branch built nothing at all. Measured on
// Mercurial 7.x - on a named branch `--rev tip` returns empty where `--rev default` names
// the changed file.
//
// The assertion runs through ChangedFiles rather than reading Base() as a string, because
// the string alone cannot show the failure: "tip" looks perfectly reasonable until you diff
// against it from a branch.
func TestBaseNamesTheMainlineNotTip(t *testing.T) {
	if _, err := exec.LookPath("hg"); err != nil {
		t.Skip("hg not available")
	}
	dir := t.TempDir()
	hgInitRepo(t, dir, map[string]string{"a.txt": "one\n"})

	vcsTestRun(t, dir, "hg", "branch", "feature")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("changed\n"), 0o644))
	vcsTestRun(t, dir, "hg", "commit", "-m", "feature work", "-u", "test")

	got, err := hgVCS{}.ChangedFiles(t.Context(), dir, hgVCS{}.Base())
	require.NoError(t, err, "ChangedFiles against the default base")
	assert.Contains(t, got, "a.txt",
		"a committed branch change reported nothing affected; affected would build nothing")
}
