package difftui

import (
	"testing"

	"github.com/egladman/magus/internal/changeset"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types/diff"
)

// TestDiffTUIFilesJoinKeepsTheAnnotationOrder guards the one thing this join must not do:
// re-derive an order. The patch arrives in whatever order the VCS emitted and the
// annotations arrive in reading order, so following the patch would quietly undo
// SortForReading and put a lockfile back at the top.
func TestDiffTUIFilesJoinKeepsTheAnnotationOrder(t *testing.T) {
	reach := 12
	rev := diff.Diff{Files: []diff.File{
		{Path: "core.go", Role: diff.RoleSource, Visibility: diff.VisibilityInternal, Reach: &reach, Project: "root"},
		{Path: "gen/out.json", Role: diff.RoleOutput, Visibility: diff.VisibilityUnknown},
	}}
	patch := "diff --git a/gen/out.json b/gen/out.json\n" +
		"@@ -1 +1 @@\n" +
		"-{}\n" +
		"+{\"a\":1}\n" +
		"diff --git a/core.go b/core.go\n" +
		"@@ -3 +3 @@\n" +
		"+func F() {}\n" +
		"@@ -9 +9 @@\n" +
		"+func G() {}\n"

	files := joinFiles(rev, changeset.ParseHunks(patch))
	require.Len(t, files, 2)
	require.Len(t, files[0].Hunks, 2)
	hunks := files[0].Hunks
	assert.NotEmpty(t, hunks[0].Digest, "the viewed set is keyed by this")
	// Index is the patch coordinate a comment is anchored by, carried rather than re-derived:
	// dropping it here would hand the viewer a second hunk numbered zero.
	assert.Equal(t, File{
		Path:  "core.go",
		Facts: []string{"12 files reference its widest changed symbol", "in root"},
		Hunks: []Hunk{
			{Index: 0, Header: "@@ -3 +3 @@", Lines: []string{"+func F() {}"}, NewStart: 3, Digest: hunks[0].Digest, Emph: hunks[0].Emph},
			{Index: 1, Header: "@@ -9 +9 @@", Lines: []string{"+func G() {}"}, NewStart: 9, Digest: hunks[1].Digest, Emph: hunks[1].Emph},
		},
	}, files[0])

	assert.Equal(t, "gen/out.json", files[1].Path)
	assert.True(t, files[1].Generated)
	require.Len(t, files[1].Hunks, 1)
}

// TestDiffTUIFilesHandsTheViewerTheEscapedForm covers the wiring rather than the sanitizer.
//
// internal/diff pins that Parse escapes both rendered forms, and that is where the sanitizing is
// tested. What NOTHING held until here is that the terminal viewer is handed the escaped one: the
// whole defence is a single displayOr call in the join below, and writing h.Lines there leaves
// every bidi test green while the viewer draws the deception. A control with one enforcement site
// and no test on that site is the shape this changeset kept finding.
func TestDiffTUIFilesHandsTheViewerTheEscapedForm(t *testing.T) {
	// Written as an escape, never as the character: bidichk gates this repo's own source, and a
	// test file carrying a raw override is one where the deception is invisible to its reviewer.
	const rlo = "\u202e"
	rev := diff.Diff{Files: []diff.File{
		{Path: "auth.go", Role: diff.RoleSource},
	}}
	patch := "diff --git a/auth.go b/auth.go\n" +
		"--- a/auth.go\n+++ b/auth.go\n" +
		"@@ -1,2 +1,2 @@ func Check()\n" +
		" ok\n" +
		"+\tif admin { // " + rlo + " } trap\n"

	files := joinFiles(rev, changeset.ParseHunks(patch))

	require.Len(t, files, 1)
	require.Len(t, files[0].Hunks, 1)
	lines := files[0].Hunks[0].Lines
	require.Len(t, lines, 2)
	assert.Contains(t, lines[1], "<U+202E>", "the viewer is shown where the reordering was")
	for _, l := range lines {
		assert.NotContains(t, l, rlo, "no line reaches the viewer able to reorder itself")
	}
}

// The control for the one above: an honest patch reaches the viewer byte for byte. Display is nil
// for every ordinary hunk, so a join that read it unconditionally would hand the viewer nothing.
func TestDiffTUIFilesLeavesAnHonestPatchAlone(t *testing.T) {
	rev := diff.Diff{Files: []diff.File{{Path: "a.go", Role: diff.RoleSource}}}
	patch := "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1 +1 @@\n-old\n+new\n"

	files := joinFiles(rev, changeset.ParseHunks(patch))

	require.Len(t, files, 1)
	require.Len(t, files[0].Hunks, 1)
	assert.Equal(t, []string{"-old", "+new"}, files[0].Hunks[0].Lines)
}

const orderTestPatch = "diff --git a/core.go b/core.go\n" +
	"@@ -3 +3 @@\n" +
	"+func F() {}\n" +
	"@@ -9 +9,2 @@\n" +
	"+func G() { F() }\n" +
	"+func H() {}\n" +
	"diff --git a/other.go b/other.go\n" +
	"@@ -20 +20 @@\n" +
	"+var _ = F\n" +
	"diff --git a/gen/out.json b/gen/out.json\n" +
	"@@ -1 +1 @@\n" +
	"-{}\n" +
	"+{\"a\":1}\n"

// orderTestDiff is orderTestPatch with its hunks attached to the files and an order over them:
// F first, then its use in other.go, then G in core.go, then the generated output.
func orderTestDiff(t *testing.T) diff.Diff {
	t.Helper()
	var rev diff.Diff
	digest := map[string]string{}
	for _, f := range changeset.ParseHunks(orderTestPatch) {
		df := diff.File{Path: f.Path}
		if f.Path == "gen/out.json" {
			df.Role = diff.RoleOutput
		}
		for _, h := range f.Hunks {
			df.Hunks = append(df.Hunks, diff.Hunk{Index: h.Index, Digest: h.Digest, NewStart: h.NewStart, NewCount: h.NewCount})
			digest[diff.HunkRef{Path: f.Path, Index: h.Index}.Key()] = h.Digest
		}
		rev.Files = append(rev.Files, df)
	}
	ref := func(path string, index int) diff.HunkRef {
		r := diff.HunkRef{Path: path, Index: index}
		r.Digest = digest[r.Key()]
		return r
	}
	rev.Order = &diff.Order{
		Groups: []diff.Group{
			{Kind: diff.GroupConnected, Label: "F", HunkCount: 3, Steps: []diff.Step{
				{Number: 1, Hunks: []diff.StepHunk{{Ref: ref("core.go", 0), Label: "F", Why: diff.Why{Relation: diff.WhyStarts, Text: "starts the group"}}}},
				{Number: 2, Hunks: []diff.StepHunk{{Ref: ref("other.go", 0), Label: "Use", Why: diff.Why{Relation: diff.WhyUses, Step: 1, Symbol: "F", Text: "uses F, defined in step 1"}}}},
				{Number: 3, Hunks: []diff.StepHunk{{Ref: ref("core.go", 1), Label: "G", Why: diff.Why{Relation: diff.WhyUses, Step: 1, Symbol: "F", Text: "uses F, defined in step 1"}}}},
			}},
			{Kind: diff.GroupGenerated, HunkCount: 1, Steps: []diff.Step{
				{Number: 4, Hunks: []diff.StepHunk{{Ref: ref("gen/out.json", 0), Why: diff.Why{Relation: diff.WhyGenerated, Text: "generated output; its source carries the review"}}}},
			}},
		},
		Count: diff.OrderCount{HunkCount: 4, Placed: 4, Complete: true},
	}
	return rev
}

func TestDiffOrderTUIFilesWalksTheStepsAndKeepsEveryHunkOnce(t *testing.T) {
	rev := orderTestDiff(t)
	parsed := changeset.ParseHunks(orderTestPatch)

	files := OrderedFiles(rev, parsed)

	var walked []string
	seen := map[string]int{}
	for _, f := range files {
		for _, h := range f.Hunks {
			walked = append(walked, f.Path)
			seen[h.Digest]++
		}
	}
	assert.Equal(t, []string{"core.go", "other.go", "core.go", "gen/out.json"}, walked, "one file per step and path, in step order")
	require.Len(t, files, 4)
	for digest, n := range seen {
		assert.Equal(t, 1, n, "hunk %s is shown once", digest)
	}
	assert.Equal(t, 4, len(seen))

	assert.Equal(t, []string{"step 1 of 4, connected around F (3 hunks)", "F: starts the group"}, files[0].Facts)
	assert.Equal(t, []string{"step 2 of 4, connected around F (3 hunks)", "Use: uses F, defined in step 1"}, files[1].Facts)
	assert.False(t, files[0].Generated)
	assert.True(t, files[3].Generated, "the generated group folds like generated files do today")
	assert.Equal(t, 3, files[0].Hunks[0].NewStart, "the viewer's hunk keeps the patch's coordinates")
}

func TestDiffOrderTUIFilesKeepsAFileWithNoHunk(t *testing.T) {
	rev := orderTestDiff(t)
	rev.Files = append(rev.Files, diff.File{Path: "logo.png"})

	files := OrderedFiles(rev, changeset.ParseHunks(orderTestPatch))

	require.Len(t, files, 5)
	assert.Equal(t, "logo.png", files[4].Path)
	assert.Empty(t, files[4].Hunks)
}

func TestDiffOrderTUIFilesFallsBackToFilesWhenTheOrderIsMissingOrIncomplete(t *testing.T) {
	rev := orderTestDiff(t)
	parsed := changeset.ParseHunks(orderTestPatch)

	rev.Order = nil
	assert.Equal(t, joinFiles(rev, parsed), OrderedFiles(rev, parsed))

	rev = orderTestDiff(t)
	rev.Order.Groups[0].Steps = rev.Order.Groups[0].Steps[:2]
	rev.Order.Count = diff.OrderCount{HunkCount: 4, Placed: 3, Missing: []diff.HunkRef{rev.Order.Groups[0].Steps[0].Hunks[0].Ref}}
	assert.Equal(t, joinFiles(rev, parsed), OrderedFiles(rev, parsed), "an order whose count is incomplete is not shown")
}

func TestDiffOrderTUIFilesFallsBackWhenAPlacedHunkIsNotInThePatch(t *testing.T) {
	rev := orderTestDiff(t)
	rev.Order.Groups[0].Steps[0].Hunks[0].Ref.Index = 7
	parsed := changeset.ParseHunks(orderTestPatch)

	assert.Equal(t, joinFiles(rev, parsed), OrderedFiles(rev, parsed))
}
