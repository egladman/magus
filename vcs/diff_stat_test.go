package vcs

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

func diffStater(t *testing.T, b parityBackend) types.DiffStater {
	t.Helper()
	ds, ok := b.drv.(types.DiffStater)
	require.Truef(t, ok, "%s must implement DiffStater", b.name)
	return ds
}

// One commit past the base that adds, edits, deletes, renames and rewrites a binary. A
// rename is a delete plus an add on every backend, though jj reports it as a rename and
// hg and Sapling record one.
func TestParityDiffStatCountsEachKindOfChange(t *testing.T) {
	eachBackend(t, func(t *testing.T, b parityBackend) {
		dir := t.TempDir()
		b.init(t, dir, map[string]string{
			"mod.txt":   "a\nb\nc\n",
			"del.txt":   "x\ny\n",
			"old.txt":   "r1\nr2\nr3\n",
			"bin.dat":   "\x00\x01\x02bin",
			"keep.txt":  "same\n",
			"sp ace.go": "one\n",
		})
		base := initialCommitID(t, b, dir)

		writeRepoFile(t, dir, "mod.txt", "a\nB\nc\nd\n")
		removePath(t, b, dir, "del.txt")
		vcsMove(t, b, dir, "old.txt", "new.txt")
		writeRepoFile(t, dir, "bin.dat", "\x00\x01\x02\x03binary2")
		writeRepoFile(t, dir, "add.txt", "n1\nn2\n")
		addPath(t, b, dir, "add.txt")
		writeRepoFile(t, dir, "pkg/inner.txt", "i\n")
		addPath(t, b, dir, "pkg/inner.txt")
		writeRepoFile(t, dir, "sp ace.go", "one\ntwo")
		commitAll(t, b, dir, "head")

		want := []types.FileStat{
			{Path: "add.txt", Added: 2},
			{Path: "bin.dat", Binary: true},
			{Path: "del.txt", Deleted: 2},
			{Path: "mod.txt", Added: 2, Deleted: 1},
			{Path: "new.txt", Added: 3},
			{Path: "old.txt", Deleted: 3},
			{Path: "pkg/inner.txt", Added: 1},
			{Path: "sp ace.go", Added: 1},
		}
		got, err := diffStater(t, b).DiffStat(t.Context(), dir, base)
		require.NoErrorf(t, err, "%s DiffStat", b.name)
		assert.Equalf(t, want, got, "%s", b.name)

		fromSubdir, err := diffStater(t, b).DiffStat(t.Context(), filepath.Join(dir, "pkg"), base)
		require.NoErrorf(t, err, "%s DiffStat from a subdirectory", b.name)
		assert.Equalf(t, want, fromSubdir, "%s: paths are repository-relative whatever dir is", b.name)
	})
}

// A base on another line of work is not an ancestor of the checkout. The count starts at
// the merge base, so the base's own file never appears and the head's edits do.
func TestParityDiffStatStartsAtTheMergeBase(t *testing.T) {
	eachBackend(t, func(t *testing.T, b parityBackend) {
		dir, _, base, _ := forkedRange(t, b)

		got, err := diffStater(t, b).DiffStat(t.Context(), dir, base)
		require.NoErrorf(t, err, "%s DiffStat", b.name)
		assert.Equalf(t, []types.FileStat{
			{Path: "head-only.txt", Added: 1},
			{Path: "root.txt", Added: 1},
		}, got, "%s", b.name)
	})
}

// Nothing past the base is an answer, an empty list. An unresolvable base is an error:
// an empty list there would read as a change that touched nothing.
func TestParityDiffStatEmptyVersusUnanswerable(t *testing.T) {
	eachBackend(t, func(t *testing.T, b parityBackend) {
		dir := t.TempDir()
		b.init(t, dir, map[string]string{"a.txt": "a\n"})
		head := initialCommitID(t, b, dir)

		if b.name != "jj" {
			got, err := diffStater(t, b).DiffStat(t.Context(), dir, head)
			require.NoErrorf(t, err, "%s DiffStat of the head against itself", b.name)
			assert.Emptyf(t, got, "%s", b.name)
		}

		_, err := diffStater(t, b).DiffStat(t.Context(), dir, "no-such-revision")
		assert.Errorf(t, err, "%s: an unresolvable base", b.name)
		_, err = diffStater(t, b).DiffStat(t.Context(), dir, "-x")
		assert.ErrorContainsf(t, err, "looks like a flag", "%s: an option-shaped base", b.name)
	})
}

// Uncommitted edits are not part of the revision, except on jj where the working copy is
// the revision.
func TestParityDiffStatLeavesTheWorkingCopyUnread(t *testing.T) {
	eachBackend(t, func(t *testing.T, b parityBackend) {
		if b.name == "jj" {
			t.Skip("jj's @ is the working copy")
		}
		dir := t.TempDir()
		b.init(t, dir, map[string]string{"a.txt": "a\n"})
		base := initialCommitID(t, b, dir)
		writeRepoFile(t, dir, "a.txt", "a\nmore\nlines\n")

		got, err := diffStater(t, b).DiffStat(t.Context(), dir, base)
		require.NoErrorf(t, err, "%s DiffStat", b.name)
		assert.Emptyf(t, got, "%s counted an uncommitted edit", b.name)
	})
}

func TestDiffStatParsesUnifiedDiffs(t *testing.T) {
	count := func(t *testing.T, diff string) []diffEntry {
		t.Helper()
		got, err := parseUnifiedStats(diff)
		require.NoError(t, err)
		return got
	}

	t.Run("a content line that reads as a header is content", func(t *testing.T) {
		got := count(t, "diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -1,2 +1,2 @@\n--- gone\n+++ here\n same\n")
		require.Len(t, got, 1)
		assert.Equal(t, types.FileStat{Path: "x", Added: 1, Deleted: 1}, got[0].FileStat)
	})
	t.Run("the no-newline marker is not a line", func(t *testing.T) {
		got := count(t, "diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -1 +1 @@\n-a\n\\ No newline at end of file\n+b\n\\ No newline at end of file\n")
		require.Len(t, got, 1)
		assert.Equal(t, types.FileStat{Path: "x", Added: 1, Deleted: 1}, got[0].FileStat)
	})
	t.Run("a binary patch and a header with no hunk", func(t *testing.T) {
		got := count(t, "diff --git a/a b b/a b\nindex 1..2\nGIT binary patch\nliteral 3\nxyz\n\ndiff --git a/empty b/empty\nnew file mode 100644\n")
		require.Len(t, got, 2)
		assert.Equal(t, types.FileStat{Path: "a b", Binary: true}, got[0].FileStat)
		assert.Equal(t, types.FileStat{Path: "empty"}, got[1].FileStat)
	})
	t.Run("a rename names where the file came from", func(t *testing.T) {
		got := count(t, "diff --git a/old name b/new name\nrename from old name\nrename to new name\n")
		require.Len(t, got, 1)
		assert.Equal(t, diffEntry{FileStat: types.FileStat{Path: "new name"}, from: "old name"}, got[0])
	})
	t.Run("a quoted path comes from the plus line", func(t *testing.T) {
		got := count(t, "diff --git \"a/t\\303\\251\" \"b/t\\303\\251\"\n--- \"a/t\\303\\251\"\n+++ \"b/t\\303\\251\"\n@@ -0,0 +1 @@\n+x\n")
		require.Len(t, got, 1)
		assert.Equal(t, types.FileStat{Path: "té", Added: 1}, got[0].FileStat)
	})
	t.Run("Mercurial plain format", func(t *testing.T) {
		got := count(t, "diff -r aaa -r bbb we ird.txt\n--- /dev/null\tThu Jan 01 00:00:00 1970 +0000\n+++ b/we ird.txt\tSat Oct 10 10:45:50 2026 -0400\n@@ -0,0 +1,1 @@\n+sp ace\ndiff -r aaa -r bbb bin.dat\nBinary file bin.dat has changed\n")
		require.Len(t, got, 2)
		assert.Equal(t, types.FileStat{Path: "we ird.txt", Added: 1}, got[0].FileStat)
		assert.Equal(t, types.FileStat{Path: "bin.dat", Binary: true}, got[1].FileStat)
	})
	t.Run("a truncated hunk is an error, not a short count", func(t *testing.T) {
		_, err := parseUnifiedStats("diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -1,5 +1,5 @@\n-a\n+b\n")
		assert.ErrorContains(t, err, "inside a hunk")
	})
}

func TestDiffStatParsesNumstat(t *testing.T) {
	got, err := parseNumstat("3\t1\tb.go\x00-\t-\ta bin\x000\t0\tmode.sh\x00")
	require.NoError(t, err)
	assert.Equal(t, []types.FileStat{
		{Path: "a bin", Binary: true},
		{Path: "b.go", Added: 3, Deleted: 1},
		{Path: "mode.sh"},
	}, got)

	_, err = parseNumstat("x\ty\tz\x00")
	assert.Error(t, err)

	got, err = parseNumstat("")
	require.NoError(t, err)
	assert.NotNil(t, got, "a clean revision is an empty list, which JSON renders as [] rather than null")
	assert.Empty(t, got)
}
