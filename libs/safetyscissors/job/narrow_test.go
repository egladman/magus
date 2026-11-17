package job

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

type NarrowSuite struct {
	suite.Suite
}

func TestNarrowSuite(t *testing.T) { suite.Run(t, new(NarrowSuite)) }

func (s *NarrowSuite) TestALiteralDirectoryCoversWhatIsUnderIt() {
	s.True(Covers("internal/ledger", "internal/ledger/store.go"))
	s.False(Covers("internal/ledger", "internal/ledgers/store.go"))
	s.True(Covers("run.go#executeStages", "run.go"), "a claim covers its whole file")
	s.False(Covers("", "a.go"), "a blank declaration claims nothing")
}

func (s *NarrowSuite) TestAGlobIsMatchedAsAGlob() {
	s.True(Covers("internal/**/*.go", "internal/job/store.go"))
	s.False(Covers("internal/*.go", "internal/job/store.go"))
}

func (s *NarrowSuite) TestMatchingNamesTheDeclarationThatFired() {
	got, ok := Matching([]string{"docs", "internal/**"}, "internal/a.go")

	s.True(ok)
	s.Equal("internal/**", got)
}

func (s *NarrowSuite) TestAPatternStopsAtAnyReleaseOfAFile() {
	row := Job{Paths: Paths{Write: []string{"**/*.buzz"}}, Releases: []Release{{Path: "magusfile.buzz"}}}

	s.True(ReleasedReach(row, "**/*.buzz", "magusfile.buzz"))
	s.False(Narrowed(row, "magusfile.buzz"), "a plain release is not a narrowing")
}

func (s *NarrowSuite) TestALiteralStopsOnlyAtANarrowing() {
	plain := Job{Paths: Paths{Write: []string{"notes.buzz"}}, Releases: []Release{{Path: "notes.buzz"}}}
	narrowed := Job{Paths: Paths{Write: []string{"notes.buzz"}}, Releases: []Release{{Path: "notes.buzz", Narrowed: true}}}

	s.False(ReleasedReach(plain, "notes.buzz", "notes.buzz"))
	s.True(ReleasedReach(narrowed, "notes.buzz", "notes.buzz"))
}

func (s *NarrowSuite) TestAClaimOnAReleasedFileIsListed() {
	row := Job{Paths: Paths{Write: []string{"**/*.buzz", "magusfile.buzz#build"}}, Releases: []Release{{Path: "magusfile.buzz", Narrowed: true}}}

	s.Equal([]string{"build"}, ClaimsOnFile(row, "magusfile.buzz"))
}

func (s *NarrowSuite) TestARequestForAFileNoLongerHeldIsAnswered() {
	row := Job{Paths: Paths{Write: []string{"a.go"}}, ReleaseRequests: []ReleaseRequest{{ID: "r1", File: "a.go"}, {ID: "r2", File: "b.go"}}}

	got := heldRequests(row)

	s.Require().Len(got, 1)
	s.Equal("r1", got[0].ID)
}

func (s *NarrowSuite) TestEntryPathRefusesAPathOutsideTheWorkspace() {
	_, err := EntryPath("../x")
	s.Error(err)
	got, err := EntryPath(" a//b.go ")
	s.Require().NoError(err)
	s.Equal("a/b.go", got)
}
