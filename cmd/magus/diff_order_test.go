package main

import (
	"bytes"
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
	for _, f := range changeset.ParseHunks(orderTestPatch) {
		df := types.DiffFile{Path: f.Path}
		if f.Path == "gen/out.json" {
			df.Role = types.DiffRoleOutput
		}
		for _, h := range f.Hunks {
			df.Hunks = append(df.Hunks, types.DiffHunk{Index: h.Index, Digest: h.Digest, NewStart: h.NewStart, NewCount: h.NewCount})
			digest[types.DiffHunkRef{Path: f.Path, Index: h.Index}.Key()] = h.Digest
		}
		rev.Files = append(rev.Files, df)
	}
	ref := func(path string, index int) types.DiffHunkRef {
		r := types.DiffHunkRef{Path: path, Index: index}
		r.Digest = digest[r.Key()]
		return r
	}
	rev.Order = &types.DiffOrder{
		Groups: []types.DiffGroup{
			{Kind: types.DiffGroupConnected, Label: "F", HunkCount: 3, Steps: []types.DiffStep{
				{Number: 1, Hunks: []types.DiffStepHunk{{Ref: ref("core.go", 0), Label: "F", Why: types.DiffWhy{Relation: types.DiffWhyStarts, Text: "starts the group"}}}},
				{Number: 2, Hunks: []types.DiffStepHunk{{Ref: ref("other.go", 0), Label: "Use", Why: types.DiffWhy{Relation: types.DiffWhyUses, Step: 1, Symbol: "F", Text: "uses F, defined in step 1"}}}},
				{Number: 3, Hunks: []types.DiffStepHunk{{Ref: ref("core.go", 1), Label: "G", Why: types.DiffWhy{Relation: types.DiffWhyUses, Step: 1, Symbol: "F", Text: "uses F, defined in step 1"}}}},
			}},
			{Kind: types.DiffGroupGenerated, HunkCount: 1, Steps: []types.DiffStep{
				{Number: 4, Hunks: []types.DiffStepHunk{{Ref: ref("gen/out.json", 0), Why: types.DiffWhy{Relation: types.DiffWhyGenerated, Text: "generated output; its source carries the review"}}}},
			}},
		},
		Count: types.DiffOrderCount{HunkCount: 4, Placed: 4, Complete: true},
	}
	return rev
}

func TestPrintDiffOrderWritesTheRenderedOrderToTheWriter(t *testing.T) {
	rev := orderTestDiff(t)

	var buf bytes.Buffer
	printDiffOrder(&buf, rev, false)

	assert.Contains(t, buf.String(), "\nreading order\n\ngroup 1 of 2: connected around F (3 hunks)\n  step 1\n    core.go:3  F\n")
	assert.Contains(t, buf.String(), "group 2 of 2: generated (1 hunk)\n  folded:")
	assert.True(t, bytes.HasSuffix(buf.Bytes(), []byte("4 hunks, 4 placed\n")))
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

func TestDiffOrderTUIFilesFallsBackToFilesWhenTheOrderIsMissingOrIncomplete(t *testing.T) {
	rev := orderTestDiff(t)
	parsed := changeset.ParseHunks(orderTestPatch)

	rev.Order = nil
	assert.Equal(t, diffTUIFiles(rev, parsed), diffOrderTUIFiles(rev, parsed))

	rev = orderTestDiff(t)
	rev.Order.Groups[0].Steps = rev.Order.Groups[0].Steps[:2]
	rev.Order.Count = types.DiffOrderCount{HunkCount: 4, Placed: 3, Missing: []types.DiffHunkRef{rev.Order.Groups[0].Steps[0].Hunks[0].Ref}}
	assert.Equal(t, diffTUIFiles(rev, parsed), diffOrderTUIFiles(rev, parsed), "an order whose count is incomplete is not shown")
}

func TestDiffOrderTUIFilesFallsBackWhenAPlacedHunkIsNotInThePatch(t *testing.T) {
	rev := orderTestDiff(t)
	rev.Order.Groups[0].Steps[0].Hunks[0].Ref.Index = 7
	parsed := changeset.ParseHunks(orderTestPatch)

	assert.Equal(t, diffTUIFiles(rev, parsed), diffOrderTUIFiles(rev, parsed))
}
