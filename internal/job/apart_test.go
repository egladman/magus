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
func apartProjects() ([]*types.Project, string) {
	return []*types.Project{{Path: ".", TargetOutputs: map[string][]types.OutputRef{
		"generate": {{Glob: "gen/**"}},
		"carve":    {{Glob: "carved/**", Except: []string{"carved/hand.go"}}},
	}}}, ""
}

func apartHolder(checkout string) types.Job {
	return types.Job{ID: "holder", State: types.StateRunning, Paths: types.JobPaths{Write: []string{"gen/decode.go"}}, CheckoutRoot: checkout}
}

// apart is HeldApart's first answer for a holder in /repos/landing.
func (s *jobSuite) apart(here string, paths ...string) bool {
	apart, unchecked := HeldApart(here, apartHolder("/repos/landing"), apartProjects, paths...)
	s.Empty(unchecked)
	return apart
}

func (s *jobSuite) TestHeldApartFreesAGeneratedOutputHeldInAnotherCheckout() {
	s.True(s.apart("/repos/mandate", "gen/decode.go"))
}

func (s *jobSuite) TestHeldApartKeepsAGeneratedOutputHeldInTheSameCheckout() {
	s.False(s.apart("/repos/landing", "gen/decode.go"))
}

func (s *jobSuite) TestHeldApartKeepsTheOutputOfAHolderNobodyTook() {
	apart, unchecked := HeldApart("/repos/mandate", apartHolder(""), apartProjects, "gen/decode.go")
	s.False(apart)
	s.Empty(unchecked)
}

func (s *jobSuite) TestHeldApartKeepsHandWrittenSourceAcrossCheckouts() {
	s.False(s.apart("/repos/mandate", "internal/run.go"))
}

func (s *jobSuite) TestHeldApartFreesTheGroundAGeneratedEntryBounds() {
	s.True(s.apart("/repos/mandate", "internal/**", "gen/decode.go"), "the ground two declarations share lies within the narrower one")
}

func (s *jobSuite) TestHeldApartFreesAGlobWithinAnOutput() {
	s.True(s.apart("/repos/mandate", "gen/decode/**"))
}

func (s *jobSuite) TestHeldApartKeepsAGlobOverAnOutputThatCarvesOutAFile() {
	s.False(s.apart("/repos/mandate", "carved/**"))
	s.True(s.apart("/repos/mandate", "carved/gen.go"))
	s.False(s.apart("/repos/mandate", "carved/hand.go"))
}

func (s *jobSuite) TestHeldApartSaysTheCheckDidNotRunWhenNoWorkspaceLoaded() {
	apart, unchecked := HeldApart("/repos/mandate", apartHolder("/repos/landing"),
		func() ([]*types.Project, string) { return nil, "the workspace did not load: boom" }, "gen/decode.go")
	s.False(apart, "the hold stays")
	s.Equal("the generated-output scope check did not run, so holder's hold in /repos/landing stays: the workspace did not load: boom", unchecked)
}

func (s *jobSuite) TestHeldApartSaysTheCheckDidNotRunWhenTheWorkspaceListsNoProjects() {
	apart, unchecked := HeldApart("/repos/mandate", apartHolder("/repos/landing"),
		func() ([]*types.Project, string) { return nil, "" }, "gen/decode.go")
	s.False(apart)
	s.Contains(unchecked, "the workspace lists no projects")
}

func (s *jobSuite) TestHeldApartSaysNothingWithinOneCheckoutWhenNoWorkspaceLoaded() {
	apart, unchecked := HeldApart("/repos/landing", apartHolder("/repos/landing"),
		func() ([]*types.Project, string) { return nil, "the workspace did not load" }, "gen/decode.go")
	s.False(apart)
	s.Empty(unchecked, "within one checkout the scope never frees a hold, so nothing was skipped")
}

// forkShare forks B onto file while A holds it in another checkout, from a store over ws.
func (s *jobSuite) forkShare(file string, ws Workspace) error {
	files := map[string]string{".gitattributes": "*.go diff=golang\n", file: "package x\n"}
	here := testkit.NewRepository(s.T(), files).Root()
	there := testkit.NewRepository(s.T(), files)
	store := NewStore(tempLocation(s.T(), here)).InWorkspace(ws)
	seed(s.T(), store, types.Job{ID: "A", State: types.StateRunning, Paths: types.JobPaths{Write: []string{file}}, Checkpoint: there.Head(), CheckoutRoot: there.Root()})
	rows, err := store.List()
	s.Require().NoError(err)
	return unorderedFileShareProblems(s.T().Context(), store, rows, types.Job{ID: "B", Paths: types.JobPaths{Write: []string{file}}}).Error()
}

func generatedWorkspace() Workspace {
	projects, _ := apartProjects()
	return Workspace{Projects: projects}
}

func (s *jobSuite) TestAForkSharesAGeneratedOutputWithAnotherCheckout() {
	s.NoError(s.forkShare("gen/decode.go", generatedWorkspace()))
}

func (s *jobSuite) TestAForkSharingHandWrittenSourceWithAnotherCheckoutIsRefused() {
	s.ErrorIs(s.forkShare("run.go", generatedWorkspace()), types.WritePathFileShared)
}

func (s *jobSuite) TestAForkWithNoWorkspaceSaysTheScopeCheckDidNotRun() {
	err := s.forkShare("gen/decode.go", Workspace{Unavailable: "boom"})
	s.ErrorIs(err, types.WritePathFileShared, "the hold stays")
	s.ErrorContains(err, "the generated-output scope check did not run")
	s.ErrorContains(err, "the workspace did not load: boom")
}

func overlapRows() []types.Job {
	return []types.Job{
		{ID: "landing", State: types.StateRunning, CheckoutRoot: "/repos/landing"},
		{ID: "mandate", State: types.StateRunning, CheckoutRoot: "/repos/mandate"},
		{ID: "beside", State: types.StateRunning, CheckoutRoot: "/repos/landing"},
	}
}

func (s *jobSuite) TestOverlapsDropAGeneratedOutputHeldInAnotherCheckout() {
	got := ContendedOverlaps(overlapRows(), []types.JobOverlap{
		{JobA: "landing", JobB: "mandate", PathsA: []string{"gen/decode.go"}, PathsB: []string{"gen/**"}},
	}, apartProjects)
	s.Empty(got)
}

func (s *jobSuite) TestOverlapsKeepAGeneratedOutputHeldInOneCheckout() {
	pair := types.JobOverlap{JobA: "landing", JobB: "beside", PathsA: []string{"gen/decode.go"}, PathsB: []string{"gen/decode.go"}}
	s.Equal([]types.JobOverlap{pair}, ContendedOverlaps(overlapRows(), []types.JobOverlap{pair}, apartProjects))
}

func (s *jobSuite) TestOverlapsKeepHandWrittenGroundBesideAGeneratedOutput() {
	got := ContendedOverlaps(overlapRows(), []types.JobOverlap{
		{JobA: "landing", JobB: "mandate", PathsA: []string{"gen/decode.go", "run.go"}, PathsB: []string{"**/*.go"}},
	}, apartProjects)
	s.Equal([]types.JobOverlap{{JobA: "landing", JobB: "mandate", PathsA: []string{"run.go"}, PathsB: []string{"**/*.go"}}}, got)
}
