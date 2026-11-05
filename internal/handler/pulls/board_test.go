package pulls

import (
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/queue"
	pullsv1 "github.com/egladman/magus/proto/gen/go/magus/pulls/v1alpha1"
	"github.com/egladman/magus/types"
)

var testNow = time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)

func ago(d time.Duration) string { return testNow.Add(-d).Format(time.RFC3339) }

func testConfig() config.Pulls {
	return config.Pulls{
		Floor:      30 * time.Second,
		Active:     time.Minute,
		Idle:       5 * time.Minute,
		Reserve:    500,
		RunnerWait: 15 * time.Minute,
		StaleAfter: 72 * time.Hour,
	}
}

func passing(name string) types.ForgeCheck {
	return types.ForgeCheck{Name: name, Kind: "check", State: "passed"}
}

func failing(name, line string, started time.Duration) types.ForgeCheck {
	return types.ForgeCheck{Name: name, Kind: "check", State: "failed", Lines: []string{line}, StartedAt: ago(started)}
}

func queued(name string, waited time.Duration) types.ForgeCheck {
	return types.ForgeCheck{Name: name, Kind: "check", State: "queued", QueuedAt: ago(waited)}
}

func running(name string) types.ForgeCheck {
	return types.ForgeCheck{Name: name, Kind: "check", State: "running"}
}

func mergeQueue(state, description string) types.ForgeCheck {
	return types.ForgeCheck{Name: queue.DefaultStatusContext, Kind: "status", State: state, Description: description}
}

func pullOf(n int, author string, checks ...types.ForgeCheck) types.ForgePull {
	return types.ForgePull{
		Number:    n,
		Author:    author,
		Mergeable: "mergeable",
		Review:    "none",
		PushedAt:  ago(time.Hour),
		Checks:    checks,
	}
}

func buildBoard(base types.BaseBranch, pulls ...types.ForgePull) *pullsv1.Board {
	return analyseBoard(boardInput{
		List:   types.PullList{Repo: "acme/acme", Viewer: "ada", Base: base, Pulls: pulls},
		Now:    testNow,
		Config: testConfig(),
		Filter: authorFilter{Saved: true},
	})
}

func onlyRow(b *pullsv1.Board) *pullsv1.Pull { return b.Pulls[0] }

type BoardSuite struct{ suite.Suite }

func TestBoardSuite(t *testing.T) { suite.Run(t, new(BoardSuite)) }

func (s *BoardSuite) TestCIIgnoresMergeQueue() {
	row := onlyRow(buildBoard(types.BaseBranch{}, pullOf(1, "ada", passing("build"), mergeQueue("failure", "behind two pulls"))))

	s.Equal(pullsv1.CiState_CI_STATE_GREEN, row.Ci)
	s.EqualValues(1, row.Passed)
	s.Zero(row.Failed)
	s.Equal("failure", row.Queue.State)
	s.Equal("behind two pulls", row.Queue.Description)
}

func (s *BoardSuite) TestCIRedWhenAnyCheckFailed() {
	row := onlyRow(buildBoard(types.BaseBranch{}, pullOf(1, "ada", passing("a"), failing("b", "boom", time.Hour), running("c"))))

	s.Equal(pullsv1.CiState_CI_STATE_RED, row.Ci)
	s.EqualValues(1, row.Passed)
	s.EqualValues(1, row.Failed)
	s.EqualValues(1, row.Pending)
}

func (s *BoardSuite) TestCIRunningWhenACheckRuns() {
	row := onlyRow(buildBoard(types.BaseBranch{}, pullOf(1, "ada", passing("a"), running("b"), queued("c", time.Minute))))

	s.Equal(pullsv1.CiState_CI_STATE_RUNNING, row.Ci)
	s.EqualValues(2, row.Pending)
}

func (s *BoardSuite) TestCIQueuedReportsTheEarliestWait() {
	row := onlyRow(buildBoard(types.BaseBranch{}, pullOf(1, "ada", queued("a", 5*time.Minute), queued("b", 20*time.Minute))))

	s.Equal(pullsv1.CiState_CI_STATE_QUEUED, row.Ci)
	s.Equal(20*time.Minute, row.QueuedFor.AsDuration())
}

func (s *BoardSuite) TestCIGreenCountsSkippedAsPassed() {
	skipped := types.ForgeCheck{Name: "s", State: "skipped"}
	row := onlyRow(buildBoard(types.BaseBranch{}, pullOf(1, "ada", passing("a"), skipped)))

	s.Equal(pullsv1.CiState_CI_STATE_GREEN, row.Ci)
	s.EqualValues(2, row.Passed)
	s.Nil(row.QueuedFor)
}

func (s *BoardSuite) TestCINoneWithoutChecks() {
	row := onlyRow(buildBoard(types.BaseBranch{}, pullOf(1, "ada")))

	s.Equal(pullsv1.CiState_CI_STATE_NONE, row.Ci)
}

func (s *BoardSuite) TestCINoneWhenOnlyTheQueueReports() {
	row := onlyRow(buildBoard(types.BaseBranch{}, pullOf(1, "ada", mergeQueue("pending", "waiting"))))

	s.Equal(pullsv1.CiState_CI_STATE_NONE, row.Ci)
	s.Equal("pending", row.Queue.State)
}

func (s *BoardSuite) TestFirstFailureNamesTheCheckAndItsLines() {
	bad := failing("docs:site-generate", "exit 1", time.Hour)
	bad.OutputRef = "out0123456789ab"
	row := onlyRow(buildBoard(types.BaseBranch{}, pullOf(1, "ada", passing("a"), bad, failing("z", "later", time.Hour))))

	s.Equal("docs:site-generate", row.FirstFailure.Name)
	s.Equal([]string{"exit 1"}, row.FirstFailure.Lines)
	s.Equal("out0123456789ab", row.FirstFailure.OutputRef)
	s.Len(row.FailedJobs, 2)
}

func (s *BoardSuite) TestBlockedConflictsNamesTheBase() {
	p := pullOf(1, "ada")
	p.Mergeable = "conflicting"

	s.Equal([]string{"conflicts with main"}, onlyRow(buildBoard(types.BaseBranch{}, p)).Blocked)
}

func (s *BoardSuite) TestBlockedFailingNamesTheCheckAndItsFirstLine() {
	row := onlyRow(buildBoard(types.BaseBranch{}, pullOf(1, "ada", failing("docs:site-generate", "exit 1", time.Hour))))

	s.Equal([]string{"`docs:site-generate` failing: exit 1"}, row.Blocked)
}

func (s *BoardSuite) TestBlockedWaitingOnRunners() {
	row := onlyRow(buildBoard(types.BaseBranch{}, pullOf(1, "ada", queued("a", 2*time.Hour))))

	s.Equal([]string{"waiting on runners for 2h"}, row.Blocked)
}

func (s *BoardSuite) TestShortQueueWaitIsNotBlocked() {
	row := onlyRow(buildBoard(types.BaseBranch{}, pullOf(1, "ada", queued("a", 5*time.Minute))))

	s.Empty(row.Blocked)
}

func (s *BoardSuite) TestBlockedChangesRequested() {
	p := pullOf(1, "ada")
	p.Review = "changes_requested"

	s.Equal([]string{"changes requested"}, onlyRow(buildBoard(types.BaseBranch{}, p)).Blocked)
}

func (s *BoardSuite) TestBlockedReviewRequired() {
	p := pullOf(1, "ada")
	p.Review = "review_required"

	s.Equal([]string{"review required"}, onlyRow(buildBoard(types.BaseBranch{}, p)).Blocked)
}

func (s *BoardSuite) TestBlockedDraft() {
	p := pullOf(1, "ada")
	p.Draft = true

	s.Equal([]string{"draft"}, onlyRow(buildBoard(types.BaseBranch{}, p)).Blocked)
}

func (s *BoardSuite) TestBlockedBehindOnlyWhenMergeableIsUnknown() {
	unknown := pullOf(1, "ada")
	unknown.Mergeable, unknown.BehindBy = "unknown", 3
	known := pullOf(2, "ada")
	known.BehindBy = 3
	b := buildBoard(types.BaseBranch{}, unknown, known)

	s.Equal([]string{"3 commits behind main"}, b.Pulls[0].Blocked)
	s.Empty(b.Pulls[1].Blocked)
}

func (s *BoardSuite) TestBlockedNamesTheConfiguredBase() {
	p := pullOf(1, "ada")
	p.Mergeable = "conflicting"

	s.Equal([]string{"conflicts with trunk"}, onlyRow(buildBoard(types.BaseBranch{Name: "trunk"}, p)).Blocked)
}

func (s *BoardSuite) TestRegressionNamesTheCommitItStartedAfter() {
	base := types.BaseBranch{Commits: []types.BaseCommit{
		{SHA: "ccccccc111", Subject: "newer", At: ago(10 * time.Minute)},
		{SHA: "bbbbbbb222", Subject: "break the docs build", At: ago(3 * time.Hour), Pull: 612},
		{SHA: "aaaaaaa333", Subject: "older", At: ago(9 * time.Hour)},
	}}
	b := buildBoard(base,
		pullOf(1, "ada", failing("docs:site-generate", "exit 1", 2*time.Hour)),
		pullOf(2, "ada", failing("docs:site-generate", "exit 1", time.Hour)),
	)

	s.Require().Len(b.Patterns, 1)
	p := b.Patterns[0]
	s.Equal(pullsv1.PatternKind_PATTERN_KIND_REGRESSION, p.Kind)
	s.Equal([]int32{1, 2}, p.Pulls)
	s.Equal("bbbbbbb222", p.SinceSha)
	s.EqualValues(612, p.SincePull)
	s.False(p.OnMain)
	s.Equal("2 PRs fail on docs:site-generate; started after bbbbbbb break the docs build (#612)", p.Message)
}

func (s *BoardSuite) TestRegressionFlagsAFailureThatMainShares() {
	base := types.BaseBranch{Checks: []types.ForgeCheck{failing("docs:site-generate", "exit 1", time.Hour)}}
	b := buildBoard(base,
		pullOf(1, "ada", failing("docs:site-generate", "exit 1", 2*time.Hour)),
		pullOf(2, "ada", failing("docs:site-generate", "exit 1", time.Hour)),
	)

	s.Require().Len(b.Patterns, 1)
	s.True(b.Patterns[0].OnMain)
	s.Contains(b.Patterns[0].Message, "main fails it too")
}

func (s *BoardSuite) TestRegressionNeedsTheSameFirstLine() {
	b := buildBoard(types.BaseBranch{},
		pullOf(1, "ada", failing("lint", "unused import", time.Hour)),
		pullOf(2, "ada", failing("lint", "missing return", time.Hour)),
	)

	s.Empty(b.Patterns)
}

func (s *BoardSuite) TestRegressionNeedsTwoPulls() {
	b := buildBoard(types.BaseBranch{}, pullOf(1, "ada", failing("lint", "x", time.Hour)))

	s.Empty(b.Patterns)
}

func (s *BoardSuite) TestRegressionWithoutAKnownStartNamesNoCommit() {
	noStart := failing("lint", "x", 0)
	noStart.StartedAt = ""
	b := buildBoard(types.BaseBranch{Commits: []types.BaseCommit{{SHA: "abc", At: ago(time.Hour)}}},
		pullOf(1, "ada", noStart), pullOf(2, "ada", noStart))

	s.Require().Len(b.Patterns, 1)
	s.Empty(b.Patterns[0].SinceSha)
	s.Equal("2 PRs fail on lint", b.Patterns[0].Message)
}

func (s *BoardSuite) TestRunnerBacklogAtThreeLongQueuedChecks() {
	b := buildBoard(types.BaseBranch{},
		pullOf(1, "ada", queued("a", time.Hour), queued("b", time.Hour)),
		pullOf(2, "ada", queued("a", 30*time.Minute)),
	)

	s.Require().Len(b.Patterns, 1)
	s.Equal(pullsv1.PatternKind_PATTERN_KIND_RUNNER_BACKLOG, b.Patterns[0].Kind)
	s.Equal("3 runs queued over 15m: runner backlog, not code", b.Patterns[0].Message)
	s.Equal([]int32{1, 2}, b.Patterns[0].Pulls)
}

func (s *BoardSuite) TestRunnerBacklogNeedsThreeChecks() {
	b := buildBoard(types.BaseBranch{}, pullOf(1, "ada", queued("a", time.Hour), queued("b", time.Hour)))

	s.Empty(b.Patterns)
}

func (s *BoardSuite) TestMergeOrderPutsApprovedGreenFirstThenGreenThenTheRest() {
	approved := pullOf(30, "ada", passing("a"))
	approved.Review = "approved"
	green := pullOf(20, "ada", passing("a"))
	red := pullOf(10, "ada", failing("a", "boom", time.Hour))
	conflict := pullOf(5, "ada", passing("a"))
	conflict.Mergeable = "conflicting"
	b := buildBoard(types.BaseBranch{}, red, green, approved, conflict)

	var order []int32
	for _, m := range b.MergeOrder {
		order = append(order, m.Number)
	}
	s.Equal([]int32{30, 20, 5, 10}, order)
	s.Equal("green, mergeable, approved", b.MergeOrder[0].Note)
	s.Equal("green, mergeable", b.MergeOrder[1].Note)
	s.Equal("conflicts with main", b.MergeOrder[2].Note)
}

func (s *BoardSuite) TestMergeOrderHoldsPullsFailingOnlyARegression() {
	b := buildBoard(types.BaseBranch{},
		pullOf(1, "ada", failing("docs", "x", time.Hour)),
		pullOf(2, "ada", failing("docs", "x", time.Hour)),
		pullOf(3, "ada", failing("lint", "y", time.Hour)),
		pullOf(4, "ada", passing("a")),
	)

	var order []int32
	for _, m := range b.MergeOrder {
		order = append(order, m.Number)
	}
	s.Equal([]int32{4, 1, 2, 3}, order)
	s.Equal("waits on the main regression in docs", b.MergeOrder[1].Note)
}

func (s *BoardSuite) TestOverlapNamesTheSharedFiles() {
	a, c := pullOf(1, "ada"), pullOf(2, "ada")
	a.Files = []string{"x.go", "y.go"}
	c.Files = []string{"y.go", "z.go", "x.go"}
	d := pullOf(3, "ada")
	d.Files = []string{"other.go"}
	b := buildBoard(types.BaseBranch{}, a, c, d)

	s.Require().Len(b.Pulls[0].Overlaps, 1)
	s.EqualValues(2, b.Pulls[0].Overlaps[0].Number)
	s.Equal([]string{"x.go", "y.go"}, b.Pulls[0].Overlaps[0].Files)
	s.Empty(b.Pulls[2].Overlaps)
}

func (s *BoardSuite) TestStaleAfterTheConfiguredWindow() {
	old, fresh := pullOf(1, "ada"), pullOf(2, "ada")
	old.PushedAt = ago(5 * 24 * time.Hour)
	b := buildBoard(types.BaseBranch{}, old, fresh)

	s.True(b.Pulls[0].Stale)
	s.False(b.Pulls[1].Stale)
}

func (s *BoardSuite) TestMainIsPinnedWithItsFirstFailure() {
	base := types.BaseBranch{
		Name:   "main",
		SHA:    "9f3c2e1",
		Checks: []types.ForgeCheck{passing("a"), failing("docs", "exit 1", time.Hour), running("c")},
	}
	b := buildBoard(base)

	s.Equal("9f3c2e1", b.Main.Sha)
	s.Equal(pullsv1.CiState_CI_STATE_RED, b.Main.Ci)
	s.Equal("docs", b.Main.FirstFailure.Name)
	s.EqualValues(1, b.Main.Pending)
}

func (s *BoardSuite) TestFilterKeepsTheChosenAuthorsOnly() {
	b := analyseBoard(boardInput{
		List:   types.PullList{Viewer: "ada", Pulls: []types.ForgePull{pullOf(1, "ada"), pullOf(2, "Grace"), pullOf(3, "bob")}},
		Now:    testNow,
		Config: testConfig(),
		Filter: authorFilter{Saved: true, Authors: []string{"grace", "bob"}},
	})

	s.Require().Len(b.Pulls, 2)
	s.EqualValues(2, b.Pulls[0].Number)
	s.EqualValues(3, b.Pulls[1].Number)
}

func (s *BoardSuite) TestDefaultFilterShowsTheViewerOnly() {
	b := analyseBoard(boardInput{
		List:   types.PullList{Viewer: "ada", Pulls: []types.ForgePull{pullOf(1, "ada"), pullOf(2, "grace")}},
		Now:    testNow,
		Config: testConfig(),
	})

	s.Require().Len(b.Pulls, 1)
	s.True(b.Filter.IsDefault)
	s.Equal([]string{"ada"}, b.Filter.Authors)
}
