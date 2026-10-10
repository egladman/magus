package review

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/egladman/magus/types"
)

// orderTestDiff has four hunks over three files and an order over them: F first, then its use
// in other.go, then G in core.go, then the generated output.
func orderTestDiff() types.Diff {
	hunk := func(index, start, count int) types.DiffHunk {
		return types.DiffHunk{Index: index, NewStart: start, NewCount: count}
	}
	step := func(n int, path string, index int, label string, why types.DiffWhy) types.DiffStep {
		return types.DiffStep{Number: n, Hunks: []types.DiffStepHunk{{Ref: types.DiffHunkRef{Path: path, Index: index}, Label: label, Why: why}}}
	}
	uses := types.DiffWhy{Relation: types.DiffWhyUses, Step: 1, Symbol: "F", Text: "uses F, defined in step 1"}
	return types.Diff{
		Files: []types.DiffFile{
			{Path: "core.go", Hunks: []types.DiffHunk{hunk(0, 3, 1), hunk(1, 9, 2)}},
			{Path: "other.go", Hunks: []types.DiffHunk{hunk(0, 20, 1)}},
			{Path: "gen/out.json", Role: types.DiffRoleOutput, Hunks: []types.DiffHunk{hunk(0, 1, 1)}},
		},
		Order: &types.DiffOrder{
			Groups: []types.DiffGroup{
				{Kind: types.DiffGroupConnected, Label: "F", HunkCount: 3, Steps: []types.DiffStep{
					step(1, "core.go", 0, "F", types.DiffWhy{Relation: types.DiffWhyStarts, Text: "starts the group"}),
					step(2, "other.go", 0, "Use", uses),
					step(3, "core.go", 1, "G", uses),
				}},
				{Kind: types.DiffGroupGenerated, HunkCount: 1, Steps: []types.DiffStep{
					step(4, "gen/out.json", 0, "", types.DiffWhy{Relation: types.DiffWhyGenerated, Text: "generated output; its source carries the review"}),
				}},
			},
			Count: types.DiffOrderCount{HunkCount: 4, Placed: 4, Complete: true},
		},
	}
}

func orderText(rev types.Diff, showGenerated bool) string {
	return strings.Join(OrderLines(rev, showGenerated), "\n") + "\n"
}

func TestOrderLinesNameEachStepWithItsHunksAndWhy(t *testing.T) {
	t.Parallel()

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
`, orderText(orderTestDiff(), true))
}

// TestOrderLinesNameEachThreadBesideItsHunk. The report is where a person finds the id
// `magus diff --thread` takes, so a thread sits under the hunk its first comment is on; one on no
// hunk of the changeset is not drawn here.
func TestOrderLinesNameEachThreadBesideItsHunk(t *testing.T) {
	t.Parallel()
	rev := orderTestDiff()
	rev.Files[0].Threads = []types.DiffThreadRef{
		{ID: "2193847561", Hunk: 1, Line: 9, Comments: 3},
		{ID: "77", Hunk: -1, Line: 40, Comments: 1, Outdated: true},
	}

	assert.Contains(t, orderText(rev, true),
		"    core.go:9-10  G\n        uses F, defined in step 1\n        thread 2193847561, 3 comments\n")
	assert.NotContains(t, orderText(rev, true), "thread 77")
}

func TestOrderLinesFoldGeneratedHunksUnlessShown(t *testing.T) {
	t.Parallel()

	got := orderText(orderTestDiff(), false)

	assert.Contains(t, got, "group 2 of 2: generated (1 hunk)\n  folded: a target rewrites these; show them with --generated\n")
	assert.NotContains(t, got, "gen/out.json:1")
	assert.Contains(t, got, "4 hunks, 4 placed", "the count still covers the folded hunks")
}

func TestOrderLinesAreEmptyWithoutAnOrder(t *testing.T) {
	t.Parallel()

	rev := orderTestDiff()
	rev.Order = nil

	assert.Empty(t, OrderLines(rev, true))
}

func TestOrderLinesNameWhatTheOrderCouldNotAccountFor(t *testing.T) {
	t.Parallel()

	rev := orderTestDiff()
	rev.Order.Count = types.DiffOrderCount{
		HunkCount: 5, Placed: 4,
		Repeated:          []types.DiffHunkRef{{Path: "core.go", Index: 1}},
		Missing:           []types.DiffHunkRef{{Path: "other.go", Index: 2}},
		FilesWithoutHunks: []string{"logo.png", "go.sum"},
	}

	assert.Contains(t, orderText(rev, true), "5 hunks, 4 placed; 1 placed more than once: core.go#1; 1 not placed: other.go#2; 2 files with no hunk to show: logo.png, go.sum\n")
}

func TestOrderLinesNameAHunkThatLeavesNoLine(t *testing.T) {
	t.Parallel()

	rev := orderTestDiff()
	rev.Files[0].Hunks[0].NewCount = 0

	assert.Contains(t, orderText(rev, true), "    core.go:3 (deleted)  F\n")
}
