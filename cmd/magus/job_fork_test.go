package main

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/guard"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/libs/safetyscissors/budget"
	jobtypes "github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
)

// ForkGraderSuite pins that the fork's grader over the CLI's rules opens its workspace
// once however many checks it grades, and that its verdicts are the ones the installed
// grader reaches judging each afresh. The guard's own suite covers the kept verdicts with a
// synthetic loader; only the CLI's loader opens a real workspace to count.
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
	resetOpenedWorkspaces(s.T())
	s.T().Chdir(s.root)
	s.opens.Store(0)
}

// counted is loadGuardDependencies with an Inspect that counts each open it makes.
func (s *ForkGraderSuite) counted(ctx context.Context, root string) (guard.Dependencies, *guard.WorkspaceRules) {
	deps, rules := loadGuardDependencies(ctx, root)
	s.Require().NoError(deps.LoadFailure)
	inspect := deps.Inspect
	deps.Inspect = func(ctx context.Context, root string) (types.WorkspaceRepository, error) {
		s.opens.Add(1)
		return inspect(ctx, root)
	}
	return deps, rules
}

// rows are checks a fork's validation grades, the denied go-build among them.
func (s *ForkGraderSuite) rows() []jobtypes.Job {
	var rows []jobtypes.Job
	for _, c := range []jobtypes.LeaseCheck{
		{Target: "go-build", Project: "."},
		{Target: "go::go-vet", Project: "."},
		{Target: "go-test", Project: "."},
		{Target: "lint", Project: ".", NoDefaultCharms: true, Args: []string{"run.go"}},
		{Target: types.TargetCI, Project: "."},
	} {
		rows = append(rows, jobtypes.Job{ID: "w", Paths: jobtypes.Paths{Write: []string{"run.go"}}, Check: &c})
	}
	return rows
}

func (s *ForkGraderSuite) TestTheUnsharedGraderOpensTheWorkspacePerJudgment() {
	grade := guard.NewCheckGrader(s.counted)
	for _, row := range s.rows() {
		grade(s.T().Context(), s.root, row)
	}
	s.Greater(s.opens.Load(), int64(len(s.rows())), "the control: every judgment opens the workspace, so the count below proves the sharing")
}

func (s *ForkGraderSuite) TestOneForkOpensTheWorkspaceOnceAcrossEveryGrade() {
	g := guard.NewForkGrader(nil, s.counted)
	for range 3 {
		for _, row := range s.rows() {
			g.Grade(s.T().Context(), s.root, row)
		}
	}
	s.Equal(int64(1), s.opens.Load())
}

func (s *ForkGraderSuite) TestAHeldWorkspaceIsNeverOpenedAgain() {
	held, err := loadMagus(s.T().Context(), s.root)
	s.Require().NoError(err)
	g := guard.NewForkGrader(held, s.counted)
	for _, row := range s.rows() {
		g.Grade(s.T().Context(), s.root, row)
	}
	s.Zero(s.opens.Load())
}

func (s *ForkGraderSuite) TestEveryVerdictMatchesTheUnsharedGrader() {
	g := guard.NewForkGrader(nil, s.counted)
	grade := guard.NewCheckGrader(s.counted)
	denied := map[string]bool{}
	for range 2 {
		for _, row := range s.rows() {
			want := grade(s.T().Context(), s.root, row)
			s.Equal(want, g.Grade(s.T().Context(), s.root, row), job.CheckCommand(*row.Check))
			if want.Denied {
				denied[row.Check.Target] = true
			}
		}
	}
	s.True(denied["go-build"], "the rule denies go-build, so a deny is compared too")
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
	claims []budget.Claim
}

func (b *fullBudget) Claim(ctx context.Context, c budget.Claim, parked func(budget.Verdict)) (budget.Verdict, error) {
	b.claims = append(b.claims, c)
	parked(b.held())
	<-ctx.Done()
	return budget.Verdict{}, ctx.Err()
}

func (b *fullBudget) Request(context.Context, budget.Claim) (budget.Verdict, error) {
	return b.held(), nil
}

// held is the verdict on a claim another invocation's graph build keeps out.
func (*fullBudget) held() budget.Verdict {
	return budget.Verdict{Fits: true, BudgetMB: 4096, HeldMB: 4096, Holders: []budget.Claimant{{Project: ".", Target: "graph-build", MemoryMB: 4096, PID: 1}}}
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
	full := &fullBudget{}

	dir := s.T().TempDir()
	_, err := admitForkValidation(s.T().Context(), full, dir)

	s.Require().ErrorIs(err, types.MachineBudgetExhausted)
	var exit types.ExitError
	s.Require().ErrorAs(err, &exit)
	s.Equal(cache.ExitCodeMachineBusy, exit.Code)
	s.Equal(75, exit.Code)
	s.Require().Len(full.claims, 1)
	got := full.claims[0]
	got.PID, got.Invocation, got.Ancestors, got.WaitBound = 0, "", nil, 0
	s.Equal(budget.Claim{Project: ".", Target: "job-fork", MemoryMB: guard.ForkValidationMemoryMB, Slots: 1, Dir: resolveRootOrEmpty(dir)}, got)
}

func (s *ForkSeatSuite) TestBrokerOffClaimsNothing() {
	globalCfg.Broker = types.BrokerOff
	full := &fullBudget{}

	release, err := admitForkValidation(s.T().Context(), full, s.T().TempDir())

	s.Require().NoError(err)
	release()
	s.Empty(full.claims)
}
