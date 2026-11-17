package guard

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/gen/typestest"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/internal/unverified"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
)

// UnverifiedReaderSuite pins the reader a command rule's unverified member calls inside a
// judgment.
type UnverifiedReaderSuite struct {
	suite.Suite
	repo  *testkit.Repo
	base  string
	deps  Dependencies
	opens atomic.Int32
}

func TestUnverifiedReaderSuite(t *testing.T) { suite.Run(t, new(UnverifiedReaderSuite)) }

func (s *UnverifiedReaderSuite) SetupTest() {
	testkit.Isolate(s.T())
	s.repo = s.newCheckout()
	s.base = s.repo.Head()
	project := &types.Project{Path: ".", Sources: []types.Glob{{Pattern: "**/*.go"}}, MagusfileTargets: []string{"test"}}
	ws := typestest.NewMockWorkspaceRepository(s.T())
	ws.EXPECT().All().Return([]*types.Project{project}).Maybe()
	ws.EXPECT().Get(".").Return(project).Maybe()
	ws.EXPECT().Get(mock.Anything).Return(nil).Maybe()
	s.deps = Dependencies{
		Inspect: func(context.Context, string) (types.WorkspaceRepository, error) {
			s.opens.Add(1)
			return ws, nil
		},
		CacheDir: func(root string) (string, error) { return filepath.Join(root, ".magus"), nil },
		VCS:      types.VCSOptions{BaseRef: s.base},
	}
}

// newCheckout is a workspace root holding one Go file, its cache directory ignored.
func (s *UnverifiedReaderSuite) newCheckout() *testkit.Repo {
	return testkit.NewRepository(s.T(), map[string]string{".gitignore": ".magus/\n", "magus.yaml": "{}\n", "a.go": "package a\n"})
}

func (s *UnverifiedReaderSuite) passed(repo *testkit.Repo, key, revision string) {
	_, err := cache.NewOutputStore(filepath.Join(repo.Root(), ".magus")).Persist(s.T().Context(), key, []byte("ok\n"), cache.OutputDescriptor{
		Project: ".", Target: "test", Revision: revision, TimestampMs: time.Now().UnixMilli(),
	})
	s.Require().NoError(err)
}

func (s *UnverifiedReaderSuite) TestTheReadIsAnsweredOncePerJudgment() {
	head := s.repo.Commit("change", map[string]string{"a.go": "package a\n\nvar A = 1\n"})
	read := guardUnverified(s.deps)

	first, err := read(s.T().Context(), s.repo.Root(), head)
	s.Require().NoError(err)
	second, err := read(s.T().Context(), s.repo.Root(), head)
	s.Require().NoError(err)

	s.Equal(first, second)
	s.Equal([]string{"a.go"}, first.Changed)
	s.Equal(int32(1), s.opens.Load(), "the workspace opened once")
}

func (s *UnverifiedReaderSuite) TestTheReadLocatesTheCheckoutHoldingDir() {
	other := s.newCheckout()
	s.deps.VCS = types.VCSOptions{BaseRef: other.Head()}
	head := other.Commit("change", map[string]string{"a.go": "package a\n\nvar A = 1\n"})
	s.passed(other, strings.Repeat("a", 64), head)
	read := guardUnverified(s.deps)

	got, err := read(s.T().Context(), other.Root(), head)
	s.Require().NoError(err)
	s.Empty(got.Unknown)
	s.Empty(got.Unverified, "the run recorded in the other checkout's store reads its file")
}

func (s *UnverifiedReaderSuite) TestALapsedReadIsUnknownAndAPhaseRecordedLapsed() {
	head := s.repo.Commit("change", map[string]string{"a.go": "package a\n\nvar A = 1\n"})
	release := make(chan struct{})
	s.T().Cleanup(func() { close(release) })
	s.deps.Inspect = func(context.Context, string) (types.WorkspaceRepository, error) {
		<-release
		return nil, os.ErrClosed
	}
	ctx, cancel := context.WithTimeout(s.T().Context(), judgeReserve+100*time.Millisecond)
	defer cancel()
	ctx = withPhaseLog(ctx, trail.Phase{})

	got, err := guardUnverified(s.deps)(ctx, s.repo.Root(), head)
	s.Require().NoError(err)
	// Whichever of the read's share and the workspace open runs out first words the lapse.
	s.Regexp(`^(weighing the changed files against recorded runs|opening the workspace to weigh the changed files) (did not finish within|had no time left)`, got.Unknown)
	s.Empty(got.Changed)
	s.True(judgmentPhases(ctx).lapsed(phaseUnverified))
}

func (s *UnverifiedReaderSuite) TestNoInspectorRaises() {
	s.deps.Inspect = nil
	_, err := guardUnverified(s.deps)(s.T().Context(), s.repo.Root(), s.base)
	s.Require().ErrorContains(err, "this caller cannot open the workspace")
}

func (s *UnverifiedReaderSuite) TestADirNoWorkspaceHoldsIsUnknown() {
	got, err := guardUnverified(s.deps)(s.T().Context(), s.T().TempDir(), s.base)
	s.Require().NoError(err)
	s.Contains(got.Unknown, "no workspace with an output store holds")
}

func (s *UnverifiedReaderSuite) TestTheCommandRuleCanCallTheReader() {
	pinned := false
	s.deps.CommandRule = func(ctx context.Context, _ types.CommandRequest, _ hint.Gate) (types.GuardVerdict, error) {
		_, pinned = unverified.PinnedReader(ctx)
		return types.GuardVerdict{}, nil
	}
	at := location{dir: s.repo.Root(), workspace: s.repo.Root()}
	gradeWorkspaceCommand(s.T().Context(), s.deps, types.GuardJudgment{Decision: decisionPass},
		commandRuleInput{command: "ls", dialect: DialectBash}, hookAttribution{}, at)
	s.True(pinned)
}

// UnverifiedBudgetSuite pins unverifiedBudget against a frozen clock.
type UnverifiedBudgetSuite struct{ suite.Suite }

func TestUnverifiedBudgetSuite(t *testing.T) { suite.Run(t, new(UnverifiedBudgetSuite)) }

// budgetWithin is unverifiedBudget of the share with left on the clock, read in a bubble
// whose clock moves only when every goroutine waits.
func (s *UnverifiedBudgetSuite) budgetWithin(left time.Duration, dryRun bool) time.Duration {
	var got time.Duration
	synctest.Test(s.T(), func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), left)
		defer cancel()
		if dryRun {
			ctx = context.WithValue(ctx, dryRunKey{}, true)
		}
		got = unverifiedBudget(ctx, unverifiedShare)
	})
	return got
}

func (s *UnverifiedBudgetSuite) TestAnUnboundedJudgmentGetsTheShare() {
	s.Equal(unverifiedShare, unverifiedBudget(s.T().Context(), unverifiedShare))
}

func (s *UnverifiedBudgetSuite) TestAWholeHookBudgetGetsTheShare() {
	s.Equal(unverifiedShare, s.budgetWithin(hookBudget, false))
}

func (s *UnverifiedBudgetSuite) TestALateStartLeavesTheReserve() {
	s.Equal(time.Second-judgeReserve, s.budgetWithin(time.Second, false))
}

func (s *UnverifiedBudgetSuite) TestNoTimePastTheReserveIsNoBudget() {
	s.Equal(time.Duration(0), s.budgetWithin(judgeReserve/2, false))
}

func (s *UnverifiedBudgetSuite) TestADryRunGetsAllButTheReserve() {
	s.Equal(dryRunBudget-judgeReserve, s.budgetWithin(dryRunBudget, true))
}
