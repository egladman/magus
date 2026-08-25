package diff

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const twoFilePatch = `diff --git a/a.go b/a.go
index 111..222 100644
--- a/a.go
+++ b/a.go
@@ -1,3 +1,3 @@ func A()
 ctx
-old
+new
 tail
@@ -20,2 +20,3 @@ func B()
 keep
+added
diff --git a/b.md b/b.md
index 333..444 100644
--- a/b.md
+++ b/b.md
@@ -1 +1 @@
-before
+after
`

func TestParseHunksSplitsFilesAndHunks(t *testing.T) {
	files := ParseHunks(twoFilePatch)
	require.Len(t, files, 2)

	assert.Equal(t, "a.go", files[0].Path)
	require.Len(t, files[0].Hunks, 2)
	assert.Equal(t, 0, files[0].Hunks[0].Index)
	assert.Equal(t, 1, files[0].Hunks[1].Index)
	// The body excludes the @@ header and every pre-hunk header line.
	assert.Equal(t, []string{" ctx", "-old", "+new", " tail"}, files[0].Hunks[0].Lines)
	assert.Equal(t, []string{" keep", "+added"}, files[0].Hunks[1].Lines)

	assert.Equal(t, "b.md", files[1].Path)
	require.Len(t, files[1].Hunks, 1)
	assert.Equal(t, []string{"-before", "+after"}, files[1].Hunks[0].Lines)
}

// The digest is the identity a viewed mark survives a rebase by, and the console computes it
// independently in TypeScript. If the two ever disagree, a hunk marked read in the browser is
// invisible to the CLI and to an agent - silently, and only for some hunks.
//
// The expectation below was computed with the CONSOLE's algorithm, in node, over the same
// bytes: sha256(path || 0x00 || each line + "\n"), first 16 hex characters. It is pinned as a
// literal so a change to either implementation has to confront the other.
func TestHunkDigestMatchesTheConsoleImplementation(t *testing.T) {
	files := ParseHunks(twoFilePatch)
	require.Len(t, files, 2)

	assert.Equal(t, "ff7da6903e60ab8d", files[0].Hunks[0].Digest,
		"Go and the console must agree byte for byte; see console/src/console/diff/session.ts")
	assert.Equal(t, HunkDigest("a.go", []string{" ctx", "-old", "+new", " tail"}),
		files[0].Hunks[0].Digest)
}

// A patch ends in a newline. Splitting on it yields a trailing empty element that is not a
// line of the diff, and letting it reach the body would change the last hunk's digest - which
// unmarks the final hunk of every changeset for whoever had marked it.
func TestTrailingNewlineDoesNotChangeTheLastDigest(t *testing.T) {
	withNewline := ParseHunks(twoFilePatch)
	without := ParseHunks(twoFilePatch[:len(twoFilePatch)-1])

	require.Len(t, withNewline, 2)
	require.Len(t, without, 2)
	assert.Equal(t,
		withNewline[1].Hunks[0].Digest,
		without[1].Hunks[0].Digest,
	)
}

func TestHunkCountsAreWhatAValidatorNeeds(t *testing.T) {
	assert.Equal(t, map[string]int{"a.go": 2, "b.md": 1}, HunkCounts(twoFilePatch))
}

// A path with a space is the case a naive split on " b/" gets wrong.
func TestPathFromGitHeaderHandlesSpaces(t *testing.T) {
	files := ParseHunks("diff --git a/dir/my file.go b/dir/my file.go\n@@ -1 +1 @@\n-a\n+b\n")
	require.Len(t, files, 1)
	assert.Equal(t, "dir/my file.go", files[0].Path)
}

func TestPatchDigestDistinguishesContent(t *testing.T) {
	assert.NotEqual(t, PatchDigest(twoFilePatch), PatchDigest(twoFilePatch+"trailing\n"))
	assert.Equal(t, PatchDigest(twoFilePatch), PatchDigest(twoFilePatch))
	assert.NotEmpty(t, PatchDigest(""))
}

func TestParseHunksOnEmptyOrHeaderOnlyPatch(t *testing.T) {
	assert.Empty(t, ParseHunks(""))
	// A file with no hunks (a pure mode change) is still a file, with nothing to address.
	files := ParseHunks("diff --git a/x b/x\nold mode 100644\nnew mode 100755\n")
	require.Len(t, files, 1)
	assert.Empty(t, files[0].Hunks)
}

// A patch from GNU `diff -u` or from `patch` carries no `diff --git` line at all. Reading only
// git's dialect made these parse to zero files, which the CLI then reported as an empty
// changeset at exit 0 - the shape of a right answer wrapped around a wrong one.
const gnuPatch = "--- old/a.go\t2026-08-25 09:00:00.000000000 -0400\n" +
	"+++ new/a.go\t2026-08-25 09:01:00.000000000 -0400\n" +
	"@@ -1,3 +1,3 @@\n" +
	" ctx\n" +
	"-old\n" +
	"+new\n" +
	"--- old/b.md\t2026-08-25 09:00:00.000000000 -0400\n" +
	"+++ new/b.md\t2026-08-25 09:01:00.000000000 -0400\n" +
	"@@ -1 +1 @@\n" +
	"-before\n" +
	"+after\n"

func TestParseHunksReadsAHeaderlessUnifiedPatch(t *testing.T) {
	files := ParseHunks(gnuPatch)
	require.Len(t, files, 2)
	// The NEW side names the file: it is the one that exists on disk now, which is what
	// every consumer of this path goes on to look up.
	assert.Equal(t, "new/a.go", files[0].Path)
	require.Len(t, files[0].Hunks, 1)
	// The second file's `---` opened a new file rather than landing in the first file's hunk,
	// which would have changed that hunk's digest and unmarked it for anyone who had read it.
	assert.Equal(t, []string{" ctx", "-old", "+new"}, files[0].Hunks[0].Lines)
	assert.Equal(t, "new/b.md", files[1].Path)
	require.Len(t, files[1].Hunks, 1)
}

func TestHeaderPathsSurviveSpacesAndTimestamps(t *testing.T) {
	// A tab separates path from timestamp, which is the only reason a path with spaces is
	// readable; cutting on whitespace would truncate this one at "my".
	assert.Equal(t, "my file.go", stripPathPrefix("a/my file.go\t2026-01-01 09:00:00 -0400"))
	// No timestamp at all is legal and common from hand-rolled tools.
	assert.Equal(t, "x.txt", stripPathPrefix("x.txt"))
	// /dev/null passes through: callers key on it to tell an add from a delete.
	assert.Equal(t, "/dev/null", stripPathPrefix("/dev/null"))
}

// A deletion names /dev/null on the new side, so the old side is the only name the patch
// carries - and a sidebar cannot list "/dev/null".
func TestADeletionIsNamedByItsOldPath(t *testing.T) {
	files := Parse("--- a/gone.go\t2026-01-01\n+++ /dev/null\t2026-01-01\n@@ -1 +0,0 @@\n-bye\n")
	require.Len(t, files, 1)
	assert.Equal(t, "gone.go", files[0].Path)
	assert.Equal(t, StatusDeleted, files[0].Status)
}

// A `-- x` removed line inside a hunk starts with "--- " when the removed text itself begins
// "-- ". Only the `+++` partner on the very next line separates a real header from that, and
// getting it wrong silently splits one file's hunks across two phantom entries.
func TestALoneDashLineStaysInsideItsHunk(t *testing.T) {
	patch := "--- a/x.md\n+++ b/x.md\n@@ -1,2 +1,2 @@\n" +
		"--- not a header, just removed text\n" +
		"+kept\n"
	files := ParseHunks(patch)
	require.Len(t, files, 1)
	assert.Equal(t, []string{"--- not a header, just removed text", "+kept"}, files[0].Hunks[0].Lines)
}

// Mercurial emits `diff -r <rev> <path>` and no `diff --git` line, so a reader keyed on git's
// header saw ZERO files in its output. The untracked half of the same working diff IS
// synthesized with git headers and did parse, which is what made the failure so quiet:
// `magus diff` on an hg tree listed the new files and silently dropped every tracked
// modification, at exit 0.
//
// Only Mercurial. Measured against the installed backends: Sapling's `sl diff` already emits
// git headers, and vcs/jj.go passes --git explicitly, so those two were never affected.
//
// Captured from hg 7.2.3 via `hg diff -U 1`, the exact call vcs/hg.go makes.
const hgPatch = "diff -r b3b854cfd6db f.txt\n" +
	"--- a/f.txt\tTue Aug 25 12:21:53 2026 -0400\n" +
	"+++ b/f.txt\tTue Aug 25 12:22:08 2026 -0400\n" +
	"@@ -1,2 +1,2 @@\n" +
	" a\n" +
	"-b\n" +
	"+B\n" +
	"diff -r b3b854cfd6db g.txt\n" +
	"--- a/g.txt\tTue Aug 25 12:21:53 2026 -0400\n" +
	"+++ b/g.txt\tTue Aug 25 12:22:08 2026 -0400\n" +
	"@@ -1,2 +1,2 @@\n" +
	"-c\n" +
	"+C\n" +
	" d\n"

func TestMercurialDialectParses(t *testing.T) {
	files := ParseHunks(hgPatch)
	require.Len(t, files, 2)
	assert.Equal(t, "f.txt", files[0].Path)
	assert.Equal(t, "g.txt", files[1].Path)
	require.Len(t, files[1].Hunks, 1)
	assert.Equal(t, []string{"-c", "+C", " d"}, files[1].Hunks[0].Lines)
}

// The tracked and untracked halves of one working diff can speak different dialects, because
// magus synthesizes the untracked half with git headers whatever the backend is. Both halves
// have to survive the same pass or the composed patch is worse than either alone.
func TestAPatchMixingBothDialectsKeepsEveryFile(t *testing.T) {
	mixed := hgPatch +
		"diff --git a/new.txt b/new.txt\n" +
		"new file mode 100644\n" +
		"--- /dev/null\n" +
		"+++ b/new.txt\n" +
		"@@ -0,0 +1,1 @@\n" +
		"+fresh\n"
	files := ParseHunks(mixed)
	require.Len(t, files, 3)
	assert.Equal(t, []string{"f.txt", "g.txt", "new.txt"},
		[]string{files[0].Path, files[1].Path, files[2].Path})
}
