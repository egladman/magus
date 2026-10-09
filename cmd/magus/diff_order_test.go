package main

import (
	"bytes"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/changeset"
	"github.com/egladman/magus/types"
)

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
func orderTestDiff(t *testing.T) types.Diff {
	t.Helper()
	var rev types.Diff
	digest := map[string]string{}
	key := func(path string, index int) string { return fmt.Sprintf("%s#%d", path, index) }
	for _, f := range changeset.ParseHunks(orderTestPatch) {
		df := types.DiffFile{Path: f.Path}
		if f.Path == "gen/out.json" {
			df.Role = types.DiffRoleOutput
		}
		for _, h := range f.Hunks {
			df.Hunks = append(df.Hunks, types.DiffHunk{Index: h.Index, Digest: h.Digest, NewStart: h.NewStart, NewCount: h.NewCount})
			digest[key(f.Path, h.Index)] = h.Digest
		}
		rev.Files = append(rev.Files, df)
	}
	ref := func(path string, index int) types.DiffHunkRef {
		return types.DiffHunkRef{Path: path, Index: index, Digest: digest[key(path, index)]}
	}
	rev.Order = &types.DiffOrder{
		Groups: []types.DiffGroup{
			{Kind: types.DiffGroupConnected, Label: "F", Hunks: 3, Steps: []types.DiffStep{
				{Number: 1, Hunks: []types.DiffStepHunk{{Hunk: ref("core.go", 0), Label: "F", Why: types.DiffWhy{Relation: types.DiffWhyStarts, Text: "starts the group"}}}},
				{Number: 2, Hunks: []types.DiffStepHunk{{Hunk: ref("other.go", 0), Label: "Use", Why: types.DiffWhy{Relation: types.DiffWhyUses, Step: 1, Symbol: "F", Text: "uses F, defined in step 1"}}}},
				{Number: 3, Hunks: []types.DiffStepHunk{{Hunk: ref("core.go", 1), Label: "G", Why: types.DiffWhy{Relation: types.DiffWhyUses, Step: 1, Symbol: "F", Text: "uses F, defined in step 1"}}}},
			}},
			{Kind: types.DiffGroupGenerated, Hunks: 1, Steps: []types.DiffStep{
				{Number: 4, Hunks: []types.DiffStepHunk{{Hunk: ref("gen/out.json", 0), Why: types.DiffWhy{Relation: types.DiffWhyGenerated, Text: "generated output; its source carries the review"}}}},
			}},
		},
		Count: types.DiffOrderCount{Hunks: 4, Placed: 4, Complete: true},
	}
	return rev
}

func TestPrintDiffOrderNamesEachStepWithItsHunksAndWhy(t *testing.T) {
	rev := orderTestDiff(t)

	var buf bytes.Buffer
	printDiffOrder(&buf, rev, true)

	assert.Equal(t, `
reading order

group 1 of 2: connected around F (3 hunks)
  step 1
    core.go:3  F
        starts the group
  step 2
    other.go:20  Use
        uses F, defined in step 1
  step 3
    core.go:9-10  G
        uses F, defined in step 1

group 2 of 2: generated (1 hunk)
  step 4
    gen/out.json:1
        generated output; its source carries the review

4 hunks, 4 placed
`, buf.String())
}

func TestPrintDiffOrderFoldsGeneratedHunksUnlessShown(t *testing.T) {
	rev := orderTestDiff(t)

	var buf bytes.Buffer
	printDiffOrder(&buf, rev, false)

	assert.Contains(t, buf.String(), "group 2 of 2: generated (1 hunk)\n  folded: a target rewrites these; show them with --generated\n")
	assert.NotContains(t, buf.String(), "gen/out.json:1")
	assert.Contains(t, buf.String(), "4 hunks, 4 placed", "the count still covers the folded hunks")
}

func TestPrintDiffOrderPrintsNothingWithoutAnOrder(t *testing.T) {
	rev := orderTestDiff(t)
	rev.Order = nil

	var buf bytes.Buffer
	printDiffOrder(&buf, rev, true)

	assert.Empty(t, buf.String())
}

func TestPrintDiffOrderNamesWhatItCouldNotAccountFor(t *testing.T) {
	rev := orderTestDiff(t)
	rev.Order.Count = types.DiffOrderCount{
		Hunks: 5, Placed: 4,
		Repeated: []types.DiffHunkRef{{Path: "core.go", Index: 1}},
		Missing:  []types.DiffHunkRef{{Path: "other.go", Index: 2}},
		Bare:     []string{"logo.png", "go.sum"},
	}

	var buf bytes.Buffer
	printDiffOrder(&buf, rev, true)

	assert.Contains(t, buf.String(), "5 hunks, 4 placed; 1 placed more than once: core.go#1; 1 not placed: other.go#2; 2 files with no hunk to show: logo.png, go.sum\n")
}

func TestPrintDiffOrderNamesAHunkThatLeavesNoLine(t *testing.T) {
	rev := orderTestDiff(t)
	rev.Files[0].Hunks[0].NewCount = 0

	var buf bytes.Buffer
	printDiffOrder(&buf, rev, true)

	assert.Contains(t, buf.String(), "    core.go:3 (deleted)  F\n")
}

func TestDiffOrderTUIFilesWalksTheStepsAndKeepsEveryHunkOnce(t *testing.T) {
	rev := orderTestDiff(t)
	parsed := changeset.ParseHunks(orderTestPatch)

	files := diffOrderTUIFiles(rev, parsed)

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
	rev.Files = append(rev.Files, types.DiffFile{Path: "logo.png"})

	files := diffOrderTUIFiles(rev, changeset.ParseHunks(orderTestPatch))

	require.Len(t, files, 5)
	assert.Equal(t, "logo.png", files[4].Path)
	assert.Empty(t, files[4].Hunks)
}

func TestDiffOrderTUIFilesFallsBackToFilesWhenTheOrderIsMissing(t *testing.T) {
	rev := orderTestDiff(t)
	parsed := changeset.ParseHunks(orderTestPatch)

	rev.Order = nil
	assert.Equal(t, diffTUIFiles(rev, parsed), diffOrderTUIFiles(rev, parsed))

	rev = orderTestDiff(t)
	rev.Order.Groups[0].Steps = rev.Order.Groups[0].Steps[:2]
	assert.Equal(t, diffTUIFiles(rev, parsed), diffOrderTUIFiles(rev, parsed), "an order that loses a hunk is not shown")
}

func TestUnreadReportListsHunksNoMarkCovers(t *testing.T) {
	parsed := changeset.ParseHunks(orderTestPatch)
	read := parsed[0].Hunks[0].Digest

	rep := buildUnreadReport("the range a...b", orderTestPatch, []string{read, "digest-of-a-hunk-elsewhere"}, nil)

	assert.Equal(t, unreadKnown, rep.State)
	assert.Equal(t, 4, rep.Hunks)
	require.Len(t, rep.Unread, 3)
	assert.Equal(t, "core.go#1, other.go#0, gen/out.json#0", refList(rep.Unread))

	assert.Equal(t, []string{
		"3 of 4 hunks in the range a...b are not marked read",
		"  core.go:9-10",
		"  other.go:20",
		"  gen/out.json:1",
		"mark them read in the viewer: magus diff --rev a...b",
	}, unreadLines(rep, orderTestPatch, "magus diff --rev a...b"))
}

func TestUnreadReportSaysUnknownWhenTheMarksCannotBeRead(t *testing.T) {
	rep := buildUnreadReport("the working tree", orderTestPatch, nil, errors.New("read marks: permission denied"))

	assert.Equal(t, unreadUnknown, rep.State)
	assert.Empty(t, rep.Unread, "an unreadable store says nothing about what is unread")
	assert.Equal(t, 4, rep.Hunks)
	assert.Equal(t, []string{
		"read state unknown for the working tree: the read marks could not be read (read marks: permission denied)",
		"4 hunks in the range; none is called unread",
	}, unreadLines(rep, orderTestPatch, ""))
}

func TestUnreadReportOfAFullyReadChangesetSaysSo(t *testing.T) {
	var all []string
	for _, f := range changeset.ParseHunks(orderTestPatch) {
		for _, h := range f.Hunks {
			all = append(all, h.Digest)
		}
	}

	rep := buildUnreadReport("the working tree", orderTestPatch, all, nil)

	assert.Empty(t, rep.Unread)
	assert.Equal(t, []string{"every hunk of the working tree is marked read (4 hunks)"}, unreadLines(rep, orderTestPatch, ""))
}
