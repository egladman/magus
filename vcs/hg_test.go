package vcs

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

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

// TestHgPrunePreservedSparesAShelfMagusDidNotWrite is the line between housekeeping and
// data loss.
//
// A magus-shaped NAME is not proof magus wrote it. `hg shelve` with no --name derives the
// shelf name from the active bookmark, so a bookmark called magus-1234567890-wip yields a
// name shelfMinted accepts, and a plain `hg shelve` REVERTS the working copy, which can
// leave that shelf as the only copy of the work. PrunePreserved runs unasked inside every
// Preserve, so deleting on a name match alone destroyed a user's only copy during work
// they never asked for.
func TestHgPrunePreservedSparesAShelfMagusDidNotWrite(t *testing.T) {
	if _, err := exec.LookPath("hg"); err != nil {
		t.Skip("hg not available")
	}
	dir := t.TempDir()
	hgInitRepo(t, dir, map[string]string{"a.txt": "one\n"})

	// The user's shelf, shaped exactly like a mint (prefix, unix seconds, suffix). Only
	// the message tells the two apart.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("half a refactor\n"), 0o644))
	vcsTestRun(t, dir, "hg", "--config", "extensions.shelve=", "shelve",
		"--keep", "--name", "magus-1234567890-wip", "--message", "half of a refactor")

	// And a real capture, so the test cannot pass by pruning nothing at all.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("two\n"), 0o644))
	handle, err := hgVCS{}.Preserve(t.Context(), dir)
	require.NoError(t, err)
	require.NotEmpty(t, handle)

	dropped, err := hgVCS{}.PrunePreserved(t.Context(), dir, time.Now().Add(time.Hour))
	require.NoError(t, err)

	assert.Equal(t, []string{handle}, dropped, "pruning reported something other than its own capture")
	names := hgListPreserved(t, dir)
	assert.Contains(t, names, "magus-1234567890-wip", "pruning deleted a shelf the user wrote")
	assert.NotContains(t, names, handle, "pruning reported a handle it did not delete")
}

// TestHgPreserveRetentionBoundaryIsThirtyDays exercises preserveRetention itself, which
// no test did: the suite only ever passed cutoffs an hour either side of now, so the
// constant could be set to thirty SECONDS and every assertion stayed green.
//
// Both sides of the window are asserted, because only the survival half pins the length.
// It runs through Preserve rather than calling PrunePreserved with a cutoff of its own,
// since a cutoff the test chose would say nothing about the one the code applies.
//
// The two ages are LITERAL days rather than preserveRetention plus or minus a day. Written
// against the constant they move with it, and 29 days either side of thirty seconds is
// still one on each side, so the test passes whatever the constant says: measured, by
// setting it to thirty seconds and watching this stay green.
func TestHgPreserveRetentionBoundaryIsThirtyDays(t *testing.T) {
	if _, err := exec.LookPath("hg"); err != nil {
		t.Skip("hg not available")
	}
	dir := t.TempDir()
	hgInitRepo(t, dir, map[string]string{"a.txt": "one\n"})

	// Aged by the only thing that dates an hg shelf: the timestamp shelfName writes into
	// the name.
	aged := func(days int, suffix string) string {
		age := time.Duration(days) * 24 * time.Hour
		name := fmt.Sprintf("%s%d-%s", shelfPrefix, time.Now().Add(-age).Unix(), suffix)
		require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte(name+"\n"), 0o644))
		vcsTestRun(t, dir, "hg", "--config", "extensions.shelve=", "shelve",
			"--keep", "--name", name, "--message", preserveMessage)
		return name
	}
	inside := aged(29, "inside")
	outside := aged(31, "outside")

	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("current\n"), 0o644))
	_, err := hgVCS{}.Preserve(t.Context(), dir)
	require.NoError(t, err)

	names := hgListPreserved(t, dir)
	assert.Contains(t, names, inside, "dropped a capture still inside the retention window")
	assert.NotContains(t, names, outside, "kept a capture past the retention window")
}
