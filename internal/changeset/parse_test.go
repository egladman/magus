package changeset

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

// These cases came from the console's parse.test.ts when the two readers were consolidated
// here. They are the behaviors the browser reader had and this one did not, so porting them
// with the parser is what makes the consolidation a merge rather than a replacement.

func TestParseNumbersLinesOnBothSides(t *testing.T) {
	files := Parse("diff --git a/a.go b/a.go\n" +
		"--- a/a.go\n+++ b/a.go\n" +
		"@@ -10,3 +10,4 @@ func A()\n" +
		" keep\n-gone\n+added\n+more\n")
	require.Len(t, files, 1)
	h := files[0].Hunks[0]
	require.Len(t, h.Rows, 4)

	num := func(p *int) int { require.NotNil(t, p); return *p }
	assert.Equal(t, KindContext, h.Rows[0].Kind)
	assert.Equal(t, 10, num(h.Rows[0].OldLine))
	assert.Equal(t, 10, num(h.Rows[0].NewLine))
	// A deletion advances only the old side; an addition only the new.
	assert.Nil(t, h.Rows[1].NewLine)
	assert.Equal(t, 11, num(h.Rows[1].OldLine))
	assert.Nil(t, h.Rows[2].OldLine)
	assert.Equal(t, 11, num(h.Rows[2].NewLine))
	assert.Equal(t, 12, num(h.Rows[3].NewLine))

	assert.Equal(t, 2, files[0].Additions)
	assert.Equal(t, 1, files[0].Deletions)
}

// `@@ -1 +1 @@` means one line per side. Reading the absent count as 0 rather than 1 silently
// drops every single-line hunk.
func TestAHunkHeaderWithoutCountsMeansOneLinePerSide(t *testing.T) {
	files := Parse("diff --git a/a.go b/a.go\n@@ -1 +1 @@\n-x\n+y\n")
	require.Len(t, files, 1)
	h := files[0].Hunks[0]
	assert.Equal(t, 1, h.OldCount)
	assert.Equal(t, 1, h.NewCount)
}

func TestStatusIsReadFromTheExtendedHeaders(t *testing.T) {
	added := Parse("diff --git a/new.go b/new.go\nnew file mode 100644\n--- /dev/null\n+++ b/new.go\n@@ -0,0 +1 @@\n+hello\n")
	require.Len(t, added, 1)
	assert.Equal(t, StatusAdded, added[0].Status)
	assert.Equal(t, "new.go", added[0].Path)
	assert.Equal(t, "100644", added[0].NewMode)

	// A deletion is identified by the OLD path. "/dev/null" must never reach a sidebar.
	deleted := Parse("diff --git a/gone.go b/gone.go\ndeleted file mode 100644\n--- a/gone.go\n+++ /dev/null\n@@ -1 +0,0 @@\n-bye\n")
	require.Len(t, deleted, 1)
	assert.Equal(t, StatusDeleted, deleted[0].Status)
	assert.Equal(t, "gone.go", deleted[0].Path)
	assert.NotEqual(t, "/dev/null", deleted[0].Path)

	renamed := Parse("diff --git a/old.go b/new.go\nrename from old.go\nrename to new.go\n")
	require.Len(t, renamed, 1)
	assert.Equal(t, StatusRenamed, renamed[0].Status)
	assert.Equal(t, "old.go", renamed[0].OldPath)
	assert.Equal(t, "new.go", renamed[0].Path)
}

// A pure mode change produces no hunks at all, so without the modes it renders as an empty
// entry - a script becoming executable is a real reviewable event.
func TestAPureModeChangeIsCapturedWithNoHunks(t *testing.T) {
	files := Parse("diff --git a/run.sh b/run.sh\nold mode 100644\nnew mode 100755\n")
	require.Len(t, files, 1)
	assert.Empty(t, files[0].Hunks)
	assert.Equal(t, "100644", files[0].OldMode)
	assert.Equal(t, "100755", files[0].NewMode)
}

// A binary file carries no hunks either, and rendering it as an empty diff reads as "nothing
// changed", which is false.
func TestABinaryFileIsFlaggedRatherThanShownEmpty(t *testing.T) {
	files := Parse("diff --git a/logo.png b/logo.png\nindex 111..222 100644\nBinary files a/logo.png and b/logo.png differ\n")
	require.Len(t, files, 1)
	assert.True(t, files[0].Binary)
	assert.Empty(t, files[0].Hunks)
}

// The marker describes the PRECEDING line and is a line of neither file, so it must consume no
// line number on either side - counting it shifts every number below it.
func TestTheNoNewlineMarkerIsMetaAndConsumesNoLineNumber(t *testing.T) {
	files := Parse("diff --git a/a.txt b/a.txt\n@@ -1,2 +1,2 @@\n-old\n\\ No newline at end of file\n+new\n")
	require.Len(t, files, 1)
	rows := files[0].Hunks[0].Rows
	require.Len(t, rows, 3)
	assert.Equal(t, KindMeta, rows[1].Kind)
	assert.Nil(t, rows[1].OldLine)
	assert.Nil(t, rows[1].NewLine)
	// The addition after it still numbers from where the hunk header said.
	require.NotNil(t, rows[2].NewLine)
	assert.Equal(t, 1, *rows[2].NewLine)
}

// Some producers drop the trailing space on an empty context line. Treating a bare "" as a
// terminator instead would cut every hunk short at its first blank line.
func TestAFullyBlankLineInsideAHunkStaysContext(t *testing.T) {
	files := Parse("diff --git a/a.txt b/a.txt\n@@ -1,3 +1,3 @@\n ctx\n\n+added\n")
	require.Len(t, files, 1)
	rows := files[0].Hunks[0].Rows
	require.Len(t, rows, 3)
	assert.Equal(t, KindContext, rows[1].Kind)
	assert.Equal(t, "", rows[1].Text)
}

// A path may contain spaces, so the header is split on " b/" rather than on whitespace.
func TestAPathContainingSpacesSurvivesTheHeaderSplit(t *testing.T) {
	files := Parse("diff --git a/my dir/my file.go b/my dir/my file.go\n@@ -1 +1 @@\n-x\n+y\n")
	require.Len(t, files, 1)
	assert.Equal(t, "my dir/my file.go", files[0].Path)
}

func TestSeveralFilesEachKeepTheirOwnHunks(t *testing.T) {
	files := Parse(twoFilePatch)
	require.Len(t, files, 2)
	assert.Len(t, files[0].Hunks, 2)
	assert.Len(t, files[1].Hunks, 1)
	// Index is per file, not per patch.
	assert.Equal(t, 0, files[1].Hunks[0].Index)
}

// The identity view: paths, hunk digests and the whole-patch digest, without the
// rendering detail. Same reader, projected - see ParseHunks.

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

// Intra-line emphasis: which PART of a changed line changed. Computed once, here, and shipped
// to both surfaces - these cases came from the terminal viewer's own test when the second
// implementation was removed, and they are the behavior both readers now share.
//
// The table names what each line's span SELECTS rather than its offsets, because the offsets
// are a coordinate and the selected text is the claim.
func TestEmphasisMarksOnlyThePairedRewrite(t *testing.T) {
	tests := []struct {
		name  string
		lines []string
		want  []string
	}{
		{"one changed argument is the only part emphasised",
			[]string{" ctx", "-call(a, b)", "+call(a, c)"},
			[]string{"", "b", "c"}},
		{"a wholly different line has nothing to point at",
			[]string{"-one", "+two"},
			[]string{"", ""}},
		{"runs of unequal length are not paired at all",
			[]string{"-one", "+two", "+three"},
			[]string{"", "", ""}},
		{"an insertion has no partner to differ from",
			[]string{" ctx", "+added"},
			[]string{"", ""}},
		{"a two-line rewrite pairs positionally",
			[]string{"-let a = 1", "-let b = 2", "+let a = 9", "+let b = 8"},
			[]string{"1", "2", "9", "8"}},
		{"a second run pairs on its own",
			[]string{"-call(a, b)", "+call(a, c)", " ctx", "-x := 1", "+x := 2"},
			[]string{"b", "c", "", "1", "2"}},
		{"a multi-byte prefix does not shift what the span selects",
			[]string{`-x = "` + "αβγ" + ` one"`, `+x = "` + "αβγ" + ` two"`},
			[]string{"one", "two"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			files := Parse("diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1 +1 @@\n" +
				strings.Join(tc.lines, "\n") + "\n")
			require.Len(t, files, 1)
			require.Len(t, files[0].Hunks, 1)
			h := files[0].Hunks[0]

			// Read back through the RAW-line view, which is the one a terminal slices: it
			// exercises the UTF-16 offsets the browser gets AND the conversion back, so a bug
			// in either shows up as the wrong word rather than as a number nobody can read.
			spans := RawLineEmphasis(h)
			got := make([]string, 0, len(h.Lines))
			for i, line := range h.Lines {
				if i >= len(spans) || spans[i].Empty() {
					got = append(got, "")
					continue
				}
				got = append(got, line[spans[i].Start:spans[i].End])
			}
			assert.Equal(t, tc.want, got)
		})
	}
}

// The renderer slices the raw line with these numbers, so a span counted in RUNES would not
// merely highlight the wrong word - it would cut a rune in half and hand the terminal invalid
// UTF-8. Three two-byte runes sit ahead of the change, so byte and rune offsets differ.
func TestEmphasisSpansAreByteOffsetsIntoTheRawLine(t *testing.T) {
	files := Parse("diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1 +1 @@\n" +
		`-x = "` + "αβγ" + ` one"` + "\n" +
		`+x = "` + "αβγ" + ` two"` + "\n")
	require.Len(t, files, 1)
	h := files[0].Hunks[0]
	spans := RawLineEmphasis(h)
	require.Len(t, spans, 2)

	assert.Equal(t, 13, spans[0].Start, "the marker plus five ASCII bytes plus six bytes of Greek")
	assert.Equal(t, 16, spans[0].End)
	assert.Equal(t, "one", h.Lines[0][spans[0].Start:spans[0].End])

	// The same change, in the UTF-16 units the browser indexes by: three Greek characters are
	// one unit each there, so the offset is three smaller and the marker is not counted.
	require.NotNil(t, h.Rows[0].Emph)
	assert.Equal(t, 9, h.Rows[0].Emph.Start)
	assert.Equal(t, 12, h.Rows[0].Emph.End)
}

// A thread is anchored to a line of the REVIEW, and the review is not the changeset in front of
// the reader: the working tree moves, and a pull request covers commits a working diff does not.
// So placement is resolved here, once, and both surfaces read the answer.
func TestPlaceThreadsResolvesALineOntoItsHunk(t *testing.T) {
	files := ParseHunks("diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n" +
		"@@ -10,3 +10,3 @@\n ten\n-old\n+new\n" +
		"@@ -40,2 +40,2 @@\n-x\n+y\n")
	require.Len(t, files, 1)
	require.Len(t, files[0].Hunks, 2)

	got := PlaceThreads(files, []types.ReviewThread{
		{ID: "t1", Path: "a.go", Line: 11},
		{ID: "t2", Path: "a.go", Line: 40},
		// The line moved out from under this remark. It still belongs to the file, and the
		// caller renders it there rather than dropping it.
		{ID: "t3", Path: "a.go", Line: 900},
		// A file this changeset does not touch has no hunks at all to search.
		{ID: "t4", Path: "other.go", Line: 2},
	})

	assert.Equal(t, 0, got[0].Hunk, "the first hunk holds line 11")
	assert.Equal(t, 1, got[1].Hunk, "the second holds line 40")
	assert.Equal(t, -1, got[2].Hunk, "a line outside every hunk is unplaced, not dropped")
	assert.Equal(t, -1, got[3].Hunk, "and so is a file outside the changeset")
	assert.Len(t, got, 4, "every thread survives placement")
}

// The NEW side, always: a host anchors an inline comment to the line as it stands after the
// change, and matching the old side would land a remark about new code on whatever used to be
// there. This hunk's two sides deliberately disagree about which lines they cover.
func TestPlaceThreadsMatchesTheNewSideNotTheOld(t *testing.T) {
	files := ParseHunks("diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n" +
		"@@ -100,2 +5,2 @@\n-old\n+new\n")
	got := PlaceThreads(files, []types.ReviewThread{
		{ID: "new", Path: "a.go", Line: 5},
		{ID: "old", Path: "a.go", Line: 100},
	})
	assert.Equal(t, 0, got[0].Hunk, "a new-side line places")
	assert.Equal(t, -1, got[1].Hunk, "an old-side line does not")
}
