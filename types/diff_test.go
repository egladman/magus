package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
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

// A file with no history is one nothing has exercised and nobody has reviewed. It used to sink
// to the bottom precisely because it had collected no evidence.
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
