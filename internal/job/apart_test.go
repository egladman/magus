package job

import (
	"testing"

	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/suite"
)

type jobSuite struct {
	suite.Suite
}

func TestJobSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(jobSuite))
}

// apartProjects declares gen/** generated whole, and carved/** generated except one
// hand-maintained file.
func apartProjects() []*types.Project {
	return []*types.Project{{Path: ".", TargetOutputs: map[string][]types.OutputRef{
		"generate": {{Glob: "gen/**"}},
		"carve":    {{Glob: "carved/**", Except: []string{"carved/hand.go"}}},
	}}}
}

func apartHolder(checkout string) types.Job {
	return types.Job{ID: "holder", State: types.StateRunning, WritePaths: []string{"gen/decode.go"}, CheckoutRoot: checkout}
}

func (s *jobSuite) TestHeldApartFreesAGeneratedOutputHeldInAnotherCheckout() {
	s.True(HeldApart("/repos/mandate", apartHolder("/repos/landing"), apartProjects, "gen/decode.go"))
}

func (s *jobSuite) TestHeldApartKeepsAGeneratedOutputHeldInTheSameCheckout() {
	s.False(HeldApart("/repos/landing", apartHolder("/repos/landing"), apartProjects, "gen/decode.go"))
}

func (s *jobSuite) TestHeldApartKeepsTheOutputOfAHolderNobodyTook() {
	s.False(HeldApart("/repos/mandate", apartHolder(""), apartProjects, "gen/decode.go"))
}

func (s *jobSuite) TestHeldApartKeepsHandWrittenSourceAcrossCheckouts() {
	s.False(HeldApart("/repos/mandate", apartHolder("/repos/landing"), apartProjects, "internal/run.go"))
}

func (s *jobSuite) TestHeldApartFreesTheGroundAGeneratedEntryBounds() {
	s.True(HeldApart("/repos/mandate", apartHolder("/repos/landing"), apartProjects, "internal/**", "gen/decode.go"),
		"the ground two declarations share lies within the narrower one")
}

func (s *jobSuite) TestHeldApartFreesAGlobWithinAnOutput() {
	s.True(HeldApart("/repos/mandate", apartHolder("/repos/landing"), apartProjects, "gen/decode/**"))
}

func (s *jobSuite) TestHeldApartKeepsAGlobOverAnOutputThatCarvesOutAFile() {
	s.False(HeldApart("/repos/mandate", apartHolder("/repos/landing"), apartProjects, "carved/**"))
	s.True(HeldApart("/repos/mandate", apartHolder("/repos/landing"), apartProjects, "carved/gen.go"))
	s.False(HeldApart("/repos/mandate", apartHolder("/repos/landing"), apartProjects, "carved/hand.go"))
}

func (s *jobSuite) TestHeldApartKeepsTheHoldWhenNoWorkspaceAnswers() {
	s.False(HeldApart("/repos/mandate", apartHolder("/repos/landing"), func() []*types.Project { return nil }, "gen/decode.go"))
}

// forkShare forks B onto file while A holds it in another checkout.
func (s *jobSuite) forkShare(file string) error {
	files := map[string]string{".gitattributes": "*.go diff=golang\n", file: "package x\n"}
	here := testkit.NewRepository(s.T(), files).Root()
	there := testkit.NewRepository(s.T(), files)
	store := NewStore(tempLocation(s.T(), here)).InWorkspace(Workspace{Projects: apartProjects()})
	seed(s.T(), store, types.Job{ID: "A", State: types.StateRunning, WritePaths: []string{file}, Checkpoint: there.Head(), CheckoutRoot: there.Root()})
	rows, err := store.List()
	s.Require().NoError(err)
	return unorderedFileShareProblems(s.T().Context(), store, rows, types.Job{ID: "B", WritePaths: []string{file}}).Error()
}

func (s *jobSuite) TestAForkSharesAGeneratedOutputWithAnotherCheckout() {
	s.NoError(s.forkShare("gen/decode.go"))
}

func (s *jobSuite) TestAForkSharingHandWrittenSourceWithAnotherCheckoutIsRefused() {
	s.ErrorIs(s.forkShare("run.go"), types.WritePathFileShared)
}

func overlapRows() []types.Job {
	return []types.Job{
		{ID: "landing", State: types.StateRunning, CheckoutRoot: "/repos/landing"},
		{ID: "mandate", State: types.StateRunning, CheckoutRoot: "/repos/mandate"},
		{ID: "beside", State: types.StateRunning, CheckoutRoot: "/repos/landing"},
	}
}

func (s *jobSuite) TestOverlapsDropAGeneratedOutputHeldInAnotherCheckout() {
	got := contendedOverlaps(overlapRows(), []types.JobOverlap{
		{JobA: "landing", JobB: "mandate", PathsA: []string{"gen/decode.go"}, PathsB: []string{"gen/**"}},
	}, apartProjects)
	s.Empty(got)
}

func (s *jobSuite) TestOverlapsKeepAGeneratedOutputHeldInOneCheckout() {
	pair := types.JobOverlap{JobA: "landing", JobB: "beside", PathsA: []string{"gen/decode.go"}, PathsB: []string{"gen/decode.go"}}
	s.Equal([]types.JobOverlap{pair}, contendedOverlaps(overlapRows(), []types.JobOverlap{pair}, apartProjects))
}

func (s *jobSuite) TestOverlapsKeepHandWrittenGroundBesideAGeneratedOutput() {
	got := contendedOverlaps(overlapRows(), []types.JobOverlap{
		{JobA: "landing", JobB: "mandate", PathsA: []string{"gen/decode.go", "run.go"}, PathsB: []string{"**/*.go"}},
	}, apartProjects)
	s.Equal([]types.JobOverlap{{JobA: "landing", JobB: "mandate", PathsA: []string{"run.go"}, PathsB: []string{"**/*.go"}}}, got)
}
