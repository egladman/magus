package review

import (
	"github.com/egladman/magus/types/diff"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

)

// orderTestDiff has four hunks over three files and an order over them: F first, then its use
// in other.go, then G in core.go, then the generated output.
func orderTestDiff() diff.Diff {
	hunk := func(index, start, count int) diff.Hunk {
		return diff.Hunk{Index: index, NewStart: start, NewCount: count}
	}
	step := func(n int, path string, index int, label string, why diff.Why) diff.Step {
		return diff.Step{Number: n, Hunks: []diff.StepHunk{{Ref: diff.HunkRef{Path: path, Index: index}, Label: label, Why: why}}}
	}
	uses := diff.Why{Relation: diff.WhyUses, Step: 1, Symbol: "F", Text: "uses F, defined in step 1"}
	return diff.Diff{
		Files: []diff.File{
			{Path: "core.go", Hunks: []diff.Hunk{hunk(0, 3, 1), hunk(1, 9, 2)}},
			{Path: "other.go", Hunks: []diff.Hunk{hunk(0, 20, 1)}},
			{Path: "gen/out.json", Role: diff.RoleOutput, Hunks: []diff.Hunk{hunk(0, 1, 1)}},
		},
		Order: &diff.Order{
			Groups: []diff.Group{
				{Kind: diff.GroupConnected, Label: "F", HunkCount: 3, Steps: []diff.Step{
					step(1, "core.go", 0, "F", diff.Why{Relation: diff.WhyStarts, Text: "starts the group"}),
					step(2, "other.go", 0, "Use", uses),
					step(3, "core.go", 1, "G", uses),
				}},
				{Kind: diff.GroupGenerated, HunkCount: 1, Steps: []diff.Step{
					step(4, "gen/out.json", 0, "", diff.Why{Relation: diff.WhyGenerated, Text: "generated output; its source carries the review"}),
				}},
			},
			Count: diff.OrderCount{HunkCount: 4, Placed: 4, Complete: true},
		},
	}
}

func orderText(rev diff.Diff, showGenerated bool) string {
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
	rev.Files[0].Threads = []diff.ThreadRef{
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
	rev.Order.Count = diff.OrderCount{
		HunkCount: 5, Placed: 4,
		Repeated:          []diff.HunkRef{{Path: "core.go", Index: 1}},
		Missing:           []diff.HunkRef{{Path: "other.go", Index: 2}},
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

// withSymbols puts F in core.go's first hunk and M, G in its second, innermost first.
func withSymbols(rev diff.Diff, syms ...diff.Symbol) diff.Diff {
	rev.Files[0].Hunks[0].Symbols = []string{"F"}
	rev.Files[0].Hunks[1].Symbols = []string{"M", "G"}
	rev.Files[0].Symbols = syms
	return rev
}

func TestOrderPrintsEachStepsSignature(t *testing.T) {
	t.Parallel()
	rev := withSymbols(orderTestDiff(),
		diff.Symbol{ID: "G", Label: "G", Signature: "func G(n int)\n\terror"},
		diff.Symbol{ID: "F", Label: "F", Signature: "func F()"},
		diff.Symbol{ID: "M", Label: "M", Signature: "func (T) M()"},
	)

	got := orderText(rev, true)

	assert.Contains(t, got, "    core.go:3  F\n        starts the group\n    signature: func F()\n  step 2\n")
	assert.Contains(t, got, "        uses F, defined in step 1\n    signature: func G(n int) error\n    signature: func (T) M()\n\ngroup 2")
}

func TestOrderMarksAChangedSignature(t *testing.T) {
	t.Parallel()
	rev := withSymbols(orderTestDiff(),
		diff.Symbol{ID: "F", Label: "F", Signature: "func F(n int)", BaseSignature: "func F()"},
		diff.Symbol{ID: "G", Label: "G", Signature: "func G()", BaseSignature: "func G()"},
		diff.Symbol{ID: "M", Label: "M", Signature: "func (T) M()"},
	)

	got := orderText(rev, true)

	assert.Contains(t, got, "    signature: func F(n int) was func F()\n")
	assert.Contains(t, got, "    signature: func G()\n", "an unchanged signature carries no was")
	assert.NotContains(t, got, "func G() was")
	assert.NotContains(t, got, "func (T) M() was", "a symbol with no base signature carries no was")
}

func TestOrderSaysWhenTheIndexerGaveNoSignature(t *testing.T) {
	t.Parallel()
	rev := withSymbols(orderTestDiff(),
		diff.Symbol{ID: "F", Label: "F"},
		diff.Symbol{ID: "G", Label: "G", Signature: "func G()"},
		diff.Symbol{ID: "M", Label: "M"},
	)

	got := orderText(rev, true)

	assert.Contains(t, got, "    signature: F (no signature from the indexer)\n")
	assert.Contains(t, got, "    signature: func G()\n    signature: M (no signature from the indexer)\n")
}
