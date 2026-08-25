package diff

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
