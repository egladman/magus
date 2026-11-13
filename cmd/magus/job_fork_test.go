package main

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
)

// ForkGraderSuite pins that one fork opens its workspace once however many checks it
// grades, and that the verdicts it reads back from its memos are the ones the installed
// grader reaches judging each afresh.
type ForkGraderSuite struct {
	suite.Suite
	root  string
	opens atomic.Int64
}

func TestForkGraderSuite(t *testing.T) {
	suite.Run(t, new(ForkGraderSuite))
}

func (s *ForkGraderSuite) SetupTest() {
	s.T().Cleanup(snapshotGlobals())
	testkit.Isolate(s.T())
	global = globalFlags{}
	s.root = testkit.NewRepository(s.T(), map[string]string{"magus.yaml": "{}\n", "magusfile.buzz": denyWorkerBuildRule, "go.mod": goModule, "run.go": "package run\n"}).Root()
	resetWorkspaceMemo(s.T())
	s.T().Chdir(s.root)
	s.opens.Store(0)
}

// counted is the root's rules with an Inspect that counts each open it makes.
func (s *ForkGraderSuite) counted() gradingRules {
	rules := loadGradingRules(s.T().Context(), s.root)
	s.Require().NoError(rules.deps.LoadFailure)
	s.Require().NotEmpty(rules.sources, "the rules name their files, or no grade is keyed")
	inspect := rules.deps.Inspect
	rules.deps.Inspect = func(ctx context.Context, root string) (types.WorkspaceRepository, error) {
		s.opens.Add(1)
		return inspect(ctx, root)
	}
	return rules
}

// forkGrader is a fork's grader over the counted rules, starting from no held workspace.
func (s *ForkGraderSuite) forkGrader() *forkGrader {
	g := newForkGrader(nil)
	g.rules.loaded[s.root] = s.counted()
	return g
}

// unshared is the installed grader over the counted rules.
func (s *ForkGraderSuite) unshared() job.CheckGrader {
	r := &rulesByRoot{loaded: map[string]gradingRules{s.root: s.counted()}}
	return r.grade
}

// rows are checks a fork's validation grades, the denied go-build among them.
func (s *ForkGraderSuite) rows() []types.Job {
	var rows []types.Job
	for _, c := range []types.LeaseCheck{
		{Target: "go-build", Project: "."},
		{Target: "go::go-vet", Project: "."},
		{Target: "go-test", Project: "."},
		{Target: "lint", Project: ".", NoDefaultCharms: true, Args: []string{"run.go"}},
		{Target: types.TargetCI, Project: "."},
	} {
		rows = append(rows, types.Job{ID: "w", Paths: types.JobPaths{Write: []string{"run.go"}}, Check: &c})
	}
	return rows
}

func (s *ForkGraderSuite) TestTheUnsharedGraderOpensTheWorkspacePerJudgment() {
	grade := s.unshared()
	for _, row := range s.rows() {
		grade(s.T().Context(), s.root, row)
	}
	s.Greater(s.opens.Load(), int64(len(s.rows())), "the control: every judgment opens the workspace, so the count below proves the sharing")
}

func (s *ForkGraderSuite) TestOneForkOpensTheWorkspaceOnceAcrossEveryGrade() {
	g := s.forkGrader()
	for range 3 {
		for _, row := range s.rows() {
			g.grade(s.T().Context(), s.root, row)
		}
	}
	s.Equal(int64(1), s.opens.Load())
}

func (s *ForkGraderSuite) TestAHeldWorkspaceIsNeverOpenedAgain() {
	held, err := loadMagus(s.T().Context(), s.root)
	s.Require().NoError(err)
	g := newForkGrader(held)
	g.rules.loaded[s.root] = s.counted()
	for _, row := range s.rows() {
		g.grade(s.T().Context(), s.root, row)
	}
	s.Zero(s.opens.Load())
}

func (s *ForkGraderSuite) TestEveryVerdictMatchesTheUnsharedGrader() {
	g := s.forkGrader()
	grade := s.unshared()
	denied := map[string]bool{}
	for range 2 {
		for _, row := range s.rows() {
			want := grade(s.T().Context(), s.root, row)
			s.Equal(want, g.grade(s.T().Context(), s.root, row), row.Check.String())
			if want.Denied {
				denied[row.Check.Target] = true
			}
		}
	}
	s.True(denied["go-build"], "the rule denies go-build, so a deny is compared too")
}

func (s *ForkGraderSuite) TestAGradeReachedOnceIsReadBack() {
	g := s.forkGrader()
	row := s.rows()[0]
	first := g.grade(s.T().Context(), s.root, row)
	again := g.grade(s.T().Context(), s.root, row)
	s.Equal(first, again)
	s.Equal(1, g.verdicts.Judged())
}

func (s *ForkGraderSuite) TestTheCharmlessFormIsGradedOnItsOwn() {
	g := s.forkGrader()
	row := s.rows()[1]
	charmless := row
	check := *row.Check
	check.NoDefaultCharms = true
	charmless.Check = &check
	g.grade(s.T().Context(), s.root, row)
	g.grade(s.T().Context(), s.root, charmless)
	s.Equal(2, g.verdicts.Judged())
}

func (s *ForkGraderSuite) TestADeferredGradeIsJudgedAgain() {
	g := s.forkGrader()
	rules := g.rules.loaded[s.root]
	release := make(chan struct{})
	defer close(release)
	rules.deps.Inspect = func(context.Context, string) (types.WorkspaceRepository, error) {
		<-release
		return nil, context.Canceled
	}
	g.rules.loaded[s.root] = rules
	ctx, cancel := context.WithTimeout(s.T().Context(), time.Second)
	defer cancel()
	row := s.rows()[0]

	first := g.grade(ctx, s.root, row)
	s.Require().NotEmpty(first.Deferred, "the guard ran out of time")
	g.grade(ctx, s.root, row)
	s.Equal(2, g.verdicts.Judged(), "a lapse is the clock's, so the next grade judges again")
}

// ForkSeatSuite pins that a fork's validation waits for a seat as a graph build does, and
// is refused at budget_wait with the budget's exit status.
type ForkSeatSuite struct {
	suite.Suite
}

func TestForkSeatSuite(t *testing.T) {
	suite.Run(t, new(ForkSeatSuite))
}

// fullBudget seats nothing: every claim waits until its wait ends, and the ask at the
// bound finds the budget still held.
type fullBudget struct {
	claims []types.MachineClaim
}

func (b *fullBudget) Claim(ctx context.Context, c types.MachineClaim, parked func(types.MachineVerdict)) (types.MachineVerdict, error) {
	b.claims = append(b.claims, c)
	parked(b.held())
	<-ctx.Done()
	return types.MachineVerdict{}, ctx.Err()
}

func (b *fullBudget) Request(context.Context, types.MachineClaim) (types.MachineVerdict, error) {
	return b.held(), nil
}

// held is the verdict on a claim another invocation's graph build keeps out.
func (*fullBudget) held() types.MachineVerdict {
	return types.MachineVerdict{Fits: true, BudgetMB: 4096, HeldMB: 4096, Holders: []types.MachineClaimant{{Project: ".", Target: "graph-build", MemoryMB: 4096, PID: 1}}}
}

func (*fullBudget) Release(context.Context, string) {}
func (*fullBudget) Overrun(context.Context, string) {}

func (*fullBudget) HoldGroup(context.Context, string, int, string) (string, error) {
	return "", nil
}

func (*fullBudget) ReleaseHold(context.Context, string) error { return nil }

func (s *ForkSeatSuite) SetupTest() {
	s.T().Cleanup(snapshotGlobals())
	testkit.Isolate(s.T())
}

func (s *ForkSeatSuite) TestAFullBudgetRefusesTheForkWithExit75() {
	globalCfg.Broker = types.BrokerBestEffort
	globalCfg.BudgetWait = 10 * time.Millisecond
	budget := &fullBudget{}

	dir := s.T().TempDir()
	_, err := admitForkValidation(s.T().Context(), budget, dir)

	s.Require().ErrorIs(err, types.MachineBudgetExhausted)
	var exit types.ExitError
	s.Require().ErrorAs(err, &exit)
	s.Equal(cache.ExitCodeMachineBusy, exit.Code)
	s.Equal(75, exit.Code)
	s.Require().Len(budget.claims, 1)
	got := budget.claims[0]
	got.PID, got.Invocation, got.Ancestors, got.WaitBoundMS = 0, "", nil, 0
	s.Equal(types.MachineClaim{Project: ".", Target: "job-fork", MemoryMB: forkValidationMemoryMB, Slots: 1, Dir: resolveRootOrEmpty(dir)}, got)
}

func (s *ForkSeatSuite) TestBrokerOffClaimsNothing() {
	globalCfg.Broker = types.BrokerOff
	budget := &fullBudget{}

	release, err := admitForkValidation(s.T().Context(), budget, s.T().TempDir())

	s.Require().NoError(err)
	release()
	s.Empty(budget.claims)
}
