package work

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/gen/typestest"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
)

type ledgerSuite struct {
	suite.Suite
	repo   string
	ledger *ledger
	vcs    *typestest.MockVCSDriver
}

func TestLedger(t *testing.T) { suite.Run(t, new(ledgerSuite)) }

func (s *ledgerSuite) SetupTest() {
	s.T().Setenv("XDG_STATE_HOME", s.T().TempDir())
	s.repo = s.T().TempDir()
	ledger, err := open(s.repo)
	s.Require().NoError(err)
	ledger.now = func() time.Time { return time.Unix(1000, 0) }
	s.ledger = ledger
	s.vcs = typestest.NewMockVCSDriver(s.T())
	s.vcs.EXPECT().Name().Return("git").Maybe()
	s.vcs.EXPECT().FindCommit(mock.Anything, s.repo, "main").Return(types.Commit{ID: "b0"}, nil).Maybe()
	s.vcs.EXPECT().TreeID(mock.Anything, s.repo, "b0").Return("tree-b0", nil).Maybe()
}

func (s *ledgerSuite) checkouts(list ...types.RegisteredCheckout) {
	s.vcs.EXPECT().RegisteredCheckouts(mock.Anything, s.repo).Return(list, nil)
}

func (s *ledgerSuite) branches(heads map[string]string) {
	s.vcs.EXPECT().BranchHeads(mock.Anything, s.repo).Return(heads, nil)
}

func (s *ledgerSuite) dirty(root string, files ...string) {
	s.vcs.EXPECT().DirtyFiles(mock.Anything, root, []string(nil)).Return(files, nil)
}

func (s *ledgerSuite) unpublished(head string, revs ...string) *typestest.MockVCSDriver_UnpublishedRevisions_Call {
	return s.vcs.EXPECT().UnpublishedRevisions(mock.Anything, s.repo, head, []string{"b0"}).Return(revs, nil)
}

func (s *ledgerSuite) merges(head string, result types.TreeMergeResult) {
	s.vcs.EXPECT().MergeTrees(mock.Anything, s.repo, types.TreeMerge{Ours: "b0", Theirs: head}).Return(result, nil)
}

func (s *ledgerSuite) survey(in surveyTerms) report {
	if in.Base == "" {
		in.Base = "main"
	}
	report, err := s.ledger.Survey(context.Background(), s.vcs, in)
	s.Require().NoError(err)
	return report
}

func (s *ledgerSuite) item(report report, key string) types.WorkItem {
	for _, w := range report.Items {
		if w.Key() == key {
			return w
		}
	}
	s.Failf("no item", "%s is not in %v", key, report.Items)
	return types.WorkItem{}
}

func (s *ledgerSuite) lines() []string {
	data, err := os.ReadFile(s.ledger.items.Path)
	s.Require().NoError(err)
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
}

func (s *ledgerSuite) TestACleanPublishedCheckoutIsClear() {
	s.checkouts(types.RegisteredCheckout{Root: s.repo, Primary: true, Head: "h1"})
	s.dirty(s.repo)
	s.unpublished("h1")
	s.branches(nil)
	got := s.item(s.survey(surveyTerms{}), "checkout:"+s.repo)
	got.At = 0 // the clock the survey wrote the line with
	s.Equal(types.WorkItem{Kind: types.WorkCheckout, Name: s.repo, Repo: s.repo, State: types.WorkClear,
		Head: "h1", Base: "b0", Why: "no uncommitted file and no unpublished revision"}, got)
}

func (s *ledgerSuite) TestADirtyCheckoutNothingHoldsIsUnaccounted() {
	s.checkouts(types.RegisteredCheckout{Root: s.repo, Primary: true, Head: "h1"})
	s.dirty(s.repo, "a.go", "b.go")
	s.unpublished("h1")
	s.branches(nil)
	got := s.item(s.survey(surveyTerms{}), "checkout:"+s.repo)
	got.At = 0 // the clock the survey wrote the line with
	s.Equal(types.WorkItem{Kind: types.WorkCheckout, Name: s.repo, Repo: s.repo, State: types.WorkUnaccounted,
		Head: "h1", Base: "b0", Dirty: 2, Why: "2 uncommitted file(s), and no job or glob holds them"}, got)
}

func (s *ledgerSuite) TestABranchWhoseMergeLeavesTheBaseTreeIsAbsorbed() {
	s.checkouts()
	s.branches(map[string]string{"topic": "h2"})
	s.unpublished("h2", "h2")
	s.merges("h2", types.TreeMergeResult{Tree: "tree-b0"})
	got := s.item(s.survey(surveyTerms{}), "branch:topic")
	got.At = 0 // the clock the survey wrote the line with
	s.Equal(types.WorkItem{Kind: types.WorkBranch, Name: "topic", Repo: s.repo, State: types.WorkAbsorbed,
		Head: "h2", Base: "b0", Commits: 1, Why: "merging it into main leaves the tree as it is"}, got)
}

func (s *ledgerSuite) TestAMergeWithConflictsIsNotAbsorbed() {
	s.checkouts()
	s.branches(map[string]string{"topic": "h2"})
	s.unpublished("h2", "h2")
	s.merges("h2", types.TreeMergeResult{Tree: "tree-b0", Conflicts: []types.Conflict{{}}})
	s.Equal(types.WorkUnaccounted, s.item(s.survey(surveyTerms{}), "branch:topic").State)
}

func (s *ledgerSuite) TestABranchWithNothingUnpublishedIsNoItem() {
	s.checkouts()
	s.branches(map[string]string{"main": "b0"})
	s.unpublished("b0")
	s.Empty(s.survey(surveyTerms{}).Items)
}

func (s *ledgerSuite) TestACheckoutALiveJobHoldsIsTrackedWithItsBranch() {
	s.checkouts(types.RegisteredCheckout{Root: s.repo, Primary: true, Head: "h3"})
	s.dirty(s.repo, "a.go")
	s.unpublished("h3", "h3")
	s.merges("h3", types.TreeMergeResult{Tree: "tree-h3"})
	s.branches(map[string]string{"topic": "h3"})
	report := s.survey(surveyTerms{Jobs: []types.Job{{ID: "fix", State: types.StateRunning, CheckoutRoot: s.repo}}})
	s.Equal(types.WorkTracked, s.item(report, "checkout:"+s.repo).State)
	s.Equal("job fix (running)", s.item(report, "checkout:"+s.repo).Why)
	s.Equal(types.WorkTracked, s.item(report, "branch:topic").State)
}

func (s *ledgerSuite) TestATerminalJobTracksNothing() {
	s.checkouts(types.RegisteredCheckout{Root: s.repo, Primary: true, Head: "h3"})
	s.dirty(s.repo, "a.go")
	s.unpublished("h3")
	s.branches(nil)
	report := s.survey(surveyTerms{Jobs: []types.Job{{ID: "fix", State: types.StatePass, CheckoutRoot: s.repo}}})
	s.Equal(types.WorkUnaccounted, s.item(report, "checkout:"+s.repo).State)
}

func (s *ledgerSuite) TestALocalOnlyGlobMatchesACheckoutByItsDirectoryName() {
	s.checkouts(types.RegisteredCheckout{Root: s.repo, Primary: true, Head: "h1"})
	s.dirty(s.repo, "a.go")
	s.unpublished("h1")
	s.branches(map[string]string{"buzzed-fold": "h4"})
	s.unpublished("h4", "h4")
	s.merges("h4", types.TreeMergeResult{Tree: "tree-h4"})
	report := s.survey(surveyTerms{LocalOnly: []string{filepath.Base(s.repo), "buzzed-*"}})
	s.Equal(types.WorkLocalOnly, s.item(report, "checkout:"+s.repo).State)
	got := s.item(report, "branch:buzzed-fold")
	got.At = 0 // the clock the survey wrote the line with
	s.Equal(types.WorkItem{Kind: types.WorkBranch, Name: "buzzed-fold", Repo: s.repo, State: types.WorkLocalOnly,
		Head: "h4", Base: "b0", Commits: 1, Why: "matches work.local_only buzzed-*"}, got)
}

func (s *ledgerSuite) TestABadGlobFailsTheSurvey() {
	_, err := s.ledger.Survey(context.Background(), s.vcs, surveyTerms{Base: "main", LocalOnly: []string{"[texteditor"}})
	s.Require().Error(err)
	s.Contains(err.Error(), "work.local_only \"[texteditor\"")
}

func (s *ledgerSuite) TestASurveyWithNoBaseIsRefused() {
	_, err := s.ledger.Survey(context.Background(), s.vcs, surveyTerms{})
	s.Require().Error(err)
	s.Contains(err.Error(), "no base revision")
}

func (s *ledgerSuite) branchTopicUnaccounted(head string) {
	s.unpublished(head, head)
	s.merges(head, types.TreeMergeResult{Tree: "tree-" + head})
}

func (s *ledgerSuite) TestAnEndedItemStaysEndedAtItsHead() {
	s.vcs.EXPECT().RegisteredCheckouts(mock.Anything, s.repo).Return(nil, nil).Times(2)
	s.vcs.EXPECT().BranchHeads(mock.Anything, s.repo).Return(map[string]string{"topic": "h5"}, nil).Times(2)
	s.branchTopicUnaccounted("h5")
	s.survey(surveyTerms{})
	s.Require().NoError(s.ledger.End(context.Background(), "branch:topic", "an experiment, dropped"))
	got := s.item(s.survey(surveyTerms{}), "branch:topic")
	got.At = 0 // the clock the survey wrote the line with
	s.Equal(types.WorkItem{Kind: types.WorkBranch, Name: "topic", Repo: s.repo, State: types.WorkEnded,
		Head: "h5", Base: "b0", Commits: 1, Why: "ended by a person", Reason: "an experiment, dropped"}, got)
	s.Len(s.lines(), 2, "the second survey agreed with the ended line")
}

func (s *ledgerSuite) TestAnEndedItemWhoseHeadMovedIsUnaccountedAgain() {
	s.vcs.EXPECT().RegisteredCheckouts(mock.Anything, s.repo).Return(nil, nil).Times(2)
	s.vcs.EXPECT().BranchHeads(mock.Anything, s.repo).Return(map[string]string{"topic": "h5"}, nil).Once()
	s.vcs.EXPECT().BranchHeads(mock.Anything, s.repo).Return(map[string]string{"topic": "h6"}, nil).Once()
	s.branchTopicUnaccounted("h5")
	s.branchTopicUnaccounted("h6")
	s.survey(surveyTerms{})
	s.Require().NoError(s.ledger.End(context.Background(), "branch:topic", "dropped"))
	got := s.item(s.survey(surveyTerms{}), "branch:topic")
	got.At = 0 // the clock the survey wrote the line with
	s.Equal(types.WorkItem{Kind: types.WorkBranch, Name: "topic", Repo: s.repo, State: types.WorkUnaccounted,
		Head: "h6", Base: "b0", Commits: 1, Why: "1 unpublished revision(s), and no job or glob holds them"}, got)
}

func (s *ledgerSuite) TestEndRefusesAnItemTheLedgerDoesNotHold() {
	err := s.ledger.End(context.Background(), "branch:nowhere", "gone")
	s.Require().Error(err)
	s.Contains(err.Error(), "no item branch:nowhere")
}

func (s *ledgerSuite) TestEndRefusesAnEmptyReason() {
	err := s.ledger.End(context.Background(), "branch:topic", "")
	s.Require().Error(err)
	s.Contains(err.Error(), "needs a reason")
}

func (s *ledgerSuite) TestASecondSurveyAppendsNothingAndAsksOnce() {
	s.vcs.EXPECT().RegisteredCheckouts(mock.Anything, s.repo).
		Return([]types.RegisteredCheckout{{Root: s.repo, Primary: true, Head: "h1"}}, nil).Times(2)
	s.vcs.EXPECT().DirtyFiles(mock.Anything, s.repo, []string(nil)).Return([]string{"a.go"}, nil).Times(2)
	s.vcs.EXPECT().BranchHeads(mock.Anything, s.repo).Return(nil, nil).Times(2)
	s.unpublished("h1", "h1").Once()
	s.merges("h1", types.TreeMergeResult{Tree: "tree-h1"})
	s.survey(surveyTerms{})
	s.survey(surveyTerms{})
	s.Len(s.lines(), 1)
}

func (s *ledgerSuite) TestAnItemThatNoLongerExistsGetsADroppedLine() {
	s.vcs.EXPECT().RegisteredCheckouts(mock.Anything, s.repo).Return(nil, nil).Times(2)
	s.vcs.EXPECT().BranchHeads(mock.Anything, s.repo).Return(map[string]string{"topic": "h5"}, nil).Once()
	s.vcs.EXPECT().BranchHeads(mock.Anything, s.repo).Return(map[string]string{}, nil).Once()
	s.branchTopicUnaccounted("h5")
	s.survey(surveyTerms{})
	s.Empty(s.survey(surveyTerms{}).Items)
	lines := s.lines()
	s.Require().Len(lines, 2, "the item's line stays; a tombstone follows it")
	s.Contains(lines[1], `"dropped":true`)
}

func (s *ledgerSuite) TestAGoneCheckoutWithUnpublishedRevisionsIsUnaccounted() {
	gone := filepath.Join(s.repo, "removed")
	s.checkouts(types.RegisteredCheckout{Root: gone, Head: "h7"})
	s.unpublished("h7", "h7")
	s.merges("h7", types.TreeMergeResult{Tree: "tree-h7"})
	s.branches(nil)
	got := s.item(s.survey(surveyTerms{}), "checkout:"+gone)
	s.True(got.Gone)
	s.Equal(types.WorkUnaccounted, got.State)
	s.Contains(got.Why, "directory is gone")
}

func (s *ledgerSuite) TestABackendListingNoHeldRevisionsSaysSo() {
	s.checkouts()
	s.branches(nil)
	report := s.survey(surveyTerms{})
	s.Contains(report.Unknown, "git lists no stashes or preserved captures, so neither was surveyed")
}

type heldDriver struct {
	*typestest.MockVCSDriver
	held []types.HeldRevision
}

func (d heldDriver) HeldRevisions(context.Context, string) ([]types.HeldRevision, error) {
	return d.held, nil
}

func (s *ledgerSuite) TestAStashIsSurveyedWhenTheBackendListsIt() {
	s.checkouts()
	s.branches(nil)
	s.unpublished("h8", "h8")
	s.merges("h8", types.TreeMergeResult{Tree: "tree-h8"})
	driver := heldDriver{MockVCSDriver: s.vcs, held: []types.HeldRevision{{Kind: types.WorkStash, Name: "stash@{0}", Head: "h8"}}}
	report, err := s.ledger.Survey(context.Background(), driver, surveyTerms{Base: "main"})
	s.Require().NoError(err)
	s.Empty(report.Unknown)
	s.Equal(types.WorkUnaccounted, s.item(report, "stash:stash@{0}").State)
}

func (s *ledgerSuite) TestAWorkItemRefusesAnUnknownState() {
	var w types.WorkItem
	err := json.Unmarshal([]byte(`{"kind":"branch","name":"topic","state":"lost","at":1}`), &w) //nolint:musttag // WorkItem encodes through MarshalJSON and its tagged workItemWire
	s.Require().Error(err)
	s.Contains(err.Error(), `state "lost"`)
}

func (s *ledgerSuite) TestAWorkItemRefusesAnUnknownKind() {
	_, err := json.Marshal(types.WorkItem{Kind: "tag", Name: "v1", State: types.WorkClear}) //nolint:musttag // WorkItem encodes through MarshalJSON and its tagged workItemWire
	s.Require().Error(err)
	s.Contains(err.Error(), `kind "tag"`)
}

func (s *ledgerSuite) TestAWorkItemRoundTrips() {
	in := types.WorkItem{Kind: types.WorkBranch, Name: "topic", Repo: s.repo, State: types.WorkEnded,
		Head: "h1", Base: "b0", Commits: 2, Reason: "dropped", At: 5}
	data, err := json.Marshal(in) //nolint:musttag // WorkItem encodes through MarshalJSON and its tagged workItemWire
	s.Require().NoError(err)
	var out types.WorkItem
	s.Require().NoError(json.Unmarshal(data, &out)) //nolint:musttag // WorkItem encodes through MarshalJSON and its tagged workItemWire
	s.Equal(in, out)
}
