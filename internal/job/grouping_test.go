package job

import (
	"fmt"
	"testing"

	"github.com/egladman/magus/internal/ci/volatility"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// worked is a passing outcome that ran durationMs up to ended, with a work profile that
// loaded setup of context tokens before its first write.
func worked(job string, ended, durationMs, setup, contextTokens int64, projects ...string) types.JobOutcome {
	return types.JobOutcome{
		Job: job, State: types.StatePass, Ended: ended, DurationMs: durationMs, WriteFiles: 1,
		Projects: projects,
		Work: &types.JobWork{
			Calls: 10, Writes: 2, SetupCalls: 4, Denials: 1,
			SetupTokens: new(setup), SetupContext: new(setup), ContextTokens: new(contextTokens),
		},
	}
}

func shapeRow(t *testing.T, out types.GroupingOutput, shape types.GroupingShape) types.GroupingRecord {
	t.Helper()
	for _, r := range out.Rows {
		if r.Shape == shape {
			return r
		}
	}
	require.Failf(t, "no row", "the lens has no %s row", shape)
	return types.GroupingRecord{}
}

func TestGroupingRecordsMeasuresSplitGroupsAsAWhole(t *testing.T) {
	t.Parallel()

	var outcomes []types.JobOutcome
	for g := range types.GroupingMinSample {
		for _, part := range []string{"a", "b"} {
			o := worked(fmt.Sprintf("g%d-%s", g, part), 10_000, 4_000, 1_000, 4_000, "internal/"+part)
			o.Parent, o.ParentAttempt = fmt.Sprintf("root%d", g), 1
			outcomes = append(outcomes, o)
		}
		outcomes[len(outcomes)-1].Ended = 12_000 // the group spans 6s, not either job's 4s
	}

	got := shapeRow(t, GroupingRecords(outcomes, nil), types.GroupingSplit)

	pass := volatility.WilsonLower(10, 10)
	assert.Equal(t, types.GroupingRecord{
		Shape: types.GroupingSplit, Verdict: types.GroupingMeasured, Jobs: 10, Groups: 5, Known: 10,
		PassLower: &pass, SetupTokensMedian: new(int64(1_000)), SetupShare: new(0.25),
		ContextPerGroup: new(8_000.0), WallMsPerGroup: new(6_000.0), DenialsPerJob: new(1.0),
		UnattributedPerJob: new(0.0), ReworkPerJob: new(0.0), WideningsPerJob: new(0.0),
	}, got)
}

// A re-forked parent's children are another fan-out, and an only child was never split.
func TestGroupingRecordsGroupsSiblingsByTheParentsAttempt(t *testing.T) {
	t.Parallel()

	first := worked("a", 1, 1, 1, 1, "internal/a")
	first.Parent, first.ParentAttempt = "root", 1
	second := worked("b", 1, 1, 1, 1, "internal/b")
	second.Parent, second.ParentAttempt = "root", 2
	chained := worked("c", 1, 1, 1, 1, "internal/a", "types")

	out := GroupingRecords([]types.JobOutcome{first, second, chained}, nil)

	assert.Equal(t, 0, shapeRow(t, out, types.GroupingSplit).Jobs)
	assert.Equal(t, 2, shapeRow(t, out, types.GroupingSolo).Groups)
	assert.Equal(t, 1, shapeRow(t, out, types.GroupingChained).Groups)
}

// Below the sample, or with most trails unknown, a shape has counts and no figure: too few
// groups say nothing about it either way.
func TestGroupingRecordsIsUnknownBelowTheSampleOrWithoutTrails(t *testing.T) {
	t.Parallel()

	var few, blind []types.JobOutcome
	for i := range types.GroupingMinSample {
		o := worked(fmt.Sprint(i), 1, 1, 1, 1, "internal/a")
		if i > 0 {
			few = append(few, o)
		}
		if i > 1 {
			o.Work = nil
		}
		blind = append(blind, o)
	}

	for name, outcomes := range map[string][]types.JobOutcome{"too few groups": few, "most trails unknown": blind} {
		got := shapeRow(t, GroupingRecords(outcomes, nil), types.GroupingSolo)
		assert.Equal(t, types.GroupingUnknown, got.Verdict, name)
		assert.Equal(t, types.GroupingRecord{
			Shape: types.GroupingSolo, Verdict: types.GroupingUnknown,
			Jobs: len(outcomes), Groups: len(outcomes), Known: got.Known,
		}, got, name)
	}
	empty := GroupingRecords(nil, nil)
	require.Len(t, empty.Rows, 3, "every shape answers, unknown when nothing was recorded")
	for _, r := range empty.Rows {
		assert.Equal(t, types.GroupingUnknown, r.Verdict)
	}
}

// An outcome with write paths and no projects predates them or did not resolve: no shape
// can claim it, so it is counted apart rather than read as solo.
func TestGroupingRecordsCountsUnresolvedProjectsAsUnclassified(t *testing.T) {
	t.Parallel()

	unresolved := worked("u", 1, 1, 1, 1)
	readOnly := worked("r", 1, 1, 1, 1)
	readOnly.WriteFiles = 0

	out := GroupingRecords([]types.JobOutcome{unresolved, readOnly}, nil)

	assert.Equal(t, 1, out.Unclassified)
	assert.Equal(t, 1, shapeRow(t, out, types.GroupingSolo).Jobs, "a job with no write paths is solo")
	assert.Zero(t, GroupingRecords([]types.JobOutcome{unresolved}, []string{"internal/a"}).Unclassified,
		"a narrowed lens cannot place it")
}

// Narrowed to projects, a split group is kept whole when any sibling wrote in one of them.
func TestGroupingRecordsNarrowedToProjectsKeepsWholeGroups(t *testing.T) {
	t.Parallel()

	a := worked("a", 1, 1, 1, 1, "internal/a")
	b := worked("b", 1, 1, 1, 1, "internal/b")
	a.Parent, a.ParentAttempt, b.Parent, b.ParentAttempt = "root", 1, "root", 1
	elsewhere := worked("c", 1, 1, 1, 1, "docs")

	out := GroupingRecords([]types.JobOutcome{a, b, elsewhere}, []string{"internal/b"})

	assert.Equal(t, 2, shapeRow(t, out, types.GroupingSplit).Jobs)
	assert.Zero(t, shapeRow(t, out, types.GroupingSolo).Jobs)
}

func TestStoreGroupingLensReadsTheRecordedOutcomes(t *testing.T) {
	t.Parallel()

	s, _ := outcomeStore(t, tierSet(t), nil, false)
	row := lease("a")
	row.Model = ""
	endWith(t, s, endWith(t, s, row, types.StateRunning), types.StatePass)

	got, err := s.GroupingLens(t.Context(), nil)

	require.NoError(t, err)
	want := GroupingRecords(nil, nil)
	want.Unclassified = 1 // its write paths resolved to no project: no Owners
	assert.Equal(t, want, got)
}

func TestGroupingLinesStateTheSampleAndNeverAdvise(t *testing.T) {
	t.Parallel()

	assert.Equal(t, []string{"none recorded for these projects"}, groupingLines(GroupingRecords(nil, nil)))

	var outcomes []types.JobOutcome
	for i := range types.GroupingMinSample {
		outcomes = append(outcomes, worked(fmt.Sprint(i), 60_000, 60_000, 1_000, 4_000, "internal/a", "types"))
	}
	outcomes = append(outcomes, worked("s", 1, 1, 1, 1, "internal/a"))

	assert.Equal(t, []string{
		"chained: 5 groups of 5 jobs, 5 with a known trail; 4000 context tokens per group; 1m0s wall per group;" +
			" median 1000 tokens at the first write; 25% of context spent before it;" +
			fmt.Sprintf(" 1.0 denials and 0.0 unattributed writes per job; pass rate at least %.0f%%", volatility.WilsonLower(5, 5)*100),
		"split: unknown, 0 groups of 0 jobs, 0 with a known trail (fewer than 5 groups, or most trails unknown)",
		"solo: unknown, 1 groups of 1 jobs, 1 with a known trail (fewer than 5 groups, or most trails unknown)",
	}, groupingLines(GroupingRecords(outcomes, nil)))
}
