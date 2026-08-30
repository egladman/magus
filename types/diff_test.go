package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func reach(n int) *int { return &n }

// The failure this whole shape exists to prevent: with no symbol index every reach is
// unmeasured, the reach comparator goes inert, and the deterministic path tiebreak becomes the
// only discriminator - so the output is alphabetical while the header still claims a ranking.
// Ranked is what a renderer checks to refuse that claim.
func TestRankedIsFalseWhenNothingWasMeasured(t *testing.T) {
	unmeasured := Diff{Files: []DiffFile{
		{Path: "a.go", Role: DiffRoleSource},
		{Path: "b.go", Role: DiffRoleSource},
	}}
	assert.False(t, unmeasured.Ranked(), "no file carries a reach, so there is no ranking key")

	// One measured file is enough: the order means something for at least part of the list.
	measured := Diff{Files: []DiffFile{
		{Path: "a.go", Role: DiffRoleSource, Reach: reach(0)},
		{Path: "b.go", Role: DiffRoleSource},
	}}
	assert.True(t, measured.Ranked())
}

// Unknown is not zero. A measured zero promises nothing references the file; an unmeasured one
// has made no promise, so it must not sort below the file that did.
func TestSortForReadingPutsUnmeasuredAboveMeasuredZero(t *testing.T) {
	d := Diff{Files: []DiffFile{
		{Path: "measured-zero.go", Role: DiffRoleSource, Reach: reach(0)},
		{Path: "unmeasured.go", Role: DiffRoleSource},
		{Path: "wide.go", Role: DiffRoleSource, Reach: reach(9)},
	}}
	d.SortForReading()

	assert.Equal(t, []string{"wide.go", "unmeasured.go", "measured-zero.go"}, paths(d))
}

// Generated output goes last whatever its reach: reading a machine's restatement before the
// source that caused it is reading the answer before the question.
func TestSortForReadingKeepsGeneratedLast(t *testing.T) {
	d := Diff{Files: []DiffFile{
		{Path: "gen.json", Role: DiffRoleOutput, Reach: reach(99)},
		{Path: "src.go", Role: DiffRoleSource, Reach: reach(1)},
	}}
	d.SortForReading()

	assert.Equal(t, []string{"src.go", "gen.json"}, paths(d))
}

// A file with no history is one nothing has exercised and nobody has reviewed. Every other
// annotation is derived from a file's past, so without this it sinks to the bottom precisely
// because it has collected no evidence.
func TestSortForReadingReadsAnUnseenFileSooner(t *testing.T) {
	d := Diff{Files: []DiffFile{
		{Path: "aaa-known.go", Role: DiffRoleSource},
		{Path: "zzz-brand-new.go", Role: DiffRoleSource, NoHistory: true},
	}}
	d.SortForReading()

	assert.Equal(t, []string{"zzz-brand-new.go", "aaa-known.go"}, paths(d),
		"no history outranks an alphabetically earlier file that has one")

	// But only as a tiebreak: a widely-referenced file still reads first.
	d = Diff{Files: []DiffFile{
		{Path: "new.go", Role: DiffRoleSource, NoHistory: true, Reach: reach(0)},
		{Path: "wide.go", Role: DiffRoleSource, Reach: reach(40)},
	}}
	d.SortForReading()
	assert.Equal(t, []string{"wide.go", "new.go"}, paths(d),
		"reach still decides whenever it is known")
}

// AttachChurn is where NoHistory becomes knowable, so it has to re-establish the order: the
// diff was sorted before any history existed.
func TestAttachChurnMarksUnseenFilesAndReorders(t *testing.T) {
	d := Diff{Files: []DiffFile{
		{Path: "a-hot.go", Role: DiffRoleSource},
		{Path: "b-new.go", Role: DiffRoleSource},
	}}
	d.AttachChurn([]FileHotspot{{Path: "a-hot.go", Commits: 12}}, nil)

	assert.Equal(t, []string{"b-new.go", "a-hot.go"}, paths(d), "the unseen file moved up")
	assert.True(t, d.Files[0].NoHistory)
	assert.False(t, d.Files[1].NoHistory)
}

// A lens that never ran must not mark every file unseen - that is "nobody looked" wearing the
// label for "nothing has touched this".
func TestAttachChurnWithNoHistoryAtAllMarksNothing(t *testing.T) {
	d := Diff{Files: []DiffFile{{Path: "a.go", Role: DiffRoleSource}}}
	d.AttachChurn(nil, nil)

	assert.False(t, d.Files[0].NoHistory)
	assert.Nil(t, d.Files[0].Churn)
}

// Churn's zero fields must never stand in for unmeasured ones: a file the hotspot lens
// never ranked has no commit count, and writing zero renders "nobody measured" as "this
// file is quiet".
func TestAttachChurnDoesNotInventHotspotCountsFromATrend(t *testing.T) {
	t.Parallel()

	d := Diff{Files: []DiffFile{
		{Path: "quiet.go", Project: "p", Role: DiffRoleSource},
		{Path: "hot.go", Project: "p", Role: DiffRoleSource},
	}}
	d.AttachChurn(
		[]FileHotspot{{Path: "hot.go", Commits: 12, Authors: 3, Score: 60}},
		[]TrendEntry{{Path: "p", Delta: 7}},
	)

	byPath := map[string]*DiffChurn{}
	for i := range d.Files {
		byPath[d.Files[i].Path] = d.Files[i].Churn
	}

	quiet := byPath["quiet.go"]
	require.NotNil(t, quiet, "an accelerating project is still worth saying about a file in it")
	assert.Equal(t, 7, quiet.ProjectTrend)
	assert.Zero(t, quiet.Commits, "the hotspot lens never ranked this file")
	assert.Zero(t, quiet.Authors)
	assert.Zero(t, quiet.Score)
	assert.Zero(t, quiet.Rank)

	hot := byPath["hot.go"]
	require.NotNil(t, hot)
	assert.Equal(t, 12, hot.Commits)
	assert.Equal(t, 3, hot.Authors)
	assert.Equal(t, 60, hot.Score)
	assert.Equal(t, 1, hot.Rank)
	assert.Equal(t, 7, hot.ProjectTrend)
}

// A trend entry that did not move is evidence of nothing, so it must not manufacture an
// all-zero Churn where nil is the honest answer.
func TestAttachChurnLeavesAFlatTrendUnmeasured(t *testing.T) {
	t.Parallel()

	d := Diff{Files: []DiffFile{{Path: "a.go", Project: "p", Role: DiffRoleSource}}}
	d.AttachChurn(nil, []TrendEntry{{Path: "p", Delta: 0}})

	assert.Nil(t, d.Files[0].Churn, "a zero delta measured this file no better than a missing one")
}

func TestNotableRankStopsAtTheCutoff(t *testing.T) {
	assert.True(t, DiffChurn{Rank: 1}.NotableRank())
	assert.True(t, DiffChurn{Rank: NotableRankCutoff}.NotableRank())
	assert.False(t, DiffChurn{Rank: NotableRankCutoff + 1}.NotableRank())
	assert.False(t, DiffChurn{Rank: 0}.NotableRank(), "unranked is not rank zero")
}

func paths(d Diff) []string {
	out := make([]string, 0, len(d.Files))
	for _, f := range d.Files {
		out = append(out, f.Path)
	}
	return out
}

// TestPermittedVerdictRefusesToApproveYourOwnChange is the rule the whole approval flow rests on.
// The provider API is perfectly happy to let a change approve itself, which is exactly why the
// refusal lives in Go rather than in a workspace-authored spell.
func TestPermittedVerdictRefusesToApproveYourOwnChange(t *testing.T) {
	mine := ReviewTarget{Author: "ada", Viewer: "ada"}

	assert.Equal(t, VerdictComment, mine.PermittedVerdict(VerdictApprove))
}

// TestPermittedVerdictAllowsApprovingSomebodyElsesChange is the positive control. Without it the
// test above passes just as happily against a function that downgrades everything, which would
// be a feature that never works rather than a rule that holds.
func TestPermittedVerdictAllowsApprovingSomebodyElsesChange(t *testing.T) {
	theirs := ReviewTarget{Author: "grace", Viewer: "ada"}

	for _, want := range []ReviewVerdict{VerdictApprove, VerdictRequestChanges} {
		assert.Equal(t, want, theirs.PermittedVerdict(want))
	}
}

// TestPermittedVerdictTreatsUnknownAuthorshipAsUnsafe. A provider that names neither party has not
// said the review belongs to someone else, and "we could not tell" resolving to "go ahead" is the
// failure OpenedByViewer reports its own certainty to prevent.
func TestPermittedVerdictTreatsUnknownAuthorshipAsUnsafe(t *testing.T) {
	for _, at := range []ReviewTarget{
		{},
		{Author: "grace"},
		{Viewer: "ada"},
	} {
		assert.Equal(t, VerdictComment, at.PermittedVerdict(VerdictApprove), "%+v", at)
	}
}

// TestPermittedVerdictLeavesARemarkAlone: asking for the thing you got is not a
// downgrade, and reporting one would make the surface announce a refusal that never happened.
func TestPermittedVerdictLeavesARemarkAlone(t *testing.T) {
	// An unrecognized word lands here too, which is what makes the handler's unchecked
	// conversion from client input safe: only the two asserting words can ever assert.
	for _, at := range []ReviewTarget{{}, {Author: "ada", Viewer: "ada"}, {Author: "grace", Viewer: "ada"}} {
		for _, want := range []ReviewVerdict{"", VerdictComment, "APPROVE", "lgtm"} {
			assert.Equal(t, VerdictComment, at.PermittedVerdict(want), "%+v asking %q", at, want)
		}
	}
}

// TestVerdictLimitCarriesItsCodeOnlyForTheGap. Two refusals, and only one is a capability gap:
// "you opened this" is how review is meant to work and has no page to send anyone to, while
// "magus could not tell" is something a provider could fix. A code on both would make the
// ordinary case look like a defect.
func TestVerdictLimitCarriesItsCodeOnlyForTheGap(t *testing.T) {
	unknown := ReviewTarget{}.VerdictLimit()
	assert.Contains(t, unknown, string(ReviewAuthorshipUnknown))

	mine := ReviewTarget{Author: "ada", Viewer: "ada"}.VerdictLimit()
	assert.NotContains(t, mine, "MGS")
	assert.NotEmpty(t, mine, "the reason is still said, it just is not a gap")

	theirs := ReviewTarget{Author: "grace", Viewer: "ada"}.VerdictLimit()
	assert.Empty(t, theirs, "nothing is limited, so nothing is explained")
}
